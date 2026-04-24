// Package handler provides HTTP handlers for the API gateway.
package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/middleware"
)

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

	h.defaultProxy = h.newProxy(target, timeoutDefault, false)
	h.uploadProxy = h.newProxy(target, max(timeoutUpload, timeoutDownload), true)

	return h
}

// newProxy creates an httputil.ReverseProxy with the given timeout.
// streaming enables FlushInterval for download/upload responses.
func (h *ProxyHandler) newProxy(target *url.URL, timeout time.Duration, streaming bool) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host

			// Preserve Authorization header across different hosts
			if auth := pr.In.Header.Get("Authorization"); auth != "" {
				pr.Out.Header.Set("Authorization", auth)
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

		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   50,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			ForceAttemptHTTP2:     true,
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Context cancellation during streaming is normal (client disconnect).
			// Don't treat it as an error — just log at debug level.
			if r.Context().Err() != nil {
				h.logger.Debug("proxy request cancelled",
					zap.String("path", r.URL.Path),
					zap.Error(err),
				)
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
// Panics from ReverseProxy (e.g. "net/http: abort Handler" during streaming)
// are caught and logged gracefully.
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			// "net/http: abort Handler" is expected when client disconnects
			// during streaming. Log it but don't crash.
			h.logger.Warn("proxy panic recovered",
				zap.Any("error", rec),
				zap.String("path", r.URL.Path),
				zap.String("method", r.Method),
			)
		}
	}()

	path := r.URL.Path

	if strings.Contains(path, "/upload") || strings.Contains(path, "/download") {
		timeout := h.timeoutUpload
		if strings.Contains(path, "/download") {
			timeout = h.timeoutDownload
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		h.uploadProxy.ServeHTTP(w, r.WithContext(ctx))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeoutDefault)
	defer cancel()
	h.defaultProxy.ServeHTTP(w, r.WithContext(ctx))
}
