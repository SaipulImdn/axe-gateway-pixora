package middleware

import (
	"context"
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
	cleanupInterval = 2 * time.Minute
)

// uploadPrefixes identifies paths subject to the upload rate limit.
var uploadPrefixes = []string{
	"/api/v1/drive/upload",
}

// RateLimiter enforces per-IP and per-user request rate limits.
type RateLimiter struct {
	rdb      *redis.Client
	cfg      config.RateLimitConfig
	logger   *zap.Logger
	mu       sync.Mutex
	counters map[string]*counter
}

type counter struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a new RateLimiter instance with automatic cleanup of expired entries.
func NewRateLimiter(rdb *redis.Client, cfg config.RateLimitConfig, logger *zap.Logger) *RateLimiter {
	rl := &RateLimiter{
		rdb:      rdb,
		cfg:      cfg,
		logger:   logger,
		counters: make(map[string]*counter),
	}
	go rl.cleanupLoop()
	return rl
}

// Middleware returns a Gin middleware that enforces rate limits.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, limit := rl.resolveKeyAndLimit(c)

		if !rl.allow(c.Request.Context(), key, limit) {
			dto.RateLimited(c)
			return
		}

		c.Next()
	}
}

// resolveKeyAndLimit determines the rate limit key and threshold for the request.
// Uses strings.Builder to avoid fmt.Sprintf allocations.
func (rl *RateLimiter) resolveKeyAndLimit(c *gin.Context) (string, int) {
	path := c.Request.URL.Path

	for _, prefix := range uploadPrefixes {
		if strings.HasPrefix(path, prefix) {
			if userID, exists := c.Get(ContextKeyUserID); exists {
				return buildKey("rl:upload:", userID.(string)), rl.cfg.Upload
			}
			return buildKey("rl:upload:ip:", c.ClientIP()), rl.cfg.Upload
		}
	}

	if userID, exists := c.Get(ContextKeyUserID); exists {
		return buildKey("rl:user:", userID.(string)), rl.cfg.Authenticated
	}

	return buildKey("rl:ip:", c.ClientIP()), rl.cfg.Public
}

// buildKey concatenates prefix + value with minimal allocation.
func buildKey(prefix, value string) string {
	var b strings.Builder
	b.Grow(len(prefix) + len(value))
	b.WriteString(prefix)
	b.WriteString(value)
	return b.String()
}

// allow checks rate limit via Redis, falling back to in-memory.
func (rl *RateLimiter) allow(ctx context.Context, key string, limit int) bool {
	if rl.rdb == nil {
		return rl.allowInMemory(key, limit)
	}

	pipe := rl.rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rateLimitWindow)
	_, err := pipe.Exec(ctx)
	if err != nil {
		rl.logger.Warn("redis rate limit failed, falling back to in-memory", zap.Error(err))
		return rl.allowInMemory(key, limit)
	}

	return incrCmd.Val() <= int64(limit)
}

// allowInMemory provides a simple in-memory rate limiter as fallback.
func (rl *RateLimiter) allowInMemory(key string, limit int) bool {
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

// cleanupLoop periodically removes expired entries from the in-memory counter map.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, c := range rl.counters {
			if now.After(c.resetAt) {
				delete(rl.counters, key)
			}
		}
		rl.mu.Unlock()
	}
}
