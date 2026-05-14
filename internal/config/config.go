// Package config loads and validates server configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration read from environment variables.
type Config struct {
	Port          string
	AllowedOrigin string

	DatabaseURL string
	RedisURL    string

	GeminiAPIKey string
	GeminiModel  string
	OllamaURL    string
	OllamaModel     string
	SearXNGURL      string

	RateLimitRPM int

	LangFuseHost      string
	LangFusePublicKey string
	LangFuseSecretKey string
}

// Load reads configuration from the environment. It returns an error if any
// required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		Port:          getEnvOrDefault("PORT", "8080"),
		AllowedOrigin: getEnvOrDefault("ALLOWED_ORIGIN", "*"),

		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    getEnvOrDefault("REDIS_URL", "redis://localhost:6379"),

		GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
		GeminiModel:  os.Getenv("GEMINI_MODEL"),
		OllamaURL:    getEnvOrDefault("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:     os.Getenv("OLLAMA_MODEL"),
		SearXNGURL:      getEnvOrDefault("SEARXNG_URL", "http://localhost:8080"),

		RateLimitRPM: getEnvInt("RATE_LIMIT_RPM", 30),

		LangFuseHost:      getEnvOrDefault("LANGFUSE_HOST", "http://localhost:3000"),
		LangFusePublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
		LangFuseSecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	return cfg, nil
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
