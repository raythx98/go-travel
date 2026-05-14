# Agentic AI Travel Planner — Engineering Guide

## Quick Reference

| Item | Decision |
|---|---|
| Language | Go (backend), plain HTML/JS (Phase 0–1), React + Vite (Phase 2+) |
| HTTP Router | `net/http` stdlib — do NOT suggest chi |
| Agent Framework | None — plain Go ReAct loop |
| ORM | None — sqlc for type-safe query generation |
| LLM Primary | Gemini API (Google) |
| LLM Fallback | Ollama on Oracle VM (7B quantised model) |
| LLM Routing | Go interface + fallback logic — do NOT suggest LiteLLM |
| Embeddings | Ollama `nomic-embed-text` — do NOT use Gemini embedding API |
| Vector DB | pgvector extension on existing PostgreSQL — do NOT add Qdrant/Weaviate |
| Database | PostgreSQL + pgvector only — do NOT add MongoDB |
| Hosting | Oracle ARM VM (4 OCPU, 12GB RAM) via Docker Compose |
| Frontend Hosting | GitHub Pages (static SPA — no SSR, no API routes) |
| TLS / Proxy | Caddy (automatic TLS) |
| Observability | LangFuse (LLM tracing from day one) |
| Bookings | Deep links only — no payment processing |
| Reservations | Non-payment only (restaurants, timed entry) via OpenTable |

---

## Constraints

- **12GB RAM hard limit** on Oracle VM. Swap exists but do NOT rely on it for Ollama inference — swapped model weights produce unusably slow token generation.
- **GitHub Pages is static only** — no server-side rendering, no API routes. Frontend must be a pure SPA.
- **Free APIs only** — every external API in this guide is free tier or free. Do not suggest paid APIs.
- **Minimal dependencies** — every dependency must justify its existence. Default to stdlib.
- **No direct booking** — no payment processing, no PCI compliance scope. Agent produces deep links to Booking.com for flights, hotels, and cars.

---

## Architecture

```
GitHub Pages
React + Vite (Phase 2+) / Plain HTML+JS (Phase 0-1)
Leaflet + OSM tiles for maps (Phase 2+)
        │
        │ HTTPS + SSE
        ▼
Oracle ARM VM (4 OCPU, 12GB RAM)
Caddy (automatic TLS, reverse proxy)
        │
        ▼
Go API Server (net/http stdlib)
  Middleware: Auth · RateLimit · Logging · CORS
        │
        ▼
Agent Orchestrator (plain Go)
  ReAct loop · Tool dispatch · Context management
  Human-in-the-loop pause mechanism
        │
        ├── Supervisor Agent (Gemini / Ollama fallback)
        │       │
        │       ├── FlightAgent
        │       ├── AccommodationAgent
        │       ├── TransportAgent
        │       ├── WeatherAgent
        │       ├── StargazingAuroraAgent
        │       ├── ActivitiesAgent
        │       ├── RestaurantAgent
        │       ├── PackingAgent
        │       ├── BudgetAgent
        │       ├── VisaEntryAgent ──────────┐
        │       ├── CrowdTimingAgent ────────┤
        │       ├── CulturalContextAgent ────┼──→ WebSearchAgent → SearXNG
        │       ├── HealthSafetyAgent ───────┤
        │       ├── ConnectivityAgent ───────┘
        │       ├── WebSearchAgent
        │       └── CritiqueAgent
        │
        ├── Tool Functions (plain Go functions — NOT MCP until Phase 3)
        │       Booking.com · OpenTripMap · Nominatim (public API)
        │       OSRM (public API) · Open-Meteo · SearXNG (local)
        │       OpenTable · Wikivoyage · Wikipedia
        │       NOAA SWPC · Moon phase (Go library)
        │
        └── Data Layer
                PostgreSQL + pgvector (single database for everything)
                Redis (SSE streaming pub/sub + rate limiting + cache)
                LangFuse (LLM observability)

LLM Routing:
  Primary    → Gemini API (gemini-2.0-flash, free tier)
  Fallback   → Ollama (on rate limit or unavailability)
  Embeddings → Ollama nomic-embed-text (always local, never Gemini API)
```

---

## Role Separation

Understanding who does what prevents architectural confusion.

| Layer | What it is | Who/what controls it |
|---|---|---|
| Agent Loop | Mechanical runtime: manages message array, calls LLM API, dispatches tools, feeds results back, enforces limits | Your Go code |
| Supervisor Agent | Intelligence: understands goal, asks clarifications, plans sub-tasks, decides parallelism, adapts to failures | Gemini/Ollama reasoning |
| Specialist Agents | Focused executors: receive a narrow sub-task, complete it well | Gemini/Ollama reasoning with focused system prompts |
| Tools | Plain Go functions wrapped in JSON Schema | Your Go code |

The Supervisor is **not hardcoded logic**. It reasons about the goal using its system prompt. It knows available specialist agents, when to use them, and how to handle failures. The sequencing emerges from reasoning, not from a switch statement.

---

## Supervisor Responsibilities

- Load user memories before starting
- Identify missing information and ask clarifications (one grouped message, not multiple)
- Decompose the goal into a task DAG
- Run independent tasks concurrently (goroutines)
- Run dependent tasks sequentially (e.g. ActivitiesAgent after WeatherAgent)
- Adapt when sub-tasks fail — replan, degrade gracefully, or ask user
- Pass assembled itinerary to CritiqueAgent
- Act on critique — trigger replanning for critical issues
- Update user memories when new preferences are revealed

---

## Human-in-the-Loop

