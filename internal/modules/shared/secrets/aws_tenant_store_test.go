package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/gaborage/go-bricks/logger"
)

// fakeSecretsManagerAPI is a hand-rolled fake for SecretsManagerAPI, the
// injectable seam AWSSecretsTenantStore uses instead of a concrete AWS SDK
// client. It lets us exercise DBConfig/ListTenants without hitting real AWS.
type fakeSecretsManagerAPI struct {
	getSecretValueFunc  func(ctx context.Context, params *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error)
	listSecretsFunc     func(ctx context.Context, params *secretsmanager.ListSecretsInput) (*secretsmanager.ListSecretsOutput, error)
	getSecretValueCalls int
	listSecretsCalls    int
}

func (f *fakeSecretsManagerAPI) GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.getSecretValueCalls++
	return f.getSecretValueFunc(ctx, params)
}

func (f *fakeSecretsManagerAPI) ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	f.listSecretsCalls++
	return f.listSecretsFunc(ctx, params)
}

const testSecretsPrefix = "/gobricks/demo"

func testLoggerAWS() logger.Logger {
	return logger.New("disabled", false)
}

func newTestStore(client SecretsManagerAPI) *AWSSecretsTenantStore {
	return &AWSSecretsTenantStore{
		client: client,
		cache:  NewCache(5*time.Minute, 100),
		prefix: "/gobricks/test",
		logger: testLoggerAWS(),
	}
}

const validSecretJSON = `{
	"type": "postgresql",
	"host": "postgres-tenant1",
	"port": 5432,
	"database": "tenant1_db",
	"username": "tenant1_user",
	"password": "tenant1_pass",
	"pool": {
		"max": {"connections": 20},
		"idle": {"connections": 5, "time": 1800000000000}
	}
}`

func TestAWSSecretsTenantStore_DBConfig_Success(t *testing.T) {
	tests := []struct {
		name        string
		tenantID    string
		secretValue string
		wantType    string
		wantHost    string
		wantPort    int
	}{
		{
			name:        "postgres",
			tenantID:    tenant1ID,
			secretValue: validSecretJSON,
			wantType:    dbTypePostgreSQL,
			wantHost:    "postgres-tenant1",
			wantPort:    5432,
		},
		{
			name:     "oracle",
			tenantID: tenant2ID,
			secretValue: `{
				"type": "oracle",
				"host": "oracle-tenant2",
				"port": 1521,
				"database": "XE",
				"username": "tenant2_user",
				"password": "tenant2_pass",
				"oracle": {"service": {"name": "XE"}}
			}`,
			wantType: dbTypeOracle,
			wantHost: "oracle-tenant2",
			wantPort: 1521,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSecretsManagerAPI{
				getSecretValueFunc: func(_ context.Context, params *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
					wantSecretName := fmt.Sprintf("/gobricks/test/%s/database", tt.tenantID)
					if aws.ToString(params.SecretId) != wantSecretName {
						t.Errorf("SecretId = %q, want %q", aws.ToString(params.SecretId), wantSecretName)
					}
					return &secretsmanager.GetSecretValueOutput{
						SecretString: aws.String(tt.secretValue),
					}, nil
				},
			}
			store := newTestStore(fake)

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

func TestAWSSecretsTenantStore_DBConfig_EmptyTenantID(t *testing.T) {
	fake := &fakeSecretsManagerAPI{}
	store := newTestStore(fake)

	cfg, err := store.DBConfig(context.Background(), "")
	if err == nil {
		t.Fatal("DBConfig() expected error for empty tenant ID")
	}
	if cfg != nil {
		t.Errorf("DBConfig() = %v, want nil", cfg)
	}
	if fake.getSecretValueCalls != 0 {
		t.Errorf("GetSecretValue called %d times, want 0", fake.getSecretValueCalls)
	}
}

func TestAWSSecretsTenantStore_DBConfig_SecretNotFound(t *testing.T) {
	// fetchDatabaseConfig maps *types.InvalidParameterException specifically
	// to the "secret not found" error message.
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return nil, &types.InvalidParameterException{Message: aws.String("not found")}
		},
	}
	store := newTestStore(fake)

	cfg, err := store.DBConfig(context.Background(), "missing-tenant")
	if err == nil {
		t.Fatal("DBConfig() expected error")
	}
	if cfg != nil {
		t.Errorf("DBConfig() = %v, want nil", cfg)
	}
	wantSubstr := "secret not found for tenant missing-tenant"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
	}
}

