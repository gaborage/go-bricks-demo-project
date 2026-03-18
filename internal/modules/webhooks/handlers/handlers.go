// Package handlers provides HTTP handlers for the webhooks module.
package handlers

import (
	"encoding/json"

	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/domain"
	"github.com/gaborage/go-bricks-demo-project/internal/modules/webhooks/service"
	"github.com/gaborage/go-bricks/logger"
	"github.com/gaborage/go-bricks/server"
)

type SignRequest struct {
	Payload json.RawMessage `json:"payload" binding:"required"`
}

type VerifyRequest struct {
	Payload   string `json:"payload" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

type VerifyResponse struct {
	Valid bool `json:"valid"`
}

// SigningServiceInterface defines the service contract for handlers.
type SigningServiceInterface interface {
	Sign(payload string) (*domain.SignedPayload, error)
	Verify(payload, signatureB64 string) (bool, error)
}

type WebhookHandler struct {
	service SigningServiceInterface
	logger  logger.Logger
}

func NewWebhookHandler(s *service.SigningService, l logger.Logger) *WebhookHandler {
	return &WebhookHandler{
		service: s,
		logger:  l,
	}
}

// SignPayload signs an arbitrary JSON payload using the configured RSA key.
func (h *WebhookHandler) SignPayload(req SignRequest, ctx server.HandlerContext) (server.Result[*domain.SignedPayload], server.IAPIError) {
	_ = ctx // unused but required by handler signature

	signed, err := h.service.Sign(string(req.Payload))
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to sign payload")
		return server.Result[*domain.SignedPayload]{}, server.NewInternalServerError("Failed to sign payload")
	}

	return server.Created(signed), nil
}

// VerifyPayload verifies a payload's signature against the configured RSA public key.
func (h *WebhookHandler) VerifyPayload(req VerifyRequest, ctx server.HandlerContext) (*VerifyResponse, server.IAPIError) {
	_ = ctx

	valid, err := h.service.Verify(req.Payload, req.Signature)
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to verify signature")
		return nil, server.NewInternalServerError("Failed to verify signature")
	}

	return &VerifyResponse{Valid: valid}, nil
}

// RegisterRoutes registers webhook HTTP endpoints.
func (h *WebhookHandler) RegisterRoutes(hr *server.HandlerRegistry, r server.RouteRegistrar) {
	server.POST(hr, r, "/webhooks/sign", h.SignPayload)
	server.POST(hr, r, "/webhooks/verify", h.VerifyPayload)
}
