# Phase 1 Backend Plan — Single Agent

**Date:** 2026-05-14  
**Goal:** Implement the full ReAct agent loop with a real LLM client, real tools, and structured itinerary output. The Phase 0 SSE pipe is reused unchanged; the handler is updated to run the agent.

---

## Inputs

- Phase 0 complete: SSE pipe, middleware stack, config, tracing, LangFuse client
- Frontend at Phase 2 (React + Vite) consuming `POST /plan` SSE
- Claude primary, Ollama fallback (via `LLMClient` interface)
- No existing Go agent or LLM code

## Constraints

- No agent framework — plain Go ReAct loop
- All agents reference `LLMClient` interface — never Claude or Ollama directly
- Every external tool call uses `context.WithTimeout`
- Retry transient failures: exponential backoff 1s/2s/4s, max 3 attempts; fail fast on 401/400
- Stream status of every tool call to the client
- Enforce `maxSteps` to prevent infinite loops
- Phase 1 is single-instance — in-memory channel for ask_user responses (Redis pub/sub deferred to Phase 2)
- Mock Booking.com — realistic hardcoded data; real API in Phase 2
- `net/http` stdlib only; no new routers

## Success Criteria

1. `go build ./...` / `go vet ./...` / `go test ./...` pass
2. `POST /plan {"goal":"..."}` runs a real Claude ReAct loop and streams tool status + final itinerary via SSE
3. Falls back to Ollama when Claude returns 429 or is unreachable
4. Agent asks clarification via `ask_user`; `POST /plan/{id}/respond` injects the answer back
5. Weather (Open-Meteo), geocoding (Nominatim), and web search (SearXNG) return real data
6. Mock flight and hotel results include Booking.com-style deep-link fields
7. Rate limiting middleware rejects excessive requests with 429
8. Eval dataset exists in `evals/scenarios/`

---

## Directory Layout (new files only)

```
internal/
  llm/
    client.go          # LLMClient interface + shared Message/Tool/Response types
    claude.go          # Claude API implementation (anthropic-sdk-go)
    ollama.go          # Ollama HTTP implementation (/api/chat endpoint)
    router.go          # Primary→fallback router; isRateLimitOrUnavailable helper
  agent/
    agent.go           # Agent struct, Run(ctx, goal, out chan<-string) error
    stream.go          # formatStepUpdate, formatToolCall, formatQuestion helpers
  tools/
    result.go          # ToolResult{Data, Error, Degraded}, ToolError{Code,Message,Retryable}
    retry.go           # withRetry(ctx, maxAttempts, fn) — exponential backoff
    registry.go        # Tool registry: name→handler dispatch, JSON Schema definitions
    weather.go         # get_weather — Open-Meteo hourly forecast
    geocode.go         # geocode — Nominatim (User-Agent header required)
    search.go          # web_search — SearXNG /search?q=&format=json
    flights.go         # search_flights — mock Booking.com deep links
    hotels.go          # search_hotels — mock Booking.com deep links
    ask_user.go        # ask_user — writes question to SSE; pauses for /respond
  itinerary/
    itinerary.go       # Typed itinerary struct (days, activities, transport, budget)
  middleware/
    auth.go            # Auth stub — reads Authorization header, logs, passes through
    ratelimit.go       # Redis sliding-window per-IP rate limit; 429 on breach
  server/
    sessions.go        # In-memory session store: id→responseChan + cancel
evals/
  scenarios/
    01_kyoto_5days.json
    02_bali_budget.json
    03_europe_multicity.json
    04_singapore_weekend.json
    05_japan_family.json
```

**Modified files:**
- `internal/server/server.go` — wire LLM router, tool registry, agent; register new routes
- `internal/server/handlers.go` — replace hardcoded SSE with real agent; add /plan/{id}/respond
- `internal/config/config.go` — add rate limit config fields
- `go.mod` — add anthropic-sdk-go, go-redis

---

## Technical Approach

### LLMClient interface

```go
type LLMClient interface {
    Complete(ctx context.Context, messages []Message, tools []Tool) (Response, error)
}
```

Message roles: `user`, `assistant`, `tool_result`. StopReason: `end_turn`, `tool_use`.

### ReAct loop (agent/agent.go)

