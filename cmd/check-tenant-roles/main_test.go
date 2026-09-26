package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/migration"
)

// fixturePassword is synthetic and built at runtime, so no credential-shaped
// literal lives in this file. Distinctive enough to grep an output for.
var fixturePassword = strings.Repeat("fx", 6)

// The fixture fleet's tenants. Each tenant's role is named after it, as in
// config.multitenant.yaml.
const (
	tenantAcme    = "acme"
	tenantGlobex  = "globex"
	tenantInitech = "initech"

	configFlag = "-config"
)

// fleetYAML mirrors config.multitenant.yaml's shape. The tenants are listed out
// of order on purpose: the lister sorts them.
func fleetYAML(password string) string {
	var b strings.Builder
	b.WriteString("app:\n  name: go-bricks-demo-migrate\n  env: development\n\nmultitenant:\n  enabled: true\n  tenants:\n")
	for _, id := range []string{tenantInitech, tenantAcme, tenantGlobex} {
		fmt.Fprintf(&b, `    %s:
      database:
        type: postgresql
        host: localhost
        port: 5432
        database: postgres
        username: %s
        password: %s
        timezone: UTC
`, id, id, password)
	}
	return b.String()
}

const emptyFleetYAML = `multitenant:
  enabled: true
  tenants: {}
`

func writeFleet(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadFleetListsTenantsLikeTheMigrateCLI(t *testing.T) {
	path := writeFleet(t, fleetYAML(fixturePassword))

	targets, err := loadFleet(context.Background(), path, "", 0)
	require.NoError(t, err)

	ids := make([]string, 0, len(targets))
	for i := range targets {
		ids = append(ids, targets[i].ID)
	}
	assert.Equal(t, []string{tenantAcme, tenantGlobex, tenantInitech}, ids, "sorted, as migration/source/static lists them")

	acme := targets[0].DB
	assert.Equal(t, config.PostgreSQL, acme.Type)
	assert.Equal(t, "localhost", acme.Host)
	assert.Equal(t, 5432, acme.Port)
	assert.Equal(t, "postgres", acme.Database)
	assert.Equal(t, tenantAcme, acme.Username)
	assert.Equal(t, fixturePassword, acme.Password)
}

func TestLoadFleetAppliesHostAndPortOverridesToEveryTenant(t *testing.T) {
	path := writeFleet(t, fleetYAML(fixturePassword))

	targets, err := loadFleet(context.Background(), path, "127.0.0.1", 25432)
	require.NoError(t, err)
	require.Len(t, targets, 3)
	for i := range targets {
		assert.Equal(t, "127.0.0.1", targets[i].DB.Host, targets[i].ID)
		assert.Equal(t, 25432, targets[i].DB.Port, targets[i].ID)
		assert.Equal(t, targets[i].ID, targets[i].DB.Username, "credentials stay the tenant's own")
	}
}

func TestLoadFleetRefusesAFleetWithNothingToCheck(t *testing.T) {
	tests := map[string]string{
		"empty tenants block":  emptyFleetYAML,
		"multitenant disabled": strings.Replace(fleetYAML(fixturePassword), "enabled: true", "enabled: false", 1),
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadFleet(context.Background(), writeFleet(t, content), "", 0)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "lists no tenants")
		})
	}
}

func TestLoadFleetReportsAMissingConfig(t *testing.T) {
	_, err := loadFleet(context.Background(), filepath.Join(t.TempDir(), "absent.yaml"), "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load fleet config")
}

func TestWithOverridesCopiesAndKeepsUnsetFields(t *testing.T) {
	orig := config.DatabaseConfig{Host: "localhost", Port: 5432, Username: tenantAcme}

	kept := withOverrides(&orig, "", 0)
	assert.Equal(t, orig, kept)

	moved := withOverrides(&orig, "db.internal", 6543)
	assert.Equal(t, "db.internal", moved.Host)
	assert.Equal(t, 6543, moved.Port)
	assert.Equal(t, "localhost", orig.Host, "the source config is never modified")
	assert.Equal(t, 5432, orig.Port)
}

