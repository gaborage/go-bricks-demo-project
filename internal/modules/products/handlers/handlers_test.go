package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/labstack/echo/v4"
)

// mockService implements service methods for testing
type mockService struct {
	createProductFunc  func(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error)
	getProductByIDFunc func(ctx context.Context, id string) (*domain.Product, error)
	listProductsFunc   func(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error)
	updateProductFunc  func(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error)
	deleteProductFunc  func(ctx context.Context, id string) error
}

func (m *mockService) CreateProduct(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error) {
	if m.createProductFunc != nil {
		return m.createProductFunc(ctx, name, description, price, imageURL)
	}
	return nil, errors.New("not implemented")
}

func (m *mockService) GetProductByID(ctx context.Context, id string) (*domain.Product, error) {
	if m.getProductByIDFunc != nil {
		return m.getProductByIDFunc(ctx, id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockService) ListProducts(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error) {
	if m.listProductsFunc != nil {
		return m.listProductsFunc(ctx, page, pageSize)
	}
	return nil, 0, errors.New("not implemented")
}

func (m *mockService) UpdateProduct(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error) {
	if m.updateProductFunc != nil {
		return m.updateProductFunc(ctx, id, name, description, price, imageURL)
	}
	return nil, errors.New("not implemented")
}

func (m *mockService) DeleteProduct(ctx context.Context, id string) error {
	if m.deleteProductFunc != nil {
		return m.deleteProductFunc(ctx, id)
	}
	return errors.New("not implemented")
}

func newMockLogger() logger.Logger {
	return logger.New("info", false)
}

func newMockConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Name:    "test",
			Version: "1.0.0",
			Env:     "test",
			Debug:   true,
		},
	}
}

func newTestContext() (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	return c, rec
}

func TestGetProduct(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	tests := []struct {
		name          string
		productID     string
		serviceFunc   func(ctx context.Context, id string) (*domain.Product, error)
		wantStatus    int
		wantErrCode   string
		checkResponse bool
		wantProductID string
	}{
		{
			name:      "successful get",
			productID: "test-id",
			serviceFunc: func(ctx context.Context, id string) (*domain.Product, error) {
				return domain.New(id, "Test Product", "Description", 99.99, "https://example.com/image.jpg"), nil
			},
			wantStatus:    http.StatusOK,
			checkResponse: true,
			wantProductID: "test-id",
		},
		{
			name:      "product not found",
			productID: "missing-id",
			serviceFunc: func(ctx context.Context, id string) (*domain.Product, error) {
				return nil, repository.ErrProductNotFound
			},
			wantStatus:  http.StatusNotFound,
			wantErrCode: "NOT_FOUND",
		},
		{
			name:      "internal error",
			productID: "test-id",
			serviceFunc: func(ctx context.Context, id string) (*domain.Product, error) {
				return nil, errors.New("database error")
			},
			wantStatus:  http.StatusInternalServerError,
			wantErrCode: "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				getProductByIDFunc: tt.serviceFunc,
			}

			handler := NewProductHandler(mockSvc, log)

			req := &GetProductRequest{ID: tt.productID}
			echoCtx, _ := newTestContext()
			ctx := server.HandlerContext{
				Echo:   echoCtx,
				Config: cfg,
			}

			response, apiErr := handler.GetProduct(*req, ctx)

			if apiErr != nil {
				if apiErr.HTTPStatus() != tt.wantStatus {
					t.Errorf("GetProduct() status = %v, want %v", apiErr.HTTPStatus(), tt.wantStatus)
				}
				if tt.wantErrCode != "" && apiErr.ErrorCode() != tt.wantErrCode {
					t.Errorf("GetProduct() errorCode = %v, want %v", apiErr.ErrorCode(), tt.wantErrCode)
				}
				return
			}

			if tt.checkResponse {
				if response == nil {
					t.Errorf("GetProduct() response = nil, want non-nil")
					return
				}
				if response.ID != tt.wantProductID {
					t.Errorf("GetProduct() ID = %v, want %v", response.ID, tt.wantProductID)
				}
			}
		})
	}
}

