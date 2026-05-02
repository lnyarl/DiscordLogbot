package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/lnyarl/discordlogbot/internal/attachments"
	"github.com/lnyarl/discordlogbot/internal/config"
	"github.com/lnyarl/discordlogbot/internal/db"
	"github.com/lnyarl/discordlogbot/internal/discord"
	"github.com/lnyarl/discordlogbot/internal/httpx"
)

func main() {
	config.Load()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx := context.Background()

	pool, err := db.NewPool(ctx, config.MustGet("DATABASE_URL"))
	if err != nil {
		slog.Error("db pool init failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("db pool ready")

	dl := attachments.New(
		config.Get("ATTACHMENTS_DIR", "data/attachments"),
		config.Get("EMOJIS_DIR", "data/emojis"),
	)

	bot, err := discord.NewBot(config.MustGet("DISCORD_TOKEN"), pool, dl)
	if err != nil {
		slog.Error("bot init failed", "err", err)
		os.Exit(1)
	}
	if err := bot.Open(); err != nil {
		slog.Error("bot open failed", "err", err)
		os.Exit(1)
	}
	defer bot.Close()
	slog.Info("bot connected to gateway")

	// Sync /logbot. Empty guild id = global; use DISCORD_TEST_GUILD for
	// instant scoped registration during development.
	if err := bot.SyncCommands(config.Get("DISCORD_TEST_GUILD", "")); err != nil {
		slog.Error("slash command sync failed", "err", err)
		os.Exit(1)
	}
	slog.Info("slash commands synced")

	// Health server runs alongside the gateway connection so docker-compose
	// can probe readiness. We keep it on a separate port from /web to avoid
	// the gateway connection blocking healthcheck responses.
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("GET /health", httpx.Health)
	healthPort := config.Get("BOT_HEALTH_PORT", "8081")
	go func() {
		slog.Info("bot health server", "port", healthPort)
		if err := http.ListenAndServe(":"+healthPort, healthMux); err != nil {
			slog.Error("health server failed", "err", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down")
}