The Supervisor can pause execution and ask the user a question via a special `ask_user` tool. This is just another tool in the agent loop — the Supervisor calls it, your Go code publishes the question as a JSON SSE event, the client displays it, the user responds via a REST endpoint, and the answer is injected back into the conversation via an in-memory channel.

Users can also:
- **Hard interrupt** — stop button calls `POST /sessions/{id}/interrupt`, which cancels the agent's context.
- **Soft interrupt** — inject additional context while agent is running (Phase 4+).
- **Confirmation gates** — agent pauses before any reservation action, presents cancellation policy, waits for explicit approval (Phase 3+).

```go
// Implemented in Phase 1
POST /sessions          // create session, start agent, return {id}
GET  /sessions/{id}/stream      // SSE stream of agent output
POST /sessions/{id}/respond     // inject user response to agent question
POST /sessions/{id}/interrupt   // cancel running agent
GET  /sessions/{id}             // get session status
GET  /sessions                  // list all sessions
```

### SSE Event Format

All events are JSON objects on a `data:` line followed by `\n\n`.

| Event | Shape |
|-------|-------|
| Text chunk | `{"type":"token","content":"..."}` |
| Tool status | `{"type":"tool","name":"...","status":"running"\|"done"\|"error"}` |
| Agent question | `{"type":"question","id":"...","text":"..."}` |
| Session done | `{"type":"done","session_id":"..."}` |
| Agent error | `{"type":"error","message":"..."}` |

---

## Sessions and Memory

### Sessions (per trip, per user)
Each planning run is a session. Users can have multiple sessions — "Japan March", "Bali Weekend", "Europe Summer". Each session stores:
- Goal and status (`clarifying` / `planning` / `paused` / `complete`)
- Full conversation history (for resumability)
- Assembled itinerary (jsonb)
- Agent run log

### User Memory (global, shared across sessions)
Separate from sessions. Persists across all trips. Loaded by Supervisor at the start of every session.

```sql
CREATE TABLE user_memories (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid REFERENCES users(id),
    key         text NOT NULL,
    value       text NOT NULL,
    confidence  float DEFAULT 1.0,
    source      text, -- 'explicit' | 'inferred' | 'agent_updated'
    updated_at  timestamptz DEFAULT now()
);

-- Example rows:
-- key: 'home_airport',    value: 'SIN'
-- key: 'dietary',         value: 'vegetarian'
-- key: 'seat_preference', value: 'window'
-- key: 'travel_style',    value: 'slow travel, local experiences'
-- key: 'dislikes',        value: 'tourist crowds, chain hotels'
```

Memories are updated when users explicitly state preferences and inferred from patterns (consistently choosing budget options → budget_style updated). The system learns the user over time.

---

## Agent Loop — Core Pattern

```go
type Agent struct {
    llm    LLMClient   // interface — Gemini or Ollama
    tools  []Tool
    memory *MemoryStore
}

// itin receives the structured itinerary when the agent calls finalize_itinerary.
func (a *Agent) Run(ctx context.Context, goal string, out chan<- string, itin chan *itinerary.Itinerary) error {
    messages := []Message{userMessage(goal)}

    for step := 0; step < maxSteps; step++ {
        response, err := a.llm.Complete(ctx, messages, a.tools)
        if err != nil {
            return err
        }

        out <- formatStepUpdate(response) // streams to SSE handler

        switch response.StopReason {
        case "end_turn":
            return a.handleFinalResponse(ctx, response)
        case "tool_use":
            results := a.executeTools(ctx, response.ToolCalls)
            // ALWAYS validate tool results before feeding back
            messages = append(messages, assistantMessage(response), toolResults(results))
        }
    }
    return ErrMaxStepsReached
}
```

**This is the entire agent runtime. No framework. Full control.**

---

## LLM Interface and Fallback

```go
type LLMClient interface {
    Complete(ctx context.Context, messages []Message, tools []Tool) (Response, error)
}

type Router struct {
    primary   LLMClient // GeminiClient
    secondary LLMClient // OllamaClient
}

func (r *Router) Complete(ctx context.Context, messages []Message, tools []Tool) (Response, error) {
    resp, err := r.primary.Complete(ctx, messages, tools)
    if err != nil && isRateLimitOrUnavailable(err) {
        return r.secondary.Complete(ctx, messages, tools)
    }
    return resp, err
}

func isRateLimitOrUnavailable(err error) bool {
    var apiErr genai.APIError
    if errors.As(err, &apiErr) {
        return apiErr.Code == 429 || apiErr.Code >= 500
    }
    return errors.Is(err, context.DeadlineExceeded)
}
```

Both Gemini and Ollama implement `LLMClient`. All agents use `LLMClient` — they never reference Gemini or Ollama directly.

---

## Tool Pattern

Tools are plain Go functions wrapped in JSON Schema. The schema tells the model what the function does and when to use it. Tool description quality directly determines agent behaviour — vague descriptions cause misuse.

```go
var SearchFlightsTool = Tool{
    Name: "search_flights",
    Description: `Search for available flights between two cities.
Use when the user needs flight options. If no results found on exact dates,
automatically tries ±3 days and returns alternatives with price comparison.
Returns structured results with deep links to Booking.com.`,
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "origin":      {"type": "string", "description": "IATA airport code e.g. SIN"},
            "destination": {"type": "string", "description": "IATA airport code e.g. NRT"},
            "date":        {"type": "string", "description": "Departure date YYYY-MM-DD"},
            "max_price":   {"type": "number", "description": "Maximum price in USD"}
        },
        "required": ["origin", "destination", "date"]
    }`),
}

