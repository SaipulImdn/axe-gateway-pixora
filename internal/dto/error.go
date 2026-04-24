// Package dto provides standardized data transfer objects for API responses.
package dto

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Error codes used across the gateway.
const (
	ErrUnauthorized       = "UNAUTHORIZED"
	ErrRateLimited        = "RATE_LIMITED"
	ErrBadGateway         = "BAD_GATEWAY"
	ErrServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrGatewayTimeout     = "GATEWAY_TIMEOUT"
)

// ErrorResponse represents a standardized error response matching the backend format.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// RespondError writes a standardized JSON error response.
func RespondError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error:   code,
		Message: message,
	})
}

// Unauthorized responds with a 401 error.
func Unauthorized(c *gin.Context, message string) {
	RespondError(c, http.StatusUnauthorized, ErrUnauthorized, message)
}

// RateLimited responds with a 429 error.
func RateLimited(c *gin.Context) {
	RespondError(c, http.StatusTooManyRequests, ErrRateLimited, "Too many requests. Please try again later.")
}

// BadGateway responds with a 502 error.
func BadGateway(c *gin.Context, message string) {
	RespondError(c, http.StatusBadGateway, ErrBadGateway, message)
}

// ServiceUnavailable responds with a 503 error.
func ServiceUnavailable(c *gin.Context, message string) {
	RespondError(c, http.StatusServiceUnavailable, ErrServiceUnavailable, message)
}

// GatewayTimeout responds with a 504 error.
func GatewayTimeout(c *gin.Context) {
	RespondError(c, http.StatusGatewayTimeout, ErrGatewayTimeout, "Backend service timed out.")
}
