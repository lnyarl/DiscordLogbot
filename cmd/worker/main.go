package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/lnyarl/discordlogbot/internal/config"
	"github.com/lnyarl/discordlogbot/internal/db"
	"github.com/lnyarl/discordlogbot/internal/httpx"
	"github.com/lnyarl/discordlogbot/internal/worker"
)

func main() {
	config.Load()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// Worker pool registers the pgvector codec on every connection so the
	// `embedding` column round-trips via pgvector.NewVector / scan.
	pool, err := db.NewPoolWithVector(context.Background(), config.MustGet("DATABASE_URL"))
	if err != nil {
		slog.Error("db pool init failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("db pool ready (with pgvector)")

	cfg := worker.DefaultConfig()
	cfg.OllamaHost = config.Get("OLLAMA_HOST", cfg.OllamaHost)
	cfg.OllamaModel = config.Get("OLLAMA_MODEL", cfg.OllamaModel)
	cfg.BatchSize = parseEnvInt("BATCH_SIZE", cfg.BatchSize)
	cfg.Concurrency = parseEnvInt("CONCURRENCY", cfg.Concurrency)
	cfg.PollInterval = time.Duration(parseEnvInt("POLL_INTERVAL", int(cfg.PollInterval.Seconds()))) * time.Second
	cfg.MaxTokens = parseEnvInt("MAX_TOKENS", cfg.MaxTokens)
	cfg.ContextBefore = parseEnvInt("CONTEXT_BEFORE", cfg.ContextBefore)
	cfg.ContextAfter = parseEnvInt("CONTEXT_AFTER", cfg.ContextAfter)
	if hours := config.Get("MAX_GAP_HOURS", ""); hours != "" {
		if n, err := strconv.ParseFloat(hours, 64); err == nil {
			cfg.MaxGap = time.Duration(n * float64(time.Hour))
		}
	}

	ollama := worker.NewOllama(cfg.OllamaHost, cfg.OllamaModel)

	// /health remains so existing healthcheck infrastructure keeps working.
	port := config.Get("WORKER_HEALTH_PORT", "8082")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.Health)
	go func() {
		slog.Info("health endpoint", "port", port)
		_ = http.ListenAndServe(":"+port, mux)
	}()

	// Cancel on SIGINT/SIGTERM so an in-flight batch can finish + commit
	// before exit.
	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := worker.Run(ctx, pool, ollama, cfg); err != nil && err != context.Canceled {
		slog.Error("worker exited", "err", err)
		os.Exit(1)
	}
	slog.Info("worker exited cleanly")
}

func parseEnvInt(key string, def int) int {
	v := config.Get(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
