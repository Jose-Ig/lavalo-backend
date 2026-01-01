package common

import (
	"errors"
	"net/http"
)

// Domain errors
var (
	ErrNotFound          = errors.New("resource not found")
	ErrInvalidInput      = errors.New("invalid input")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrConflict          = errors.New("resource conflict")
	ErrInternalServer    = errors.New("internal server error")
	ErrSlotNotAvailable  = errors.New("slot not available")
	ErrReservationFailed = errors.New("reservation failed")
)

// APIError represents a structured API error response
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// NewAPIError creates a new API error
func NewAPIError(code int, message, details string) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// Error implements the error interface
func (e *APIError) Error() string {
	return e.Message
}

// MapErrorToHTTPStatus maps domain errors to HTTP status codes
func MapErrorToHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrSlotNotAvailable):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

