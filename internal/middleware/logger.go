package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const logMessage = "request completed"

// skipLogPaths are high-frequency paths that should not be logged to reduce noise.
var skipLogPaths = map[string]bool{
	"/health": true,
}

// LoggerMiddleware logs every request with structured fields.
// Health check requests are skipped to reduce log volume.
func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip logging for health checks (Koyeb polls every few seconds)
		if skipLogPaths[path] {
			c.Next()
			return
		}

		start := time.Now()

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", c.Request.URL.RawQuery),
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
			logger.Error(logMessage, fields...)
		case status >= 400:
			logger.Warn(logMessage, fields...)
		default:
			logger.Info(logMessage, fields...)
		}
	}
}