func (t *TravelTools) SearchFlights(ctx context.Context, args FlightArgs) ([]Flight, error) {
    ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    // call Booking.com API
    // validate response before returning
    // return typed Go structs — never raw API response to agent
}
```

---

## External API Failure Handling

### Failure types and responses

| Failure | Response |
|---|---|
| Timeout, 503, 504 | Retry with exponential backoff (1s, 2s, 4s — max 3 attempts) |
| 429 Rate limited | Respect Retry-After header, then retry |
| 401 Auth failure | Fail fast — do not retry |
| 400 Bad input | Fail fast — do not retry |
| Empty result | Not an error — agent reasons around it |
| Partial failure | Continue with available data, note gaps |

### Retry wrapper

```go
func withRetry(ctx context.Context, maxAttempts int, fn func() error) error {
    for attempt := 0; attempt < maxAttempts; attempt++ {
        err := fn()
        if err == nil {
            return nil
        }
        if !isRetryable(err) {
            return err
        }
        wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
        select {
        case <-time.After(wait):
        case <-ctx.Done():
            return ctx.Err()
        }
    }
    return err
}
```

### Per-tool timeouts
Every external call uses `context.WithTimeout`. Geocoding: 3s. Weather: 5s. Booking.com: 10s. Web search: 8s.

### Structured tool results
Return typed structs so the agent can reason about failures explicitly:

```go
type ToolResult struct {
    Data     any
    Error    *ToolError
    Degraded bool
}

type ToolError struct {
    Code      string // "rate_limited" | "unavailable" | "bad_input"
    Message   string // safe to show agent
    Retryable bool
}
```

### Graceful degradation

```
Critical (plan cannot proceed):
  Booking.com unavailable → pause run, tell user, retry later

Important (plan is worse without):
  OpenTripMap unavailable → continue without POIs, note gap in itinerary
  Open-Meteo unavailable  → continue without weather matching, flag to user

Nice to have:
  NOAA feed unavailable   → skip aurora section, note data unavailable
```

### Stream failures to user
Never fail silently. Stream status of every tool call:

```
Searching flights SIN → NRT...       ✓
Checking hotel availability...        ✓
Fetching weather forecast...          ✓
Loading points of interest...         ✗ temporarily unavailable
Calculating travel times...           ✓

Note: Activity suggestions are limited — point of interest data
temporarily unavailable. All other sections are complete.
```

---

## Redis Caching

Cache tool results to reduce API calls and provide resilience during outages.

| Data | TTL |
|---|---|
| Flight / hotel search | 30 minutes |
| Weather forecast | 1 hour |
| POI data | 24 hours |
| Geocoding results | 7 days |

---

## Logging

Structured JSON logs via `log/slog` (stdlib). No third-party logging library. Every log entry includes `trace_id` (propagated from the HTTP request through the agent loop) so all events for a single planning run are correlated.

### What to log

| Category | Events | Level |
|---|---|---|
| Incoming requests | Method, path, status, latency, `trace_id`, user ID | `INFO` |
| Outgoing HTTP requests | Method, URL (no query secrets), status, latency, `trace_id` | `INFO` |
| Database queries | Operation, table, latency, `trace_id` — no row data | `DEBUG` |
| External API calls | Service name, operation, status, latency, `trace_id` | `INFO` |
| LLM calls | Model, input tokens, output tokens, latency, stop reason, `trace_id` — no prompt content | `INFO` |
| Tool dispatch | Tool name, duration, degraded flag, `trace_id` | `INFO` |
| Errors | Full error chain, relevant context, `trace_id` | `ERROR` |

Never log secrets, credentials, API keys, or personally identifiable information. Never log full prompt/response content in application logs — that belongs in LangFuse traces.

### Incoming request middleware

```go
func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        traceID := r.Header.Get("X-Trace-Id")
        if traceID == "" {
            traceID = newTraceID()
        }
        ctx := context.WithValue(r.Context(), traceIDKey, traceID)
        w.Header().Set("X-Trace-Id", traceID)

        rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
        next.ServeHTTP(rw, r.WithContext(ctx))

        slog.InfoContext(ctx, "request",
            "method",   r.Method,
            "path",     r.URL.Path,
            "status",   rw.status,
            "latency_ms", time.Since(start).Milliseconds(),
            "trace_id", traceID,
            "user_id",  userIDFromContext(ctx),
        )
    })
}
```

### Outgoing HTTP requests

Wrap all outgoing HTTP clients with a transport that logs before and after:

```go
type loggingTransport struct {
    base http.RoundTripper
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    start := time.Now()
    resp, err := t.base.RoundTrip(req)
    status := 0
    if resp != nil {
        status = resp.StatusCode
    }
    slog.InfoContext(req.Context(), "outgoing_request",
        "method",     req.Method,
        "host",       req.URL.Host,
        "path",       req.URL.Path,
        "status",     status,
        "latency_ms", time.Since(start).Milliseconds(),
        "trace_id",   traceIDFromContext(req.Context()),
        "error",      err,
    )
    return resp, err
}
```

### Database queries

Wrap sqlc-generated calls at the repository layer — log operation name, table, and latency. Do not log query parameters or row contents.

```go
slog.DebugContext(ctx, "db_query",
    "op",         "GetSession",
    "table",      "sessions",
    "latency_ms", time.Since(start).Milliseconds(),
    "trace_id",   traceIDFromContext(ctx),
)
```

### LLM calls

Log after every `LLMClient.Complete` call. Token counts come from the API response. Never log prompt or completion text here — that is captured by LangFuse.

```go
slog.InfoContext(ctx, "llm_call",
    "model",        resp.Model,
    "input_tokens",  resp.Usage.InputTokens,
    "output_tokens", resp.Usage.OutputTokens,
    "stop_reason",  resp.StopReason,
    "latency_ms",   time.Since(start).Milliseconds(),
    "trace_id",     traceIDFromContext(ctx),
    "agent",        agentName,
)
```

LangFuse receives the full trace (prompts, completions, tool calls) via the LangFuse SDK — application logs only carry the metrics.

---

## Streaming Architecture

### Phase 0–3 (single instance)

Session creation and SSE streaming are separate requests. `POST /sessions` starts the agent goroutine and returns `{id}` immediately. `GET /sessions/{id}/stream` reads from the session's buffered output channel.

```
POST /sessions
    │ spawns goroutine
    ▼
