package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	gobricksConfig "github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
)

type AWSSecretsConfig struct {
	Prefix      string        `json:"prefix" koanf:"custom.aws.secrets.prefix"`
	Cache       time.Duration `json:"cache" koanf:"custom.aws.secrets.cache.ttl"`
	MaxSize     int           `json:"max" koanf:"custom.aws.secrets.cache.max.size"`
	EndpointURL string        `json:"endpoint_url" koanf:"custom.aws.endpoint.url"`
}

// AWSSecretsTenantStore implements the database.TenantStore interface
// using AWS Secrets Manager as the configuration source with intelligent caching
type AWSSecretsTenantStore struct {
	client SecretsManagerAPI
	cache  *Cache
	prefix string
	logger logger.Logger
	mu     sync.RWMutex
}

// SecretsManagerAPI defines the interface for AWS Secrets Manager operations
// This allows for easy mocking and testing
type SecretsManagerAPI interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
}

// SecretDatabaseConfig represents the structure of database configuration stored in AWS Secrets Manager
type SecretDatabaseConfig struct {
	Type     string              `json:"type"`
	Host     string              `json:"host"`
	Port     int                 `json:"port"`
	Database string              `json:"database"`
	Username string              `json:"username"`
	Password string              `json:"password"`
	Pool     *secretPoolConfig   `json:"pool,omitempty"`
	Query    *secretQueryConfig  `json:"query,omitempty"`
	TLS      *secretTLSConfig    `json:"tls,omitempty"`
	Oracle   *secretOracleConfig `json:"oracle,omitempty"`
}

type secretPoolConfig struct {
	Max      *secretPoolMaxConfig      `json:"max"`
	Idle     *secretPoolIdleConfig     `json:"idle"`
	Lifetime *secretPoolLifetimeConfig `json:"lifetime"`
}

type secretPoolMaxConfig struct {
	Connections int32 `json:"connections"`
}

type secretPoolIdleConfig struct {
	Connections int32         `json:"connections"`
	Time        time.Duration `json:"time"`
}

type secretPoolLifetimeConfig struct {
	Max time.Duration `json:"max"`
}

type secretQueryConfig struct {
	Slow *secretQuerySlowConfig `json:"slow"`
	Log  *secretQueryLogConfig  `json:"log"`
}

type secretQuerySlowConfig struct {
	Threshold time.Duration `json:"threshold"`
	Enabled   bool          `json:"enabled"`
}

type secretQueryLogConfig struct {
	Parameters bool `json:"parameters"`
	MaxLength  int  `json:"max"`
}

type secretTLSConfig struct {
	Mode     string `json:"mode"`
	CertFile string `json:"cert"`
	KeyFile  string `json:"key"`
	CAFile   string `json:"ca"`
}

type secretOracleConfig struct {
	Service *secretOracleServiceConfig `json:"service"`
}

type secretOracleServiceConfig struct {
	Name string `json:"name"`
	SID  string `json:"sid"`
}

// NewAWSSecretsTenantStore creates a new AWS Secrets Manager-backed tenant store
func NewAWSSecretsTenantStore(ctx context.Context, logger logger.Logger, cfg AWSSecretsConfig) (*AWSSecretsTenantStore, error) {
	if cfg.Prefix == "" {
		return nil, fmt.Errorf("AWS Secrets Manager prefix cannot be empty")
	}
	// Load AWS configuration
	awsConfig, err := loadAWSConfig(cfg, ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create Secrets Manager client
	client := secretsmanager.NewFromConfig(awsConfig)

	// Extract configuration from the config
	prefix := cfg.Prefix
	cacheTTL := 5 * time.Minute
	cacheMaxSize := 1000

	if cfg.Cache > 0 {
		cacheTTL = cfg.Cache
	}
	if cfg.MaxSize > 0 {
		cacheMaxSize = cfg.MaxSize
	}

	logger.Info().
		Str("prefix", prefix).
		Dur("cache_ttl", cacheTTL).
		Int("cache_max_size", cacheMaxSize).
		Msg("Initializing AWS Secrets Manager tenant store")

	return &AWSSecretsTenantStore{
		client: client,
		cache:  NewCache(cacheTTL, cacheMaxSize),
		prefix: prefix,
		logger: logger,
	}, nil
}

// DBConfig implements the database.TenantStore interface
// It retrieves database configuration for a specific tenant from AWS Secrets Manager
func (s *AWSSecretsTenantStore) DBConfig(ctx context.Context, tenantID string) (*gobricksConfig.DatabaseConfig, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant ID cannot be empty")
	}

	// Check cache first
	cacheKey := fmt.Sprintf("db_%s", tenantID)
	if cached := s.cache.Get(cacheKey); cached != nil {
		s.logger.Debug().
			Str("tenant_id", tenantID).
			Msg("Retrieved database config from cache")
		return cached.(*gobricksConfig.DatabaseConfig), nil
	}

	// Cache miss - fetch from AWS Secrets Manager
	s.logger.Debug().
		Str("tenant_id", tenantID).
		Msg("Cache miss - fetching database config from AWS Secrets Manager")

	config, err := s.fetchDatabaseConfig(ctx, tenantID)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("tenant_id", tenantID).
			Msg("Failed to fetch database config from AWS Secrets Manager")
		return nil, err
	}

	// Cache the result
	s.cache.Set(cacheKey, config)

	s.logger.Info().
		Str("tenant_id", tenantID).
		Str("db_type", config.Type).
		Str("host", config.Host).
		Int("port", config.Port).
		Msg("Successfully retrieved and cached database config")

	return config, nil
}

