package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	producthandlers "github.com/gaborage/go-bricks-demo-project/internal/modules/products/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
	"github.com/labstack/echo/v4"
)

// mockService implements the subset of ProductServiceInterface needed by legacy handlers.
type mockService struct {
	getProductByIDFunc func(ctx context.Context, id string) (*domain.Product, error)
	listProductsFunc   func(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error)
}

func (m *mockService) CreateProduct(context.Context, string, string, float64, string) (*domain.Product, error) {
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

func (m *mockService) UpdateProduct(context.Context, string, *string, *string, *float64, *string) (*domain.Product, error) {
	return nil, errors.New("not implemented")
}

func (m *mockService) DeleteProduct(context.Context, string) error {
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
			serviceFunc: func(_ context.Context, id string) (*domain.Product, error) {
				return domain.New(id, "Test Product", "Description", 99.99, "https://example.com/image.jpg"), nil
			},
			wantStatus:    http.StatusOK,
			checkResponse: true,
			wantProductID: "test-id",
		},
		{
			name:      "product not found",
			productID: "missing-id",
			serviceFunc: func(_ context.Context, _ string) (*domain.Product, error) {
				return nil, repository.ErrProductNotFound
			},
			wantStatus:  http.StatusNotFound,
			wantErrCode: "NOT_FOUND",
		},
		{
			name:      "internal error",
			productID: "test-id",
			serviceFunc: func(_ context.Context, _ string) (*domain.Product, error) {
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

			handler := NewLegacyHandler(mockSvc, log)

			req := &producthandlers.GetProductRequest{ID: tt.productID}
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
			serviceFunc: func(_ context.Context, _, _ int) ([]*domain.Product, int, error) {
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
			serviceFunc: func(_ context.Context, _, _ int) ([]*domain.Product, int, error) {
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
			serviceFunc: func(_ context.Context, _, _ int) ([]*domain.Product, int, error) {
				return nil, 0, fmt.Errorf("%w: page must be greater than 0", service.ErrValidation)
			},
			wantStatus:  http.StatusBadRequest,
			wantErrCode: "BAD_REQUEST",
		},
		{
			name:     "internal error",
			page:     1,
			pageSize: 10,
			serviceFunc: func(_ context.Context, _, _ int) ([]*domain.Product, int, error) {
				return nil, 0, fmt.Errorf("%w: failed to list products: database error", service.ErrInternal)
			},
			wantStatus:  http.StatusInternalServerError,
			wantErrCode: "INTERNAL_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := &mockService{
				listProductsFunc: tt.serviceFunc,
			}

			handler := NewLegacyHandler(mockSvc, log)

			req := &producthandlers.ListProductsRequest{
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
