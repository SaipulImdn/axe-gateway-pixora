package middleware

import (
	"context"
	"hash/fnv"
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
	numShards       = 32 // power-of-two for fast modulo via bitmask
)

var uploadPrefixes = []string{
	"/api/v1/drive/upload",
}

// shard holds a subset of rate-limit counters behind its own mutex,
// reducing lock contention under high concurrency.
type shard struct {
	mu       sync.Mutex
	counters map[string]*counter
}

type counter struct {
	count   int
	resetAt time.Time
}

// RateLimiter enforces per-IP and per-user request rate limits.
// In-memory counters are distributed across shards to minimise mutex contention.
type RateLimiter struct {
	rdb    *redis.Client
	cfg    config.RateLimitConfig
	logger *zap.Logger
	shards [numShards]shard
}

// NewRateLimiter creates a new RateLimiter with automatic cleanup.
func NewRateLimiter(rdb *redis.Client, cfg config.RateLimitConfig, logger *zap.Logger) *RateLimiter {
	rl := &RateLimiter{
		rdb:    rdb,
		cfg:    cfg,
		logger: logger,
	}
	for i := range rl.shards {
		rl.shards[i].counters = make(map[string]*counter)
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

// getShard returns the shard for the given key using FNV-1a hash.
func (rl *RateLimiter) getShard(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &rl.shards[h.Sum32()&(numShards-1)]
}

func (rl *RateLimiter) allowInMemory(key string, limit int) bool {
	s := rl.getShard(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	c, exists := s.counters[key]
	if !exists || now.After(c.resetAt) {
		s.counters[key] = &counter{count: 1, resetAt: now.Add(rateLimitWindow)}
		return true
	}

	c.count++
	return c.count <= limit
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		for i := range rl.shards {
			s := &rl.shards[i]
			s.mu.Lock()
			for key, c := range s.counters {
				if now.After(c.resetAt) {
					delete(s.counters, key)
				}
			}
			s.mu.Unlock()
		}
	}
}
