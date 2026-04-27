# Axe Gateway Pixora

API Gateway for Pixora microservices. Routes requests to the correct backend service based on path prefix.

## Services

| Path Prefix | Backend Service |
|---|---|
| `/api/v1/auth/*`, `/api/v1/activity`, `/api/v1/favorites/*`, `/api/v1/notifications/*`, `/api/v1/share/*` | pixora-backend |
| `/api/v1/drive/*`, `/api/v1/sync/*`, `/api/v1/duplicates/*`, `/api/v1/faces/*` | clockwerk-media-pixora |

## Features

- Path-based routing to 2 backend microservices
- JWT validation with Redis-based token blacklist
- Per-IP and per-user rate limiting (Redis + in-memory fallback)
- CORS, structured logging (Zap), panic recovery
- Aggregated health checks (gateway + both backends + Redis)
- Streaming support for upload/download with extended timeouts
- Native Go `net/http` + `httputil.ReverseProxy` — zero framework overhead

## Quick Start

```bash
cp .env.example .env
# Edit .env with your service URLs and JWT secret
make run
```

## Health Check

```
GET /health
```
```json
{
  "gateway": "ok",
  "pixora_backend": "ok",
  "clockwerk_media": "ok",
  "redis": "ok"
}
```

## Environment Variables

| Variable | Description |
|---|---|
| `PIXORA_BACKEND_URL` | URL of pixora-backend service |
| `CLOCKWERK_MEDIA_URL` | URL of clockwerk-media-pixora service |
| `JWT_SECRET` | Shared JWT secret (same across all services) |
| `REDIS_URL` | Redis connection URL |
| `RATE_LIMIT_PUBLIC` | Rate limit for public routes (req/min) |
| `RATE_LIMIT_AUTHENTICATED` | Rate limit for authenticated routes (req/min) |
| `RATE_LIMIT_UPLOAD` | Rate limit for upload routes (req/min) |
