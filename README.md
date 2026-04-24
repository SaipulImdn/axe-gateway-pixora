# Axe Gateway Pixora

API Gateway for the Pixora Backend microservice. Built with Go, Gin, and clean architecture.

## Features

- Reverse proxy to pixora-backend with streaming support
- JWT validation with Redis-based token blacklist
- Per-IP and per-user rate limiting (Redis + in-memory fallback)
- CORS, structured logging (Zap), panic recovery
- Aggregated health checks (gateway + backend + Redis)
- Multi-stage Docker build for minimal image size

## Quick Start

```bash
# Copy and configure environment
cp .env.example .env

# Run locally
make run

# Or with Docker Compose
docker compose up -d
```

## Architecture

```
cmd/gateway/main.go          → Entry point, DI, graceful shutdown
internal/config/              → Viper-based configuration
internal/middleware/          → Auth, CORS, rate limiter, logger, recovery
internal/handler/proxy.go    → Reverse proxy with timeout & retry
internal/service/health.go   → Health check aggregator
internal/router/router.go    → Route definitions
internal/dto/                → Standardized error/response types
pkg/httpclient/              → Reusable HTTP client with retry
```

## Deployment

Built for Koyeb deployment via Docker Hub. See `.github/workflows/docker-publish.yml`.

Environment variables required in Koyeb dashboard:

| Variable | Example |
|---|---|
| `GATEWAY_PORT` | `9090` |
| `PIXORA_BACKEND_URL` | `https://<service>.koyeb.app` |
| `JWT_SECRET` | Same as backend |
| `REDIS_URL` | Managed Redis URL |
