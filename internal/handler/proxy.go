// Package handler provides HTTP handlers for the API gateway.
package handler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/middleware"
)

// sharedTransport is reused across all proxy handlers to maximise connection
// pooling between the gateway and its backends. A single pool avoids the
// overhead of separate TCP/TLS handshakes per proxy instance.
var sharedTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     true,
	// ResponseHeaderTimeout is NOT set here because it differs per proxy
	// (default vs upload/download). We use per-request context deadlines instead.
}

// ProxyHandler forwards requests to the backend using httputil.ReverseProxy.
type ProxyHandler struct {
	defaultProxy    *httputil.ReverseProxy
	uploadProxy     *httputil.ReverseProxy
	logger          *zap.Logger
	timeoutDefault  time.Duration
	timeoutUpload   time.Duration
	timeoutDownload time.Duration
}

// NewProxyHandler creates a new ProxyHandler with separate proxies for default and long-lived requests.
func NewProxyHandler(backendURL string, proxyCfg config.ProxyConfig, logger *zap.Logger) *ProxyHandler {
	target, _ := url.Parse(strings.TrimRight(backendURL, "/"))

	timeoutDefault := time.Duration(proxyCfg.TimeoutDefault) * time.Second
	timeoutUpload := time.Duration(proxyCfg.TimeoutUpload) * time.Second
	timeoutDownload := time.Duration(proxyCfg.TimeoutDownload) * time.Second

	h := &ProxyHandler{
		logger:          logger,
		timeoutDefault:  timeoutDefault,
		timeoutUpload:   timeoutUpload,
		timeoutDownload: timeoutDownload,
	}

	h.defaultProxy = h.newProxy(target, false)
	h.uploadProxy = h.newProxy(target, true)

	return h
}

// newProxy creates an httputil.ReverseProxy backed by the shared transport.
// streaming enables FlushInterval for download/upload responses.
func (h *ProxyHandler) newProxy(target *url.URL, streaming bool) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host

			// Preserve Authorization header across different hosts
			if auth := pr.In.Header.Get("Authorization"); auth != "" {
				pr.Out.Header.Set("Authorization", auth)
			}

			// Inject authenticated user ID so downstream services can
			// identify the caller without parsing the JWT themselves.
			if uid, ok := middleware.GetUserID(pr.Out.Context()); ok {
				pr.Out.Header.Set("X-User-ID", uid)
			}

			if clientIP := middleware.GetClientIP(pr.Out.Context()); clientIP != "" {
				pr.Out.Header.Set("X-Forwarded-For", clientIP)
			}
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)

			proto := pr.In.Header.Get("X-Forwarded-Proto")
			if proto == "" {
				if pr.In.TLS != nil {
					proto = "https"
				} else {
					proto = "http"
				}
			}
			pr.Out.Header.Set("X-Forwarded-Proto", proto)
		},

		Transport: sharedTransport,

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Context cancellation by the client (e.g. disconnect during
			// streaming) is expected — log at debug and don't write a response
			// because the client is already gone.
			if r.Context().Err() != nil {
				h.logger.Debug("proxy request cancelled",
					zap.String("path", r.URL.Path),
					zap.Error(err),
				)
				return
			}

			// Distinguish timeout from other proxy errors so the client
			// gets the correct 504 instead of a generic 502.
			if isTimeout(err) {
				h.logger.Warn("proxy timeout", zap.Error(err), zap.String("path", r.URL.Path))
				dto.GatewayTimeout(w)
				return
			}

			h.logger.Error("proxy error", zap.Error(err), zap.String("path", r.URL.Path))
			dto.BadGateway(w, "Backend service is unavailable.")
		},
	}

	// Enable streaming flush for upload/download proxy to avoid buffering
	// large responses in memory. This flushes data to the client immediately.
	if streaming {
		proxy.FlushInterval = -1 // flush immediately
	}

	return proxy
}

// ServeHTTP routes the request to the appropriate proxy based on the path.
// Binary/streaming paths (upload, download, thumbnail) use the streaming proxy
// with longer timeouts. Everything else uses the default proxy.
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			h.logger.Warn("proxy panic recovered",
				zap.Any("error", rec),
				zap.String("path", r.URL.Path),
				zap.String("method", r.Method),
			)
		}
	}()

	path := r.URL.Path

	switch {
	case strings.Contains(path, "/upload"):
		ctx, cancel := context.WithTimeout(r.Context(), h.timeoutUpload)
		defer cancel()
		h.uploadProxy.ServeHTTP(w, r.WithContext(ctx))

	case strings.Contains(path, "/download"), strings.Contains(path, "/thumbnail"):
		ctx, cancel := context.WithTimeout(r.Context(), h.timeoutDownload)
		defer cancel()
		h.uploadProxy.ServeHTTP(w, r.WithContext(ctx))

	default:
		ctx, cancel := context.WithTimeout(r.Context(), h.timeoutDefault)
		defer cancel()
		h.defaultProxy.ServeHTTP(w, r.WithContext(ctx))
	}
}

// isTimeout returns true when the error (or any wrapped error) indicates a
// timeout — context deadline exceeded, net.Error timeout, or OS timeout.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	return false
}
