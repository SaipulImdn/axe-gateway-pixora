// Package service provides business logic services for the gateway.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

const (
	statusOK   = "ok"
	statusDown = "down"

	// healthCacheTTL controls how long a cached health result is considered fresh.
	// This prevents thundering-herd problems when many clients poll /health.
	healthCacheTTL = 5 * time.Second
)

// HealthChecker aggregates health status from gateway dependencies.
// Results are cached for healthCacheTTL to avoid hammering backends on every probe.
type HealthChecker struct {
	rdb          *redis.Client
	pixoraURL    string
	clockwerkURL string
	httpClient   *http.Client
	logger       *zap.Logger

	// Cached result
	cacheMu     sync.Mutex
	cachedResp  atomic.Value // *cachedHealth
	cacheExpiry atomic.Int64 // unix nano
}

type cachedHealth struct {
	resp   dto.HealthResponse
	status int
	body   []byte // pre-marshalled JSON
}

// NewHealthChecker creates a new HealthChecker with a dedicated HTTP client.
func NewHealthChecker(rdb *redis.Client, pixoraURL, clockwerkURL string, logger *zap.Logger) *HealthChecker {
	return &HealthChecker{
		rdb:          rdb,
		pixoraURL:    pixoraURL,
		clockwerkURL: clockwerkURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   3 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout: 3 * time.Second,
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     60 * time.Second,
			},
		},
		logger: logger,
	}
}

// ServeHTTP handles GET and HEAD /health requests.
func (h *HealthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ch := h.getCachedOrRefresh(r.Context())

	if r.Method == http.MethodHead {
		w.WriteHeader(ch.status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ch.status)
	_, _ = w.Write(ch.body)
}

// getCachedOrRefresh returns a cached health result if fresh, otherwise refreshes.
// Only one goroutine refreshes at a time; others get the stale (or initial) cache.
func (h *HealthChecker) getCachedOrRefresh(ctx context.Context) *cachedHealth {
	now := time.Now().UnixNano()
	if expiry := h.cacheExpiry.Load(); expiry > 0 && now < expiry {
		if cached, ok := h.cachedResp.Load().(*cachedHealth); ok {
			return cached
		}
	}

	// Try to acquire the refresh lock; if another goroutine is refreshing, return stale.
	if !h.cacheMu.TryLock() {
		if cached, ok := h.cachedResp.Load().(*cachedHealth); ok {
			return cached
		}
		// No cache yet and another goroutine is refreshing — wait.
		h.cacheMu.Lock()
		h.cacheMu.Unlock()
		if cached, ok := h.cachedResp.Load().(*cachedHealth); ok {
			return cached
		}
	} else {
		defer h.cacheMu.Unlock()
	}

	result := h.check(ctx)

	status := http.StatusOK
	if result.PixoraBackend == statusDown || result.ClockwerkMedia == statusDown || result.Redis == statusDown {
		status = http.StatusServiceUnavailable
	}

	body, _ := json.Marshal(result)

	ch := &cachedHealth{resp: result, status: status, body: body}
	h.cachedResp.Store(ch)
	h.cacheExpiry.Store(time.Now().Add(healthCacheTTL).UnixNano())

	return ch
}

// check returns the aggregated health status of all dependencies in parallel.
func (h *HealthChecker) check(ctx context.Context) dto.HealthResponse {
	var pixoraStatus, clockwerkStatus, redisStatus string
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		pixoraStatus = h.checkURL(ctx, h.pixoraURL)
	}()
	go func() {
		defer wg.Done()
		clockwerkStatus = h.checkURL(ctx, h.clockwerkURL)
	}()
	go func() {
		defer wg.Done()
		redisStatus = h.checkRedis(ctx)
	}()

	wg.Wait()

	return dto.HealthResponse{
		Gateway:        statusOK,
		PixoraBackend:  pixoraStatus,
		ClockwerkMedia: clockwerkStatus,
		Redis:          redisStatus,
	}
}

// checkURL pings a backend health endpoint.
func (h *HealthChecker) checkURL(ctx context.Context, baseURL string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/health", baseURL), nil)
	if err != nil {
		return statusDown
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return statusDown
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return statusOK
	}
	return statusDown
}

// checkRedis pings the Redis connection.
func (h *HealthChecker) checkRedis(ctx context.Context) string {
	if h.rdb == nil {
		return statusDown
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := h.rdb.Ping(ctx).Err(); err != nil {
		return statusDown
	}
	return statusOK
}