func TestAWSSecretsTenantStore_DBConfig_RetrievalErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"decryption_failure", &types.DecryptionFailure{Message: aws.String("boom")}},
		{"internal_service_error", &types.InternalServiceError{Message: aws.String("boom")}},
		{"invalid_request", &types.InvalidRequestException{Message: aws.String("boom")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSecretsManagerAPI{
				getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
					return nil, tt.err
				},
			}
			store := newTestStore(fake)

			_, err := store.DBConfig(context.Background(), tenant1ID)
			if err == nil {
				t.Fatal("DBConfig() expected error")
			}
			wantSubstr := "error retrieving secret for tenant tenant1"
			if !strings.Contains(err.Error(), wantSubstr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
			}
		})
	}
}

func TestAWSSecretsTenantStore_DBConfig_GenericError(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return nil, errors.New("network unreachable")
		},
	}
	store := newTestStore(fake)

	_, err := store.DBConfig(context.Background(), tenant1ID)
	if err == nil {
		t.Fatal("DBConfig() expected error")
	}
	wantSubstr := "failed to retrieve secret for tenant tenant1"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
	}
}

func TestAWSSecretsTenantStore_DBConfig_NilSecretString(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: nil}, nil
		},
	}
	store := newTestStore(fake)

	_, err := store.DBConfig(context.Background(), tenant1ID)
	if err == nil {
		t.Fatal("DBConfig() expected error")
	}
	wantSubstr := "secret value is empty for tenant tenant1"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
	}
}

func TestAWSSecretsTenantStore_DBConfig_MalformedJSON(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: aws.String("{not valid json")}, nil
		},
	}
	store := newTestStore(fake)

	_, err := store.DBConfig(context.Background(), tenant1ID)
	if err == nil {
		t.Fatal("DBConfig() expected error")
	}
	wantSubstr := "failed to parse secret JSON for tenant tenant1"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
	}
}

func TestAWSSecretsTenantStore_DBConfig_CacheHit(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(validSecretJSON)}, nil
		},
	}
	store := newTestStore(fake)

	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() first call error = %v", err)
	}
	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() second call error = %v", err)
	}

	if fake.getSecretValueCalls != 1 {
		t.Errorf("GetSecretValue called %d times, want 1 (second call should be served from cache)", fake.getSecretValueCalls)
	}
}

func TestAWSSecretsTenantStore_InvalidateCache(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(validSecretJSON)}, nil
		},
	}
	store := newTestStore(fake)

	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}

	store.InvalidateCache(tenant1ID)

	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}

	if fake.getSecretValueCalls != 2 {
		t.Errorf("GetSecretValue called %d times, want 2 (cache should have been invalidated)", fake.getSecretValueCalls)
	}
}

func TestAWSSecretsTenantStore_ClearCache(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(validSecretJSON)}, nil
		},
	}
	store := newTestStore(fake)

	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}
	if _, err := store.DBConfig(context.Background(), tenant2ID); err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}

	store.ClearCache()

	if size := store.cache.Size(); size != 0 {
		t.Errorf("cache.Size() after ClearCache() = %d, want 0", size)
	}
}

func TestAWSSecretsTenantStore_CacheMetrics(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(_ context.Context, _ *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(validSecretJSON)}, nil
		},
	}
	store := newTestStore(fake)

	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}
	if _, err := store.DBConfig(context.Background(), tenant1ID); err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}

	metrics := store.CacheMetrics()
	if metrics.Misses != 1 {
		t.Errorf("Misses = %d, want 1", metrics.Misses)
	}
	if metrics.Hits != 1 {
		t.Errorf("Hits = %d, want 1", metrics.Hits)
	}
}

