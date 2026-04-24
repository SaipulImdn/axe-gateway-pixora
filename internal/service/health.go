// Package service provides business logic services for the gateway.
package service

import (
	"context"
	"fmt"
	"net/http"
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
	logger     *zap.Logger
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker(rdb *redis.Client, backendURL string, logger *zap.Logger) *HealthChecker {
	return &HealthChecker{
		rdb:        rdb,
		backendURL: backendURL,
		logger:     logger,
	}
}

// Check returns the aggregated health status of all dependencies.
func (h *HealthChecker) Check(ctx context.Context) dto.HealthResponse {
	resp := dto.HealthResponse{
		Gateway: statusOK,
		Backend: h.checkBackend(ctx),
		Redis:   h.checkRedis(ctx),
	}
	return resp
}

// IsHealthy returns true if all dependencies are healthy.
func (h *HealthChecker) IsHealthy(ctx context.Context) bool {
	resp := h.Check(ctx)
	return resp.Backend == statusOK && resp.Redis == statusOK
}

// checkBackend pings the backend health endpoint.
func (h *HealthChecker) checkBackend(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/health", h.backendURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		h.logger.Warn("failed to create backend health request", zap.Error(err))
		return statusDown
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.logger.Warn("backend health check failed", zap.Error(err))
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
		h.logger.Warn("redis health check failed", zap.Error(err))
		return statusDown
	}
	return statusOK
}
