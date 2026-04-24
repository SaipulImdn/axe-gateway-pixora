// Package middleware provides HTTP middleware for the API gateway.
package middleware

import (
	"context"
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
		// Skip auth for public routes
		if publicPaths[c.Request.URL.Path] {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			dto.Unauthorized(c, "Missing authorization header.")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			dto.Unauthorized(c, "Invalid authorization header format. Expected: Bearer <token>")
			return
		}

		tokenStr := parts[1]

		// Parse and validate the JWT token
		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			logger.Warn("invalid JWT token", zap.Error(err))
			dto.Unauthorized(c, "Invalid or expired token.")
			return
		}

		// Verify token_type is "access"
		tokenType, _ := claims["token_type"].(string)
		if tokenType != "access" {
			dto.Unauthorized(c, "Invalid token type.")
			return
		}

		// Check token blacklist in Redis
		if rdb != nil {
			hash := sha256.Sum256([]byte(tokenStr))
			blacklistKey := fmt.Sprintf("blacklist:%x", hash)
			exists, err := rdb.Exists(context.Background(), blacklistKey).Result()
			if err != nil {
				logger.Error("redis blacklist check failed", zap.Error(err))
				// Fail open: allow request if Redis is unavailable
			} else if exists > 0 {
				dto.Unauthorized(c, "Token has been revoked.")
				return
			}
		}

		// Store user ID in context for downstream use
		if sub, ok := claims["sub"].(string); ok {
			c.Set(ContextKeyUserID, sub)
		}
		c.Set(ContextKeyToken, tokenStr)

		c.Next()
	}
}