func TestAWSSecretsTenantStore_ListTenants(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		listSecretsFunc: func(_ context.Context, params *secretsmanager.ListSecretsInput) (*secretsmanager.ListSecretsOutput, error) {
			if params.NextToken == nil {
				return &secretsmanager.ListSecretsOutput{
					SecretList: []types.SecretListEntry{
						{Name: aws.String("/gobricks/test/tenant1/database")},
						{Name: aws.String("/gobricks/test/tenant2/database")},
						{Name: aws.String("/gobricks/test/other/messaging")}, // ignored: wrong suffix
					},
					NextToken: aws.String("page2"),
				}, nil
			}
			return &secretsmanager.ListSecretsOutput{
				SecretList: []types.SecretListEntry{
					{Name: aws.String("/gobricks/test/tenant3/database")},
				},
			}, nil
		},
	}
	store := newTestStore(fake)

	tenants, err := store.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}

	want := []string{tenant1ID, tenant2ID, tenant3ID}
	if len(tenants) != len(want) {
		t.Fatalf("ListTenants() = %v, want %v", tenants, want)
	}
	for i, tenant := range want {
		if tenants[i] != tenant {
			t.Errorf("ListTenants()[%d] = %q, want %q", i, tenants[i], tenant)
		}
	}
	if fake.listSecretsCalls != 2 {
		t.Errorf("ListSecrets called %d times, want 2 (pagination)", fake.listSecretsCalls)
	}
}

func TestAWSSecretsTenantStore_ListTenants_Error(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		listSecretsFunc: func(_ context.Context, _ *secretsmanager.ListSecretsInput) (*secretsmanager.ListSecretsOutput, error) {
			return nil, errors.New("access denied")
		},
	}
	store := newTestStore(fake)

	_, err := store.ListTenants(context.Background())
	if err == nil {
		t.Fatal("ListTenants() expected error")
	}
	wantSubstr := "failed to list secrets"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
	}
}

func TestAWSSecretsTenantStore_ListTenants_NoMatches(t *testing.T) {
	fake := &fakeSecretsManagerAPI{
		listSecretsFunc: func(_ context.Context, _ *secretsmanager.ListSecretsInput) (*secretsmanager.ListSecretsOutput, error) {
			return &secretsmanager.ListSecretsOutput{
				SecretList: []types.SecretListEntry{
					{Name: aws.String("/gobricks/test/other/messaging")},
					{Name: nil},
				},
			}, nil
		},
	}
	store := newTestStore(fake)

	tenants, err := store.ListTenants(context.Background())
	if err != nil {
		t.Fatalf("ListTenants() error = %v", err)
	}
	if len(tenants) != 0 {
		t.Errorf("ListTenants() = %v, want empty", tenants)
	}
}

