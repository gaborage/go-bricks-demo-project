// check-tenant-roles proves that every tenant role in the multi-tenant
// migration fleet sits at the PostgreSQL privilege floor: no SUPERUSER,
// CREATEDB, CREATEROLE, REPLICATION or BYPASSRLS (go-bricks v0.66.0, #1718).
//
// It reads the fleet config go-bricks-migrate reads (config.multitenant.yaml),
// through the same framework pieces: config.TenantStore for each tenant's
// database block and the migration/source/static lister for the tenant IDs. It
// then logs in as each tenant with that tenant's own credential and asks
// migration.CheckPGRoleFloor about the role it logged in as.
//
// The check reads only pg_catalog.pg_roles, so a role needs nothing beyond
// LOGIN to run it. Every session is also opened with
// default_transaction_read_only=on, so it cannot write even by mistake.
// Credentials are never printed: an error is reported with the tenant's
// password removed, and a tenant described by database.connectionstring is
// refused rather than quoted.
//
// Usage:
//
//	make migrate-multitenant-check-roles                # builds bin/check-tenant-roles and runs it
//	make migrate-multitenant-check-roles PG_PORT=55432  # postgres published on another host port
//	go run ./cmd/check-tenant-roles -port 55432         # the same, but go run reports any failure as exit 1
//
// Flags:
//
//	-config   fleet config                          (default config.multitenant.yaml)
//	-host     host override for every tenant        (default: each tenant's database.host)
//	-port     port override for every tenant        (default: each tenant's database.port)
//	-timeout  connect + check budget per tenant     (default 10s)
//
// Exit codes follow go-bricks-migrate's (ADR-115): 0 every role is at the floor;
// 1 at least one role is above it, missing, or could not be checked; 2 nothing
// was checked (bad flags, unreadable config, empty fleet).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/migration"
	"github.com/gaborage/go-bricks/migration/source/static"
)

const (
	exitAllAtFloor     = 0
	exitNotAllAtFloor  = 1
	exitNothingChecked = 2

	defaultConfig  = "config.multitenant.yaml"
	defaultTimeout = 10 * time.Second

	// appName is the session's application_name, so the check is identifiable
	// in pg_stat_activity next to go-bricks-migrate's own sessions.
	appName = "check-tenant-roles"

	redacted = "[REDACTED]"
)

// errConnSettings replaces pgx's parse error, which renders the connection
// string and removes the password from it only on a best-effort basis.
var errConnSettings = errors.New("cannot build a PostgreSQL connection from the tenant's database block")

// errConnStringForm refuses a tenant described by database.connectionstring.
// The string carries the credential, so the error never quotes it.
var errConnStringForm = errors.New("database.connectionstring is not supported here: describe the tenant with host, port, database, username and password")

// tenantTarget is one tenant to check: its ID and its database block with any
// host/port override already applied.
type tenantTarget struct {
	ID string
	DB config.DatabaseConfig
}

// floorCheck checks the role a tenant logs in as. It is a parameter of run so
// the report and exit-code logic are testable without a database.
type floorCheck func(ctx context.Context, db *config.DatabaseConfig) error

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, checkRoleFloor))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, check floorCheck) int {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfig, "fleet config with a multitenant.tenants block (the file go-bricks-migrate --source-config reads)")
	host := fs.String("host", "", "host override for every tenant (default: each tenant's database.host)")
	port := fs.Int("port", 0, "port override for every tenant (default: each tenant's database.port)")
	timeout := fs.Duration("timeout", defaultTimeout, "connect + check budget per tenant")
	if err := fs.Parse(args); err != nil {
		return exitNothingChecked
	}
	if fs.NArg() > 0 || *port < 0 || *port > 65535 || *timeout <= 0 {
		fmt.Fprintln(stderr, "check-tenant-roles: takes no arguments; -port must be 0-65535 and -timeout positive")
		return exitNothingChecked
	}

	targets, err := loadFleet(ctx, *configPath, *host, *port)
	if err != nil {
		fmt.Fprintln(stderr, "check-tenant-roles:", err)
		return exitNothingChecked
	}

	fmt.Fprintf(stdout, "Tenant roles in %s vs the PostgreSQL privilege floor (go-bricks migration.CheckPGRoleFloor)\n", *configPath)
	fmt.Fprintln(stdout, "Each tenant logs in as itself, read-only, and reads only pg_catalog.pg_roles.")
	if *host != "" || *port != 0 {
		fmt.Fprintf(stdout, "Connection override: host=%s port=%s\n", orKept(*host), orKept(portString(*port)))
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	var atFloor, above, unchecked int
	for i := range targets {
		t := &targets[i]
		tctx, cancel := context.WithTimeout(ctx, *timeout)
		status, detail := classify(check(tctx, &t.DB))
		cancel()

		switch status {
		case statusOK:
			atFloor++
		case statusAboveFloor, statusMissing:
			above++
		default:
			unchecked++
		}
		// The detail can quote a driver error; the password never leaves it.
		// Redact before flattening, so a password spelled with whitespace
		// still matches.
		fmt.Fprintf(tw, "  %s\trole %s\t%s\t%s\n", t.ID, t.DB.Username, status, oneLine(redact(detail, t.DB.Password)))
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(stderr, "check-tenant-roles:", err)
		return exitNotAllAtFloor
	}
	fmt.Fprintf(stdout, "%d tenants: %d at the floor, %d above it or missing, %d not checked\n",
		len(targets), atFloor, above, unchecked)

	if atFloor == len(targets) {
		return exitAllAtFloor
	}
	return exitNotAllAtFloor
}

