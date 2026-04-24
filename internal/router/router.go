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
	proxy := handler.NewProxyHandler(cfg.Backend.URL, cfg.Proxy, logger)
	health := service.NewHealthChecker(rdb, cfg.Backend.URL, logger)
	rateLimiter := middleware.NewRateLimiter(rdb, cfg.RateLimit, logger)

	mux := http.NewServeMux()

	// Health check
	mux.Handle("GET /health", health)
	mux.Handle("HEAD /health", health)

	// All API routes → proxy to backend
	mux.Handle("/api/v1/", proxy)

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
