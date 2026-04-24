package middleware

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

const (
	rateLimitWindow = 60 * time.Second
)

// uploadPrefixes identifies paths subject to the upload rate limit.
var uploadPrefixes = []string{
	"/api/v1/drive/upload",
}

// RateLimiter enforces per-IP and per-user request rate limits.
type RateLimiter struct {
	rdb    *redis.Client
	cfg    config.RateLimitConfig
	logger *zap.Logger
	// In-memory fallback when Redis is unavailable
	mu       sync.Mutex
	counters map[string]*counter
}

type counter struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a new RateLimiter instance.
func NewRateLimiter(rdb *redis.Client, cfg config.RateLimitConfig, logger *zap.Logger) *RateLimiter {
	return &RateLimiter{
		rdb:      rdb,
		cfg:      cfg,
		logger:   logger,
		counters: make(map[string]*counter),
	}
}

// Middleware returns a Gin middleware that enforces rate limits.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, limit := rl.resolveKeyAndLimit(c)

		allowed := rl.checkRedis(c.Request.Context(), key, limit)
		if !allowed {
			dto.RateLimited(c)
			return
		}

		c.Next()
	}
}

// resolveKeyAndLimit determines the rate limit key and threshold for the request.
func (rl *RateLimiter) resolveKeyAndLimit(c *gin.Context) (string, int) {
	path := c.Request.URL.Path

	// Check if this is an upload path
	for _, prefix := range uploadPrefixes {
		if strings.HasPrefix(path, prefix) {
			if userID, exists := c.Get(ContextKeyUserID); exists {
				return fmt.Sprintf("rl:upload:%s", userID), rl.cfg.Upload
			}
			return fmt.Sprintf("rl:upload:ip:%s", c.ClientIP()), rl.cfg.Upload
		}
	}

	// Authenticated user
	if userID, exists := c.Get(ContextKeyUserID); exists {
		return fmt.Sprintf("rl:user:%s", userID), rl.cfg.Authenticated
	}

	// Public / unauthenticated
	return fmt.Sprintf("rl:ip:%s", c.ClientIP()), rl.cfg.Public
}

// checkRedis attempts Redis-based rate limiting, falling back to in-memory.
func (rl *RateLimiter) checkRedis(ctx context.Context, key string, limit int) bool {
	if rl.rdb == nil {
		return rl.checkInMemory(key, limit)
	}

	pipe := rl.rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rateLimitWindow)
	_, err := pipe.Exec(ctx)
	if err != nil {
		rl.logger.Warn("redis rate limit check failed, falling back to in-memory", zap.Error(err))
		return rl.checkInMemory(key, limit)
	}

	return incrCmd.Val() <= int64(limit)
}

// checkInMemory provides a simple in-memory rate limiter as fallback.
func (rl *RateLimiter) checkInMemory(key string, limit int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	c, exists := rl.counters[key]
	if !exists || now.After(c.resetAt) {
		rl.counters[key] = &counter{count: 1, resetAt: now.Add(rateLimitWindow)}
		return true
	}

	c.count++
	return c.count <= limit
}
