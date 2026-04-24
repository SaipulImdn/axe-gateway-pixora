// Package middleware provides HTTP middleware for the API gateway.
package middleware

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/dto"
)

// Context keys for storing parsed JWT claims.
const (
	ContextKeyUserID = "user_id"
	ContextKeyToken  = "raw_token"
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

// AuthMiddleware validates JWT tokens and checks the Redis blacklist.
func AuthMiddleware(jwtSecret string, rdb *redis.Client, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if publicPaths[c.Request.URL.Path] {
			c.Next()
			return
		}

		tokenStr, ok := extractBearerToken(c)
		if !ok {
			return
		}

		claims, ok := validateToken(c, tokenStr, jwtSecret, logger)
		if !ok {
			return
		}

		if !checkBlacklist(c, tokenStr, rdb, logger) {
			return
		}

		// Store user ID and raw token in context for downstream use
		if sub, ok := claims["sub"].(string); ok {
			c.Set(ContextKeyUserID, sub)
		}
		c.Set(ContextKeyToken, tokenStr)

		c.Next()
	}
}

// extractBearerToken extracts the Bearer token from the Authorization header.
// Returns the token string and true on success, or aborts the request and returns false.
func extractBearerToken(c *gin.Context) (string, bool) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		dto.Unauthorized(c, "Missing authorization header.")
		return "", false
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		dto.Unauthorized(c, "Invalid authorization header format. Expected: Bearer <token>")
		return "", false
	}

	return parts[1], true
}

// validateToken parses and validates the JWT token, ensuring it's an access token.
// Returns the claims and true on success, or aborts the request and returns false.
func validateToken(c *gin.Context, tokenStr, jwtSecret string, logger *zap.Logger) (jwt.MapClaims, bool) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})
	if err != nil || !token.Valid {
		logger.Warn("invalid JWT token", zap.Error(err))
		dto.Unauthorized(c, "Invalid or expired token.")
		return nil, false
	}

	tokenType, _ := claims["token_type"].(string)
	if tokenType != "access" {
		dto.Unauthorized(c, "Invalid token type.")
		return nil, false
	}

	return claims, true
}

// checkBlacklist verifies the token is not revoked via Redis blacklist.
// Returns true if the token is allowed, or aborts the request and returns false.
func checkBlacklist(c *gin.Context, tokenStr string, rdb *redis.Client, logger *zap.Logger) bool {
	if rdb == nil {
		return true
	}

	hash := sha256.Sum256([]byte(tokenStr))
	blacklistKey := fmt.Sprintf("blacklist:%x", hash)

	exists, err := rdb.Exists(c.Request.Context(), blacklistKey).Result()
	if err != nil {
		logger.Error("redis blacklist check failed", zap.Error(err))
		// Fail open: allow request if Redis is unavailable
		return true
	}

	if exists > 0 {
		dto.Unauthorized(c, "Token has been revoked.")
		return false
	}

	return true
}
