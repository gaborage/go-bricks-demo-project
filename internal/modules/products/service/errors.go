package service

import "errors"

// Sentinel errors for service-layer error classification.
// These allow handlers to use errors.Is() for type-safe error checking
// instead of fragile string-based classification.
var (
	// ErrValidation indicates input validation failure (HTTP 400).
	ErrValidation = errors.New("validation error")

	// ErrInternal indicates an internal service error (HTTP 500).
	ErrInternal = errors.New("internal error")
)
