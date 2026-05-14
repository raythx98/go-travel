# Modules

Per-package summaries — what each package owns and its public interface.

## `cmd/server`

Entry point. Loads config, connects to Postgres/Redis/LangFuse, wires LLM clients (Gemini → Ollama router), registers tools, and starts the HTTP server.

## `internal/config`

`Config` struct loaded from environment variables via `godotenv`. Key fields: `Port`, `DatabaseURL`, `RedisURL`, `GeminiAPIKey`, `GeminiModel`, `OllamaURL`, `OllamaModel`, `SearxNGURL`, `LangfuseHost/PublicKey/SecretKey`, `RateLimitRPM`, `AllowedOrigin`.

## `internal/tracing`

Single function `FromContext(ctx)` returning a trace ID string. Trace IDs are set from the `X-Trace-Id` request header (or generated) by the logging middleware and propagated via context.

## `internal/event`

Typed SSE event structs (`Token`, `Tool`, `Question`, `Done`, `Error`) and `Encode(any) string` (JSON marshal, never errors). All tool and agent code must use this package to emit SSE events — never raw strings.

## `internal/llm`

### `LLMClient` interface
```go
Complete(ctx context.Context, system string, messages []Message, tools []Tool) (Response, error)
```

### `Message` / `ContentBlock`
Internal message format shared by all LLM implementations. `ContentBlock.ThoughtSignature []byte` carries Gemini thinking-model signatures across the loop.

### `GeminiClient`
Wraps `google.golang.org/genai`. Default model: `gemini-2.0-flash`. Translates internal types ↔ Gemini SDK types. Handles thought-signature preservation.

`ShouldFallback(err) bool` — exported; used by Router. True for any `genai.APIError` or `context.DeadlineExceeded`; false for `context.Canceled`.

### `OllamaClient`
Calls local Ollama `/api/chat`. Tool results are folded into user message content (Ollama has no native tool_result block).

### `Router`
Tries primary, falls back to secondary when `ShouldFallback` returns true. Logs the fallback with `slog.WarnContext`.

## `internal/itinerary`

Typed `Itinerary` struct with nested `Day`, `Activity`, `Flight`, `Accommodation`, `Budget` structs. This is what `finalize_itinerary` receives and `formatItinerary` renders.

## `internal/tools`

### `Tool` interface
```go
Definition() llm.Tool
Execute(ctx context.Context, input json.RawMessage, out chan<- string) ToolResult
```

### `ToolResult`
```go
type ToolResult struct {
    Data     any
    Error    *ToolError
    Degraded bool
}
```
All tools return this. Never return raw API responses.

### `Registry`
Holds all registered tools. `Execute` dispatches by tool name, emits `tool/running` and `tool/done|error` events around each call, logs result metadata.

### Tool implementations
| Tool | Package file | API |
|------|-------------|-----|
| `get_weather` | `weather.go` | Open-Meteo forecast or archive |
| `web_search` | `search.go` | SearXNG (self-hosted) |
| `geocode` | `geocode.go` | Nominatim (public) |
| `search_flights` | `flights.go` | Mock (Phase 1 placeholder) |
| `search_hotels` | `hotels.go` | Mock (Phase 1 placeholder) |
| `ask_user` | `ask_user.go` | SSE question + in-memory channel |
| `finalize_itinerary` | `finalize.go` | Renders markdown, emits token event |

### `withRetry`
`retry.go` — exponential backoff (1s, 2s, 4s), max 3 attempts. Skips retry on `context.Canceled`, respects `context.DeadlineExceeded`.

## `internal/agent`

`Agent` struct with `llm LLMClient`, `tools *tools.Registry`, `outCh chan<- string`. `Run(ctx, goal, outCh, itinCh)` is the ReAct loop (maxSteps=20). Emits `token` events for text responses and delegates tool dispatch to the registry.

## `internal/server`

### Session store (`sessions.go`)
In-memory `map[string]*session` protected by `sync.RWMutex`. Each session holds: `goal`, `status` (`running`/`done`/`error`), `outCh chan string (512)`, `respChan chan string (1)`, `cancel context.CancelFunc`, `createdAt`.

### Handlers (`handlers.go`)
Six handlers matching the REST API contract (see `docs/architecture.md`).

### Server (`server.go`)
Registers routes, applies middleware chain, owns `/health`.

### Middleware (`internal/middleware`)
- `CORS` — configurable allowed origin
- `Logging` — request/response log with trace ID and latency
- `Auth` — stub (passes all requests through; Phase 4 replaces with JWT)
- `RateLimit` — Redis sliding-window per IP, `RATE_LIMIT_RPM` limit, fail-open on Redis errors

## `sqlc/`

Generated code — do not edit manually. Regenerate with `make sqlc` after changing `sqlc/query.sql`. Migrations live in `sqlc/migrations/`.
