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
)

// publicPaths lists routes that do not require JWT authentication.
var publicPaths = map[string]bool{
	"/api/v1/auth/signup":       true,
	"/api/v1/auth/signin":       true,
	"/api/v1/auth/google":       true,
	"/api/v1/auth/refresh":      true,
	"/api/v1/auth/set-password": true,
	"/health":                   true,
}

// Auth returns middleware that validates JWT tokens and checks the Redis blacklist.
func Auth(jwtSecret string, rdb *redis.Client, logger *zap.Logger) func(http.Handler) http.Handler {
	secretBytes := []byte(jwtSecret)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] {
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

func checkBlacklist(w http.ResponseWriter, r *http.Request, tokenStr string, rdb *redis.Client, logger *zap.Logger) bool {
	if rdb == nil {
		return true
	}

	hash := sha256.Sum256([]byte(tokenStr))
	var buf [74]byte
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
