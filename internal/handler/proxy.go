// Package handler provides HTTP handlers for the API gateway.
package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
	"github.com/SaipulImdn/axe-gateway-pixora/pkg/httpclient"
)

// streamBufSize is the buffer size for streaming proxy responses (32 KB).
const streamBufSize = 32 * 1024

// bufPool reuses byte slices for io.CopyBuffer to reduce GC pressure.
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, streamBufSize)
		return &buf
	},
}

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
	// Pre-computed timeout durations to avoid per-request conversion
	timeoutDefault  time.Duration
	timeoutUpload   time.Duration
	timeoutDownload time.Duration
}

// NewProxyHandler creates a new ProxyHandler.
func NewProxyHandler(backendURL string, proxyCfg config.ProxyConfig, logger *zap.Logger) *ProxyHandler {
	timeoutDefault := time.Duration(proxyCfg.TimeoutDefault) * time.Second
	timeoutUpload := time.Duration(proxyCfg.TimeoutUpload) * time.Second
	timeoutDownload := time.Duration(proxyCfg.TimeoutDownload) * time.Second

	defaultClient := httpclient.New(
		httpclient.WithTimeout(timeoutDefault),
		httpclient.WithMaxRetries(1),
	)
	longTimeout := max(timeoutUpload, timeoutDownload)
	longLivedClient := httpclient.New(
		httpclient.WithTimeout(longTimeout),
		httpclient.WithMaxRetries(0),
	)

	return &ProxyHandler{
		backendURL:      strings.TrimRight(backendURL, "/"),
		proxyCfg:        proxyCfg,
		defaultClient:   defaultClient,
		longLivedClient: longLivedClient,
		logger:          logger,
		timeoutDefault:  timeoutDefault,
		timeoutUpload:   timeoutUpload,
		timeoutDownload: timeoutDownload,
	}
}

// Forward proxies the incoming request to the backend.
func (h *ProxyHandler) Forward(c *gin.Context) {
	targetURL := h.buildTargetURL(c)

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

	// Stream response body using pooled buffer to reduce GC pressure
	bufPtr := bufPool.Get().(*[]byte)
	_, err = io.CopyBuffer(c.Writer, resp.Body, *bufPtr)
	bufPool.Put(bufPtr)
	if err != nil {
		h.logger.Warn("error streaming response body", zap.Error(err))
	}
}

// buildTargetURL constructs the backend URL using strings.Builder to minimize allocations.
func (h *ProxyHandler) buildTargetURL(c *gin.Context) string {
	path := c.Request.URL.Path
	query := c.Request.URL.RawQuery

	// Pre-calculate capacity: backendURL + path + "?" + query
	capacity := len(h.backendURL) + len(path)
	if query != "" {
		capacity += 1 + len(query)
	}

	var b strings.Builder
	b.Grow(capacity)
	b.WriteString(h.backendURL)
	b.WriteString(path)
	if query != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	return b.String()
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
		return h.timeoutUpload, h.longLivedClient
	}
	if strings.Contains(path, "/download") {
		return h.timeoutDownload, h.longLivedClient
	}
	return h.timeoutDefault, h.defaultClient
}
