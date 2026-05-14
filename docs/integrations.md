# Integrations

External APIs and services — contracts, auth, failure modes, and TTLs.

---

## Google Gemini API

**Purpose:** Primary LLM for the ReAct agent loop.  
**SDK:** `google.golang.org/genai v1.57.0`  
**Auth:** API key via `GEMINI_API_KEY` env var, passed to `genai.ClientConfig{APIKey: ...}`  
**Default model:** `gemini-2.0-flash` (override with `GEMINI_MODEL`)  
**Free tier limits:** 15 RPM, 1M tokens/day (Google AI Studio key)  
**Max output tokens:** 8192 (`geminiMaxOutputTokens` constant)  
**Failure modes:**
- 429 (rate limit) → triggers Ollama fallback via `ShouldFallback`
- 400 (bad request, including thought_signature errors) → triggers Ollama fallback
- 5xx → triggers Ollama fallback
- `context.Canceled` → does NOT trigger fallback (user interrupt)

**Notes:** Gemini uses role `"model"` for assistant turns (not `"assistant"`). Tool results travel as `FunctionResponse` parts in user turns. See `docs/gotchas.md` for thinking model handling.

---

## Ollama

**Purpose:** LLM fallback when Gemini is unavailable.  
**Endpoint:** `OLLAMA_URL` env var (default `http://localhost:11434`), path `/api/chat`  
**Model:** `OLLAMA_MODEL` env var (e.g. `llama3.2`)  
**Auth:** None  
**Phase 1 role:** Fallback only. Phase 3 will add a 7B model for cheap sub-task routing.  
**First-time setup:** After first `docker compose up`, run:
```bash
docker exec ollama ollama pull llama3.2
docker exec ollama ollama pull nomic-embed-text  # for Phase 2 embeddings
```
**Failure modes:** HTTP errors returned as-is (no fallback from fallback).

---

## Open-Meteo

**Purpose:** Weather forecasts and historical climate data.  
**Auth:** None (no API key required)  
**Rate limits:** None documented; be polite with retries.  
**Forecast API:** `https://api.open-meteo.com/v1/forecast` — covers up to 16 days ahead only. Rejects past dates with HTTP 400.  
**Archive API:** `https://archive-api.open-meteo.com/v1/archive` — historical data; used for past dates (actual dates) and far-future dates (shifted -1 year as seasonal proxy).  
**Timeout:** 5s per request, 3 retries with exponential backoff.  
**Key variables:** `temperature_2m_max`, `temperature_2m_min`, `precipitation_sum`, `windspeed_10m_max`, `weathercode`

---

## SearXNG (self-hosted)

**Purpose:** Web search for the `web_search` tool.  
**Endpoint:** `SEARXNG_URL` env var (default `http://localhost:8080`)  
**Auth:** None  
**Request format:** `GET /search?q=...&format=json&categories=general&engines=duckduckgo,bing`  
**Configuration requirement:** `json` must be listed in `search.formats` in `searxng/settings.yml`, or all JSON API requests return HTTP 403.  
**Timeout:** 8s per request, 3 retries.  
**Returns:** Up to 5 results with `title`, `url`, `content`.  
**Failure mode:** `Degraded: true` in `ToolResult`; agent continues without search data.

---

## Nominatim (Geocoding)

**Purpose:** Latitude/longitude lookup for the `geocode` tool.  
**Endpoint:** `https://nominatim.openstreetmap.org/search`  
**Auth:** None. Must set a `User-Agent` header identifying the application.  
**Rate limits:** 1 request/second on the public instance. Respect this.  
**Timeout:** 3s per request.  
**Failure mode:** `Degraded: true`; agent falls back to asking the user for coordinates.

---

## LangFuse

**Purpose:** LLM tracing — full prompt/completion/tool call traces for observability.  
**Image:** `langfuse/langfuse:2` (pinned — v3 requires ClickHouse)  
**Auth:** `LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY` env vars. Keys are created in the LangFuse UI at `http://localhost:3000`.  
**Required env vars:** `SALT` (for API key encryption), `NEXTAUTH_SECRET`, `DATABASE_URL`.  
**UI:** `http://localhost:3000`  
**Failure mode:** Smoke-test trace failures are logged as WARN and do not block server startup. The agent loop continues without tracing if LangFuse is unreachable.

---

## Redis

**Purpose:** Rate limiting (sliding window per IP) and session pub/sub in Phase 2+.  
**URL:** `REDIS_URL` env var (default `redis://localhost:6379`)  
**Image:** `redis:7-alpine`, `maxmemory 256mb`, `allkeys-lru` eviction  
**Phase 1 use:** Rate limiting only. Fail-open: middleware allows all requests if Redis is down.  
**Phase 2 use:** SSE pub/sub for multi-instance streaming; API response caching with TTLs.  
**Host access:** Requires `ports: - "6379:6379"` in docker-compose when running the server locally outside Docker.

---

## PostgreSQL + pgvector

**Purpose:** Primary database. Vector similarity search for RAG (Phase 2).  
**Image:** `pgvector/pgvector:pg16`  
**URL:** `DATABASE_URL` env var  
**Phase 1 use:** Schema migrations applied; tables exist but not yet used by server code (Phase 2 adds active queries).  
**Schema:** See `GUIDE.md` for core table definitions. Migrations in `sqlc/migrations/`.
