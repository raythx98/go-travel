# Gemini Migration Plan — Replace Anthropic/Claude with Google Gemini

**Date:** 2026-05-14  
**Goal:** Swap the primary LLM from Anthropic Claude to Google Gemini. Ollama remains the fallback. All other code (agent loop, tools, server) is unchanged.

---

## Inputs

- Phase 1 complete with Claude as primary LLM
- User cannot use Claude due to billing
- Gemini API is free via Google AI Studio (aistudio.google.com)

## Constraints

- `LLMClient` interface stays identical — only the implementation changes
- Ollama remains the fallback (same router pattern)
- No changes to agent loop, tools, server, or middleware
- GUIDE.md and .envrc.example must be updated to reflect Gemini

## Success Criteria

1. `go build ./...` / `go vet ./...` / `go test ./...` pass
2. `POST /plan` runs the full ReAct loop using Gemini
3. Falls back to Ollama on Gemini quota/rate errors (429 / RESOURCE_EXHAUSTED)
4. GUIDE.md reflects Gemini as primary LLM
5. `.envrc.example` and `docker-compose.yml` use `GEMINI_API_KEY` / `GEMINI_MODEL`

---

## SDK Choice

`google.golang.org/genai` — the new unified Google GenAI SDK (recommended over the older `github.com/google/generative-ai-go`).

Default model: `gemini-2.0-flash` — free tier, fast, supports function calling.
Free tier: 15 RPM, 1M tokens/day.

API key from: https://aistudio.google.com/apikey (free, no billing required)

---

## Gemini ↔ Internal Type Translation

| Internal type | Gemini equivalent |
|---------------|------------------|
| `RoleUser` | `"user"` |
| `RoleAssistant` | `"model"` |
| `ContentBlock{Type:"text"}` | `genai.Text` part |
| `ContentBlock{Type:"tool_use"}` | `genai.FunctionCall` part |
| `ContentBlock{Type:"tool_result"}` | `genai.FunctionResponse` part in `"function"` role turn |
| `Tool{Name,Desc,InputSchema}` | `genai.FunctionDeclaration` inside `genai.Tool` |
| `StopReasonEndTurn` | `genai.FinishReasonStop` |
| `StopReasonToolUse` | response contains `FunctionCall` parts |

---

## Files to Change

| File | Change |
|------|--------|
| `go.mod` | Remove `anthropic-sdk-go`, add `google.golang.org/genai` |
| `internal/llm/claude.go` | **Delete** |
| `internal/llm/gemini.go` | **New** — GeminiClient implementing LLMClient |
| `internal/llm/router.go` | Move `IsRateLimitOrUnavailable` here; make it Gemini-aware |
| `internal/config/config.go` | Replace `AnthropicAPIKey`/`ClaudeModel` → `GeminiAPIKey`/`GeminiModel` |
| `cmd/server/main.go` | Wire GeminiClient instead of ClaudeClient |
| `docker-compose.yml` | Replace `ANTHROPIC_API_KEY` → `GEMINI_API_KEY` |
| `.envrc.example` | Replace Anthropic vars with Gemini vars |
| `GUIDE.md` | Update LLM section, dependency table, Quick Reference |

## New Files

- `internal/llm/gemini.go`

## Assumptions

1. User will obtain a Gemini API key from aistudio.google.com.
2. `gemini-2.0-flash` is the default model (overridable via `GEMINI_MODEL` env var).
3. Gemini quota errors return HTTP 429 or gRPC RESOURCE_EXHAUSTED — both trigger Ollama fallback.
4. GUIDE.md Ollama section unchanged — Ollama remains the fallback.