func TestConnStringOpensAReadOnlySessionAsTheTenant(t *testing.T) {
	// Reserved URL characters must survive the escaping round trip.
	password := fixturePassword + "@:/?#%"
	db := &config.DatabaseConfig{
		Type:     config.PostgreSQL,
		Host:     "localhost",
		Port:     25432,
		Database: "postgres",
		Username: tenantGlobex,
		Password: password,
		TLS:      config.TLSConfig{Mode: "disable"},
	}

	cfg, err := pgx.ParseConfig(connString(db))
	require.NoError(t, err)

	assert.Equal(t, "localhost", cfg.Host)
	assert.Equal(t, uint16(25432), cfg.Port)
	assert.Equal(t, "postgres", cfg.Database)
	assert.Equal(t, tenantGlobex, cfg.User)
	assert.Equal(t, password, cfg.Password)
	assert.Equal(t, "on", cfg.RuntimeParams["default_transaction_read_only"])
	assert.Equal(t, appName, cfg.RuntimeParams["application_name"])
	assert.Nil(t, cfg.TLSConfig, "database.tls.mode=disable is honoured")
}

func TestCheckRoleFloorCallsTheFrameworkCheckBeforeAnyDial(t *testing.T) {
	// CheckPGRoleFloor validates the role name before its query, so a reserved
	// name fails without a connection: the .invalid host is never resolved.
	db := &config.DatabaseConfig{
		Type:     config.PostgreSQL,
		Host:     "check-tenant-roles.invalid",
		Port:     5432,
		Database: "postgres",
		Username: "public",
	}

	err := checkRoleFloor(context.Background(), db)
	require.ErrorIs(t, err, migration.ErrInvalidPGIdentifier)
}

func TestCheckRoleFloorRefusesANonPostgreSQLTenant(t *testing.T) {
	err := checkRoleFloor(context.Background(), &config.DatabaseConfig{Type: config.Oracle, Username: tenantAcme})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PostgreSQL check")
}

func TestCheckRoleFloorRefusesAConnectionStringTenant(t *testing.T) {
	// Built from the fields, the session would dial somewhere else than the
	// string names, so the tenant is refused before any dial.
	db := &config.DatabaseConfig{
		Type:             config.PostgreSQL,
		Username:         tenantAcme,
		ConnectionString: "host=check-tenant-roles.invalid dbname=postgres user=acme",
	}

	err := checkRoleFloor(context.Background(), db)
	require.ErrorIs(t, err, errConnStringForm)
	assert.NotContains(t, err.Error(), "check-tenant-roles.invalid", "the connection string is never quoted")
}

// violation builds the error CheckPGRoleFloor returns for a role above the floor.
func violation(role string, attrs ...string) error {
	return fmt.Errorf("%w: role %q holds %s", migration.ErrPGRoleFloorViolated, role, strings.Join(attrs, ", "))
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantDetail string
	}{
		{"at the floor", nil, statusOK, "at the floor"},
		{"above the floor", violation(tenantAcme, "CREATEDB", "BYPASSRLS"), statusAboveFloor, "holds CREATEDB, BYPASSRLS"},
		{"role missing", fmt.Errorf("%w: %q", migration.ErrPGRoleNotFound, tenantAcme), statusMissing, `migration: role not found: "acme"`},
		{"unreachable", errors.New("dial tcp: connection refused"), statusNotChecked, "dial tcp: connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, detail := classify(tt.err)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantDetail, detail)
		})
	}
}

func TestHeldAttributesFallsBackToTheWholeMessage(t *testing.T) {
	err := fmt.Errorf("%w: reworded", migration.ErrPGRoleFloorViolated)
	assert.Equal(t, err.Error(), heldAttributes(err))
}

func TestRedactRemovesEverySpellingOfTheSecret(t *testing.T) {
	secret := fixturePassword + " /?"
	msg := strings.Join([]string{secret, url.PathEscape(secret), url.QueryEscape(secret)}, " | ")

	out := redact(msg, secret)
	assert.NotContains(t, out, fixturePassword)
	assert.Equal(t, strings.Repeat(redacted+" | ", 2)+redacted, out)
	assert.Equal(t, "untouched", redact("untouched", ""))
}