// loadFleet reads the fleet config the way go-bricks-migrate does (koanf YAML
// into config.Config, then config.TenantStore) and lists the tenants with the
// CLI's own static lister, so both tools see the same tenants in the same
// order. host and port, when set, replace every tenant's own.
func loadFleet(ctx context.Context, path, host string, port int) ([]tenantTarget, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load fleet config %q: %w", path, err)
	}
	var cfg config.Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("decode fleet config %q: %w", path, err)
	}

	store := config.NewTenantStore(&cfg)
	ids, err := static.FromConfigStore(store).ListTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants in %q: %w", path, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%q lists no tenants (multitenant.enabled must be true with a tenants block)", path)
	}

	targets := make([]tenantTarget, 0, len(ids))
	for _, id := range ids {
		db, err := store.DBConfig(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("tenant %q: %w", id, err)
		}
		targets = append(targets, tenantTarget{ID: id, DB: withOverrides(db, host, port)})
	}
	return targets, nil
}

// withOverrides returns a copy of db with host and port replaced when set; the
// store's own config is never modified.
func withOverrides(db *config.DatabaseConfig, host string, port int) config.DatabaseConfig {
	out := *db
	if host != "" {
		out.Host = host
	}
	if port != 0 {
		out.Port = port
	}
	return out
}

// checkRoleFloor opens a read-only session as the tenant and asks the
// framework whether the role it logged in as holds any attribute above the
// floor.
func checkRoleFloor(ctx context.Context, db *config.DatabaseConfig) error {
	if db.Type != config.PostgreSQL {
		return fmt.Errorf("database.type %q: the privilege floor is a PostgreSQL check", db.Type)
	}
	if db.ConnectionString != "" {
		// connString builds from the fields; ignoring the string would dial
		// somewhere else than the tenant's migrations do.
		return errConnStringForm
	}
	connCfg, err := pgx.ParseConfig(connString(db))
	if err != nil {
		return errConnSettings
	}
	sqlDB := stdlib.OpenDB(*connCfg)
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)

	return migration.CheckPGRoleFloor(ctx, sqlDB, db.Username)
}

// connString builds the tenant's connection URL. TLS follows the framework's
// PostgreSQL connection: sslmode and the CA come from database.tls when set,
// and pgx's default applies otherwise.
func connString(db *config.DatabaseConfig) string {
	q := url.Values{}
	q.Set("application_name", appName)
	// A startup parameter, so every transaction in the session is read-only.
	q.Set("default_transaction_read_only", "on")
	if db.TLS.Mode != "" {
		q.Set("sslmode", db.TLS.Mode)
	}
	if db.TLS.CAFile != "" {
		q.Set("sslrootcert", db.TLS.CAFile)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(db.Username, db.Password),
		Host:     net.JoinHostPort(db.Host, strconv.Itoa(db.Port)),
		Path:     "/" + db.Database,
		RawQuery: q.Encode(),
	}
	return u.String()
}

// Report statuses, one per tenant.
const (
	statusOK         = "OK"
	statusAboveFloor = "ABOVE FLOOR"
	statusMissing    = "MISSING"
	statusNotChecked = "NOT CHECKED"
)

// classify turns a CheckPGRoleFloor outcome into a report status and detail.
func classify(err error) (status, detail string) {
	switch {
	case err == nil:
		return statusOK, "at the floor"
	case errors.Is(err, migration.ErrPGRoleFloorViolated):
		return statusAboveFloor, heldAttributes(err)
	case errors.Is(err, migration.ErrPGRoleNotFound):
		return statusMissing, err.Error()
	default:
		return statusNotChecked, err.Error()
	}
}

// heldAttributes extracts the attribute list from a floor violation
// (`<sentinel>: role "acme" holds CREATEDB, BYPASSRLS`), falling back to the
// whole message should the framework ever word it differently. The sentinel's
// own text says "holds" too, so it is cut off before looking for the list.
func heldAttributes(err error) string {
	msg := err.Error()
	rest, ok := strings.CutPrefix(msg, migration.ErrPGRoleFloorViolated.Error()+": ")
	if !ok {
		return msg
	}
	if i := strings.LastIndex(rest, " holds "); i >= 0 {
		return "holds " + rest[i+len(" holds "):]
	}
	return msg
}

// redact removes every spelling of secret an error could carry: the raw value
// and the two URL escapings a connection string uses.
func redact(msg, secret string) string {
	if secret == "" {
		return msg
	}
	for _, form := range []string{secret, url.PathEscape(secret), url.QueryEscape(secret)} {
		msg = strings.ReplaceAll(msg, form, redacted)
	}
	return msg
}

// oneLine keeps a report row on one line: pgx lists every address it tried
// on a line of its own.
func oneLine(msg string) string {
	return strings.Join(strings.Fields(msg), " ")
}

func orKept(v string) string {
	if v == "" {
		return "(per tenant)"
	}
	return v
}

func portString(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}
