package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

const (
	bearerPrefix    = "Bearer "
	blacklistPrefix = "blacklist:"
	// blacklistKeyLen = len("blacklist:") + hex-encoded SHA-256 (64 chars) = 74
	blacklistKeyLen = 74
)

// publicPaths lists routes that do not require JWT authentication.
// Using a map[string]struct{} avoids the 1-byte overhead per entry of map[string]bool.
var publicPaths = map[string]struct{}{
	"/api/v1/auth/signup":       {},
	"/api/v1/auth/signin":       {},
	"/api/v1/auth/google":       {},
	"/api/v1/auth/refresh":      {},
	"/api/v1/auth/set-password": {},
	"/health":                   {},
}

// Auth returns middleware that validates JWT tokens and checks the Redis blacklist.
func Auth(jwtSecret string, rdb *redis.Client, logger *zap.Logger) func(http.Handler) http.Handler {
	secretBytes := []byte(jwtSecret)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := publicPaths[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			tokenStr, ok := extractBearerToken(w, r)
			if !ok {
				return
			}

			claims, ok := validateToken(w, tokenStr, secretBytes, logger)
			if !ok {
				return
			}

			if !checkBlacklist(w, r, tokenStr, rdb, logger) {
				return
			}

			ctx := r.Context()
			if sub, ok := claims["sub"].(string); ok {
				ctx = context.WithValue(ctx, UserIDKey, sub)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		dto.Unauthorized(w, "Missing authorization header.")
		return "", false
	}

	token, found := strings.CutPrefix(authHeader, bearerPrefix)
	if !found {
		token, found = strings.CutPrefix(authHeader, "bearer ")
		if !found {
			dto.Unauthorized(w, "Invalid authorization header format. Expected: Bearer <token>")
			return "", false
		}
	}

	return token, true
}

func validateToken(w http.ResponseWriter, tokenStr string, secretBytes []byte, logger *zap.Logger) (jwt.MapClaims, bool) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secretBytes, nil
	})
	if err != nil || !token.Valid {
		logger.Warn("invalid JWT token", zap.Error(err))
		dto.Unauthorized(w, "Invalid or expired token.")
		return nil, false
	}

	tokenType, _ := claims["token_type"].(string)
	if tokenType != "access" {
		dto.Unauthorized(w, "Invalid token type.")
		return nil, false
	}

	return claims, true
}

// checkBlacklist verifies the token is not revoked.
// Uses a stack-allocated [74]byte buffer to build the Redis key without heap allocation.
func checkBlacklist(w http.ResponseWriter, r *http.Request, tokenStr string, rdb *redis.Client, logger *zap.Logger) bool {
	if rdb == nil {
		return true
	}

	hash := sha256.Sum256([]byte(tokenStr))
	var buf [blacklistKeyLen]byte
	copy(buf[:], blacklistPrefix)
	hex.Encode(buf[len(blacklistPrefix):], hash[:])

	exists, err := rdb.Exists(r.Context(), string(buf[:])).Result()
	if err != nil {
		logger.Error("redis blacklist check failed", zap.Error(err))
		return true
	}

	if exists > 0 {
		dto.Unauthorized(w, "Token has been revoked.")
		return false
	}

	return true
}