func TestRunExitCodesAndReport(t *testing.T) {
	fleet := writeFleet(t, fleetYAML(fixturePassword))

	tests := []struct {
		name     string
		args     []string
		check    floorCheck
		wantExit int
		wantOut  []string
	}{
		{
			name:     "every role at the floor",
			args:     []string{configFlag, fleet},
			check:    func(context.Context, *config.DatabaseConfig) error { return nil },
			wantExit: exitAllAtFloor,
			wantOut:  []string{tenantAcme, tenantGlobex, tenantInitech, "3 tenants: 3 at the floor, 0 above it or missing, 0 not checked"},
		},
		{
			name: "one role above the floor",
			args: []string{configFlag, fleet},
			check: func(_ context.Context, db *config.DatabaseConfig) error {
				if db.Username == tenantGlobex {
					return violation(tenantGlobex, "CREATEDB")
				}
				return nil
			},
			wantExit: exitNotAllAtFloor,
			wantOut:  []string{"ABOVE FLOOR", "holds CREATEDB", "3 tenants: 2 at the floor, 1 above it or missing, 0 not checked"},
		},
		{
			name: "a tenant that cannot be reached",
			args: []string{configFlag, fleet},
			check: func(context.Context, *config.DatabaseConfig) error {
				return errors.New("failed to connect: server closed the connection")
			},
			wantExit: exitNotAllAtFloor,
			wantOut:  []string{"NOT CHECKED", "3 tenants: 0 at the floor, 0 above it or missing, 3 not checked"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := run(context.Background(), tt.args, &stdout, &stderr, tt.check)
			assert.Equal(t, tt.wantExit, got, "stderr: %s", stderr.String())
			for _, want := range tt.wantOut {
				assert.Contains(t, stdout.String(), want)
			}
		})
	}
}

func TestRunNeverPrintsATenantPassword(t *testing.T) {
	passwords := map[string]string{
		"plain": fixturePassword,
		// Redaction must run before a row is flattened onto one line, or the
		// double space collapses and the password no longer matches.
		"with a double space": fixturePassword + "  " + fixturePassword,
	}
	for name, password := range passwords {
		t.Run(name, func(t *testing.T) {
			fleet := writeFleet(t, fleetYAML(password))
			// A driver error that echoed the password must not carry it to the report.
			leaky := func(_ context.Context, db *config.DatabaseConfig) error {
				return fmt.Errorf("cannot connect as %s with password %s", db.Username, db.Password)
			}

			var stdout, stderr bytes.Buffer
			got := run(context.Background(), []string{configFlag, fleet}, &stdout, &stderr, leaky)

			assert.Equal(t, exitNotAllAtFloor, got)
			assert.NotContains(t, stdout.String()+stderr.String(), fixturePassword)
			assert.Contains(t, stdout.String(), redacted)
		})
	}
}

func TestRunKeepsEachTenantOnOneLine(t *testing.T) {
	fleet := writeFleet(t, fleetYAML(fixturePassword))
	// pgx lists every address it tried on a line of its own.
	multiLine := func(context.Context, *config.DatabaseConfig) error {
		return errors.New("failed to connect:\n\t127.0.0.1:1: refused\n\t127.0.0.1:1: refused")
	}

	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{configFlag, fleet}, &stdout, &stderr, multiLine)

	assert.Equal(t, exitNotAllAtFloor, got)
	assert.Equal(t, 3, strings.Count(stdout.String(), "failed to connect: 127.0.0.1:1: refused 127.0.0.1:1: refused\n"))
}

func TestRunPassesOverridesToTheCheck(t *testing.T) {
	fleet := writeFleet(t, fleetYAML(fixturePassword))
	var seen []string
	record := func(_ context.Context, db *config.DatabaseConfig) error {
		seen = append(seen, fmt.Sprintf("%s@%s:%d", db.Username, db.Host, db.Port))
		return nil
	}

	var stdout, stderr bytes.Buffer
	got := run(context.Background(), []string{configFlag, fleet, "-host", "127.0.0.1", "-port", "25432"}, &stdout, &stderr, record)

	assert.Equal(t, exitAllAtFloor, got)
	assert.Equal(t, []string{"acme@127.0.0.1:25432", "globex@127.0.0.1:25432", "initech@127.0.0.1:25432"}, seen)
	assert.Contains(t, stdout.String(), "Connection override: host=127.0.0.1 port=25432")
}

func TestRunChecksNothingOnBadInput(t *testing.T) {
	fleet := writeFleet(t, fleetYAML(fixturePassword))
	checked := 0
	count := func(context.Context, *config.DatabaseConfig) error {
		checked++
		return nil
	}

	tests := map[string][]string{
		"unknown flag":   {configFlag, fleet, "-verbose"},
		"stray argument": {configFlag, fleet, tenantAcme},
		"port too large": {configFlag, fleet, "-port", "70000"},
		"zero timeout":   {configFlag, fleet, "-timeout", "0s"},
		"empty fleet":    {configFlag, writeFleet(t, emptyFleetYAML)},
		"missing config": {configFlag, filepath.Join(t.TempDir(), "absent.yaml")},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			assert.Equal(t, exitNothingChecked, run(context.Background(), args, &stdout, &stderr, count))
			assert.Zero(t, checked, "no tenant may be checked")
		})
	}
}
