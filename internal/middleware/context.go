// Package middleware provides HTTP middleware for the API gateway.
package middleware

import "context"

// contextKey is an unexported type for context keys to avoid collisions.
type contextKey int

const (
	// UserIDKey is the context key for the authenticated user's ID.
	UserIDKey contextKey = iota
	// ClientIPKey is the context key for the client's IP address.
	ClientIPKey
)

// GetUserID extracts the user ID from the request context.
func GetUserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(UserIDKey).(string)
	return v, ok
}

// GetClientIP extracts the client IP from the request context.
func GetClientIP(ctx context.Context) string {
	v, _ := ctx.Value(ClientIPKey).(string)
	return v
}