func TestAWSSecretsTenantStore_Close(t *testing.T) {
	store := newTestStore(&fakeSecretsManagerAPI{})

	if err := store.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestToDatabaseConfig(t *testing.T) {
	store := &AWSSecretsTenantStore{}

	t.Run("base_fields_only", func(t *testing.T) {
		secret := &SecretDatabaseConfig{
			Type:     dbTypePostgreSQL,
			Host:     "h",
			Port:     1,
			Database: "d",
			Username: "u",
			Password: "p",
		}
		got := store.toDatabaseConfig(secret)
		if got.Type != dbTypePostgreSQL || got.Host != "h" || got.Port != 1 || got.Database != "d" || got.Username != "u" || got.Password != "p" {
			t.Errorf("toDatabaseConfig() = %+v, want base fields copied", got)
		}
		if got.Pool.Max.Connections != 0 {
			t.Errorf("Pool.Max.Connections = %d, want 0 (no pool in secret)", got.Pool.Max.Connections)
		}
	})

	t.Run("pool_zero_values_not_applied", func(t *testing.T) {
		secret := &SecretDatabaseConfig{
			Type: dbTypePostgreSQL,
			Pool: &secretPoolConfig{
				Max: &secretPoolMaxConfig{Connections: 0},
			},
		}
		got := store.toDatabaseConfig(secret)
		if got.Pool.Max.Connections != 0 {
			t.Errorf("Pool.Max.Connections = %d, want 0 (zero value should not be applied)", got.Pool.Max.Connections)
		}
	})

	t.Run("pool_query_tls_oracle_fully_populated", func(t *testing.T) {
		secret := &SecretDatabaseConfig{
			Type:     dbTypePostgreSQL,
			Host:     "h",
			Port:     1,
			Database: "d",
			Username: "u",
			Password: "p",
			Pool: &secretPoolConfig{
				Max:      &secretPoolMaxConfig{Connections: 20},
				Idle:     &secretPoolIdleConfig{Connections: 5, Time: 30 * time.Minute},
				Lifetime: &secretPoolLifetimeConfig{Max: time.Hour},
			},
			Query: &secretQueryConfig{
				Slow: &secretQuerySlowConfig{Threshold: 200 * time.Millisecond, Enabled: true},
				Log:  &secretQueryLogConfig{Parameters: true, MaxLength: 500},
			},
			TLS: &secretTLSConfig{Mode: "require", CertFile: "cert.pem", KeyFile: "key.pem", CAFile: "ca.pem"},
			Oracle: &secretOracleConfig{
				Service: &secretOracleServiceConfig{Name: "XE", SID: "SID1"},
			},
		}

		got := store.toDatabaseConfig(secret)

		if got.Pool.Max.Connections != 20 {
			t.Errorf("Pool.Max.Connections = %d, want 20", got.Pool.Max.Connections)
		}
		if got.Pool.Idle.Connections != 5 {
			t.Errorf("Pool.Idle.Connections = %d, want 5", got.Pool.Idle.Connections)
		}
		if got.Pool.Idle.Time != 30*time.Minute {
			t.Errorf("Pool.Idle.Time = %v, want 30m", got.Pool.Idle.Time)
		}
		if got.Pool.Lifetime.Max != time.Hour {
			t.Errorf("Pool.Lifetime.Max = %v, want 1h", got.Pool.Lifetime.Max)
		}
		if got.Query.Slow.Threshold != 200*time.Millisecond || !got.Query.Slow.Enabled {
			t.Errorf("Query.Slow = %+v, want {200ms true}", got.Query.Slow)
		}
		if !got.Query.Log.Parameters || got.Query.Log.MaxLength != 500 {
			t.Errorf("Query.Log = %+v, want {true 500}", got.Query.Log)
		}
		if got.TLS.Mode != "require" || got.TLS.CertFile != "cert.pem" || got.TLS.KeyFile != "key.pem" || got.TLS.CAFile != "ca.pem" {
			t.Errorf("TLS = %+v, want fully populated", got.TLS)
		}
		if got.Oracle.Service.Name != "XE" || got.Oracle.Service.SID != "SID1" {
			t.Errorf("Oracle.Service = %+v, want {XE SID1}", got.Oracle.Service)
		}
	})

	t.Run("oracle_present_but_service_nil", func(t *testing.T) {
		secret := &SecretDatabaseConfig{
			Type:   dbTypeOracle,
			Oracle: &secretOracleConfig{Service: nil},
		}
		got := store.toDatabaseConfig(secret)
		if got.Oracle.Service.Name != "" {
			t.Errorf("Oracle.Service.Name = %q, want empty", got.Oracle.Service.Name)
		}
	})
}

func TestBuildSecretName(t *testing.T) {
	store := &AWSSecretsTenantStore{prefix: "/gobricks/test"}

	tests := []struct {
		tenantID   string
		configType string
		want       string
	}{
		{tenant1ID, "database", "/gobricks/test/tenant1/database"},
		{"tenant-foo", "messaging", "/gobricks/test/tenant-foo/messaging"},
		{"test123", "cache", "/gobricks/test/test123/cache"},
	}

	for _, tt := range tests {
		t.Run(tt.tenantID+"_"+tt.configType, func(t *testing.T) {
			got := store.buildSecretName(tt.tenantID, tt.configType)
			if got != tt.want {
				t.Errorf("buildSecretName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewAWSSecretsTenantStore_EmptyPrefix(t *testing.T) {
	_, err := NewAWSSecretsTenantStore(context.Background(), testLoggerAWS(), AWSSecretsConfig{})
	if err == nil {
		t.Fatal("NewAWSSecretsTenantStore() expected error for empty prefix")
	}
	wantSubstr := "prefix cannot be empty"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error = %q, want to contain %q", err.Error(), wantSubstr)
	}
}

func TestNewAWSSecretsTenantStore_Defaults(t *testing.T) {
	store, err := NewAWSSecretsTenantStore(context.Background(), testLoggerAWS(), AWSSecretsConfig{Prefix: testSecretsPrefix})
	if err != nil {
		t.Fatalf("NewAWSSecretsTenantStore() error = %v", err)
	}
	defer store.Close()

	if store.prefix != testSecretsPrefix {
		t.Errorf("prefix = %q, want /gobricks/demo", store.prefix)
	}
	if store.cache.ttl != 5*time.Minute {
		t.Errorf("cache ttl = %v, want 5m (default)", store.cache.ttl)
	}
	if store.cache.maxSize != 1000 {
		t.Errorf("cache maxSize = %d, want 1000 (default)", store.cache.maxSize)
	}
}

func TestNewAWSSecretsTenantStore_CustomCacheSettings(t *testing.T) {
	store, err := NewAWSSecretsTenantStore(context.Background(), testLoggerAWS(), AWSSecretsConfig{
		Prefix:  testSecretsPrefix,
		Cache:   30 * time.Second,
		MaxSize: 50,
	})
	if err != nil {
		t.Fatalf("NewAWSSecretsTenantStore() error = %v", err)
	}
	defer store.Close()

	if store.cache.ttl != 30*time.Second {
		t.Errorf("cache ttl = %v, want 30s", store.cache.ttl)
	}
	if store.cache.maxSize != 50 {
		t.Errorf("cache maxSize = %d, want 50", store.cache.maxSize)
	}
}

func TestLoadAWSConfig_CustomEndpoint(t *testing.T) {
	cfg, err := loadAWSConfig(AWSSecretsConfig{EndpointURL: "http://localhost:4566"}, context.Background())
	if err != nil {
		t.Fatalf("loadAWSConfig() error = %v", err)
	}
	if cfg.BaseEndpoint == nil || *cfg.BaseEndpoint != "http://localhost:4566" {
		t.Errorf("BaseEndpoint = %v, want http://localhost:4566", cfg.BaseEndpoint)
	}
}

func TestLoadAWSConfig_NoEndpoint(t *testing.T) {
	cfg, err := loadAWSConfig(AWSSecretsConfig{}, context.Background())
	if err != nil {
		t.Fatalf("loadAWSConfig() error = %v", err)
	}
	if cfg.BaseEndpoint != nil {
		t.Errorf("BaseEndpoint = %v, want nil", *cfg.BaseEndpoint)
	}
}

// TestAWSSecretsTenantStore_DBConfig_DecodesNestedSections pins the secret's
// JSON contract end to end: every nested section (pool, query, tls, oracle)
// is decoded from the stored secret and lands on the go-bricks config.
// Durations are JSON integers in nanoseconds, as time.Duration unmarshals them.
func TestAWSSecretsTenantStore_DBConfig_DecodesNestedSections(t *testing.T) {
	const secret = `{
		"type": "oracle",
		"host": "oracle-tenant1",
		"port": 1521,
		"database": "XE",
		"username": "tenant1_user",
		"password": "tenant1_pass",
		"pool": {
			"max": {"connections": 20},
			"idle": {"connections": 5, "time": 1800000000000},
			"lifetime": {"max": 3600000000000}
		},
		"query": {
			"slow": {"threshold": 200000000, "enabled": true},
			"log": {"parameters": true, "max": 500}
		},
		"tls": {"mode": "verify-full", "cert": "client.crt", "key": "client.key", "ca": "root.crt"},
		"oracle": {"service": {"name": "XEPDB1", "sid": "ORCL"}}
	}`
	fake := &fakeSecretsManagerAPI{
		getSecretValueFunc: func(context.Context, *secretsmanager.GetSecretValueInput) (*secretsmanager.GetSecretValueOutput, error) {
			return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(secret)}, nil
		},
	}

	got, err := newTestStore(fake).DBConfig(context.Background(), tenant1ID)
	if err != nil {
		t.Fatalf("DBConfig() error = %v", err)
	}

	if got.Pool.Max.Connections != 20 || got.Pool.Idle.Connections != 5 ||
		got.Pool.Idle.Time != 30*time.Minute || got.Pool.Lifetime.Max != time.Hour {
		t.Errorf("Pool = %+v, want max 20, idle 5/30m, lifetime 1h", got.Pool)
	}
	if got.Query.Slow.Threshold != 200*time.Millisecond || !got.Query.Slow.Enabled {
		t.Errorf("Query.Slow = %+v, want {200ms true}", got.Query.Slow)
	}
	if !got.Query.Log.Parameters || got.Query.Log.MaxLength != 500 {
		t.Errorf("Query.Log = %+v, want {true 500}", got.Query.Log)
	}
	if got.TLS.Mode != "verify-full" || got.TLS.CertFile != "client.crt" || got.TLS.KeyFile != "client.key" || got.TLS.CAFile != "root.crt" {
		t.Errorf("TLS = %+v, want every field decoded", got.TLS)
	}
	if got.Oracle.Service.Name != "XEPDB1" || got.Oracle.Service.SID != "ORCL" {
		t.Errorf("Oracle.Service = %+v, want {XEPDB1 ORCL}", got.Oracle.Service)
	}
}
