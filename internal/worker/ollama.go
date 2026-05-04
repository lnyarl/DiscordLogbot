package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// OllamaClient calls the local Ollama embeddings endpoint.
//
// Migration plan §7 explicitly adds two stability improvements over the
// Python worker (which had neither): an http.Client.Timeout and an
// explicit backoff retry on transient errors. Embeddings are idempotent
// so retries are safe.
type OllamaClient struct {
	BaseURL    string
	Model      string
	HTTPClient *http.Client

	// MaxRetries 0 = no retries (one attempt total). Default in NewOllama.
	MaxRetries  int
	BaseBackoff time.Duration
}

// NewOllama builds a client with sensible defaults: 60s per-request
// timeout, up to 3 retries with 500ms → 1s → 2s exponential backoff.
func NewOllama(baseURL, model string) *OllamaClient {
	return &OllamaClient{
		BaseURL:     baseURL,
		Model:       model,
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		MaxRetries:  3,
		BaseBackoff: 500 * time.Millisecond,
	}
}

type embeddingsRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embeddingsResponse struct {
	Embedding []float64 `json:"embedding"`
	Error     string    `json:"error,omitempty"`
}

// Embed returns the float32 embedding for prompt. Conversion []float64 →
// []float32 happens here at the API boundary because pgvector stores
// float32 anyway — the conversion is lossless against what's eventually
// in the DB (R4 from the migration plan).
func (c *OllamaClient) Embed(ctx context.Context, prompt string) ([]float32, error) {
	body, err := json.Marshal(embeddingsRequest{Model: c.Model, Prompt: prompt})
	if err != nil {
		return nil, err
	}
	url := c.BaseURL + "/api/embeddings"

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.BaseBackoff << (attempt - 1)
			slog.Warn("ollama retry", "attempt", attempt, "backoff", backoff, "err", lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if !isTransient(err) {
				return nil, err
			}
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("ollama %d: %s", resp.StatusCode, respBody)
			if !isTransientStatus(resp.StatusCode) {
				return nil, lastErr
			}
			continue
		}

		var er embeddingsResponse
		if err := json.Unmarshal(respBody, &er); err != nil {
			return nil, err
		}
		if er.Error != "" {
			return nil, fmt.Errorf("ollama error: %s", er.Error)
		}
		if len(er.Embedding) == 0 {
			return nil, errors.New("empty embedding response")
		}
		out := make([]float32, len(er.Embedding))
		for i, v := range er.Embedding {
			out[i] = float32(v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("ollama exhausted retries: %w", lastErr)
}

// isTransient reports whether err is the kind of network error worth
// retrying. Permanent failures like "no such host" or "connection refused"
// (the dominant case when Ollama is simply not running) get filtered out
// so we don't burn 3 retries every batch on an obvious wiring problem.
// Genuine transient errors — read timeouts, broken pipes mid-stream —
// fall through to true.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	// Permanent: bad hostname.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	// Permanent: connection refused (Ollama not listening).
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") {
		return false
	}
	return true
}

// isTransientStatus returns true for HTTP statuses likely to clear on
// retry: 408 Request Timeout, 429 Too Many Requests, and any 5xx.
func isTransientStatus(code int) bool {
	if code == 408 || code == 429 {
		return true
	}
	return code >= 500 && code < 600
}
