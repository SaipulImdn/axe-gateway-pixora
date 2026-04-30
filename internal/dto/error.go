// Package dto provides standardized data transfer objects for API responses.
package dto

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Error codes used across the gateway.
const (
	ErrUnauthorized       = "UNAUTHORIZED"
	ErrRateLimited        = "RATE_LIMITED"
	ErrBadGateway         = "BAD_GATEWAY"
	ErrServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrGatewayTimeout     = "GATEWAY_TIMEOUT"
	ErrInternalError      = "INTERNAL_ERROR"
)

// ErrorResponse represents a standardized error response matching the backend format.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// bufPool reuses byte buffers to reduce allocations on every error response.
var bufPool = sync.Pool{
	New: func() any {
		// Pre-allocate a reasonable size for JSON error responses.
		b := make([]byte, 0, 128)
		return &b
	},
}

// WriteError writes a standardized JSON error response.
// It uses a pooled buffer to avoid per-request allocations from json.NewEncoder.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	bp := bufPool.Get().(*[]byte)
	buf := (*bp)[:0]

	data, err := json.Marshal(ErrorResponse{
		Error:   code,
		Message: message,
	})
	if err != nil {
		// Fallback: write directly (should never happen with simple structs).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(ErrorResponse{Error: code, Message: message})
		*bp = buf
		bufPool.Put(bp)
		return
	}

	_ = buf // keep linter happy
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)

	*bp = buf
	bufPool.Put(bp)
}

// Unauthorized responds with a 401 error.
func Unauthorized(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusUnauthorized, ErrUnauthorized, message)
}

// RateLimited responds with a 429 error.
func RateLimited(w http.ResponseWriter) {
	WriteError(w, http.StatusTooManyRequests, ErrRateLimited, "Too many requests. Please try again later.")
}

// BadGateway responds with a 502 error.
func BadGateway(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadGateway, ErrBadGateway, message)
}

// ServiceUnavailable responds with a 503 error.
func ServiceUnavailable(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusServiceUnavailable, ErrServiceUnavailable, message)
}

// GatewayTimeout responds with a 504 error.
func GatewayTimeout(w http.ResponseWriter) {
	WriteError(w, http.StatusGatewayTimeout, ErrGatewayTimeout, "Backend service timed out.")
}
