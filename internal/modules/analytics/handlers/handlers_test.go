package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/domain"
	"github.com/gaborage/go-bricks/config"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

const testProductID = "product-123"

// mockService implements AnalyticsServiceInterface for testing.
type mockService struct {
	recordProductViewFunc    func(ctx context.Context, productID, userAgent, ipAddress, sessionID, referrer string) error
	getProductViewStatsFunc  func(ctx context.Context, productID string) (*domain.ViewStats, error)
	getTopViewedProductsFunc func(ctx context.Context, limit int) ([]*domain.TopProductStats, error)
}

func (m *mockService) RecordProductView(ctx context.Context, productID, userAgent, ipAddress, sessionID, referrer string) error {
	if m.recordProductViewFunc != nil {
		return m.recordProductViewFunc(ctx, productID, userAgent, ipAddress, sessionID, referrer)
	}
	return errors.New("not implemented")
}

func (m *mockService) GetProductViewStats(ctx context.Context, productID string) (*domain.ViewStats, error) {
	if m.getProductViewStatsFunc != nil {
		return m.getProductViewStatsFunc(ctx, productID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockService) GetTopViewedProducts(ctx context.Context, limit int) ([]*domain.TopProductStats, error) {
	if m.getTopViewedProductsFunc != nil {
		return m.getTopViewedProductsFunc(ctx, limit)
	}
	return nil, errors.New("not implemented")
}

func newMockLogger() logger.Logger {
	return logger.New("info", false)
}

func newMockConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{Name: "test", Version: "1.0.0", Env: "test"},
	}
}

func newTestContext(cfg *config.Config) server.HandlerContext {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return server.NewHandlerContextForTest(rec, req, cfg)
}