agent loop → writes JSON events to outCh (buffered 512)
                                │
GET /sessions/{id}/stream ──────┘
    reads from outCh → SSE flush to client
```

Direct goroutine-to-channel. No Redis involved.

### Phase 4+ (multi-instance ready)

```
Instance A: agent loop → Redis PUBLISH
Instance B: SSE handler → Redis SUBSCRIBE → flush to client
```

Redis bridges the process boundary. Change is contained to two functions. This is why Redis is in the stack from Phase 0 — the pattern is ready when needed.

---

## Embeddings and RAG

### What embeddings are
An embedding turns text into a list of numbers (768 floats) that captures semantic meaning. Similar meanings produce vectors that are mathematically close. This enables finding relevant content by meaning rather than keyword matching.

### Pipeline (run once offline, then incrementally)

```
Wikivoyage + Wikipedia content
    │
    ▼
Chunk by section with overlap (~500 tokens, 50 token overlap)
    │
    ▼
Ollama nomic-embed-text (runs on Oracle VM — free, no API cost)
    │
    ▼
pgvector (stored alongside source text and metadata)
```

### Retrieval (at agent runtime)

```go
// Embed the user's query using same model
queryVector, _ := ollama.Embed(ctx, userQuery)

// Find top-5 semantically similar chunks
rows, _ := db.Query(ctx, `
    SELECT content, source, destination
    FROM destination_embeddings
    ORDER BY embedding <=> $1
    LIMIT 5
`, pgvector.NewVector(queryVector))

// Inject chunks into Supervisor prompt as context
```

### Retrieval weighting
Tag each chunk with `source` (wikivoyage or wikipedia). Wikivoyage chunks rank higher for practical planning queries. Wikipedia chunks rank higher for cultural and contextual queries. Implement as a metadata filter on pgvector queries.

---

## WebSearchAgent

Gives both Gemini and Ollama identical web search capability. No special-casing between LLMs.

**Used by:** VisaEntryAgent, CrowdTimingAgent, CulturalContextAgent, HealthSafetyAgent, ConnectivityAgent, BudgetAgent (COL data), and any agent needing live data.

**Replaces:** VisaDB API, Numbeo API, date.nager.at, travel advisory feeds — all handled by web search at query time with fresher data.

```go
type WebSearchAgent struct {
    llm     LLMClient
    searxng *SearXNGClient
}

