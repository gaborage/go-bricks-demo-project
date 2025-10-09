package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks/logger"
)

// mockRepository implements repository methods for testing
type mockRepository struct {
	createFunc  func(ctx context.Context, product *domain.Product) error
	getByIDFunc func(ctx context.Context, id string) (*domain.Product, error)
	listFunc    func(ctx context.Context, limit, offset int) ([]*domain.Product, int, error)
	updateFunc  func(ctx context.Context, id string, updates map[string]any) error
	deleteFunc  func(ctx context.Context, id string) error
}

func (m *mockRepository) Create(ctx context.Context, product *domain.Product) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, product)
	}
	return nil
}

func (m *mockRepository) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockRepository) List(ctx context.Context, limit, offset int) ([]*domain.Product, int, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, limit, offset)
	}
	return nil, 0, errors.New("not implemented")
}

func (m *mockRepository) Update(ctx context.Context, id string, updates map[string]any) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, id, updates)
	}
	return nil
}

func (m *mockRepository) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

func newMockLogger() logger.Logger {
	return logger.New("info", false)
}

func TestCreateProduct(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name        string
		productName string
		description string
		price       float64
		imageURL    string
		repoErr     error
		wantErr     bool
		errContains string
	}{
		{
			name:        "successful create",
			productName: "Test Product",
			description: "Test Description",
			price:       99.99,
			imageURL:    "https://example.com/image.jpg",
			repoErr:     nil,
			wantErr:     false,
		},
		{
			name:        "empty name",
			productName: "",
			description: "Test Description",
			price:       99.99,
			imageURL:    "",
			wantErr:     true,
			errContains: "required",
		},
		{
			name:        "name too long",
			productName: strings.Repeat("a", 151),
			description: "Test Description",
			price:       99.99,
			imageURL:    "",
			wantErr:     true,
			errContains: "150 characters",
		},
		{
			name:        "negative price",
			productName: "Test Product",
			description: "Test Description",
			price:       -10.00,
			imageURL:    "",
			wantErr:     true,
			errContains: "non-negative",
		},
		{
			name:        "invalid URL scheme",
			productName: "Test Product",
			description: "Test Description",
			price:       99.99,
			imageURL:    "ftp://example.com/image.jpg",
			wantErr:     true,
			errContains: "invalid image URL",
		},
		{
			name:        "invalid URL format",
			productName: "Test Product",
			description: "Test Description",
			price:       99.99,
			imageURL:    "not a url",
			wantErr:     true,
			errContains: "invalid image URL",
		},
		{
			name:        "repository error",
			productName: "Test Product",
			description: "Test Description",
			price:       99.99,
			imageURL:    "https://example.com/image.jpg",
			repoErr:     errors.New("database error"),
			wantErr:     true,
			errContains: "failed to create product",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockRepository{
				createFunc: func(ctx context.Context, product *domain.Product) error {
					return tt.repoErr
				},
			}

			service := &ProductService{
				repository: mockRepo,
				logger:     log,
			}

			product, err := service.CreateProduct(ctx, tt.productName, tt.description, tt.price, tt.imageURL)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CreateProduct() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("CreateProduct() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("CreateProduct() unexpected error = %v", err)
				return
			}

			if product.Name != tt.productName {
				t.Errorf("CreateProduct() name = %v, want %v", product.Name, tt.productName)
			}
			if product.Price != tt.price {
				t.Errorf("CreateProduct() price = %v, want %v", product.Price, tt.price)
			}
		})
	}
}