```go
func (a *Agent) Run(ctx context.Context, goal string, out chan<- string) error {
    messages := []Message{userMessage(goal)}
    for step := 0; step < maxSteps; step++ {
        resp, err := a.llm.Complete(ctx, messages, a.tools.Definitions())
        if err != nil { return err }
        out <- formatStepUpdate(resp)
        switch resp.StopReason {
        case StopReasonEndTurn:
            return a.emitFinal(ctx, resp, out)
        case StopReasonToolUse:
            results := a.tools.Execute(ctx, resp.ToolCalls, out)
            messages = append(messages, assistantMessage(resp), toolResults(results))
        }
    }
    return ErrMaxStepsReached
}
```

`maxSteps = 20`.

### /plan handler flow

```
POST /plan {"goal":"..."}
  → generate session ID (UUID)
  → create responseChan in session store
  → launch goroutine: agent.Run(ctx, goal, sseOut)
  → SSE stream: read sseOut channel, flush each line
  → on agent done: close sseOut
  → return session ID in X-Session-Id header (for /respond)
```

### ask_user tool

When Claude calls `ask_user`:
1. Write `question: <text>` to SSE stream
2. Block on `responseChan` (with context timeout)
3. Return user's answer as tool result

Client must POST to `POST /plan/{id}/respond {"answer":"..."}` which sends on `responseChan`.

### Ollama fallback

Ollama exposes an OpenAI-compatible `/api/chat` endpoint. The `OllamaClient` translates `[]Message + []Tool` to Ollama's format and back. Tools are passed as `tools` array in the request body.

### Tool JSON Schema definitions

Each tool file exports:
- A `Definition() anthropic.Tool` (or equivalent struct) with name, description, input_schema
- An `Execute(ctx, args json.RawMessage) ToolResult` method

### Rate limiting (middleware/ratelimit.go)

Redis sliding-window counter: `INCR rl:<ip>`, `EXPIRE 60`. Default: 30 req/min per IP. Returns 429 with `Retry-After` header on breach.

### Eval dataset format

```json
{
  "id": "01_kyoto_5days",
  "goal": "Plan 5 days in Kyoto in October, budget $3000",
  "expected": {
    "must_include": ["weather", "accommodation", "activities"],
    "must_not_include": ["error", "failed"],
    "budget_ceiling_usd": 3000
  }
}
```

---

## New Dependencies

| Package | Reason |
|---------|--------|
| `github.com/anthropics/anthropic-sdk-go` | Claude API client |
| `github.com/redis/go-redis/v9` | Rate limiting middleware |

## Files to Change

- `internal/server/server.go`
- `internal/server/handlers.go`
- `internal/config/config.go`
- `go.mod` / `go.sum`

## New Files

See directory layout above.

## Assumptions

1. Ollama is running locally or on Oracle VM at `OLLAMA_URL`. The Phase 1 Ollama client only needs tool-capable chat — `llama3.2` or `qwen2.5:7b`. If `OLLAMA_URL` is unreachable, the router returns the original Claude error.
2. SearXNG is accessible at `SEARXNG_URL`. If unreachable, `web_search` returns a degraded ToolResult and the agent reasons around it.
3. The frontend will read the `X-Session-Id` response header and use it for `/plan/{id}/respond` calls.
4. Rate limit defaults (30 req/min/IP) can be overridden via `RATE_LIMIT_RPM` env var.
5. No auth enforcement in Phase 1 — the stub middleware only logs the header.

## Future Work (not in scope)

- Real Booking.com API (Phase 2)
- Redis pub/sub SSE for multi-instance (Phase 2)
- Database persistence for sessions and conversations (Phase 2)
- JWT auth enforcement (Phase 4)

## Post-Plan Amendments

**2026-05-14 — Session API restructure** (see `20260514_083000_sessions_api.md`):  
The frontend (Phase 2, React + Vite) required a session-based REST API that was originally deferred to Phase 4. It was implemented in Phase 1:
- `POST /plan` replaced by `POST /sessions` (returns `{id}` immediately, no streaming)
- `GET /sessions/{id}/stream` for SSE (separate from creation)
- `POST /sessions/{id}/respond`, `POST /sessions/{id}/interrupt`, `GET /sessions/{id}`, `GET /sessions`
- All SSE events converted from raw strings to JSON objects (`{"type":"token"|"tool"|"question"|"done"|"error",...}`)
- New `internal/event` package for event types