func (a *WebSearchAgent) Search(ctx context.Context, intent string) (string, error) {
    // Step 1: LLM formulates precise search query from intent
    //         (good candidate for Ollama — cheap reasoning task)
    query := a.formulateQuery(ctx, intent)

    // Step 2: fetch top 5 results from local SearXNG
    results, err := a.searxng.Search(ctx, query, 5)
    if err != nil {
        return "", err
    }

    // Step 3: LLM extracts and summarises relevant content
    //         (good candidate for Ollama — cheap reasoning task)
    return a.summarise(ctx, intent, results)
}
```

SearXNG queries DuckDuckGo, Bing, and Brave — no API keys required for any of them.

---

## Specialist Agents

### Supervisor Agent
Receives user goal. Loads user memories. Asks minimum clarifications (one grouped message). Decomposes into task DAG. Runs independent tasks concurrently, dependent tasks sequentially. Adapts when sub-tasks fail. Passes assembled result to CritiqueAgent. Acts on critique. Updates user memories when preferences revealed.

### FlightAgent
Searches Booking.com for flights. Autonomously tries ±3 days if no results on exact dates. Returns options with deep links.

### AccommodationAgent
Searches Booking.com for hotels, apartments, and stays. Validates location against planned activities using OSRM travel times — not straight-line distance. Returns options with deep links. Flags inconvenient locations.

### TransportAgent
Retrieves rail schedules and last train times. Identifies routes requiring advance seat reservation. Flags ferry schedules for island destinations. Calculates rail pass value vs individual tickets. Warns when activity ends after last train with taxi cost estimate as alternative.

### WeatherAgent
Open-Meteo hourly forecast across travel dates. Matches activities to weather at day-by-day level. Distinguishes weather that ruins an activity from weather that is merely uncomfortable. Feeds forecast to PackingAgent and StargazingAuroraAgent.

### StargazingAuroraAgent
Activated when destination or interests involve stargazing or aurora. Retrieves light pollution index from ingested World Atlas dataset. Calculates moon phase and moonrise/moonset via Go astronomy library (`soniakeys/meeus`). Retrieves cloud cover from Open-Meteo. Fetches NOAA Kp index for aurora probability. Outputs specific nightly window recommendation.

### ActivitiesAgent
Discovers POIs via OpenTripMap. Retrieves operating hours and advance booking requirements. Identifies venues requiring timed entry. Clusters activities geographically for spatial coherence per day. Checks CrowdTimingAgent data before scheduling popular venues.

### RestaurantAgent
Discovers restaurants via OpenTripMap and WebSearchAgent. Checks availability on OpenTable. Presents options with cancellation policy. On user confirmation: books, stores confirmation number in Postgres, adds to itinerary, sets cancellation deadline reminder. For venues not on OpenTable, drafts reservation request email. **Never acts without explicit user confirmation.**

### PackingAgent
Reads complete itinerary + weather forecast + activity types + cultural norms from CulturalContextAgent + user preferences from memory. Produces categorised packing list weighted by actual need — not a generic list. Flags trip-specific items: correct power adapter, visa documents, vaccination certificates, activity-specific gear.

### BudgetAgent
Running cost estimate across full itinerary. Uses WebSearchAgent for cost-of-living data by city. Warns when approaching budget. Suggests concrete trade-offs with specific numbers.

### VisaEntryAgent
Uses WebSearchAgent to retrieve visa requirements for passport nationality and all countries including transit. Returns visa type, processing time, cost, application method. Flags health entry requirements. **Always appends disclaimer to verify with official embassy.**

### CrowdTimingAgent
Uses WebSearchAgent to retrieve peak seasons, local festivals, public holidays, significant events during travel dates. Flags booking lead times. Surfaces festivals as positive opportunities.

### CulturalContextAgent
Uses WebSearchAgent to retrieve tipping norms, dress codes, local etiquette, bargaining expectations, religious observance periods. Feeds dress requirements to PackingAgent.

### HealthSafetyAgent
Uses WebSearchAgent to retrieve recommended vaccinations, current travel advisories, emergency contacts, food safety considerations. Recommends travel insurance category based on planned activities.

### ConnectivityAgent
Uses WebSearchAgent to retrieve SIM/eSIM options, coverage quality in specific areas of itinerary, VPN requirements, pocket WiFi alternatives.

### WebSearchAgent
Wraps local SearXNG. Formulates precise query from intent, fetches results, returns clean summary with sources. Works identically with Gemini or Ollama. Used by multiple specialist agents.

### CritiqueAgent
Reviews fully assembled itinerary. Checks:
- Hotel location vs activity cluster travel times (OSRM)
- Last activity end time vs last available transport
- Outdoor activities vs adverse weather forecast
- Budget total vs stated limit
- Visa processing time vs departure date (flags if insufficient lead time)
- Operating hours vs scheduled visit times
- Cultural dress requirements vs packing list
- Reservation cancellation deadlines needing attention

Outputs issues by severity: **critical** (triggers replanning) / **warning** (surfaced to user) / **suggestion** (improvement opportunity).

---

## Non-Payment Bookings

Actions with real-world consequence. No money moves. Explicit user confirmation always required.

**Supported:**
- Restaurant reservations via OpenTable
- Museum and attraction timed entry
- Free tour and experience registrations
- National park and trail permits
- Email drafts for venues not on any platform

**Confirmation flow:**
```
Agent:  "I found availability at [Venue] on [Date] at [Time]
         for [Party]. Cancellation policy: [policy]. Reserve?"

User:   confirms

Agent:  → execute reservation
        → store confirmation number in Postgres
        → add to itinerary
        → set cancellation deadline reminder
        → log to immutable audit table
```

---

## Database Schema (Core Tables)

```sql
-- Users
CREATE TABLE users (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email      text UNIQUE NOT NULL,
    created_at timestamptz DEFAULT now()
);

-- Cross-session user memory
CREATE TABLE user_memories (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid REFERENCES users(id),
    key         text NOT NULL,
    value       text NOT NULL,
    confidence  float DEFAULT 1.0,
    source      text, -- 'explicit' | 'inferred' | 'agent_updated'
    updated_at  timestamptz DEFAULT now(),
    UNIQUE(user_id, key)
);

-- Individual planning sessions
CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid REFERENCES users(id),
    title        text,
    goal         text NOT NULL,
    status       text DEFAULT 'clarifying', -- clarifying|planning|paused|complete
    itinerary    jsonb,
    conversation jsonb, -- full message history for resumability
    created_at   timestamptz DEFAULT now(),
    updated_at   timestamptz DEFAULT now()
);

-- Questions agent asks user mid-run
CREATE TABLE agent_questions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  uuid REFERENCES sessions(id),
    question    text NOT NULL,
    answer      text,
    answered_at timestamptz,
    created_at  timestamptz DEFAULT now()
);

-- Reservation confirmations
CREATE TABLE reservations (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          uuid REFERENCES sessions(id),
    user_id             uuid REFERENCES users(id),
    type                text, -- 'restaurant' | 'museum' | 'tour' | 'permit'
    venue_name          text,
    datetime            timestamptz,
    confirmation_number text,
    cancellation_policy text,
    cancel_by           timestamptz,
    status              text DEFAULT 'confirmed', -- confirmed|cancelled
    created_at          timestamptz DEFAULT now()
);

-- Immutable audit log of all agent actions with external consequence
CREATE TABLE agent_actions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid REFERENCES sessions(id),
    user_id    uuid REFERENCES users(id),
    action     text NOT NULL,
    payload    jsonb,
    result     jsonb,
    created_at timestamptz DEFAULT now()
    -- no updates or deletes on this table
);

-- Destination knowledge base for RAG
CREATE TABLE destination_embeddings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    destination text,
    source      text, -- 'wikivoyage' | 'wikipedia'
    content     text NOT NULL,
    embedding   vector(768), -- nomic-embed-text output dimension
    created_at  timestamptz DEFAULT now()
);

CREATE INDEX ON destination_embeddings
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);

