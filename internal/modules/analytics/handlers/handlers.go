// Package handlers provides HTTP handlers for the analytics module.
package handlers

import (
	"context"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/analytics/domain"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// Request types

// RecordViewRequest is the request body for recording a product view.
type RecordViewRequest struct {
	ProductID string `json:"productId" binding:"required"`
	UserAgent string `json:"userAgent"`
	IPAddress string `json:"ipAddress"`
	SessionID string `json:"sessionId"`
	Referrer  string `json:"referrer"`
}

// GetProductStatsRequest is the request for getting stats for a specific product.
type GetProductStatsRequest struct {
	ProductID string `param:"productId" binding:"required"`
}

// ListTopViewedRequest is the request for getting top viewed products.
type ListTopViewedRequest struct {
	Limit int `query:"limit"`
}

// Response types

// ViewStatsResponse is the response for product view statistics.
type ViewStatsResponse struct {
	ProductID     string `json:"productId"`
	TotalViews    int64  `json:"totalViews"`
	ViewsToday    int64  `json:"viewsToday"`
	ViewsThisWeek int64  `json:"viewsThisWeek"`
	LastViewedAt  string `json:"lastViewedAt,omitempty"`
}

// TopViewedResponse is the response for top viewed products.
type TopViewedResponse struct {
	Products []TopProductResponse `json:"products"`
}

// TopProductResponse is a single product in the top viewed list.
type TopProductResponse struct {
	ProductID  string `json:"productId"`
	TotalViews int64  `json:"totalViews"`
}

// AnalyticsServiceInterface defines the service contract for handlers.
type AnalyticsServiceInterface interface {
	RecordProductView(ctx context.Context, productID, userAgent, ipAddress, sessionID, referrer string) error
	GetProductViewStats(ctx context.Context, productID string) (*domain.ViewStats, error)
	GetTopViewedProducts(ctx context.Context, limit int) ([]*domain.TopProductStats, error)
}

// AnalyticsHandler handles HTTP requests for analytics operations.
type AnalyticsHandler struct {
	service AnalyticsServiceInterface
	logger  logger.Logger
}

// NewAnalyticsHandler creates a new analytics handler.
func NewAnalyticsHandler(s AnalyticsServiceInterface, l logger.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{
		service: s,
		logger:  l,
	}
}

// RecordView handles POST /analytics/views - records a product view event.
func (h *AnalyticsHandler) RecordView(req *RecordViewRequest, ctx server.HandlerContext) (server.NoContentResult, server.IAPIError) {
	err := h.service.RecordProductView(
		ctx.Echo.Request().Context(),
		req.ProductID,
		req.UserAgent,
		req.IPAddress,
		req.SessionID,
		req.Referrer,
	)
	if err != nil {
		h.logger.Error().Err(err).Str("productId", req.ProductID).Msg("Failed to record view")
		return server.NoContentResult{}, server.NewBadRequestError(err.Error())
	}

	return server.NoContent(), nil
}

// GetProductStats handles GET /analytics/views/:productId - gets view stats for a product.
func (h *AnalyticsHandler) GetProductStats(req GetProductStatsRequest, ctx server.HandlerContext) (*ViewStatsResponse, server.IAPIError) {
	stats, err := h.service.GetProductViewStats(ctx.Echo.Request().Context(), req.ProductID)
	if err != nil {
		h.logger.Error().Err(err).Str("productId", req.ProductID).Msg("Failed to get view stats")
		return nil, server.NewInternalServerError("Failed to retrieve view statistics")
	}

	response := &ViewStatsResponse{
		ProductID:     stats.ProductID,
		TotalViews:    stats.TotalViews,
		ViewsToday:    stats.ViewsToday,
		ViewsThisWeek: stats.ViewsThisWeek,
	}
	if !stats.LastViewedAt.IsZero() {
		response.LastViewedAt = stats.LastViewedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return response, nil
}

// GetTopViewed handles GET /analytics/views - gets top viewed products.
func (h *AnalyticsHandler) GetTopViewed(req ListTopViewedRequest, ctx server.HandlerContext) (*TopViewedResponse, server.IAPIError) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10 // Default limit
	}

	stats, err := h.service.GetTopViewedProducts(ctx.Echo.Request().Context(), limit)
	if err != nil {
		h.logger.Error().Err(err).Int("limit", limit).Msg("Failed to get top viewed")
		return nil, server.NewInternalServerError("Failed to retrieve top viewed products")
	}

	products := make([]TopProductResponse, len(stats))
	for i, s := range stats {
		products[i] = TopProductResponse{
			ProductID:  s.ProductID,
			TotalViews: s.TotalViews,
		}
	}

	return &TopViewedResponse{Products: products}, nil
}

// RegisterRoutes registers analytics HTTP routes.
func (h *AnalyticsHandler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/analytics/views", h.RecordView)
	server.GET(hr, r, "/analytics/views/:productId", h.GetProductStats)
	server.GET(hr, r, "/analytics/views", h.GetTopViewed)
}
