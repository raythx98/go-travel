# Architecture

## System Overview (Phase 1)

```
Frontend (React + Vite — Phase 2 / plain HTML Phase 0-1)
        │ HTTPS + SSE
        ▼
Go API Server (net/http stdlib, port configurable via PORT env)
  Middleware chain: CORS → Logging → Auth stub → Rate limit (Redis)
        │
        ▼
Session Store (in-memory, Phase 1; Postgres in Phase 2)
  POST /sessions → goroutine spawned, outCh buffered(512)
  GET  /sessions/{id}/stream → reads outCh, flushes SSE
        │
        ▼
Agent (internal/agent — plain Go ReAct loop, maxSteps=20)
  System prompt → LLM → tool dispatch → feed results back → repeat
        │
        ├── LLM Router (internal/llm)
        │     Primary:   GeminiClient (google.golang.org/genai)
        │     Fallback:  OllamaClient (local HTTP /api/chat)
        │     Trigger:   ShouldFallback(err) — any genai.APIError or DeadlineExceeded
        │
        └── Tool Registry (internal/tools)
              get_weather    → Open-Meteo (forecast or archive API)
              web_search     → SearXNG (self-hosted)
              geocode        → Nominatim (public)
              search_flights → mock Booking.com (Phase 1 placeholder)
              search_hotels  → mock Booking.com (Phase 1 placeholder)
              ask_user       → SSE question event + in-memory response channel
              finalize_itinerary → renders markdown, emits token event
```

## Agent Loop

```go
messages := []Message{userMessage(goal)}
for step := 0; step < maxSteps; step++ {
    resp, err := llm.Complete(ctx, system, messages, tools)
    // on error: emit error event, return
    switch resp.StopReason {
    case StopReasonEndTurn:
        return  // agent finished without calling finalize — treated as done
    case StopReasonToolUse:
        results := registry.Execute(ctx, resp.Content, outCh)
        messages = append(messages, assistantMsg(resp), toolResultMsg(results))
    case StopReasonMaxTokens:
        return ErrMaxTokens
    }
}
return ErrMaxSteps
```

## LLM Routing

- `Router.Complete` calls primary (`GeminiClient`).
- On error, calls `ShouldFallback(err)`: returns true for any `genai.APIError` (4xx/5xx) or `context.DeadlineExceeded`. Returns false for `context.Canceled` (user interrupts must not trigger fallback).
- Falls through to `OllamaClient` if secondary is configured.

## Gemini Thinking Model Quirks

- Parts with `Thought: true` must be excluded from conversation history (they are internal reasoning).
- `ThoughtSignature []byte` on function call parts must be stored in `ContentBlock` and echoed back on the corresponding history entry, or Gemini returns Error 400.

## SSE Streaming

All events are JSON objects on a `data:` line followed by `\n\n`.

| Type | Shape |
|------|-------|
| `token` | `{"type":"token","content":"..."}` |
| `tool` | `{"type":"tool","name":"...","status":"running"\|"done"\|"error"}` |
| `question` | `{"type":"question","id":"...","text":"..."}` |
| `done` | `{"type":"done","session_id":"..."}` |
| `error` | `{"type":"error","message":"..."}` |

Session creation (`POST /sessions`) returns immediately with `{id}`. The SSE stream (`GET /sessions/{id}/stream`) is a separate request. The agent goroutine writes to `outCh (chan string, buffer 512)`; the SSE handler reads from it.

## Session Lifecycle (Phase 1 — in-memory)

```
POST /sessions         → status: running, goroutine started
GET  /sessions/{id}/stream → reads outCh until closed
agent finishes         → status: done or error, done/error event emitted, outCh closed
POST /sessions/{id}/interrupt → calls cancel(), agent context cancelled
POST /sessions/{id}/respond   → sends answer into respChan (ask_user tool blocks on it)
```

Sessions are never evicted in Phase 1. Phase 2 will persist them to Postgres.
