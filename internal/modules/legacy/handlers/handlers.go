// Package handlers provides HTTP handlers for the legacy module.
// These handlers demonstrate WithRawResponse() — they return the same data
// as the products module but bypass the standard APIResponse envelope.
package handlers

import (
	"errors"

	producthandlers "github.com/gaborage/go-bricks-demo-project/internal/modules/products/handlers"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/service"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

// LegacyHandler serves product data without the APIResponse envelope.
// It reuses the same ProductServiceInterface from the products module.
type LegacyHandler struct {
	service producthandlers.ProductServiceInterface
	logger  logger.Logger
}

// NewLegacyHandler creates a new legacy handler.
func NewLegacyHandler(s producthandlers.ProductServiceInterface, l logger.Logger) *LegacyHandler {
	return &LegacyHandler{
		service: s,
		logger:  l,
	}
}

// GetProduct returns a single product without the APIResponse envelope.
func (h *LegacyHandler) GetProduct(req producthandlers.GetProductRequest, ctx server.HandlerContext) (*producthandlers.ProductResponse, server.IAPIError) {
	product, err := h.service.GetProductByID(ctx.Echo.Request().Context(), req.ID)
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, server.NewNotFoundError("Product")
		}
		h.logger.Error().Err(err).Str("productID", req.ID).Msg("Failed to get product")
		return nil, server.NewInternalServerError("Failed to retrieve product")
	}

	return producthandlers.ToProductResponse(product), nil
}

// ListProducts returns a paginated list of products without the APIResponse envelope.
func (h *LegacyHandler) ListProducts(req producthandlers.ListProductsRequest, ctx server.HandlerContext) (*producthandlers.ListProductsResponse, server.IAPIError) {
	products, total, err := h.service.ListProducts(ctx.Echo.Request().Context(), req.Page, req.PageSize)
	if err != nil {
		h.logger.Error().Err(err).Int("page", req.Page).Int("pageSize", req.PageSize).Msg("Failed to list products")
		if errors.Is(err, service.ErrValidation) {
			return nil, server.NewBadRequestError(err.Error())
		}
		return nil, server.NewInternalServerError("Failed to retrieve products")
	}

	productResponses := make([]producthandlers.ProductResponse, len(products))
	for i, p := range products {
		productResponses[i] = *producthandlers.ToProductResponse(p)
	}

	return &producthandlers.ListProductsResponse{
		Products: productResponses,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}

// RegisterRoutes registers legacy HTTP routes with WithRawResponse().
// These routes bypass the standard APIResponse envelope, returning
// the handler's response directly as JSON — ideal for Strangler Fig migrations.
func (h *LegacyHandler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.GET(hr, r, "/legacy/products/:id", h.GetProduct,
		server.WithRawResponse(),
		server.WithTags("legacy"),
	)
	server.GET(hr, r, "/legacy/products", h.ListProducts,
		server.WithRawResponse(),
		server.WithTags("legacy"),
	)
}
