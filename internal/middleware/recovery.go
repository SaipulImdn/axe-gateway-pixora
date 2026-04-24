package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

// RecoveryMiddleware catches panics and returns a 500 error response.
func RecoveryMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					zap.Any("error", r),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
				)
				dto.RespondError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
			}
		}()
		c.Next()
	}
}
