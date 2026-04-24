package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LoggerMiddleware logs every request with structured fields.
func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Int("status", status),
			zap.Duration("duration", duration),
			zap.String("client_ip", c.ClientIP()),
			zap.Int("body_size", c.Writer.Size()),
		}

		if userID, exists := c.Get(ContextKeyUserID); exists {
			fields = append(fields, zap.String("user_id", userID.(string)))
		}

		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("errors", c.Errors.String()))
		}

		switch {
		case status >= 500:
			logger.Error("request completed", fields...)
		case status >= 400:
			logger.Warn("request completed", fields...)
		default:
			logger.Info("request completed", fields...)
		}
	}
}