func TestListProducts(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	tests := []struct {
		name        string
		page        int
		pageSize    int
		serviceFunc func(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error)
		wantStatus  int
		wantErrCode string
		wantTotal   int
		wantCount   int
	}{
		{
			name:     "successful list",
			page:     1,
			pageSize: 10,
			serviceFunc: func(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error) {
				products := []*domain.Product{
					domain.New("1", "Product 1", "Desc 1", 10.00, ""),
					domain.New("2", "Product 2", "Desc 2", 20.00, ""),
				}
				return products, 2, nil
			},
			wantStatus: http.StatusOK,
			wantTotal:  2,
			wantCount:  2,
		},
		{
			name:     "empty list",
			page:     1,
			pageSize: 10,
			serviceFunc: func(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error) {
				return []*domain.Product{}, 0, nil
			},
			wantStatus: http.StatusOK,
			wantTotal:  0,
			wantCount:  0,
		},
		{
			name:     "validation error",
			page:     0,
			pageSize: 10,
			serviceFunc: func(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error) {
				return nil, 0, errors.New("page must be greater than 0")
			},
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "BAD_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				listProductsFunc: tt.serviceFunc,
			}

			handler := NewProductHandler(mockSvc, log)

			req := &ListProductsRequest{
				Page:     tt.page,
				PageSize: tt.pageSize,
			}
			echoCtx, _ := newTestContext()
			ctx := server.HandlerContext{
				Echo:   echoCtx,
				Config: cfg,
			}

			response, apiErr := handler.ListProducts(*req, ctx)

			if apiErr != nil {
				if apiErr.HTTPStatus() != tt.wantStatus {
					t.Errorf("ListProducts() status = %v, want %v", apiErr.HTTPStatus(), tt.wantStatus)
				}
				if tt.wantErrCode != "" && apiErr.ErrorCode() != tt.wantErrCode {
					t.Errorf("ListProducts() errorCode = %v, want %v", apiErr.ErrorCode(), tt.wantErrCode)
				}
				return
			}

			if response == nil {
				t.Errorf("ListProducts() response = nil, want non-nil")
				return
			}

			if response.Total != tt.wantTotal {
				t.Errorf("ListProducts() total = %v, want %v", response.Total, tt.wantTotal)
			}

			if len(response.Products) != tt.wantCount {
				t.Errorf("ListProducts() count = %v, want %v", len(response.Products), tt.wantCount)
			}
		})
	}
}

func TestCreateProduct(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	tests := []struct {
		name        string
		request     *CreateProductRequest
		serviceFunc func(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error)
		wantStatus  int
		wantErrCode string
	}{
		{
			name: "successful create",
			request: &CreateProductRequest{
				Name:        "New Product",
				Description: "Description",
				Price:       99.99,
				ImageURL:    "https://example.com/image.jpg",
			},
			serviceFunc: func(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error) {
				return domain.New("new-id", name, description, price, imageURL), nil
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "validation error",
			request: &CreateProductRequest{
				Name:        "",
				Description: "Description",
				Price:       99.99,
				ImageURL:    "",
			},
			serviceFunc: func(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error) {
				return nil, errors.New("product name is required")
			},
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "BAD_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				createProductFunc: tt.serviceFunc,
			}

			handler := NewProductHandler(mockSvc, log)

			echoCtx, _ := newTestContext()
			ctx := server.HandlerContext{
				Echo:   echoCtx,
				Config: cfg,
			}

			result, apiErr := handler.CreateProduct(*tt.request, ctx)

			if apiErr != nil {
				if apiErr.HTTPStatus() != tt.wantStatus {
					t.Errorf("CreateProduct() status = %v, want %v", apiErr.HTTPStatus(), tt.wantStatus)
				}
				if tt.wantErrCode != "" && apiErr.ErrorCode() != tt.wantErrCode {
					t.Errorf("CreateProduct() errorCode = %v, want %v", apiErr.ErrorCode(), tt.wantErrCode)
				}
				return
			}

			// Check that Created() result has correct status
			status, _, _ := result.ResultMeta()
			if status != tt.wantStatus {
				t.Errorf("CreateProduct() result status = %v, want %v", status, tt.wantStatus)
			}
		})
	}
}

