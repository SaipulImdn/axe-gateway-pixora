package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// skipLogPaths are high-frequency paths that should not be logged.
var skipLogPaths = map[string]bool{
	"/health": true,
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	n, err := sw.ResponseWriter.Write(b)
	sw.size += n
	return n, err
}

// Flush implements http.Flusher for streaming support.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Logger returns middleware that logs requests with structured fields.
// Also extracts client IP and stores it in context for downstream use.
func Logger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract and store client IP in context
			clientIP := extractClientIP(r)
			ctx := context.WithValue(r.Context(), ClientIPKey, clientIP)
			r = r.WithContext(ctx)

			if skipLogPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(sw, r)

			fields := []zap.Field{
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("query", r.URL.RawQuery),
				zap.Int("status", sw.status),
				zap.Duration("duration", time.Since(start)),
				zap.String("client_ip", clientIP),
				zap.Int("body_size", sw.size),
			}

			if uid, ok := GetUserID(r.Context()); ok {
				fields = append(fields, zap.String("user_id", uid))
			}

			switch {
			case sw.status >= 500:
				logger.Error("request completed", fields...)
			case sw.status >= 400:
				logger.Warn("request completed", fields...)
			default:
				logger.Info("request completed", fields...)
			}
		})
	}
}

// extractClientIP gets the real client IP from X-Forwarded-For or RemoteAddr.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}
