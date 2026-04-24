package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

const (
	rateLimitWindow = 60 * time.Second
	cleanupInterval = 2 * time.Minute
)

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

// NewRateLimiter creates a new RateLimiter with automatic cleanup.
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

// Wrap returns middleware that enforces rate limits.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, limit := rl.resolveKeyAndLimit(r)

		if !rl.allow(r.Context(), key, limit) {
			dto.RateLimited(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) resolveKeyAndLimit(r *http.Request) (string, int) {
	path := r.URL.Path

	for _, prefix := range uploadPrefixes {
		if strings.HasPrefix(path, prefix) {
			if uid, ok := GetUserID(r.Context()); ok {
				return "rl:upload:" + uid, rl.cfg.Upload
			}
			return "rl:upload:ip:" + GetClientIP(r.Context()), rl.cfg.Upload
		}
	}

	if uid, ok := GetUserID(r.Context()); ok {
		return "rl:user:" + uid, rl.cfg.Authenticated
	}

	return "rl:ip:" + GetClientIP(r.Context()), rl.cfg.Public
}

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
