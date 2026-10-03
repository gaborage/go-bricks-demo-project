package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/domain"
	"github.com/gaborage/go-bricks/logger"
)

const (
	testProductID   = "product-123"
	repositoryError = "repository error"
	requiredMsg     = "required"
)

// mockRepository implements repository.Repository for testing.
type mockRepository struct {
	recordViewFunc   func(ctx context.Context, view *domain.ProductView) error
	getViewStatsFunc func(ctx context.Context, productID string) (*domain.ViewStats, error)
	getTopViewedFunc func(ctx context.Context, limit int) ([]*domain.TopProductStats, error)
}

func (m *mockRepository) RecordView(ctx context.Context, view *domain.ProductView) error {
	if m.recordViewFunc != nil {
		return m.recordViewFunc(ctx, view)
	}
	return errors.New("not implemented")
}

func (m *mockRepository) GetViewStats(ctx context.Context, productID string) (*domain.ViewStats, error) {
	if m.getViewStatsFunc != nil {
		return m.getViewStatsFunc(ctx, productID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockRepository) GetTopViewed(ctx context.Context, limit int) ([]*domain.TopProductStats, error) {
	if m.getTopViewedFunc != nil {
		return m.getTopViewedFunc(ctx, limit)
	}
	return nil, errors.New("not implemented")
}

func newMockLogger() logger.Logger {
	return logger.New("info", false)
}

func TestRecordProductView(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name        string
		productID   string
		repoErr     error
		wantErr     bool
		errContains string
	}{
		{
			name:      "successful record",
			productID: testProductID,
			wantErr:   false,
		},
		{
			name:        "empty product id",
			productID:   "",
			wantErr:     true,
			errContains: requiredMsg,
		},
		{
			name:        repositoryError,
			productID:   testProductID,
			repoErr:     errors.New("database error"),
			wantErr:     true,
			errContains: "failed to record product view",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recordedView *domain.ProductView
			mockRepo := &mockRepository{
				recordViewFunc: func(_ context.Context, view *domain.ProductView) error {
					recordedView = view
					return tt.repoErr
				},
			}

			svc := NewService(mockRepo, log)
			err := svc.RecordProductView(ctx, tt.productID, "Mozilla/5.0", "127.0.0.1", "session-1", "https://example.com")

			if tt.wantErr {
				if err == nil {
					t.Fatalf("RecordProductView() error = nil, wantErr %v", tt.wantErr)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("RecordProductView() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("RecordProductView() unexpected error = %v", err)
			}
			if recordedView == nil {
				t.Fatal("RecordProductView() expected repository to receive a view")
			}
			if recordedView.ProductID != tt.productID {
				t.Errorf("recordedView.ProductID = %v, want %v", recordedView.ProductID, tt.productID)
			}
		})
	}
}

func TestGetProductViewStats(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name        string
		productID   string
		repoErr     error
		wantErr     bool
		errContains string
	}{
		{
			name:      "successful get",
			productID: testProductID,
			wantErr:   false,
		},
		{
			name:        "empty product id",
			productID:   "",
			wantErr:     true,
			errContains: requiredMsg,
		},
		{
			name:        repositoryError,
			productID:   testProductID,
			repoErr:     errors.New("database error"),
			wantErr:     true,
			errContains: "failed to get view stats",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockRepository{
				getViewStatsFunc: func(_ context.Context, productID string) (*domain.ViewStats, error) {
					if tt.repoErr != nil {
						return nil, tt.repoErr
					}
					return &domain.ViewStats{ProductID: productID, TotalViews: 10}, nil
				},
			}

			svc := NewService(mockRepo, log)
			stats, err := svc.GetProductViewStats(ctx, tt.productID)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("GetProductViewStats() error = nil, wantErr %v", tt.wantErr)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("GetProductViewStats() error = %v, want error containing %v", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("GetProductViewStats() unexpected error = %v", err)
			}
			if stats.ProductID != tt.productID {
				t.Errorf("stats.ProductID = %v, want %v", stats.ProductID, tt.productID)
			}
		})
	}
}

func TestGetTopViewedProducts(t *testing.T) {
	ctx := context.Background()
	log := newMockLogger()

	tests := []struct {
		name      string
		limit     int
		wantLimit int
		repoErr   error
		wantErr   bool
		wantCount int
	}{
		{
			name:      "explicit limit passed through",
			limit:     5,
			wantLimit: 5,
			wantCount: 5,
		},
		{
			name:      "zero limit defaults to 10",
			limit:     0,
			wantLimit: 10,
			wantCount: 10,
		},
		{
			name:      "negative limit defaults to 10",
			limit:     -3,
			wantLimit: 10,
			wantCount: 10,
		},
		{
			name:      "limit above maximum is clamped to 100",
			limit:     500,
			wantLimit: 100,
			wantCount: 100,
		},
		{
			name:    repositoryError,
			limit:   5,
			repoErr: errors.New("database error"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotLimit int
			mockRepo := &mockRepository{
				getTopViewedFunc: func(_ context.Context, limit int) ([]*domain.TopProductStats, error) {
					gotLimit = limit
					if tt.repoErr != nil {
						return nil, tt.repoErr
					}
					results := make([]*domain.TopProductStats, limit)
					for i := range results {
						results[i] = &domain.TopProductStats{ProductID: testProductID, TotalViews: int64(i)}
					}
					return results, nil
				},
			}

			svc := NewService(mockRepo, log)
			results, err := svc.GetTopViewedProducts(ctx, tt.limit)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("GetTopViewedProducts() error = nil, wantErr %v", tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("GetTopViewedProducts() unexpected error = %v", err)
			}
			if gotLimit != tt.wantLimit {
				t.Errorf("repository called with limit = %v, want %v", gotLimit, tt.wantLimit)
			}
			if len(results) != tt.wantCount {
				t.Errorf("GetTopViewedProducts() count = %v, want %v", len(results), tt.wantCount)
			}
		})
	}
}
