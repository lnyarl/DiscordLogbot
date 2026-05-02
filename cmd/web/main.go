package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/lnyarl/discordlogbot/internal/config"
	"github.com/lnyarl/discordlogbot/internal/db"
	"github.com/lnyarl/discordlogbot/internal/httpx"
)

func main() {
	config.Load()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	pool, err := db.NewPool(context.Background(), config.MustGet("DATABASE_URL"))
	if err != nil {
		slog.Error("db pool init failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("db pool ready")

	port := config.Get("WEB_PORT", "8080")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.Health)

	slog.Info("web starting (Phase 0 stub)", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("web failed", "err", err)
		os.Exit(1)
	}
}
