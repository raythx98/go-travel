# Gemini Migration — Implementation Log

**Date:** 2026-05-14  
**Plan:** `agent_logs/20260514_020000_gemini_migration_plan.md`

## Summary

User requested migration from Anthropic Claude to Google Gemini as the primary LLM.  
Motivation: Anthropic billing error (HTTP 400, credit balance too low). Gemini API is free via Google AI Studio.

## Changes Made

### Deleted
- `internal/llm/claude.go` — Claude implementation removed entirely

### New Files
- `internal/llm/gemini.go` — GeminiClient implementing LLMClient using `google.golang.org/genai v1.57.0`

### Modified Files

| File | Change |
|------|--------|
| `go.mod` | Removed `anthropics/anthropic-sdk-go v1.43.0`; added `google.golang.org/genai v1.57.0` as direct dependency |
| `internal/config/config.go` | `AnthropicAPIKey`/`ClaudeModel` → `GeminiAPIKey`/`GeminiModel`; env vars `GEMINI_API_KEY`/`GEMINI_MODEL` |
| `cmd/server/main.go` | Wires `NewGeminiClient` instead of Claude; graceful fallback to Ollama when `GEMINI_API_KEY` is empty |
| `docker-compose.yml` | `ANTHROPIC_API_KEY` → `GEMINI_API_KEY` + `GEMINI_MODEL` |
| `.envrc` | `ANTHROPIC_API_KEY`/`CLAUDE_MODEL` → `GEMINI_API_KEY`/`GEMINI_MODEL` (key value cleared — user must supply) |
| `GUIDE.md` | All Claude/Anthropic references updated to Gemini throughout |

## Key Implementation Notes

- Default model: `gemini-2.0-flash` (free tier, 15 RPM, 1M tokens/day)
- Gemini role for assistant turns is `"model"` (not `"assistant"`)  
- Tool results travel as `genai.FunctionResponse` parts in user turns (not a separate `function` role)
- `IsRateLimitOrUnavailable` uses `genai.APIError.Code` (HTTP int) — triggers Ollama fallback on 429 or ≥500
- FunctionCall ID falls back to Name if Gemini omits it (Gemini may not always populate the ID field)
- `ParametersJsonSchema: t.InputSchema` used to avoid building `genai.Schema` objects from raw JSON

## Verification

```
go build ./...  ✓
go vet ./...    ✓
go test ./...   ✓ (no test files — all packages pass)
```

## Next Steps (not implemented)

- Add `GEMINI_API_KEY` to Oracle VM environment / secrets manager
- Smoke-test `POST /plan` with a real Gemini key to confirm full ReAct loop
- Consider adding `GEMINI_MODEL` override in `.envrc` for trying gemini-1.5-pro etc.
