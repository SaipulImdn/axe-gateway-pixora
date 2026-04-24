// Package router defines all route groups and middleware bindings.
package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/handler"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/middleware"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/service"
)

// maxUploadBodySize is the maximum request body size (512 MB).
const maxUploadBodySize = 512 << 20

// Setup configures the Gin engine with all routes and middleware.
func Setup(cfg *config.Config, rdb *redis.Client, logger *zap.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()

	// Trust proxy headers
	_ = r.SetTrustedProxies(nil)

	// Set max multipart memory
	r.MaxMultipartMemory = maxUploadBodySize

	// Global middleware
	r.Use(middleware.RecoveryMiddleware(logger))
	r.Use(middleware.CORSMiddleware())
	r.Use(middleware.LoggerMiddleware(logger))

	// Rate limiter
	rateLimiter := middleware.NewRateLimiter(rdb, cfg.RateLimit, logger)
	r.Use(rateLimiter.Middleware())

	// Auth middleware
	r.Use(middleware.AuthMiddleware(cfg.JWT.Secret, rdb, logger))

	// Proxy handler
	proxy := handler.NewProxyHandler(cfg.Backend.URL, cfg.Proxy, logger)

	// Health check (aggregated)
	healthChecker := service.NewHealthChecker(rdb, cfg.Backend.URL, logger)
	r.GET("/health", func(c *gin.Context) {
		result := healthChecker.Check(c.Request.Context())
		status := http.StatusOK
		if result.Backend == "down" || result.Redis == "down" {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, result)
	})
	r.HEAD("/health", func(c *gin.Context) {
		result := healthChecker.Check(c.Request.Context())
		status := http.StatusOK
		if result.Backend == "down" || result.Redis == "down" {
			status = http.StatusServiceUnavailable
		}
		c.Status(status)
	})

	// ── Auth Routes ──────────────────────────────────────────────
	auth := r.Group("/api/v1/auth")
	{
		auth.POST("/signup", proxy.Forward)
		auth.POST("/signin", proxy.Forward)
		auth.POST("/google", proxy.Forward)
		auth.POST("/refresh", proxy.Forward)
		auth.POST("/set-password", proxy.Forward)
		auth.GET("/me", proxy.Forward)
		auth.POST("/signout", proxy.Forward)
	}

	// ── Drive Routes ─────────────────────────────────────────────
	drive := r.Group("/api/v1/drive")
	{
		drive.GET("/connect", proxy.Forward)
		drive.POST("/callback", proxy.Forward)
		drive.GET("/oauth/callback", proxy.Forward)
		drive.DELETE("/disconnect", proxy.Forward)
		drive.GET("/status", proxy.Forward)
		drive.GET("/storage", proxy.Forward)
		drive.GET("/accounts", proxy.Forward)
		drive.POST("/accounts/:id/activate", proxy.Forward)
		drive.PUT("/accounts/:id/label", proxy.Forward)
		drive.DELETE("/accounts/:id", proxy.Forward)
		drive.GET("/folders/:parent_id/contents", proxy.Forward)
		drive.GET("/search", proxy.Forward)
		drive.POST("/folders", proxy.Forward)
		drive.GET("/folders/find", proxy.Forward)
		drive.POST("/upload", proxy.Forward)
		drive.POST("/upload/init", proxy.Forward)
		drive.GET("/upload/token", proxy.Forward)
		drive.GET("/files/:file_id/download", proxy.Forward)
		drive.DELETE("/files/:file_id", proxy.Forward)
		drive.POST("/files/:file_id/trash", proxy.Forward)
		drive.POST("/files/:file_id/restore", proxy.Forward)
		drive.PUT("/files/:file_id/rename", proxy.Forward)
		drive.GET("/files/exists", proxy.Forward)
		drive.GET("/trash", proxy.Forward)
	}

	// ── Sync Routes ──────────────────────────────────────────────
	sync := r.Group("/api/v1/sync")
	{
		sync.GET("/status", proxy.Forward)
		sync.PUT("/settings", proxy.Forward)
		sync.POST("/compare", proxy.Forward)
		sync.POST("/complete", proxy.Forward)
		sync.POST("/trigger", proxy.Forward)
		sync.POST("/reconcile", proxy.Forward)
		sync.GET("/jobs/:job_id", proxy.Forward)
	}

	// ── Notification Routes ──────────────────────────────────────
	r.POST("/api/v1/notifications/register", proxy.Forward)

	// ── Activity Routes ──────────────────────────────────────────
	r.GET("/api/v1/activity", proxy.Forward)

	// ── Favorite Routes ──────────────────────────────────────────
	favorites := r.Group("/api/v1/favorites")
	{
		favorites.POST("", proxy.Forward)
		favorites.DELETE("/:file_id", proxy.Forward)
		favorites.GET("", proxy.Forward)
		favorites.GET("/check", proxy.Forward)
	}

	// ── Share Routes ─────────────────────────────────────────────
	share := r.Group("/api/v1/share")
	{
		share.POST("/folder", proxy.Forward)
		share.DELETE("/folder/:folder_id", proxy.Forward)
		share.GET("/folders", proxy.Forward)
	}

	// ── Duplicate Routes ─────────────────────────────────────────
	duplicates := r.Group("/api/v1/duplicates")
	{
		duplicates.POST("/scan", proxy.Forward)
		duplicates.GET("/scans/:scan_id", proxy.Forward)
		duplicates.DELETE("/files/:file_id", proxy.Forward)
	}

	// ── Face Recognition Routes ──────────────────────────────────
	faces := r.Group("/api/v1/faces")
	{
		faces.POST("/groups", proxy.Forward)
		faces.GET("/groups", proxy.Forward)
		faces.GET("/groups/:group_id/photos", proxy.Forward)
		faces.PUT("/groups/:group_id", proxy.Forward)
		faces.DELETE("/groups/:group_id", proxy.Forward)
	}

	return r
}
