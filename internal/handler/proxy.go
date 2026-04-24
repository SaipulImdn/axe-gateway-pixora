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
	defaultProxy *httputil.ReverseProxy
	uploadProxy  *httputil.ReverseProxy
	logger       *zap.Logger
	// Pre-computed timeout durations
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

	h.defaultProxy = h.newProxy(target, timeoutDefault)
	h.uploadProxy = h.newProxy(target, max(timeoutUpload, timeoutDownload))

	return h
}

// newProxy creates an httputil.ReverseProxy with the given timeout.
func (h *ProxyHandler) newProxy(target *url.URL, timeout time.Duration) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		// Rewrite replaces the deprecated Director. It rewrites the request
		// to target the backend while preserving all original headers including Authorization.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host

			// Explicitly copy Authorization header — Rewrite strips it by default
			// when proxying to a different host (security measure we need to override).
			if auth := pr.In.Header.Get("Authorization"); auth != "" {
				pr.Out.Header.Set("Authorization", auth)
			}

			// Set X-Forwarded headers
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
			if r.Context().Err() == context.DeadlineExceeded {
				dto.GatewayTimeout(w)
				return
			}
			h.logger.Error("proxy error", zap.Error(err), zap.String("path", r.URL.Path))
			dto.BadGateway(w, "Backend service is unavailable.")
		},
	}

	return proxy
}

// ServeHTTP routes the request to the appropriate proxy based on the path.
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