// fetchDatabaseConfig retrieves and parses database configuration from AWS Secrets Manager
func (s *AWSSecretsTenantStore) fetchDatabaseConfig(ctx context.Context, tenantID string) (*gobricksConfig.DatabaseConfig, error) {
	secretName := s.buildSecretName(tenantID, "database")

	// Fetch secret from AWS Secrets Manager
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	result, err := s.client.GetSecretValue(ctx, input)
	if err != nil {
		// Check if it's a resource not found error
		var notFoundError *types.InvalidParameterException
		var decryptError *types.DecryptionFailure
		var internalServiceError *types.InternalServiceError
		var invalidRequestError *types.InvalidRequestException
		if errors.As(err, &notFoundError) {
			return nil, fmt.Errorf("secret not found for tenant %s (secret: %s): %w", tenantID, secretName, err)
		}
		if errors.As(err, &decryptError) || errors.As(err, &internalServiceError) || errors.As(err, &invalidRequestError) {
			return nil, fmt.Errorf("error retrieving secret for tenant %s (secret: %s): %w", tenantID, secretName, err)
		}
		// Other errors
		return nil, fmt.Errorf("failed to retrieve secret for tenant %s: %w", tenantID, err)
	}

	if result.SecretString == nil {
		return nil, fmt.Errorf("secret value is empty for tenant %s", tenantID)
	}

	// Parse the secret JSON
	var secretConfig SecretDatabaseConfig
	if err := json.Unmarshal([]byte(*result.SecretString), &secretConfig); err != nil {
		return nil, fmt.Errorf("failed to parse secret JSON for tenant %s: %w", tenantID, err)
	}

	// Convert to go-bricks DatabaseConfig
	return s.toDatabaseConfig(&secretConfig), nil
}

// toDatabaseConfig converts SecretDatabaseConfig to go-bricks DatabaseConfig
func (s *AWSSecretsTenantStore) toDatabaseConfig(secret *SecretDatabaseConfig) *gobricksConfig.DatabaseConfig {
	config := &gobricksConfig.DatabaseConfig{
		Type:     secret.Type,
		Host:     secret.Host,
		Port:     secret.Port,
		Database: secret.Database,
		Username: secret.Username,
		Password: secret.Password,
	}

	applyPoolConfig(config, secret.Pool)
	applyQueryConfig(config, secret.Query)
	applyTLSConfig(config, secret.TLS)
	applyOracleConfig(config, secret.Oracle)

	return config
}

// applyPoolConfig copies non-zero connection pool settings from the secret into config.
func applyPoolConfig(config *gobricksConfig.DatabaseConfig, pool *secretPoolConfig) {
	if pool == nil {
		return
	}
	if pool.Max != nil && pool.Max.Connections > 0 {
		config.Pool.Max.Connections = pool.Max.Connections
	}
	if pool.Idle != nil {
		if pool.Idle.Connections > 0 {
			config.Pool.Idle.Connections = pool.Idle.Connections
		}
		if pool.Idle.Time > 0 {
			config.Pool.Idle.Time = pool.Idle.Time
		}
	}
	if pool.Lifetime != nil && pool.Lifetime.Max > 0 {
		config.Pool.Lifetime.Max = pool.Lifetime.Max
	}
}

// applyQueryConfig copies slow-query and logging settings from the secret into config.
func applyQueryConfig(config *gobricksConfig.DatabaseConfig, query *secretQueryConfig) {
	if query == nil {
		return
	}
	if query.Slow != nil {
		config.Query.Slow.Threshold = query.Slow.Threshold
		config.Query.Slow.Enabled = query.Slow.Enabled
	}
	if query.Log != nil {
		config.Query.Log.Parameters = query.Log.Parameters
		config.Query.Log.MaxLength = query.Log.MaxLength
	}
}

