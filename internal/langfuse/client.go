// Package langfuse provides a minimal HTTP client for the LangFuse tracing API.
package langfuse

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client posts traces to the LangFuse REST API using HTTP Basic Auth.
type Client struct {
	host       string
	authHeader string
	http       *http.Client
}

// New returns a Client configured for the given host and credentials.
// If publicKey or secretKey are empty, calls are still made but will be
// rejected by the server — useful for development without LangFuse.
func New(host, publicKey, secretKey string) *Client {
	creds := base64.StdEncoding.EncodeToString([]byte(publicKey + ":" + secretKey))
	return &Client{
		host:       host,
		authHeader: "Basic " + creds,
		http:       &http.Client{Timeout: 5 * time.Second},
	}
}

// tracePayload is the request body sent to POST /api/public/traces.
type tracePayload struct {
	Name      string            `json:"name"`
	UserID    string            `json:"userId,omitempty"`
	SessionID string            `json:"sessionId,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Trace creates a named trace in LangFuse. Errors are logged but not fatal —
// observability should not break the happy path.
func (c *Client) Trace(ctx context.Context, name, userID, sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	payload := tracePayload{
		Name:      name,
		UserID:    userID,
		SessionID: sessionID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("langfuse: marshal trace: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.host+"/api/public/traces", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("langfuse: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("langfuse: post trace: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("langfuse: trace returned %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("langfuse: decode response: %w", err)
	}

	return result.ID, nil
}
