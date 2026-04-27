// Package router defines all route definitions and middleware chain.
package router

import (
	"net/http"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/handler"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/middleware"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/service"
)

// Setup configures the HTTP handler with all routes and middleware.
func Setup(cfg *config.Config, rdb *redis.Client, logger *zap.Logger) http.Handler {
	// Create proxy handlers for each backend service
	pixoraProxy := handler.NewProxyHandler(cfg.Backend.PixoraURL, cfg.Proxy, logger)
	clockwerkProxy := handler.NewProxyHandler(cfg.Backend.ClockwerkURL, cfg.Proxy, logger)

	health := service.NewHealthChecker(rdb, cfg.Backend.PixoraURL, cfg.Backend.ClockwerkURL, logger)
	rateLimiter := middleware.NewRateLimiter(rdb, cfg.RateLimit, logger)

	mux := http.NewServeMux()

	// Health check (aggregated)
	mux.Handle("GET /health", health)
	mux.Handle("HEAD /health", health)

	// ── Routes to pixora-backend ─────────────────────────────────
	mux.Handle("/api/v1/auth/", pixoraProxy)
	mux.Handle("/api/v1/notifications/", pixoraProxy)
	mux.Handle("/api/v1/activity", pixoraProxy)
	mux.Handle("/api/v1/favorites", pixoraProxy)
	mux.Handle("/api/v1/favorites/", pixoraProxy)
	mux.Handle("/api/v1/share/", pixoraProxy)

	// ── Routes to clockwerk-media-pixora ─────────────────────────
	mux.Handle("/api/v1/drive/", clockwerkProxy)
	mux.Handle("/api/v1/sync/", clockwerkProxy)
	mux.Handle("/api/v1/duplicates/", clockwerkProxy)
	mux.Handle("/api/v1/faces/", clockwerkProxy)

	// Middleware chain (outermost first):
	// Recovery → CORS → Logger → RateLimiter → Auth → mux
	var h http.Handler = mux
	h = middleware.Auth(cfg.JWT.Secret, rdb, logger)(h)
	h = rateLimiter.Wrap(h)
	h = middleware.Logger(logger)(h)
	h = middleware.CORS(h)
	h = middleware.Recovery(logger)(h)

	return h
}
