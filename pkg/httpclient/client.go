// Package httpclient provides a reusable HTTP client with configurable timeout and retry logic.
package httpclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Client wraps http.Client with retry capabilities.
type Client struct {
	http       *http.Client
	maxRetries int
}

// Option configures the Client.
type Option func(*Client)

// WithTimeout sets the client timeout duration.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.http.Timeout = d
	}
}

// WithMaxRetries sets the maximum number of retries for retriable status codes.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		c.maxRetries = n
	}
}

// New creates a new HTTP client with the given options.
func New(opts ...Option) *Client {
	c := &Client{
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		maxRetries: 1,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Do executes an HTTP request with retry logic for 502/503 responses.
// POST requests are NOT retried to avoid duplicate side effects.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)

	retriable := req.Method != http.MethodPost
	var resp *http.Response
	var err error

	attempts := 1 + c.maxRetries
	if !retriable {
		attempts = 1
	}

	for i := range attempts {
		resp, err = c.http.Do(req)
		if err != nil {
			if i < attempts-1 && retriable {
				time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
				continue
			}
			return nil, fmt.Errorf("http request failed after %d attempts: %w", i+1, err)
		}

		if retriable && (resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable) {
			if i < attempts-1 {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
				continue
			}
		}

		return resp, nil
	}

	return resp, err
}