func TestUpdateProduct(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	updatedName := "Updated Product"
	updatedPrice := 149.99

	tests := []struct {
		name        string
		request     *UpdateProductRequest
		serviceFunc func(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error)
		wantStatus  int
		wantErrCode string
	}{
		{
			name: "successful update",
			request: &UpdateProductRequest{
				ID:    "test-id",
				Name:  &updatedName,
				Price: &updatedPrice,
			},
			serviceFunc: func(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error) {
				return domain.New(id, *name, "Description", *price, ""), nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "product not found",
			request: &UpdateProductRequest{
				ID:   "missing-id",
				Name: &updatedName,
			},
			serviceFunc: func(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error) {
				return nil, repository.ErrProductNotFound
			},
			wantStatus:  http.StatusNotFound,
			wantErrCode: "NOT_FOUND",
		},
		{
			name: "validation error",
			request: &UpdateProductRequest{
				ID:   "test-id",
				Name: &updatedName,
			},
			serviceFunc: func(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error) {
				return nil, errors.New("validation failed")
			},
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "BAD_REQUEST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				updateProductFunc: tt.serviceFunc,
			}

			handler := NewProductHandler(mockSvc, log)

			echoCtx, _ := newTestContext()
			ctx := server.HandlerContext{
				Echo:   echoCtx,
				Config: cfg,
			}

			response, apiErr := handler.UpdateProduct(*tt.request, ctx)

			if apiErr != nil {
				if apiErr.HTTPStatus() != tt.wantStatus {
					t.Errorf("UpdateProduct() status = %v, want %v", apiErr.HTTPStatus(), tt.wantStatus)
				}
				if tt.wantErrCode != "" && apiErr.ErrorCode() != tt.wantErrCode {
					t.Errorf("UpdateProduct() errorCode = %v, want %v", apiErr.ErrorCode(), tt.wantErrCode)
				}
				return
			}

			if response == nil {
				t.Errorf("UpdateProduct() response = nil, want non-nil")
			}
		})
	}
}

func TestDeleteProduct(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	tests := []struct {
		name        string
		productID   string
		serviceFunc func(ctx context.Context, id string) error
		wantStatus  int
		wantErrCode string
	}{
		{
			name:      "successful delete",
			productID: "test-id",
			serviceFunc: func(ctx context.Context, id string) error {
				return nil
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:      "product not found",
			productID: "missing-id",
			serviceFunc: func(ctx context.Context, id string) error {
				return repository.ErrProductNotFound
			},
			wantStatus:  http.StatusNotFound,
			wantErrCode: "NOT_FOUND",
		},
		{
			name:      "internal error",
			productID: "test-id",
			serviceFunc: func(ctx context.Context, id string) error {
				return errors.New("database error")
			},
			wantStatus:  http.StatusInternalServerError,
			wantErrCode: "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				deleteProductFunc: tt.serviceFunc,
			}

			handler := NewProductHandler(mockSvc, log)

			req := &DeleteProductRequest{ID: tt.productID}
			echoCtx, _ := newTestContext()
			ctx := server.HandlerContext{
				Echo:   echoCtx,
				Config: cfg,
			}

			result, apiErr := handler.DeleteProduct(*req, ctx)

			if apiErr != nil {
				if apiErr.HTTPStatus() != tt.wantStatus {
					t.Errorf("DeleteProduct() status = %v, want %v", apiErr.HTTPStatus(), tt.wantStatus)
				}
				if tt.wantErrCode != "" && apiErr.ErrorCode() != tt.wantErrCode {
					t.Errorf("DeleteProduct() errorCode = %v, want %v", apiErr.ErrorCode(), tt.wantErrCode)
				}
				return
			}

			// Check NoContent result
			status, _, _ := result.ResultMeta()
			if status != tt.wantStatus {
				t.Errorf("DeleteProduct() result status = %v, want %v", status, tt.wantStatus)
			}
		})
	}
}

func TestToProductResponse(t *testing.T) {
	product := domain.New("test-id", "Test Product", "Description", 99.99, "https://example.com/image.jpg")

	response := ToProductResponse(product)

	if response == nil {
		t.Fatal("ToProductResponse() returned nil")
	}

	if response.ID != product.ID {
		t.Errorf("ToProductResponse() ID = %v, want %v", response.ID, product.ID)
	}

	if response.Name != product.Name {
		t.Errorf("ToProductResponse() Name = %v, want %v", response.Name, product.Name)
	}

	if response.Price != product.Price {
		t.Errorf("ToProductResponse() Price = %v, want %v", response.Price, product.Price)
	}

	if response.CreatedDate == "" {
		t.Error("ToProductResponse() CreatedDate is empty")
	}

	if response.UpdatedDate == "" {
		t.Error("ToProductResponse() UpdatedDate is empty")
	}
}
