package llm

import (
	"context"
	"log/slog"
)

// Router tries the primary LLMClient first and falls back to the secondary
// when the primary returns a rate limit or unavailability error.
type Router struct {
	primary   LLMClient
	secondary LLMClient
}

// NewRouter returns a Router with the given primary and fallback clients.
// If secondary is nil, errors from primary are returned as-is.
func NewRouter(primary, secondary LLMClient) *Router {
	return &Router{primary: primary, secondary: secondary}
}

// Complete calls the primary client. On rate limit or unavailability it falls
// back to the secondary client and logs the switch.
func (r *Router) Complete(ctx context.Context, system string, messages []Message, tools []Tool) (Response, error) {
	resp, err := r.primary.Complete(ctx, system, messages, tools)
	if err == nil {
		return resp, nil
	}

	if r.secondary == nil || !ShouldFallback(err) {
		return Response{}, err
	}

	slog.WarnContext(ctx, "llm_fallback",
		"reason", err.Error(),
		"fallback", "ollama",
	)

	return r.secondary.Complete(ctx, system, messages, tools)
}
