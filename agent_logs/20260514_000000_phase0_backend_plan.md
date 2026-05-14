# Phase 0 Backend Plan — Streaming Pipe

**Date:** 2026-05-14  
**Goal:** Establish the full Go backend skeleton for Phase 0 as defined in GUIDE.md.

---

## Inputs

- Go module path: `github.com/raythx98/go-travel`
- Frontend origin: GitHub Pages (exact domain TBD — CORS must accept it)
- Infrastructure: Oracle ARM VM via Docker Compose
- No existing Go code — greenfield

## Constraints

- `net/http` stdlib only — no chi or other routers
- No agent framework
- No MCP yet (Phase 3+)
- Plain Go goroutine-to-channel SSE (Redis pub/sub deferred to Phase 2)
- LangFuse tracing from day one

## Success Criteria

1. `go build ./...` succeeds
2. `go vet ./...` passes
3. `go test ./...` passes (no tests yet — just must not error)
4. `POST /plan` streams a hardcoded chunked response via SSE
5. CORS headers allow the GitHub Pages frontend origin
6. Docker Compose starts cleanly: Postgres+pgvector, Redis, LangFuse, Caddy, Ollama, SearXNG
7. LangFuse receives a test trace on startup (smoke test)

## Technical Approach

### Directory layout

```
go-travel/
├── cmd/server/main.go          # entry point: load env, wire deps, start HTTP server
├── internal/
│   ├── config/config.go        # env-driven config struct
│   ├── server/server.go        # http.Server setup, route registration
│   ├── server/handlers.go      # /plan SSE handler + /health handler
│   ├── middleware/
│   │   ├── logging.go          # structured slog request/response logging
│   │   ├── cors.go             # CORS middleware (GitHub Pages origin)
│   │   └── recovery.go         # panic recovery → 500
│   └── langfuse/client.go      # thin LangFuse HTTP client (trace/span/score)
├── Makefile
├── docker-compose.yml
├── Caddyfile
├── .envrc.example
└── go.mod
```

### /plan endpoint

```
POST /plan
Body: {"goal": "..."}
Response: text/event-stream

Emits hardcoded chunked lines:
  data: Thinking about your trip...\n\n
  data: Checking flights...\n\n
  data: Done!\n\n
```

No real Claude call in Phase 0 — verifies the streaming pipe end-to-end.

### Middleware stack (innermost first)

```
recovery → logging → cors → mux
```

### LangFuse integration

Thin HTTP client posting to LangFuse REST API. On server start, emit one test trace so the connection is verified. Full LLM traces wired in Phase 1.

### Config

All config from environment variables. `.envrc.example` documents required vars. `config.go` reads them with `os.Getenv`, validates required ones, returns typed struct.

### Makefile targets

| Target | Action |
|--------|--------|
| `make build` | `go build -o bin/server ./cmd/server` |
| `make run` | build + run |
| `make run_local` | sqlc + run with local .envrc |
| `make test` | `go test ./...` |
| `make sqlc` | `sqlc generate` |
| `make up` | `docker compose up -d` |
| `make down` | `docker compose down` |
| `make logs` | `docker compose logs -f` |
| `make create_migration` | `migrate create -ext sql -dir sqlc/migrations -seq $(name)` |
| `make fmt` | `go fmt ./...` |
| `make vet` | `go vet ./...` |

## Files to Change

None — all files are new.

## New Files

- `go.mod`
- `cmd/server/main.go`
- `internal/config/config.go`
- `internal/server/server.go`
- `internal/server/handlers.go`
- `internal/middleware/logging.go`
- `internal/middleware/cors.go`
- `internal/middleware/recovery.go`
- `internal/langfuse/client.go`
- `Makefile`
- `docker-compose.yml`
- `Caddyfile`
- `.envrc.example`

## Assumptions

1. GitHub Pages frontend origin not yet known — CORS will accept a configurable `ALLOWED_ORIGIN` env var. Defaults to `*` locally.
2. LangFuse public key and secret key come from env vars (`LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`, `LANGFUSE_HOST`).
3. Docker Compose runs on Oracle VM; local dev uses `make run_local` against a locally sourced `.envrc`.
4. No auth in Phase 0 — stub middleware placeholder only.
5. `go-migrate` CLI is available locally for migration management.

## Future Work (not in scope)

- Real Claude API call (Phase 1)
- Redis pub/sub SSE (Phase 2)
- Auth middleware (Phase 4)
- sqlc schema + queries (Phase 1)
