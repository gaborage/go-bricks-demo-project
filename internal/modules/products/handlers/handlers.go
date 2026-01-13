// Package handlers provides HTTP handlers for the products module.
package handlers

import (
	"context"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/products/repository"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

type CreateProductRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Price       float64 `json:"price" binding:"required"`
	ImageURL    string  `json:"imageURL"`
}

type UpdateProductRequest struct {
	ID          string   `param:"id" binding:"required"`
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Price       *float64 `json:"price"`
	ImageURL    *string  `json:"imageURL"`
}

type GetProductRequest struct {
	ID string `param:"id"  binding:"required"`
}

type ListProductsRequest struct {
	Page     int `query:"page" binding:"required"`
	PageSize int `query:"pageSize" binding:"required"`
}

type DeleteProductRequest struct {
	ID string `param:"id" binding:"required"`
}

type ProductResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	ImageURL    string  `json:"imageURL"`
	CreatedDate string  `json:"createdDate"`
	UpdatedDate string  `json:"updatedDate"`
}

type ListProductsResponse struct {
	Products []ProductResponse `json:"products"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
}

func ToProductResponse(p *domain.Product) *ProductResponse {
	return &ProductResponse{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Price:       p.Price,
		ImageURL:    p.ImageURL,
		CreatedDate: p.CreatedDate.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedDate: p.UpdatedDate.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ProductServiceInterface defines the service contract for handlers
//
//nolint:dupl // Interface matches test mock signatures - this is expected
type ProductServiceInterface interface {
	CreateProduct(ctx context.Context, name, description string, price float64, imageURL string) (*domain.Product, error)
	GetProductByID(ctx context.Context, id string) (*domain.Product, error)
	ListProducts(ctx context.Context, page, pageSize int) ([]*domain.Product, int, error)
	UpdateProduct(ctx context.Context, id string, name *string, description *string, price *float64, imageURL *string) (*domain.Product, error)
	DeleteProduct(ctx context.Context, id string) error
}

type ProductHandler struct {
	service ProductServiceInterface
	logger  logger.Logger
}

func NewProductHandler(s ProductServiceInterface, l logger.Logger) *ProductHandler {
	return &ProductHandler{
		service: s,
		logger:  l,
	}
}

func (h *ProductHandler) GetProduct(req GetProductRequest, ctx server.HandlerContext) (*ProductResponse, server.IAPIError) {
	product, err := h.service.GetProductByID(ctx.Echo.Request().Context(), req.ID)
	if err != nil {
		if err == repository.ErrProductNotFound {
			return nil, server.NewNotFoundError("Product")
		}
		h.logger.Error().Err(err).Str("productID", req.ID).Msg("Failed to get product")
		return nil, server.NewInternalServerError("Failed to retrieve product")
	}

	return ToProductResponse(product), nil
}

func (h *ProductHandler) ListProducts(req ListProductsRequest, ctx server.HandlerContext) (*ListProductsResponse, server.IAPIError) {
	products, total, err := h.service.ListProducts(ctx.Echo.Request().Context(), req.Page, req.PageSize)
	if err != nil {
		h.logger.Error().Err(err).Int("page", req.Page).Int("pageSize", req.PageSize).Msg("Failed to list products")
		return nil, server.NewBadRequestError(err.Error())
	}

	// Convert products to response format
	productResponses := make([]ProductResponse, len(products))
	for i, p := range products {
		productResponses[i] = *ToProductResponse(p)
	}

	return &ListProductsResponse{
		Products: productResponses,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}

func (h *ProductHandler) CreateProduct(req CreateProductRequest, ctx server.HandlerContext) (server.Result[*ProductResponse], server.IAPIError) {
	product, err := h.service.CreateProduct(
		ctx.Echo.Request().Context(),
		req.Name,
		req.Description,
		req.Price,
		req.ImageURL,
	)
	if err != nil {
		h.logger.Error().Err(err).Str("name", req.Name).Msg("Failed to create product")
		return server.Result[*ProductResponse]{}, server.NewBadRequestError(err.Error())
	}

	response := ToProductResponse(product)
	return server.Created(response), nil
}

func (h *ProductHandler) UpdateProduct(req UpdateProductRequest, ctx server.HandlerContext) (*ProductResponse, server.IAPIError) {
	product, err := h.service.UpdateProduct(
		ctx.Echo.Request().Context(),
		req.ID,
		req.Name,
		req.Description,
		req.Price,
		req.ImageURL,
	)
	if err != nil {
		if err == repository.ErrProductNotFound {
			return nil, server.NewNotFoundError("Product")
		}
		h.logger.Error().Err(err).Str("productID", req.ID).Msg("Failed to update product")
		return nil, server.NewBadRequestError(err.Error())
	}

	return ToProductResponse(product), nil
}

func (h *ProductHandler) DeleteProduct(req DeleteProductRequest, ctx server.HandlerContext) (server.NoContentResult, server.IAPIError) {
	err := h.service.DeleteProduct(ctx.Echo.Request().Context(), req.ID)
	if err != nil {
		if err == repository.ErrProductNotFound {
			return server.NoContentResult{}, server.NewNotFoundError("Product")
		}
		h.logger.Error().Err(err).Str("productID", req.ID).Msg("Failed to delete product")
		return server.NoContentResult{}, server.NewInternalServerError("Failed to delete product")
	}

	return server.NoContent(), nil
}

// RegisterProductRoutes registers product-related HTTP routes
func (h *ProductHandler) RegisterProductRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.GET(hr, r, "/products/:id", h.GetProduct)
	server.GET(hr, r, "/products", h.ListProducts)
	server.POST(hr, r, "/products", h.CreateProduct)
	server.PUT(hr, r, "/products/:id", h.UpdateProduct)
	server.DELETE(hr, r, "/products/:id", h.DeleteProduct)
}
