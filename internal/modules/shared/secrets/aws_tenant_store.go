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
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	Pool     *struct {
		Max *struct {
			Connections int32 `json:"connections"`
		} `json:"max"`
		Idle *struct {
			Connections int32         `json:"connections"`
			Time        time.Duration `json:"time"`
		} `json:"idle"`
		Lifetime *struct {
			Max time.Duration `json:"max"`
		} `json:"lifetime"`
	} `json:"pool,omitempty"`
	Query *struct {
		Slow *struct {
			Threshold time.Duration `json:"threshold"`
			Enabled   bool          `json:"enabled"`
		} `json:"slow"`
		Log *struct {
			Parameters bool `json:"parameters"`
			MaxLength  int  `json:"max"`
		} `json:"log"`
	} `json:"query,omitempty"`
	TLS *struct {
		Mode     string `json:"mode"`
		CertFile string `json:"cert"`
		KeyFile  string `json:"key"`
		CAFile   string `json:"ca"`
	} `json:"tls,omitempty"`
	Oracle *struct {
		Service *struct {
			Name string `json:"name"`
			SID  string `json:"sid"`
		} `json:"service"`
	} `json:"oracle,omitempty"`
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

	// Set pool configuration if provided
	if secret.Pool != nil {
		if secret.Pool.Max != nil && secret.Pool.Max.Connections > 0 {
			config.Pool.Max.Connections = secret.Pool.Max.Connections
		}
		if secret.Pool.Idle != nil {
			if secret.Pool.Idle.Connections > 0 {
				config.Pool.Idle.Connections = secret.Pool.Idle.Connections
			}
			if secret.Pool.Idle.Time > 0 {
				config.Pool.Idle.Time = secret.Pool.Idle.Time
			}
		}
		if secret.Pool.Lifetime != nil && secret.Pool.Lifetime.Max > 0 {
			config.Pool.Lifetime.Max = secret.Pool.Lifetime.Max
		}
	}

	// Set query configuration if provided
	if secret.Query != nil {
		if secret.Query.Slow != nil {
			config.Query.Slow.Threshold = secret.Query.Slow.Threshold
			config.Query.Slow.Enabled = secret.Query.Slow.Enabled
		}
		if secret.Query.Log != nil {
			config.Query.Log.Parameters = secret.Query.Log.Parameters
			config.Query.Log.MaxLength = secret.Query.Log.MaxLength
		}
	}

	// Set TLS configuration if provided
	if secret.TLS != nil {
		config.TLS.Mode = secret.TLS.Mode
		config.TLS.CertFile = secret.TLS.CertFile
		config.TLS.KeyFile = secret.TLS.KeyFile
		config.TLS.CAFile = secret.TLS.CAFile
	}

	// Set Oracle-specific configuration if provided
	if secret.Oracle != nil && secret.Oracle.Service != nil {
		config.Oracle.Service.Name = secret.Oracle.Service.Name
		config.Oracle.Service.SID = secret.Oracle.Service.SID
	}

	return config
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
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}

		for _, secret := range result.SecretList {
			if secret.Name != nil && strings.HasSuffix(*secret.Name, "/database") {
				// Extract tenant ID from secret name
				secretName := *secret.Name
				tenantPart := strings.TrimPrefix(secretName, prefix)
				tenantID := strings.TrimSuffix(tenantPart, "/database")
				if tenantID != "" {
					tenants = append(tenants, tenantID)
				}
			}
		}

		if result.NextToken == nil {
			break
		}
		nextToken = result.NextToken
	}

	s.logger.Debug().
		Int("tenant_count", len(tenants)).
		Str("tenants", strings.Join(tenants, ", ")).
		Msg("Listed tenants from AWS Secrets Manager")

	return tenants, nil
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