// applyTLSConfig copies TLS settings from the secret into config.
func applyTLSConfig(config *gobricksConfig.DatabaseConfig, tls *secretTLSConfig) {
	if tls == nil {
		return
	}
	config.TLS.Mode = tls.Mode
	config.TLS.CertFile = tls.CertFile
	config.TLS.KeyFile = tls.KeyFile
	config.TLS.CAFile = tls.CAFile
}

// applyOracleConfig copies Oracle service settings from the secret into config.
func applyOracleConfig(config *gobricksConfig.DatabaseConfig, oracle *secretOracleConfig) {
	if oracle == nil || oracle.Service == nil {
		return
	}
	config.Oracle.Service.Name = oracle.Service.Name
	config.Oracle.Service.SID = oracle.Service.SID
}

// buildSecretName constructs the full secret name based on prefix, tenant ID, and config type
func (s *AWSSecretsTenantStore) buildSecretName(tenantID, configType string) string {
	return fmt.Sprintf("%s/%s/%s", s.prefix, tenantID, configType)
}

// ListTenants returns a list of all configured tenants by listing secrets with the correct prefix
func (s *AWSSecretsTenantStore) ListTenants(ctx context.Context) ([]string, error) {
	prefix := fmt.Sprintf("%s/", s.prefix)

	var tenants []string
	var nextToken *string

	for {
		pageTenants, next, err := s.fetchTenantIDPage(ctx, prefix, nextToken)
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, pageTenants...)
		if next == nil {
			break
		}
		nextToken = next
	}

	s.logger.Debug().
		Int("tenant_count", len(tenants)).
		Str("tenants", strings.Join(tenants, ", ")).
		Msg("Listed tenants from AWS Secrets Manager")

	return tenants, nil
}

// fetchTenantIDPage fetches a single page of secrets matching prefix and
// returns the tenant IDs found on that page along with AWS's pagination token.
func (s *AWSSecretsTenantStore) fetchTenantIDPage(ctx context.Context, prefix string, nextToken *string) (tenantIDs []string, nextPageToken *string, err error) {
	input := &secretsmanager.ListSecretsInput{
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{prefix},
			},
		},
	}
	if nextToken != nil {
		input.NextToken = nextToken
	}

	result, err := s.client.ListSecrets(ctx, input)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	for _, secret := range result.SecretList {
		if secret.Name == nil {
			continue
		}
		if tenantID := tenantIDFromSecretName(*secret.Name, prefix); tenantID != "" {
			tenantIDs = append(tenantIDs, tenantID)
		}
	}

	return tenantIDs, result.NextToken, nil
}

// tenantIDFromSecretName extracts the tenant ID from a "<prefix>/<tenantID>/database"
// secret name, returning "" if the name doesn't match that pattern.
func tenantIDFromSecretName(secretName, prefix string) string {
	if !strings.HasSuffix(secretName, "/database") {
		return ""
	}
	tenantPart := strings.TrimPrefix(secretName, prefix)
	return strings.TrimSuffix(tenantPart, "/database")
}

// InvalidateCache removes a specific tenant's configuration from the cache
func (s *AWSSecretsTenantStore) InvalidateCache(tenantID string) {
	cacheKey := fmt.Sprintf("db_%s", tenantID)
	s.cache.Delete(cacheKey)
	s.logger.Debug().
		Str("tenant_id", tenantID).
		Msg("Invalidated tenant cache")
}

// ClearCache removes all cached configurations
func (s *AWSSecretsTenantStore) ClearCache() {
	s.cache.Clear()
	s.logger.Debug().Msg("Cleared all tenant cache")
}

// CacheMetrics returns current cache performance metrics
func (s *AWSSecretsTenantStore) CacheMetrics() CacheMetrics {
	return s.cache.Metrics()
}

// Close releases resources used by the tenant store
func (s *AWSSecretsTenantStore) Close() error {
	s.cache.Close()
	s.logger.Debug().Msg("Closed AWS Secrets Manager tenant store")
	return nil
}

// loadAWSConfig loads AWS configuration with support for custom endpoint (LocalStack)
func loadAWSConfig(cfg AWSSecretsConfig, ctx context.Context) (aws.Config, error) {
	result, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return result, err
	}

	// Support LocalStack or other custom endpoints
	if endpoint := cfg.EndpointURL; endpoint != "" {
		result.BaseEndpoint = aws.String(endpoint)
	}

	return result, nil
}
