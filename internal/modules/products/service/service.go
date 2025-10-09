package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks/logger"
	"github.com/google/uuid"
)

type ProductService struct {
	repository repository.Repository
	logger     logger.Logger
}

func NewService(repo repository.Repository, log logger.Logger) *ProductService {
	return &ProductService{
		repository: repo,
		logger:     log,
	}
}

// CreateProduct creates a new product with validation
func (s *ProductService) CreateProduct(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error) {
	// Validate name
	if err := validateName(name); err != nil {
		return nil, err
	}

	// Validate price
	if price < 0 {
		return nil, fmt.Errorf("price must be non-negative")
	}

	// Validate image URL if provided
	if imageURL != "" {
		if err := validateURL(imageURL); err != nil {
			return nil, fmt.Errorf("invalid image URL: %w", err)
		}
	}

	// Generate UUID for new product
	id := uuid.New().String()

	// Create product domain object
	product := domain.New(id, name, description, price, imageURL)

	// Validate domain object
	if err := product.Validate(); err != nil {
		return nil, err
	}

	// Persist to repository
	if err := s.repository.Create(ctx, product); err != nil {
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to create product")
		return nil, fmt.Errorf("failed to create product: %w", err)
	}

	s.logger.Info().Str("productID", id).Str("name", name).Msg("Product created successfully")
	return product, nil
}

// GetProductByID retrieves a product by its ID
func (s *ProductService) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	product, err := s.repository.GetByID(ctx, id)
	if err != nil {
		if err == repository.ErrProductNotFound {
			return nil, err
		}
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to get product")
		return nil, fmt.Errorf("failed to get product: %w", err)
	}

	return product, nil
}

// validateName checks if the product name is valid
func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("product name is required")
	}
	if len(name) > 150 {
		return fmt.Errorf("product name must be less than 150 characters")
	}
	return nil
}

// validateURL checks if the URL is valid
func validateURL(urlStr string) error {
	if urlStr == "" {
		return nil
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return err
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme")
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must have a valid host")
	}

	return nil
}

// ListProducts retrieves a paginated list of products
func (s *ProductService) ListProducts(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error) {
	// Validate pagination parameters
	if page < 1 {
		return nil, 0, fmt.Errorf("page must be greater than 0")
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, 0, fmt.Errorf("pageSize must be between 1 and 100")
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Fetch from repository
	products, total, err := s.repository.List(ctx, pageSize, offset)
	if err != nil {
		s.logger.Error().Err(err).Int("page", page).Int("pageSize", pageSize).Msg("Failed to list products")
		return nil, 0, fmt.Errorf("failed to list products: %w", err)
	}

	return products, total, nil
}

// UpdateProduct performs a partial update on a product
func (s *ProductService) UpdateProduct(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error) {
	// Build update map with only provided fields
	updates := make(map[string]any)

	if name != nil {
		if err := validateName(*name); err != nil {
			return nil, err
		}
		updates["name"] = *name
	}

	if description != nil {
		updates["description"] = *description
	}

	if price != nil {
		if *price < 0 {
			return nil, fmt.Errorf("price must be non-negative")
		}
		updates["price"] = *price
	}

	if imageURL != nil {
		if *imageURL != "" {
			if err := validateURL(*imageURL); err != nil {
				return nil, fmt.Errorf("invalid image URL: %w", err)
			}
		}
		updates["image_url"] = *imageURL
	}

	// Return error if no fields to update
	if len(updates) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	// Always update the updated_date
	updates["updated_date"] = "NOW()"

	// Perform update in repository
	if err := s.repository.Update(ctx, id, updates); err != nil {
		if err == repository.ErrProductNotFound {
			return nil, err
		}
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to update product")
		return nil, fmt.Errorf("failed to update product: %w", err)
	}

	// Fetch and return updated product
	product, err := s.repository.GetByID(ctx, id)
	if err != nil {
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to fetch updated product")
		return nil, fmt.Errorf("failed to fetch updated product: %w", err)
	}

	s.logger.Info().Str("productID", id).Msg("Product updated successfully")
	return product, nil
}

// DeleteProduct removes a product
func (s *ProductService) DeleteProduct(ctx context.Context, id string) error {
	if err := s.repository.Delete(ctx, id); err != nil {
		if err == repository.ErrProductNotFound {
			return err
		}
		s.logger.Error().Err(err).Str("productID", id).Msg("Failed to delete product")
		return fmt.Errorf("failed to delete product: %w", err)
	}

	s.logger.Info().Str("productID", id).Msg("Product deleted successfully")
	return nil
}
