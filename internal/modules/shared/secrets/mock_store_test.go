package secrets

import (
	"context"
	"slices"
	"sort"
	"sync"
	"testing"

	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
)

// Fixture values shared by the secrets tests: the sample tenants
// NewMockTenantStore seeds, and the database types they use.
const (
	tenant1ID        = "tenant1"
	tenant2ID        = "tenant2"
	tenant3ID        = "tenant3"
	dbTypePostgreSQL = "postgresql"
	dbTypeOracle     = "oracle"
)

func testLoggerMock() logger.Logger {
	return logger.New("disabled", false)
}

func TestNewMockTenantStore_SeedsSampleTenants(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	tenants, err := store.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}

	sort.Strings(tenants)
	want := []string{tenant1ID, tenant2ID, tenant3ID}
	if len(tenants) != len(want) {
		t.Fatalf("ListTenants() = %v, want %v", tenants, want)
	}
	for i, tenant := range want {
		if tenants[i] != tenant {
			t.Errorf("ListTenants()[%d] = %q, want %q", i, tenants[i], tenant)
		}
	}
}

func TestMockTenantStore_DBConfig_Success(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		wantType string
		wantHost string
		wantPort int
	}{
		{"tenant1_postgres", tenant1ID, dbTypePostgreSQL, localhostAddr, 5433},
		{"tenant2_postgres", tenant2ID, dbTypePostgreSQL, localhostAddr, 5434},
		{"tenant3_oracle", tenant3ID, dbTypeOracle, localhostAddr, 1522},
	}

	store := NewMockTenantStore(testLoggerMock())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := store.DBConfig(context.Background(), tt.tenantID)
			if err != nil {
				t.Fatalf("DBConfig() error = %v", err)
			}
			if cfg.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", cfg.Type, tt.wantType)
			}
			if cfg.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", cfg.Host, tt.wantHost)
			}
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.wantPort)
			}
		})
	}
}

func TestMockTenantStore_DBConfig_EmptyTenantID(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	cfg, err := store.DBConfig(context.Background(), "")
	if err == nil {
		t.Fatal("DBConfig() expected error for empty tenant ID")
	}
	if cfg != nil {
		t.Errorf("DBConfig() = %v, want nil", cfg)
	}
}

func TestMockTenantStore_DBConfig_NotFound(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	cfg, err := store.DBConfig(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("DBConfig() expected error for unknown tenant")
	}
	if cfg != nil {
		t.Errorf("DBConfig() = %v, want nil", cfg)
	}
}

func TestMockTenantStore_AddTenant(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	newCfg := &config.DatabaseConfig{
		Type:     dbTypePostgreSQL,
		Host:     localhostAddr,
		Port:     6000,
		Database: "newtenant_db",
		Username: "newtenant_user",
		Password: "newtenant_pass",
	}
	store.AddTenant("newtenant", newCfg)

	got, err := store.DBConfig(context.Background(), "newtenant")
	if err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}
	if got != newCfg {
		t.Errorf("DBConfig() = %v, want the exact config added", got)
	}

	tenants, err := store.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}
	if !slices.Contains(tenants, "newtenant") {
		t.Errorf("ListTenants() = %v, want to contain newtenant", tenants)
	}
}

func TestMockTenantStore_AddTenant_OverwritesExisting(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	overwritten := &config.DatabaseConfig{Type: dbTypePostgreSQL, Host: localhostAddr, Port: 9999}
	store.AddTenant(tenant1ID, overwritten)

	got, err := store.DBConfig(context.Background(), tenant1ID)
	if err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}
	if got.Port != 9999 {
		t.Errorf("Port = %d, want 9999 (overwritten config)", got.Port)
	}
}

func TestMockTenantStore_RemoveTenant(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	store.RemoveTenant(tenant1ID)

	_, err := store.DBConfig(context.Background(), tenant1ID)
	if err == nil {
		t.Fatal("DBConfig() expected error after RemoveTenant()")
	}

	tenants, err := store.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}
	if slices.Contains(tenants, tenant1ID) {
		t.Errorf("ListTenants() = %v, want tenant1 removed", tenants)
	}
}

func TestMockTenantStore_RemoveTenant_MissingKeyNoPanic(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	store.RemoveTenant("does-not-exist")
}

func TestMockTenantStore_Close(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	if err := store.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestMockTenantStore_ConcurrentAccess(t *testing.T) {
	store := NewMockTenantStore(testLoggerMock())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			store.AddTenant("concurrent-tenant", &config.DatabaseConfig{Type: dbTypePostgreSQL})
		}()
		go func() {
			defer wg.Done()
			_, _ = store.DBConfig(context.Background(), tenant1ID)
		}()
		go func() {
			defer wg.Done()
			_, _ = store.ListTenants(context.Background())
		}()
	}
	wg.Wait()
}
