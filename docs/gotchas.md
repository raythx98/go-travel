# Gotchas

Traps, quirks, and things not to change without care.

---

## Gemini thinking models: ThoughtSignature must be preserved

Gemini thinking models (and `gemini-2.0-flash` with reasoning) return `Part.Thought = true` for internal reasoning text, and attach `Part.ThoughtSignature []byte` to function call parts.

**Rules:**
1. Never include `Thought: true` parts in conversation history — they must be stripped before appending to messages.
2. When a function call part has a `ThoughtSignature`, store it on `ContentBlock.ThoughtSignature`. When replaying that turn in history, echo it back on the `genai.Part`. Failure causes Error 400: "Function call is missing a thought_signature in functionCall parts."

This is handled in `internal/llm/gemini.go` — `fromGeminiResponse` and `toGeminiContents`.

---

## Open-Meteo: forecast API rejects past dates AND has a 16-day ceiling

The forecast API at `api.open-meteo.com/v1/forecast`:
- Rejects dates in the past (HTTP 400)
- Only covers up to 16 days ahead

The archive API at `archive-api.open-meteo.com/v1/archive` must be used for both past dates and far-future dates. For far-future dates, shift the request back 1 year as a seasonal proxy and remap the response dates back to actual travel dates. For past dates, use the actual dates with no shift.

See `internal/tools/weather.go` `forecastWindow` constant and `useArchive`/`shiftDates` logic.

---

## SearXNG: json format must be explicitly listed in settings.yml

SearXNG returns HTTP 403 for any request using a format not listed in `search.formats` in `settings.yml`. The default config only includes `html`. API calls from the backend use `format=json` and will get 403 until `json` is added.

```yaml
search:
  formats:
    - html
    - json
```

See `searxng/settings.yml`.

---

## Redis: no host port mapping by default in docker-compose

The Go server runs locally (not in a container) during development. `make dev` starts services via Docker Compose. Redis by default only exposes its port on the Docker internal network. Without an explicit `ports: - "6379:6379"` entry, `redis://localhost:6379` from the server fails with connection refused.

Same applies to any service the local server needs to reach.

---

## LangFuse v3 requires ClickHouse — pin to v2

`langfuse/langfuse:latest` is now v3 which requires ClickHouse and S3 blob storage. Pulling `:latest` will cause a boot loop with "CLICKHOUSE_URL is not configured." Pin to `langfuse/langfuse:2`.

LangFuse v2 also requires a `SALT` environment variable for API key encryption. Omitting it causes a boot loop with "Invalid environment variables: SALT".

---

## Gemini FunctionCall.ID may be empty

Gemini does not always populate `FunctionCall.ID`. When it is empty, fall back to the function name as the ID. This is handled in `fromGeminiResponse`.

---

## Ollama tool result format differs from Gemini

Ollama's `/api/chat` endpoint does not have a native `tool_result` content block type. Tool results must be folded into the user message as plain text content. This is handled in `OllamaClient` and differs from `GeminiClient`. Keep the two implementations separate — do not try to share message translation logic.

---

## Rate limiter is fail-open

The Redis-backed rate limiter in `internal/middleware/ratelimit.go` fails open: if Redis is unavailable, all requests are allowed through. This is intentional during development so a Redis outage does not block the server. Production should alert on Redis failures separately.