func TestGetProductByID(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name      string
		id        string
		repoError error
		wantErr   bool
		wantName  string
	}{
		{
			name:      "successful get",
			id:        "test-id",
			repoError: nil,
			wantErr:   false,
			wantName:  "Test Product",
		},
		{
			name:      "product not found",
			id:        "missing-id",
			repoError: repository.ErrProductNotFound,
			wantErr:   true,
		},
		{
			name:      "repository error",
			id:        "test-id",
			repoError: errors.New("database error"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockRepository{
				getByIDFunc: func(ctx context.Context, id string) (*domain.Product, error) {
					if tt.repoError != nil {
						return nil, tt.repoError
					}
					return domain.New(id, "Test Product", "Description", 99.99, "https://example.com/image.jpg"), nil
				},
			}

			service := &ProductService{
				repository: mockRepo,
				logger:     log,
			}

			product, err := service.GetProductByID(ctx, tt.id)

			if tt.wantErr {
				if err == nil {
					t.Errorf("GetProductByID() error = nil, wantErr %v", tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("GetProductByID() unexpected error = %v", err)
				return
			}

			if product.Name != tt.wantName {
				t.Errorf("GetProductByID() name = %v, want %v", product.Name, tt.wantName)
			}
		})
	}
}

func TestListProducts(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name        string
		page        int
		pageSize    int
		repoError   error
		wantErr     bool
		errContains string
		wantTotal   int
		wantCount   int
	}{
		{
			name:      "successful list",
			page:      1,
			pageSize:  10,
			repoError: nil,
			wantErr:   false,
			wantTotal: 5,
			wantCount: 5,
		},
		{
			name:        "invalid page (zero)",
			page:        0,
			pageSize:    10,
			wantErr:     true,
			errContains: "greater than 0",
		},
		{
			name:        "invalid page (negative)",
			page:        -1,
			pageSize:    10,
			wantErr:     true,
			errContains: "greater than 0",
		},
		{
			name:        "invalid pageSize (zero)",
			page:        1,
			pageSize:    0,
			wantErr:     true,
			errContains: "between 1 and 100",
		},
		{
			name:        "invalid pageSize (too large)",
			page:        1,
			pageSize:    101,
			wantErr:     true,
			errContains: "between 1 and 100",
		},
		{
			name:      "repository error",
			page:      1,
			pageSize:  10,
			repoError: errors.New("database error"),
			wantErr:   true,
		},
		{
			name:      "page 2 offset calculation",
			page:      2,
			pageSize:  10,
			repoError: nil,
			wantErr:   false,
			wantTotal: 25,
			wantCount: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockRepository{
				listFunc: func(ctx context.Context, limit, offset int) ([]*domain.Product, int, error) {
					if tt.repoError != nil {
						return nil, 0, tt.repoError
					}

					// Verify offset calculation
					expectedOffset := (tt.page - 1) * tt.pageSize
					if offset != expectedOffset {
						t.Errorf("List() offset = %v, want %v", offset, expectedOffset)
					}

					// Create mock products
					products := make([]*domain.Product, tt.wantCount)
					for i := 0; i < tt.wantCount; i++ {
						products[i] = domain.New("id", "Product", "Description", 99.99, "")
					}
					return products, tt.wantTotal, nil
				},
			}

			service := &ProductService{
				repository: mockRepo,
				logger:     log,
			}

			products, total, err := service.ListProducts(ctx, tt.page, tt.pageSize)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ListProducts() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ListProducts() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("ListProducts() unexpected error = %v", err)
				return
			}

			if total != tt.wantTotal {
				t.Errorf("ListProducts() total = %v, want %v", total, tt.wantTotal)
			}

			if len(products) != tt.wantCount {
				t.Errorf("ListProducts() count = %v, want %v", len(products), tt.wantCount)
			}
		})
	}
}

