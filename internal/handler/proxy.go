// Package handler provides HTTP handlers for the API gateway.
package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
	"github.com/SaipulImdn/axe-gateway-pixora/pkg/httpclient"
)

// hopByHopHeaders are headers that should not be forwarded by a proxy.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// ProxyHandler forwards requests to the backend service.
type ProxyHandler struct {
	backendURL string
	proxyCfg   config.ProxyConfig
	client     *httpclient.Client
	logger     *zap.Logger
}

// NewProxyHandler creates a new ProxyHandler.
func NewProxyHandler(backendURL string, proxyCfg config.ProxyConfig, logger *zap.Logger) *ProxyHandler {
	client := httpclient.New(
		httpclient.WithTimeout(time.Duration(proxyCfg.TimeoutDefault)*time.Second),
		httpclient.WithMaxRetries(1),
	)
	return &ProxyHandler{
		backendURL: strings.TrimRight(backendURL, "/"),
		proxyCfg:   proxyCfg,
		client:     client,
		logger:     logger,
	}
}

// Forward proxies the incoming request to the backend.
func (h *ProxyHandler) Forward(c *gin.Context) {
	targetURL := h.backendURL + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	timeout := h.resolveTimeout(c.Request.URL.Path, c.Request.Method)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	// Build the upstream request
	proxyReq, err := http.NewRequestWithContext(ctx, c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		h.logger.Error("failed to create proxy request", zap.Error(err))
		dto.BadGateway(c, "Failed to create upstream request.")
		return
	}

	// Copy headers, skipping hop-by-hop
	for key, values := range c.Request.Header {
		if hopByHopHeaders[key] {
			continue
		}
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	// Set X-Forwarded headers
	proxyReq.Header.Set("X-Forwarded-For", c.ClientIP())
	proxyReq.Header.Set("X-Forwarded-Host", c.Request.Host)
	proxyReq.Header.Set("X-Forwarded-Proto", c.Request.URL.Scheme)

	// Preserve content length for multipart uploads
	proxyReq.ContentLength = c.Request.ContentLength

	resp, err := h.client.Do(ctx, proxyReq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			dto.GatewayTimeout(c)
			return
		}
		h.logger.Error("backend request failed", zap.Error(err), zap.String("url", targetURL))
		dto.BadGateway(c, "Backend service is unavailable.")
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		if hopByHopHeaders[key] {
			continue
		}
		for _, v := range values {
			c.Writer.Header().Add(key, v)
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)

	// Stream the response body
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		h.logger.Warn("error streaming response body", zap.Error(err))
	}
}

// resolveTimeout returns the appropriate timeout for the given path and method.
func (h *ProxyHandler) resolveTimeout(path, method string) time.Duration {
	if strings.Contains(path, "/upload") {
		return time.Duration(h.proxyCfg.TimeoutUpload) * time.Second
	}
	if strings.Contains(path, "/download") {
		return time.Duration(h.proxyCfg.TimeoutDownload) * time.Second
	}
	return time.Duration(h.proxyCfg.TimeoutDefault) * time.Second
}
