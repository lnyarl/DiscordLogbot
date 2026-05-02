package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/lnyarl/discordlogbot/internal/config"
	"github.com/lnyarl/discordlogbot/internal/httpx"
)

func main() {
	config.Load()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	port := config.Get("BOT_HEALTH_PORT", "8081")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.Health)

	slog.Info("bot starting (Phase 0 stub)", "health_port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("bot failed", "err", err)
		os.Exit(1)
	}
}
