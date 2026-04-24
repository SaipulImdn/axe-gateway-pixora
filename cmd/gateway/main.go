// Package main is the entry point for the Pixora API Gateway.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/SaipulImdn/axe-gateway-pixora/internal/config"
	"github.com/SaipulImdn/axe-gateway-pixora/internal/router"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger := initLogger(cfg.Log.Level)
	defer func() { _ = logger.Sync() }()

	// Connect to Redis
	rdb := initRedis(cfg.Redis.URL, logger)
	if rdb != nil {
		defer func() { _ = rdb.Close() }()
	}

	// Setup router
	engine := router.Setup(cfg, rdb, logger)

	// Create HTTP server
	srv := &http.Server{
		Addr:              cfg.Address(),
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// Start server in a goroutine
	go func() {
		logger.Info("starting gateway",
			zap.String("address", cfg.Address()),
			zap.String("backend", cfg.Backend.URL),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("shutting down gateway", zap.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}

	logger.Info("gateway stopped")
}

// initLogger creates a structured Zap logger with the given level.
func initLogger(level string) *zap.Logger {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := cfg.Build()
	if err != nil {
		panic(fmt.Sprintf("failed to initialize logger: %v", err))
	}
	return logger
}

// initRedis creates a Redis client. Returns nil if connection fails (gateway runs without Redis).
func initRedis(url string, logger *zap.Logger) *redis.Client {
	opts, err := redis.ParseURL(url)
	if err != nil {
		logger.Warn("invalid redis URL, running without Redis", zap.Error(err))
		return nil
	}

	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Warn("redis connection failed, running without Redis", zap.Error(err))
		return nil
	}

	logger.Info("connected to Redis")
	return rdb
}