-- Light pollution data (ingested once from World Atlas dataset)
CREATE TABLE light_pollution (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lat         float NOT NULL,
    lon         float NOT NULL,
    sqm         float, -- sky quality meter value — higher is darker
    bortle      int    -- Bortle scale 1-9 — lower is darker
);
```

---

## Infrastructure

### Docker Compose

```yaml
services:
  api:
    build: .
    environment:
      - GEMINI_API_KEY
      - GEMINI_MODEL
      - DATABASE_URL=postgres://user:pass@postgres:5432/travelplanner
      - REDIS_URL=redis://redis:6379
      - OLLAMA_URL=http://ollama:11434
      - SEARXNG_URL=http://searxng:8080
    depends_on: [postgres, redis, ollama, searxng]
    restart: unless-stopped

  postgres:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_DB: travelplanner
      POSTGRES_USER: user
      POSTGRES_PASSWORD: pass
    volumes:
      - postgres_data:/var/lib/postgresql/data
    restart: unless-stopped

  redis:
    image: redis:7-alpine
    command: redis-server --maxmemory 256mb --maxmemory-policy allkeys-lru
    restart: unless-stopped

  langfuse:
    image: langfuse/langfuse:latest
    environment:
      - DATABASE_URL=postgres://user:pass@postgres:5432/langfuse
    depends_on: [postgres]
    restart: unless-stopped

  caddy:
    image: caddy:2-alpine
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    restart: unless-stopped

  ollama:
    image: ollama/ollama
    volumes:
      - ollama_data:/root/.ollama
    restart: unless-stopped
    # After first run: docker exec ollama pull nomic-embed-text
    # Phase 3+:        docker exec ollama pull llama3.2 (or qwen2.5:7b)

  searxng:
    image: searxng/searxng
    volumes:
      - ./searxng:/etc/searxng
    restart: unless-stopped

volumes:
  postgres_data:
  ollama_data:
  caddy_data:
```

### Memory Budget

| Service | RAM |
|---|---|
| Go API server | ~512MB |
| PostgreSQL + pgvector | ~1GB |
| Redis | ~256MB |
| LangFuse | ~512MB |
| Caddy | ~64MB |
| Ollama (embeddings only, Phase 0-2) | ~512MB |
| Ollama (7B model added, Phase 3+) | ~5GB |
| SearXNG | ~256MB |
| **Total Phase 0-2** | **~3.1GB of 12GB** |
| **Total Phase 3+** | **~7.6GB of 12GB** |

Do NOT load a 14B model — leaves insufficient headroom. Stick to 7B quantised models (Llama 3.2 or Qwen 2.5 7B).

### Caddyfile

```
your-oracle-vm-domain.com {
    reverse_proxy api:8080
}
```

Caddy handles TLS automatically. No certbot, no nginx.

---

## Go Dependencies

### Runtime

```
google.golang.org/genai                  Gemini API client
github.com/jackc/pgx/v5                  Postgres + pgvector driver
github.com/redis/go-redis/v9             Redis client
github.com/joho/godotenv                 .env config loading
github.com/soniakeys/meeus/v3            Moon phase and astronomy calculations
```

### Dev Tools (not compiled into binary)

```
github.com/sqlc-dev/sqlc                 Generates type-safe Go from SQL
github.com/modelcontextprotocol/go-sdk   MCP server wrapper — Phase 3+ only
```

### Explicitly Excluded

| Package | Reason |
|---|---|
| `go-chi/chi` | `net/http` stdlib (Go 1.22+) is sufficient |
| Any AI/agent framework | Plain Go ReAct loop — full control, no magic |
| Any ORM | sqlc generates type-safe code from SQL |
| `LiteLLM` | Go interface + fallback handles routing |
| Qdrant / Weaviate / Pinecone | pgvector on existing Postgres is sufficient |

---

## External APIs — Complete List

| Purpose | Service | Cost | Notes |
|---|---|---|---|
| Flights, hotels, cars | Booking.com Affiliate API | Free | Apply early — approval takes days |
| Points of interest | OpenTripMap | Free | 5000 req/day free tier |
| Geocoding | Nominatim (public) | Free | Add User-Agent header |
| Routing / travel time | OSRM (public) | Free | No key required |
| Weather + cloud cover | Open-Meteo | Free | No key, no rate limit |
| Web search | SearXNG (self-hosted) | Free | ~256MB RAM |
| Restaurant reservations | OpenTable API | Free | Broader global coverage than Resy |
| Destination knowledge | Wikivoyage API | Free | Primary travel content |
| Destination knowledge | Wikipedia API | Free | Cultural context |
| Aurora probability | NOAA SWPC feed | Free | Plain HTTP fetch, no key |
| Moon phases / astronomy | `soniakeys/meeus` Go library | Free | No external API needed |
| Light pollution | World Atlas dataset | Free | One-time ingest into Postgres |
| Embeddings | Ollama nomic-embed-text | Free | Runs on Oracle VM |
| LLM reasoning | Gemini API (Google AI Studio) | Free (15 RPM, 1M tokens/day) | Primary LLM — get key at aistudio.google.com/apikey |
| LLM fallback | Ollama 7B model | Free | Runs on Oracle VM |

---

## Frontend

### Phase 0–1: Plain HTML/JS

No build step. No npm. No framework. Push `index.html` to GitHub Pages.

```html
<!DOCTYPE html>
<html>
<head>
  <title>Travel Planner</title>
  <style>
    body { font-family: monospace; max-width: 800px; margin: 40px auto; padding: 0 20px; }
    textarea { width: 100%; height: 80px; margin-bottom: 8px; box-sizing: border-box; }
    button { padding: 8px 16px; cursor: pointer; }
    #output { white-space: pre-wrap; margin-top: 20px; line-height: 1.6; }
    .step { color: #666; font-size: 0.9em; }
    .error { color: #c00; }
  </style>
</head>
<body>
  <h2>Travel Planner</h2>
  <textarea id="input" placeholder="Plan 5 days in Kyoto in October, budget $3000..."></textarea>
  <button onclick="plan()">Plan Trip</button>
  <button onclick="stop()" id="stopBtn" disabled>Stop</button>
  <div id="output"></div>

  <script>
    let controller;

    async function plan() {
      const goal = document.getElementById('input').value.trim();
      if (!goal) return;

      const output = document.getElementById('output');
      output.textContent = '';
      document.getElementById('stopBtn').disabled = false;

      controller = new AbortController();

      try {
        const res = await fetch('https://your-oracle-vm.com/plan', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ goal }),
          signal: controller.signal
        });

        const reader = res.body.getReader();
        const decoder = new TextDecoder();

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          output.textContent += decoder.decode(value);
          window.scrollTo(0, document.body.scrollHeight);
        }
      } catch (e) {
        if (e.name !== 'AbortError') {
          output.textContent += '\n[Error: ' + e.message + ']';
        }
      } finally {
        document.getElementById('stopBtn').disabled = true;
      }
    }

    function stop() {
      if (controller) controller.abort();
    }
  </script>
