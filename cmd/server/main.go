// Command server is the entry point for the go-travel HTTP API.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/raythx98/go-travel/internal/agent"
	"github.com/raythx98/go-travel/internal/config"
	"github.com/raythx98/go-travel/internal/langfuse"
	"github.com/raythx98/go-travel/internal/llm"
	"github.com/raythx98/go-travel/internal/server"
	"github.com/raythx98/go-travel/internal/tools"
)

func main() {
	// Load .env if present (local dev convenience — production uses env vars directly).
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Warn("godotenv: could not load .env", "error", err)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// LLM routing: Gemini primary with Ollama fallback.
	// If no API key is configured, Ollama is used directly as the primary client.
	ollamaClient := llm.NewOllamaClient(cfg.OllamaURL, cfg.OllamaModel)
	var llmRouter llm.LLMClient
	if cfg.GeminiAPIKey == "" {
		slog.Info("llm: no GEMINI_API_KEY set, using Ollama as primary")
		llmRouter = ollamaClient
	} else {
		geminiClient, err := llm.NewGeminiClient(ctx, cfg.GeminiAPIKey, cfg.GeminiModel)
		if err != nil {
			slog.Error("gemini: failed to create client", "error", err)
			os.Exit(1)
		}
		llmRouter = llm.NewRouter(geminiClient, ollamaClient)
	}

	// Tool registry.
	registry := tools.New()
	registry.Register(tools.NewWeatherTool())
	registry.Register(tools.NewGeocodeTool())
	registry.Register(tools.NewSearchTool(cfg.SearXNGURL))
	registry.Register(&tools.FlightsTool{})
	registry.Register(&tools.HotelsTool{})
	registry.Register(&tools.AskUserTool{})
	registry.Register(&tools.FinalizeTool{})

	planAgent := agent.New(llmRouter, registry)

	// LangFuse tracing client.
	lf := langfuse.New(cfg.LangFuseHost, cfg.LangFusePublicKey, cfg.LangFuseSecretKey)

	// Smoke-test LangFuse on startup.
	smokeCtx, smokeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer smokeCancel()
	if _, err := lf.Trace(smokeCtx, "server_start", "", ""); err != nil {
		slog.Warn("langfuse: smoke-test trace failed", "error", err)
	} else {
		slog.Info("langfuse: smoke-test trace sent")
	}

	// Redis (optional — rate limiting degrades gracefully without it).
	var rdb *redis.Client
	if cfg.RedisURL != "" {
		opt, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			slog.Warn("redis: invalid URL, rate limiting disabled", "error", err)
		} else {
			rdb = redis.NewClient(opt)
			pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
			defer pingCancel()
			if err := rdb.Ping(pingCtx).Err(); err != nil {
				slog.Warn("redis: ping failed, rate limiting disabled", "error", err)
				rdb = nil
			} else {
				slog.Info("redis: connected")
			}
		}
	}

	h := server.New(planAgent, lf, rdb, cfg.AllowedOrigin, cfg.RateLimitRPM)

	addr := ":" + cfg.Port
	slog.Info("server starting", "addr", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      h,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
