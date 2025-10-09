package secrets

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
)

// MockTenantStore provides a mock implementation of the TenantStore interface
// for local development and testing without requiring AWS Secrets Manager
type MockTenantStore struct {
	configs map[string]*config.DatabaseConfig
	logger  logger.Logger
	mu      sync.RWMutex
}

// NewMockTenantStore creates a new mock tenant store with predefined configurations
func NewMockTenantStore(logger logger.Logger) *MockTenantStore {
	store := &MockTenantStore{
		configs: make(map[string]*config.DatabaseConfig),
		logger:  logger,
	}

	// Add some sample tenant configurations
	store.addSampleTenants()

	return store
}

// DBConfig implements the database.TenantStore interface
func (m *MockTenantStore) DBConfig(ctx context.Context, tenantID string) (*config.DatabaseConfig, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant ID cannot be empty")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[tenantID]
	if !exists {
		m.logger.Warn().
			Str("tenant_id", tenantID).
			Msg("Tenant configuration not found in mock store")
		return nil, fmt.Errorf("no database configuration found for tenant: %s", tenantID)
	}

	m.logger.Debug().
		Str("tenant_id", tenantID).
		Str("db_type", config.Type).
		Str("host", config.Host).
		Int("port", config.Port).
		Msg("Retrieved database config from mock store")

	return config, nil
}

// AddTenant adds a new tenant configuration to the mock store
func (m *MockTenantStore) AddTenant(tenantID string, config *config.DatabaseConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.configs[tenantID] = config
	m.logger.Info().
		Str("tenant_id", tenantID).
		Str("db_type", config.Type).
		Msg("Added tenant configuration to mock store")
}

// RemoveTenant removes a tenant configuration from the mock store
func (m *MockTenantStore) RemoveTenant(tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.configs, tenantID)
	m.logger.Info().
		Str("tenant_id", tenantID).
		Msg("Removed tenant configuration from mock store")
}

// ListTenants returns a list of all configured tenants
func (m *MockTenantStore) ListTenants(ctx context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenants := make([]string, 0, len(m.configs))
	for tenantID := range m.configs {
		tenants = append(tenants, tenantID)
	}

	m.logger.Debug().
		Int("tenant_count", len(tenants)).
		Str("tenants", fmt.Sprintf("%v", tenants)).
		Msg("Listed tenants from mock store")

	return tenants, nil
}

// Close implements the cleanup interface
func (m *MockTenantStore) Close() error {
	m.logger.Debug().Msg("Closed mock tenant store")
	return nil
}

// addSampleTenants adds predefined tenant configurations for demonstration
func (m *MockTenantStore) addSampleTenants() {
	// Tenant 1 - PostgreSQL
	m.configs["tenant1"] = &config.DatabaseConfig{
		Type:     "postgresql",
		Host:     "localhost",
		Port:     5433,
		Database: "tenant1_db",
		Username: "tenant1_user",
		Password: "tenant1_pass",
		Pool: config.PoolConfig{
			Max: config.PoolMaxConfig{
				Connections: 20,
			},
			Idle: config.PoolIdleConfig{
				Connections: 5,
				Time:        30 * time.Minute,
			},
		},
		Query: config.QueryConfig{
			Slow: config.SlowQueryConfig{
				Threshold: 200 * time.Millisecond,
				Enabled:   true,
			},
			Log: config.QueryLogConfig{
				Parameters: false,
				MaxLength:  1000,
			},
		},
	}

	// Tenant 2 - PostgreSQL with different configuration
	m.configs["tenant2"] = &config.DatabaseConfig{
		Type:     "postgresql",
		Host:     "localhost",
		Port:     5434,
		Database: "tenant2_db",
		Username: "tenant2_user",
		Password: "tenant2_pass",
		Pool: config.PoolConfig{
			Max: config.PoolMaxConfig{
				Connections: 15,
			},
			Idle: config.PoolIdleConfig{
				Connections: 3,
				Time:        20 * time.Minute,
			},
		},
		Query: config.QueryConfig{
			Slow: config.SlowQueryConfig{
				Threshold: 300 * time.Millisecond,
				Enabled:   true,
			},
			Log: config.QueryLogConfig{
				Parameters: true,
				MaxLength:  500,
			},
		},
	}

	// Tenant 3 - Oracle
	m.configs["tenant3"] = &config.DatabaseConfig{
		Type:     "oracle",
		Host:     "localhost",
		Port:     1522,
		Database: "XE",
		Username: "tenant3_user",
		Password: "tenant3_pass",
		Pool: config.PoolConfig{
			Max: config.PoolMaxConfig{
				Connections: 10,
			},
			Idle: config.PoolIdleConfig{
				Connections: 2,
				Time:        15 * time.Minute,
			},
		},
		Query: config.QueryConfig{
			Slow: config.SlowQueryConfig{
				Threshold: 500 * time.Millisecond,
				Enabled:   true,
			},
			Log: config.QueryLogConfig{
				Parameters: false,
				MaxLength:  800,
			},
		},
		Oracle: config.OracleConfig{
			Service: config.ServiceConfig{
				Name: "XE",
			},
		},
	}

	m.logger.Info().
		Int("tenant_count", len(m.configs)).
		Msg("Initialized mock tenant store with sample configurations")
}
