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
	backendURL      string
	proxyCfg        config.ProxyConfig
	defaultClient   *httpclient.Client
	longLivedClient *httpclient.Client
	logger          *zap.Logger
}

// NewProxyHandler creates a new ProxyHandler.
func NewProxyHandler(backendURL string, proxyCfg config.ProxyConfig, logger *zap.Logger) *ProxyHandler {
	defaultClient := httpclient.New(
		httpclient.WithTimeout(time.Duration(proxyCfg.TimeoutDefault)*time.Second),
		httpclient.WithMaxRetries(1),
	)
	// Separate client for upload/download with longer timeout
	maxTimeout := proxyCfg.TimeoutUpload
	if proxyCfg.TimeoutDownload > maxTimeout {
		maxTimeout = proxyCfg.TimeoutDownload
	}
	longLivedClient := httpclient.New(
		httpclient.WithTimeout(time.Duration(maxTimeout)*time.Second),
		httpclient.WithMaxRetries(0),
	)
	return &ProxyHandler{
		backendURL:      strings.TrimRight(backendURL, "/"),
		proxyCfg:        proxyCfg,
		defaultClient:   defaultClient,
		longLivedClient: longLivedClient,
		logger:          logger,
	}
}

// Forward proxies the incoming request to the backend.
func (h *ProxyHandler) Forward(c *gin.Context) {
	targetURL := h.backendURL + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	timeout, client := h.resolveTimeoutAndClient(c.Request.URL.Path)
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	proxyReq, err := http.NewRequestWithContext(ctx, c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		h.logger.Error("failed to create proxy request", zap.Error(err))
		dto.BadGateway(c, "Failed to create upstream request.")
		return
	}

	h.copyRequestHeaders(c, proxyReq)
	proxyReq.ContentLength = c.Request.ContentLength

	resp, err := client.Do(ctx, proxyReq)
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

	h.copyResponseHeaders(c, resp)
	c.Writer.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		h.logger.Warn("error streaming response body", zap.Error(err))
	}
}

// copyRequestHeaders copies incoming headers to the proxy request, skipping hop-by-hop headers.
func (h *ProxyHandler) copyRequestHeaders(c *gin.Context, proxyReq *http.Request) {
	for key, values := range c.Request.Header {
		if hopByHopHeaders[key] {
			continue
		}
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	proxyReq.Header.Set("X-Forwarded-For", c.ClientIP())
	proxyReq.Header.Set("X-Forwarded-Host", c.Request.Host)

	// Detect protocol from X-Forwarded-Proto or TLS
	proto := c.GetHeader("X-Forwarded-Proto")
	if proto == "" {
		if c.Request.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	proxyReq.Header.Set("X-Forwarded-Proto", proto)
}

// copyResponseHeaders copies backend response headers, skipping hop-by-hop headers.
func (h *ProxyHandler) copyResponseHeaders(c *gin.Context, resp *http.Response) {
	for key, values := range resp.Header {
		if hopByHopHeaders[key] {
			continue
		}
		for _, v := range values {
			c.Writer.Header().Add(key, v)
		}
	}
}

// resolveTimeoutAndClient returns the appropriate timeout and HTTP client for the given path.
func (h *ProxyHandler) resolveTimeoutAndClient(path string) (time.Duration, *httpclient.Client) {
	if strings.Contains(path, "/upload") {
		return time.Duration(h.proxyCfg.TimeoutUpload) * time.Second, h.longLivedClient
	}
	if strings.Contains(path, "/download") {
		return time.Duration(h.proxyCfg.TimeoutDownload) * time.Second, h.longLivedClient
	}
	return time.Duration(h.proxyCfg.TimeoutDefault) * time.Second, h.defaultClient
}
