// Package config provides application configuration loaded from environment variables.
package config

import (
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration values for the gateway.
type Config struct {
	Gateway   GatewayConfig
	Backend   BackendConfig
	JWT       JWTConfig
	Redis     RedisConfig
	RateLimit RateLimitConfig
	Proxy     ProxyConfig
	Log       LogConfig
}

// GatewayConfig holds gateway server settings.
type GatewayConfig struct {
	Host string
	Port string
}

// BackendConfig holds upstream service URLs.
type BackendConfig struct {
	PixoraURL    string // Auth, Activity, Favorites, Notifications, Share
	ClockwerkURL string // Drive, Sync, Duplicates
	SpectreURL   string // Faces (detection, recognition, grouping)
}

// JWTConfig holds JWT validation settings.
type JWTConfig struct {
	Secret string
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	URL string
}

// RateLimitConfig holds rate limiting thresholds (requests per minute).
type RateLimitConfig struct {
	Public        int
	Authenticated int
	Upload        int
}

// ProxyConfig holds reverse proxy timeout settings in seconds.
type ProxyConfig struct {
	TimeoutDefault  int
	TimeoutUpload   int
	TimeoutDownload int
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level string
}

// Load reads configuration from environment variables and .env file.
func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigFile(".env")
	v.SetConfigType("env")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("GATEWAY_HOST", "0.0.0.0")
	v.SetDefault("GATEWAY_PORT", "9090")
	v.SetDefault("PIXORA_BACKEND_URL", "https://inappropriate-vanessa-pixora-435590e7.koyeb.app")
	v.SetDefault("CLOCKWERK_MEDIA_URL", "http://localhost:8081")
	v.SetDefault("SPECTRE_FACE_URL", "http://localhost:8082")
	v.SetDefault("JWT_SECRET", "")
	v.SetDefault("REDIS_URL", "redis://localhost:6379")
	v.SetDefault("RATE_LIMIT_PUBLIC", 60)
	v.SetDefault("RATE_LIMIT_AUTHENTICATED", 600)
	v.SetDefault("RATE_LIMIT_UPLOAD", 30)
	v.SetDefault("PROXY_TIMEOUT_DEFAULT", 30)
	v.SetDefault("PROXY_TIMEOUT_UPLOAD", 300)
	v.SetDefault("PROXY_TIMEOUT_DOWNLOAD", 300)
	v.SetDefault("LOG_LEVEL", "info")

	_ = v.ReadInConfig()

	cfg := &Config{
		Gateway: GatewayConfig{
			Host: v.GetString("GATEWAY_HOST"),
			Port: v.GetString("GATEWAY_PORT"),
		},
		Backend: BackendConfig{
			PixoraURL:    v.GetString("PIXORA_BACKEND_URL"),
			ClockwerkURL: v.GetString("CLOCKWERK_MEDIA_URL"),
			SpectreURL:   v.GetString("SPECTRE_FACE_URL"),
		},
		JWT: JWTConfig{
			Secret: v.GetString("JWT_SECRET"),
		},
		Redis: RedisConfig{
			URL: v.GetString("REDIS_URL"),
		},
		RateLimit: RateLimitConfig{
			Public:        v.GetInt("RATE_LIMIT_PUBLIC"),
			Authenticated: v.GetInt("RATE_LIMIT_AUTHENTICATED"),
			Upload:        v.GetInt("RATE_LIMIT_UPLOAD"),
		},
		Proxy: ProxyConfig{
			TimeoutDefault:  v.GetInt("PROXY_TIMEOUT_DEFAULT"),
			TimeoutUpload:   v.GetInt("PROXY_TIMEOUT_UPLOAD"),
			TimeoutDownload: v.GetInt("PROXY_TIMEOUT_DOWNLOAD"),
		},
		Log: LogConfig{
			Level: v.GetString("LOG_LEVEL"),
		},
	}

	return cfg, nil
}

// Address returns the gateway listen address in host:port format.
func (c *Config) Address() string {
	return c.Gateway.Host + ":" + c.Gateway.Port
}