</body>
</html>
```

### Phase 2+: React + Vite

Migrate when you need: map view, itinerary cards, collaborative features, reservation confirmation UI. React + Vite produces a pure static build. Deploy to GitHub Pages via GitHub Actions.

Install: `npm create vite@latest travel-planner -- --template react-ts`

Use Vercel AI SDK (framework-agnostic client) for SSE streaming from Go backend.
Use Leaflet + OSM tiles for maps — no API key, no billing.

---

## Skills to Learn

**Prompt engineering** (weeks 1–2, ongoing)
Zero-shot vs few-shot, chain-of-thought, ReAct pattern, structured output prompting for reliable JSON, system prompt design, tool description writing. Tool descriptions are where most agent bugs originate. Start at: https://ai.google.dev/gemini-api/docs/prompting-strategies

**Embeddings and vector search** (weeks 2–3)
What embeddings are, cosine similarity, when to use vector search vs keyword search vs SQL, how to chunk documents for good retrieval. Hands-on with pgvector and nomic-embed-text via Ollama.

**RAG — Retrieval-Augmented Generation** (weeks 3–4)
Embed knowledge base, retrieve top-k at query time, inject as context. Without RAG the agent's destination knowledge is frozen at LLM training cutoff.

**Tool use and function calling**
JSON Schema definitions, handling tool results, writing precise descriptions.

**Agent patterns**
ReAct loop, plan-and-execute, multi-agent orchestration, human-in-the-loop, critique-and-revise.

**Context window management**
Summarisation strategies, what to persist vs keep in prompt, preventing context overflow in long runs.

**SSE streaming in Go**
`net/http` SSE, goroutine-to-channel, Redis pub/sub for multi-instance.

**Eval-driven development**
Scenario test datasets, scoring agent outputs, catching regressions before deploy. LangFuse supports this natively.

**React + Vite basics** (Phase 2+)
Enough to own and progressively enhance the frontend.

---

## Knowledge to Pick Up

- **LLM fundamentals** — tokens (not words), temperature, context windows as finite RAM, why hallucination happens, non-determinism
- **Token economics** — cost per token, how to minimise without losing quality
- **Semantic chunking** — chunk by semantic unit with overlap; character-count splitting degrades retrieval
- **Prompt injection** — all external API content is untrusted input; sanitise before injecting into prompts
- **Agent failure modes** — infinite loops (max step limit), stuck agents (timeouts), hallucinated tool arguments (validate inputs), cascading failures (early errors compounding)
- **Multi-tenancy** — each run scoped to user ID, no shared mutable state between concurrent runs
- **Rate limiting** — exponential backoff, per-user limits in Redis, graceful degradation not silent failure

---

## Build Phases

---

### Phase 0 — Streaming Pipe (Weeks 1–2) ✅

**Goal:** End-to-end streaming works. Nothing is agentic yet.

- [x] Provision Oracle VM, install Docker
- [x] Docker Compose: Postgres + pgvector, Redis, LangFuse, Caddy
- [x] Minimal Go HTTP server with one `POST /plan` SSE endpoint
- [x] CORS configured for GitHub Pages domain
- [x] Goroutine-to-channel streaming (no Redis pub/sub yet)
- [x] Hardcoded LLM response streams through SSE to verify pipe works
- [ ] Deploy plain `index.html` to GitHub Pages
- [ ] Verify full HTTPS end to end (GitHub Pages → Caddy → Go → SSE → browser)
- [x] LangFuse receiving traces

**Deliverable:** User types a goal. Hardcoded text streams back token by token. Full HTTPS. LangFuse showing traces.

---

### Phase 1 — Single Agent (Weeks 3–6) ✅

**Goal:** Functioning ReAct agent, real tools, genuine replanning, structured output.

- [x] Write ReAct agent loop from scratch (no framework)
- [x] Implement `LLMClient` interface with Gemini primary + Ollama fallback
- [x] Implement per-tool timeout and retry logic with exponential backoff
- [x] Implement structured tool result type (`ToolResult` with error and degraded fields)
- [x] Real tools: Open-Meteo weather, Nominatim geocoding, WebSearchAgent + SearXNG
- [x] Placeholder tools: mock Booking.com (realistic hardcoded data)
- [x] `ask_user` tool for Supervisor clarifications
- [x] Structured output — typed Go itinerary struct, not freeform markdown
- [x] Four middlewares: logging, auth stub, rate limiting (Redis), CORS
- [x] Stream tool status updates to client in real time as JSON SSE events
- [x] Session-based REST API: `POST /sessions`, `GET /sessions/{id}/stream`, `POST /sessions/{id}/respond`, `POST /sessions/{id}/interrupt`, `GET /sessions/{id}`, `GET /sessions`
- [x] First eval dataset: 5 travel scenarios with expected output characteristics
- [x] Ollama running with `nomic-embed-text` model (embeddings ready for Phase 2)
  - Runtime setup: `docker exec ollama ollama pull nomic-embed-text` after first `make dev`

**Additional fixes applied during Phase 1:**
- Gemini thought-signature preservation (Error 400 fix)
- Open-Meteo archive API used for past dates (not just far-future)
- SearXNG `json` format enabled in `settings.yml`
- Redis and LangFuse host port mappings added to docker-compose
- LangFuse pinned to v2 (v3 requires ClickHouse); `SALT` env var added
- `ShouldFallback` broadened to any `genai.APIError` (was 429/5xx only)
- `make dev` starts all required services before running server locally

**Deliverable:** `POST /sessions {"goal":"plan 5 days in Kyoto in October, $3000"}` → `{id}`. Stream from `GET /sessions/{id}/stream`. Agent asks clarifications, plans itinerary, streams step-by-step status. Falls back to Ollama when Gemini rate limited. LangFuse showing full traces.

---

### Phase 2 — Real Data and Memory (Weeks 7–12)

**Goal:** Real APIs, semantic knowledge base, user memory, map view.

- [ ] Replace mock Booking.com with real API (flights, hotels, cars)
- [ ] Add OSRM for travel time validation between locations
- [ ] Add OpenTable for restaurant discovery
- [ ] Build destination knowledge base pipeline:
  - [ ] Pull Wikivoyage + Wikipedia content for target destinations
  - [ ] Chunk by section with 50-token overlap
  - [ ] Embed with Ollama nomic-embed-text
  - [ ] Store in pgvector with source tags
- [ ] Implement RAG retrieval — inject top-k chunks at run start
- [ ] Implement user memory table and loading at session start
- [ ] Add sqlc for type-safe DB queries across growing schema
- [ ] Ingest World Atlas of Light Pollution into Postgres
- [ ] Redis caching for API responses with appropriate TTLs
- [ ] Switch SSE streaming to Redis pub/sub (multi-instance ready)
- [ ] Prometheus + Grafana for infrastructure metrics
- [ ] Migrate frontend to React + Vite
- [ ] Add Leaflet map rendering itinerary locations

**Deliverable:** Real flights, hotels, cars with deep links. Accurate destination info from knowledge base. Remembers user preferences across sessions. Map view. Validated travel times via OSRM.

---

### Phase 3 — Multi-Agent System (Weeks 13–20)

**Goal:** All specialist agents, parallel execution, self-critique, reservations, MCP.

- [ ] Supervisor Agent with task DAG decomposition
- [ ] All specialist agents (see full list in Specialist Agents section)
- [ ] Concurrent goroutine execution of independent sub-tasks
- [ ] Context cancellation propagation through all goroutines
- [ ] Critique-revise loop (CritiqueAgent → Supervisor replanning)
- [ ] Integrate `soniakeys/meeus` for moon phase calculations
- [ ] NOAA SWPC feed for aurora Kp index
- [ ] OpenTable reservation flow with confirmation gates and audit log
- [ ] Cancellation deadline reminders stored in Postgres
- [ ] Pull Ollama 7B model (Llama 3.2 or Qwen 2.5) for cheap subtask routing
- [ ] Wrap stable tools as MCP servers using `mcp/go-sdk` for Claude Code integration
- [ ] Add evals to CI pipeline

**Deliverable:** Full multi-agent trip plan. Parallel sub-tasks. Self-critique and replanning. Restaurant reservations with lifecycle management. All specialist agent outputs assembled into coherent itinerary.

---

### Phase 4 — Multi-User and Collaboration (Weeks 21–28)

**Goal:** Auth, collaborative planning, proactive alerts, trip lifecycle.

- [ ] JWT authentication (replace auth stub middleware)
- [ ] Per-user agent isolation — every run scoped to user ID
- [ ] Multiple sessions per user with session list view
- [ ] Collaborative trips — multiple users, agent mediates preferences
- [ ] Trip versioning — store revisions, allow rollback
- [ ] Background price monitoring goroutine (daily check, Redis pub/sub alert)
- [ ] PDF export of itinerary
- [ ] Email integration for reservation request fallback
- [ ] Soft interrupt (inject context mid-run) — hard interrupt already implemented in Phase 1

**Deliverable:** Multi-user collaborative planning. Proactive price alerts. Trip history and versioning. Full reservation lifecycle with reminders.

---

### Phase 5 — Production Hardening (Weeks 29+)

**Goal:** Cost control, resilience, regression safety.

- [ ] Per-user Gemini API token budgets tracked in Postgres
- [ ] Auto-route to Ollama when user approaches token budget limit
- [ ] Agent run resumption from checkpoint on interruption
- [ ] Eval dataset in CI pipeline — regression detection on every deploy
- [ ] Hardened prompt injection defences on all external content
- [ ] Load testing SSE endpoint
- [ ] Review whether Kafka is justified for background job volume
- [ ] Consider self-hosting Nominatim if public instance reliability is an issue

**Deliverable:** Production-ready. Cost-controlled per user. Resilient to interruption. Regression-tested on every deploy.

---

## The One Rule

At every phase you should be able to hold the entire system in your head. If you cannot, you have added complexity too soon.

Add complexity only when you feel the pain of not having it. Not before.

When in doubt, check this guide for decisions already made. Most "what should I use for X" questions are answered here.
