package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/lnyarl/discordlogbot/internal/auth"
	"github.com/lnyarl/discordlogbot/internal/config"
	"github.com/lnyarl/discordlogbot/internal/db"
	"github.com/lnyarl/discordlogbot/internal/httpx"
	"github.com/lnyarl/discordlogbot/internal/mcp"
	"github.com/lnyarl/discordlogbot/internal/permissions"
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

	// Phase 2: MCP server with list_channels (no cache yet — Discord API
	// is hit on every call). Phase 5/6 will add channel_access_cache and
	// the remaining tools.
	discord := permissions.NewClient("", config.MustGet("DISCORD_TOKEN"))
	mcpServer := mcp.NewServer(mcp.NewDiscordLister(discord))
	verifier := auth.NewMCPVerifier(config.MustGet("JWT_SECRET"))
	// Production defaults: DNS-rebinding protection ON (opts == nil).
	mcpHandler := mcp.NewHandler(verifier, mcpServer, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.Health)
	mcpHandler.Routes(mux)

	port := config.Get("WEB_PORT", "8080")
	slog.Info("web starting", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("web failed", "err", err)
		os.Exit(1)
	}
}
