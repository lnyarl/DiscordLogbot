package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOllamaClient_Embed_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("path=%q", r.URL.Path)
		}
		var req embeddingsRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "test-model" || req.Prompt != "hello" {
			t.Errorf("req=%+v", req)
		}
		_ = json.NewEncoder(w).Encode(embeddingsResponse{
			Embedding: []float64{0.1, 0.2, 0.3},
		})
	}))
	defer srv.Close()

	c := NewOllama(srv.URL, "test-model")
	got, err := c.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []float32{0.1, 0.2, 0.3}
	if len(got) != len(want) {
		t.Fatalf("len=%d", len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got=%f want=%f", i, got[i], want[i])
		}
	}
}

func TestOllamaClient_Embed_RetriesOn503ThenSucceeds(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n < 3 {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(embeddingsResponse{Embedding: []float64{1.0}})
	}))
	defer srv.Close()

	c := NewOllama(srv.URL, "m")
	c.BaseBackoff = time.Millisecond // fast test
	got, err := c.Embed(context.Background(), "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if atomic.LoadInt32(&count) != 3 {
		t.Errorf("expected 3 attempts, got %d", count)
	}
	if len(got) != 1 || got[0] != 1.0 {
		t.Errorf("got %v", got)
	}
}

func TestOllamaClient_Embed_FailsOn4xxNoRetry(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()
	c := NewOllama(srv.URL, "m")
	c.BaseBackoff = time.Millisecond
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Error("expected error")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("4xx must not be retried; attempts=%d", count)
	}
}

func TestOllamaClient_Embed_FailsAfterAllRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewOllama(srv.URL, "m")
	c.MaxRetries = 2
	c.BaseBackoff = time.Millisecond
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Error("expected exhausted retries error")
	}
}

func TestOllamaClient_Embed_EmptyResponseRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(embeddingsResponse{})
	}))
	defer srv.Close()
	c := NewOllama(srv.URL, "m")
	if _, err := c.Embed(context.Background(), "x"); err == nil {
		t.Error("expected empty embedding error")
	}
}

func TestOllamaClient_Embed_RespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewOllama(srv.URL, "m")
	c.BaseBackoff = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	if _, err := c.Embed(ctx, "x"); err == nil {
		t.Error("expected cancellation error")
	}
}
