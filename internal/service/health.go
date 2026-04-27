// Package service provides business logic services for the gateway.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

const (
	statusOK   = "ok"
	statusDown = "down"
)

// HealthChecker aggregates health status from gateway dependencies.
type HealthChecker struct {
	rdb          *redis.Client
	pixoraURL    string
	clockwerkURL string
	httpClient   *http.Client
	logger       *zap.Logger
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
					Timeout: 3 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout: 3 * time.Second,
				DisableKeepAlives:   true,
			},
		},
		logger: logger,
	}
}

// ServeHTTP handles GET and HEAD /health requests.
func (h *HealthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	result := h.Check(r.Context())

	status := http.StatusOK
	if result.PixoraBackend == statusDown || result.ClockwerkMedia == statusDown || result.Redis == statusDown {
		status = http.StatusServiceUnavailable
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

// Check returns the aggregated health status of all dependencies in parallel.
func (h *HealthChecker) Check(ctx context.Context) dto.HealthResponse {
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
