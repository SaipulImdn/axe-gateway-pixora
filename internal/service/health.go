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
	rdb        *redis.Client
	backendURL string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewHealthChecker creates a new HealthChecker with a dedicated HTTP client.
func NewHealthChecker(rdb *redis.Client, backendURL string, logger *zap.Logger) *HealthChecker {
	return &HealthChecker{
		rdb:        rdb,
		backendURL: backendURL,
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
	if result.Backend == statusDown || result.Redis == statusDown {
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
	var backendStatus, redisStatus string
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		backendStatus = h.checkBackend(ctx)
	}()
	go func() {
		defer wg.Done()
		redisStatus = h.checkRedis(ctx)
	}()

	wg.Wait()

	return dto.HealthResponse{
		Gateway: statusOK,
		Backend: backendStatus,
		Redis:   redisStatus,
	}
}

func (h *HealthChecker) checkBackend(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/health", h.backendURL), nil)
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
