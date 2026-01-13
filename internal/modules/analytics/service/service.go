// Package service provides business logic for the analytics module.
package service

import (
	"context"
	"fmt"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/repository"
	"github.com/gaborage/go-bricks/logger"
)

// AnalyticsService handles analytics business logic.
type AnalyticsService struct {
	repo   repository.Repository
	logger logger.Logger
}

// NewService creates a new analytics service.
func NewService(repo repository.Repository, log logger.Logger) *AnalyticsService {
	return &AnalyticsService{
		repo:   repo,
		logger: log,
	}
}

// RecordProductView records a product view event in the analytics database.
func (s *AnalyticsService) RecordProductView(ctx context.Context, productID, userAgent, ipAddress, sessionID, referrer string) error {
	// Validate product ID
	if productID == "" {
		return fmt.Errorf("product ID is required")
	}

	view := domain.NewProductView(productID, userAgent, ipAddress, sessionID, referrer)

	if err := s.repo.RecordView(ctx, view); err != nil {
		s.logger.Error().
			Err(err).
			Str("productId", productID).
			Msg("Failed to record product view")
		return fmt.Errorf("failed to record product view: %w", err)
	}

	s.logger.Debug().
		Str("productId", productID).
		Msg("Product view recorded")

	return nil
}

// GetProductViewStats retrieves view statistics for a specific product.
func (s *AnalyticsService) GetProductViewStats(ctx context.Context, productID string) (*domain.ViewStats, error) {
	if productID == "" {
		return nil, fmt.Errorf("product ID is required")
	}

	stats, err := s.repo.GetViewStats(ctx, productID)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("productId", productID).
			Msg("Failed to get view stats")
		return nil, fmt.Errorf("failed to get view stats: %w", err)
	}

	return stats, nil
}

// GetTopViewedProducts retrieves the top viewed products.
func (s *AnalyticsService) GetTopViewedProducts(ctx context.Context, limit int) ([]*domain.TopProductStats, error) {
	// Apply default and maximum limits
	if limit <= 0 {
		limit = 10 // Default limit
	}
	if limit > 100 {
		limit = 100 // Maximum limit
	}

	stats, err := s.repo.GetTopViewed(ctx, limit)
	if err != nil {
		s.logger.Error().
			Err(err).
			Int("limit", limit).
			Msg("Failed to get top viewed products")
		return nil, fmt.Errorf("failed to get top viewed products: %w", err)
	}

	return stats, nil
}