func TestUpdateProduct(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	name := "Updated Product"
	price := 149.99
	invalidURL := "not a url"

	tests := []struct {
		name        string
		id          string
		updateName  *string
		updatePrice *float64
		updateURL   *string
		updateErr   error
		getByIDErr  error
		wantErr     bool
		errContains string
	}{
		{
			name:        "successful update name and price",
			id:          "test-id",
			updateName:  &name,
			updatePrice: &price,
			updateErr:   nil,
			getByIDErr:  nil,
			wantErr:     false,
		},
		{
			name:        "no fields to update",
			id:          "test-id",
			wantErr:     true,
			errContains: "no fields to update",
		},
		{
			name:       "product not found",
			id:         "missing-id",
			updateName: &name,
			updateErr:  repository.ErrProductNotFound,
			wantErr:    true,
		},
		{
			name:        "invalid URL",
			id:          "test-id",
			updateURL:   &invalidURL,
			wantErr:     true,
			errContains: "invalid image URL",
		},
		{
			name:       "repository error on fetch",
			id:         "test-id",
			updateName: &name,
			updateErr:  nil,
			getByIDErr: errors.New("database error"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockRepository{
				updateFunc: func(ctx context.Context, id string, updates map[string]any) error {
					return tt.updateErr
				},
				getByIDFunc: func(ctx context.Context, id string) (*domain.Product, error) {
					if tt.getByIDErr != nil {
						return nil, tt.getByIDErr
					}
					return domain.New(id, "Updated Product", "Description", 149.99, "https://example.com/image.jpg"), nil
				},
			}

			service := &ProductService{
				repository: mockRepo,
				logger:     log,
			}

			product, err := service.UpdateProduct(ctx, tt.id, tt.updateName, nil, tt.updatePrice, tt.updateURL)

			if tt.wantErr {
				if err == nil {
					t.Errorf("UpdateProduct() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("UpdateProduct() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("UpdateProduct() unexpected error = %v", err)
				return
			}

			if product == nil {
				t.Errorf("UpdateProduct() returned nil product")
			}
		})
	}
}

func TestDeleteProduct(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name    string
		id      string
		repoErr error
		wantErr bool
	}{
		{
			name:    "successful delete",
			id:      "test-id",
			repoErr: nil,
			wantErr: false,
		},
		{
			name:    "product not found",
			id:      "missing-id",
			repoErr: repository.ErrProductNotFound,
			wantErr: true,
		},
		{
			name:    "repository error",
			id:      "test-id",
			repoErr: errors.New("database error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockRepository{
				deleteFunc: func(ctx context.Context, id string) error {
					return tt.repoErr
				},
			}

			service := &ProductService{
				repository: mockRepo,
				logger:     log,
			}

			err := service.DeleteProduct(ctx, tt.id)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DeleteProduct() error = nil, wantErr %v", tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("DeleteProduct() unexpected error = %v", err)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name        string
		productName string
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid name",
			productName: "Test Product",
			wantErr:     false,
		},
		{
			name:        "empty name",
			productName: "",
			wantErr:     true,
			errContains: "required",
		},
		{
			name:        "whitespace only",
			productName: "   ",
			wantErr:     true,
			errContains: "required",
		},
		{
			name:        "name too long",
			productName: strings.Repeat("a", 151),
			wantErr:     true,
			errContains: "150 characters",
		},
		{
			name:        "name exactly 150 chars",
			productName: strings.Repeat("a", 150),
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.productName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateName() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateName() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("validateName() unexpected error = %v", err)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid http URL",
			url:     "http://example.com/image.jpg",
			wantErr: false,
		},
		{
			name:    "valid https URL",
			url:     "https://example.com/image.jpg",
			wantErr: false,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: false,
		},
		{
			name:        "invalid scheme",
			url:         "ftp://example.com/image.jpg",
			wantErr:     true,
			errContains: "http or https",
		},
		{
			name:        "no scheme",
			url:         "example.com/image.jpg",
			wantErr:     true,
			errContains: "http or https",
		},
		{
			name:        "invalid URL format",
			url:         "not a url",
			wantErr:     true,
			errContains: "http or https",
		},
		{
			name:        "missing host",
			url:         "https://",
			wantErr:     true,
			errContains: "valid host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateURL() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateURL() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("validateURL() unexpected error = %v", err)
			}
		})
	}
}