func TestRecordView(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	t.Run("successful record returns no content", func(t *testing.T) {
		svc := &mockService{
			recordProductViewFunc: func(_ context.Context, productID, _, _, _, _ string) error {
				if productID != testProductID {
					t.Errorf("productID = %v, want %v", productID, testProductID)
				}
				return nil
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		req := &RecordViewRequest{ProductID: testProductID, UserAgent: "Mozilla/5.0"}
		result, apiErr := handler.RecordView(req, ctx)

		if apiErr != nil {
			t.Fatalf("RecordView() error = %v", apiErr)
		}
		status, _, _ := result.ResultMeta()
		if status != http.StatusNoContent {
			t.Errorf("RecordView() status = %v, want %v", status, http.StatusNoContent)
		}
	})

	t.Run("service error returns bad request", func(t *testing.T) {
		svc := &mockService{
			recordProductViewFunc: func(_ context.Context, _, _, _, _, _ string) error {
				return errors.New("product ID is required")
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		req := &RecordViewRequest{ProductID: ""}
		_, apiErr := handler.RecordView(req, ctx)

		if apiErr == nil {
			t.Fatal("RecordView() expected error")
		}
		if apiErr.HTTPStatus() != http.StatusBadRequest {
			t.Errorf("status = %v, want %v", apiErr.HTTPStatus(), http.StatusBadRequest)
		}
	})
}

func TestGetProductStats(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	t.Run("successful stats", func(t *testing.T) {
		lastViewed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		svc := &mockService{
			getProductViewStatsFunc: func(_ context.Context, productID string) (*domain.ViewStats, error) {
				return &domain.ViewStats{
					ProductID:     productID,
					TotalViews:    42,
					ViewsToday:    3,
					ViewsThisWeek: 10,
					LastViewedAt:  lastViewed,
				}, nil
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		req := GetProductStatsRequest{ProductID: testProductID}
		resp, apiErr := handler.GetProductStats(req, ctx)

		if apiErr != nil {
			t.Fatalf("GetProductStats() error = %v", apiErr)
		}
		if resp.ProductID != testProductID {
			t.Errorf("ProductID = %v, want %v", resp.ProductID, testProductID)
		}
		if resp.TotalViews != 42 {
			t.Errorf("TotalViews = %v, want %v", resp.TotalViews, 42)
		}
		if resp.LastViewedAt == "" {
			t.Error("LastViewedAt is empty, want formatted timestamp")
		}
	})

	t.Run("zero last viewed time omitted", func(t *testing.T) {
		svc := &mockService{
			getProductViewStatsFunc: func(_ context.Context, productID string) (*domain.ViewStats, error) {
				return &domain.ViewStats{ProductID: productID}, nil
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		req := GetProductStatsRequest{ProductID: testProductID}
		resp, apiErr := handler.GetProductStats(req, ctx)

		if apiErr != nil {
			t.Fatalf("GetProductStats() error = %v", apiErr)
		}
		if resp.LastViewedAt != "" {
			t.Errorf("LastViewedAt = %v, want empty", resp.LastViewedAt)
		}
	})

	t.Run("service error returns internal server error", func(t *testing.T) {
		svc := &mockService{
			getProductViewStatsFunc: func(_ context.Context, _ string) (*domain.ViewStats, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		req := GetProductStatsRequest{ProductID: testProductID}
		_, apiErr := handler.GetProductStats(req, ctx)

		if apiErr == nil {
			t.Fatal("GetProductStats() expected error")
		}
		if apiErr.HTTPStatus() != http.StatusInternalServerError {
			t.Errorf("status = %v, want %v", apiErr.HTTPStatus(), http.StatusInternalServerError)
		}
	})
}

func TestGetTopViewed(t *testing.T) {
	log := newMockLogger()
	cfg := newMockConfig()

	t.Run("successful list with explicit limit", func(t *testing.T) {
		var gotLimit int
		svc := &mockService{
			getTopViewedProductsFunc: func(_ context.Context, limit int) ([]*domain.TopProductStats, error) {
				gotLimit = limit
				return []*domain.TopProductStats{
					{ProductID: "p1", TotalViews: 100},
					{ProductID: "p2", TotalViews: 50},
				}, nil
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		resp, apiErr := handler.GetTopViewed(ListTopViewedRequest{Limit: 5}, ctx)

		if apiErr != nil {
			t.Fatalf("GetTopViewed() error = %v", apiErr)
		}
		if gotLimit != 5 {
			t.Errorf("service called with limit = %v, want 5", gotLimit)
		}
		if len(resp.Products) != 2 {
			t.Fatalf("Products count = %v, want 2", len(resp.Products))
		}
		if resp.Products[0].ProductID != "p1" || resp.Products[0].TotalViews != 100 {
			t.Errorf("Products[0] = %+v, want ProductID=p1 TotalViews=100", resp.Products[0])
		}
	})

	t.Run("zero limit defaults to 10", func(t *testing.T) {
		var gotLimit int
		svc := &mockService{
			getTopViewedProductsFunc: func(_ context.Context, limit int) ([]*domain.TopProductStats, error) {
				gotLimit = limit
				return []*domain.TopProductStats{}, nil
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		resp, apiErr := handler.GetTopViewed(ListTopViewedRequest{Limit: 0}, ctx)

		if apiErr != nil {
			t.Fatalf("GetTopViewed() error = %v", apiErr)
		}
		if gotLimit != 10 {
			t.Errorf("service called with limit = %v, want 10", gotLimit)
		}
		if len(resp.Products) != 0 {
			t.Errorf("Products count = %v, want 0", len(resp.Products))
		}
	})

	t.Run("service error returns internal server error", func(t *testing.T) {
		svc := &mockService{
			getTopViewedProductsFunc: func(_ context.Context, _ int) ([]*domain.TopProductStats, error) {
				return nil, errors.New("database error")
			},
		}

		handler := NewAnalyticsHandler(svc, log)
		ctx := newTestContext(cfg)

		_, apiErr := handler.GetTopViewed(ListTopViewedRequest{Limit: 10}, ctx)

		if apiErr == nil {
			t.Fatal("GetTopViewed() expected error")
		}
		if apiErr.HTTPStatus() != http.StatusInternalServerError {
			t.Errorf("status = %v, want %v", apiErr.HTTPStatus(), http.StatusInternalServerError)
		}
	})
}
