# Decisions

Architectural decision records — why things are the way they are.

---

## ADR-001: Gemini as primary LLM (not Claude)

**Date:** 2026-05-14  
**Status:** Active

**Context:** Original implementation used Anthropic Claude. A billing error (HTTP 400, credit balance too low) blocked all API calls.

**Decision:** Migrate to Google Gemini (`gemini-2.0-flash`) as the primary LLM. Gemini free tier provides 15 RPM and 1M tokens/day via Google AI Studio — sufficient for development and early production.

**Consequences:** Go SDK changed from `anthropics/anthropic-sdk-go` to `google.golang.org/genai`. Gemini role names differ (`model` not `assistant`). Thinking model handling required (see `docs/gotchas.md`). Ollama remains as the fallback.

---

## ADR-002: ShouldFallback triggers on any genai.APIError

**Date:** 2026-05-14  
**Status:** Active

**Context:** Original `IsRateLimitOrUnavailable` only triggered fallback on HTTP 429 and ≥500. Gemini thinking models return HTTP 400 (`thought_signature` missing) when history is incorrect. This 400 was not triggering Ollama fallback.

**Decision:** Rename to `ShouldFallback` and broaden: any `genai.APIError` (including 4xx) triggers fallback, except `context.Canceled` (user interrupt must not cascade to Ollama).

**Consequences:** A 400 caused by a bug in our code (e.g. bad input schema) will also trigger Ollama fallback rather than surfacing the error directly. Acceptable trade-off: Ollama will also fail on bad input, and the error will surface at that point.

---

## ADR-003: Session-based REST API from Phase 1

**Date:** 2026-05-14  
**Status:** Active

**Context:** The Phase 1 plan deferred the full session API to Phase 4. The original backend had `POST /plan` (combined create + stream). The React frontend (already in progress at `/Users/raytoh/code/js/travel`) expected 6 separate endpoints from day one.

**Decision:** Implement the full session API in Phase 1: `POST /sessions`, `GET /sessions`, `GET /sessions/{id}`, `GET /sessions/{id}/stream`, `POST /sessions/{id}/respond`, `POST /sessions/{id}/interrupt`.

**Consequences:** Sessions are in-memory only (Phase 1). Phase 2 will persist to Postgres. The interface contract is now stable and the frontend can rely on it.

---

## ADR-004: Open-Meteo archive API for past dates

**Date:** 2026-05-14  
**Status:** Active

**Context:** The weather tool originally used the forecast API for dates within the 16-day window and the archive API (shifted -1 year) for far-future dates. It did not handle past dates — the forecast API returns HTTP 400 for dates in the past, and the LLM sometimes supplies past dates in test scenarios.

**Decision:** Use the archive API when `daysAhead < 0` (past dates) with the actual dates unshifted. Use the archive API with -1 year shift only for far-future dates (`daysAhead > 16`). Forecast API for dates within the window.

**Consequences:** Past dates return actual historical data (accurate). Far-future dates return a seasonal proxy from the previous year (clearly flagged as `historical: true` in the response).

---

## ADR-005: LangFuse pinned to v2

**Date:** 2026-05-14  
**Status:** Active

**Context:** `langfuse/langfuse:latest` pulled v3, which requires ClickHouse and an S3-compatible blob store. This is significant additional infrastructure not worth adding for LLM tracing alone.

**Decision:** Pin to `langfuse/langfuse:2`. V2 runs on Postgres only and fits within the existing stack.

**Consequences:** Must set `SALT` environment variable (required by v2 for API key encryption). Must not use `latest` tag for this service.
