package middleware

import (
	"net/http"
	"runtime/debug"

	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

// Recovery returns middleware that catches panics and returns a 500 error.
func Recovery(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						zap.Any("error", rec),
						zap.String("path", r.URL.Path),
						zap.String("method", r.Method),
						zap.String("stack", string(debug.Stack())),
					)
					dto.WriteError(w, http.StatusInternalServerError, dto.ErrInternalError, "An unexpected error occurred.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
