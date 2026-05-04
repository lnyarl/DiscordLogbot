package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/time/rate"

	"github.com/lnyarl/discordlogbot/internal/auth"
	"github.com/lnyarl/discordlogbot/internal/config"
	"github.com/lnyarl/discordlogbot/internal/db"
	"github.com/lnyarl/discordlogbot/internal/httpx"
	"github.com/lnyarl/discordlogbot/internal/mcp"
	"github.com/lnyarl/discordlogbot/internal/oauth"
	"github.com/lnyarl/discordlogbot/internal/permissions"
	"github.com/lnyarl/discordlogbot/internal/web"
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

	// ── shared dependencies ──────────────────────────────────────────────
	jwtSecret := []byte(config.MustGet("JWT_SECRET"))
	discordToken := config.MustGet("DISCORD_TOKEN")
	discord := permissions.NewClient("", discordToken)

	// ── MCP (Phase 6) — full 4-tool surface backed by the DB ─────────────
	mcpServer := mcp.NewServerWithPool(mcp.NewDiscordLister(discord), pool)
	verifier := auth.NewMCPVerifier(string(jwtSecret))
	mcpHandler := mcp.NewHandler(verifier, mcpServer, nil)

	// ── OAuth authorization server (Phase 6) ─────────────────────────────
	oauthSrv := oauth.New(
		config.Get("BASE_URL", "http://localhost:8080"),
		jwtSecret,
		config.Get("DISCORD_CLIENT_ID", ""),
		config.Get("DISCORD_CLIENT_SECRET", ""),
		config.Get("MCP_CLIENT_IDS", ""),
		config.Get("MCP_ALLOWED_REDIRECT_URIS", ""),
		pool,
		discord,
	)

	// ── Web (Phase 5) ────────────────────────────────────────────────────
	tpl, err := web.LoadTemplates()
	if err != nil {
		slog.Error("template load failed", "err", err)
		os.Exit(1)
	}

	dataBase := config.Get("WEB_DATA_DIR", filepath.Join(".", "data"))
	staticDir := config.Get("WEB_STATIC_DIR", "")
	staticDirs := web.StaticDirs{
		Static:      staticDir, // optional; "" disables /static
		Attachments: config.Get("ATTACHMENTS_DIR", filepath.Join(dataBase, "attachments")),
		Emojis:      config.Get("EMOJIS_DIR", filepath.Join(dataBase, "emojis")),
	}
	if err := staticDirs.CheckExists(); err != nil {
		slog.Error("static dirs", "err", err)
		os.Exit(1)
	}

	clientID := config.Get("DISCORD_CLIENT_ID", "")
	botInviteURL := ""
	if clientID != "" {
		botInviteURL = "https://discord.com/oauth2/authorize?client_id=" + clientID +
			"&permissions=66560&integration_type=0&scope=bot+applications.commands"
	}

	authH := &web.AuthHandler{
		OAuth: web.OAuth2Config{
			ClientID:     clientID,
			ClientSecret: config.Get("DISCORD_CLIENT_SECRET", ""),
			RedirectURI:  config.Get("DISCORD_REDIRECT_URI", "http://localhost:8080/auth/callback"),
			Scopes:       "identify guilds guilds.members.read",
		},
		JWTSecret:   jwtSecret,
		Pool:        pool,
		Permissions: discord,
		Secure:      config.Get("WEB_COOKIE_SECURE", "true") == "true",
	}

	searchH := &web.SearchHandler{
		Pool:         pool,
		Permissions:  discord,
		BotInviteURL: botInviteURL,
	}

	sessionVerifier := auth.NewSessionVerifier(string(jwtSecret))

	// Rate limiters mirror search.py: 60/min on /api/channels, 30/min on
	// /api/search. slowapi's "N/minute" maps to rate=N/60, burst=N.
	channelsRL := web.NewIPRateLimiter(rate.Every(time.Second), 60)         // 60/min sustained
	searchRL := web.NewIPRateLimiter(rate.Every(2*time.Second), 30)         // 30/min sustained

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", httpx.Health)
	mcpHandler.Routes(mux)
	oauthSrv.Mount(mux)

	// Static assets
	staticDirs.Mount(mux)

	// Public pages — / accepts both anonymous (login page) and signed-in
	// (redirect to /search) sessions, so we use AuthOptional.
	mux.Handle("GET /{$}", web.AuthOptional(sessionVerifier, authH.IndexHandler(tpl)))
	mux.Handle("GET /auth/login", authH.LoginHandler())
	mux.Handle("GET /auth/callback", authH.CallbackHandler())

	// Authenticated pages / endpoints
	mux.Handle("POST /auth/logout", web.RequireSession(sessionVerifier, authH.LogoutHandler()))
	mux.Handle("GET /search", web.RequireSession(sessionVerifier, searchH.SearchPage(tpl)))
	mux.Handle("GET /api/channels",
		web.RequireSession(sessionVerifier, channelsRL.Middleware(searchH.ChannelsAPI())))
	mux.Handle("GET /api/search",
		web.RequireSession(sessionVerifier, searchRL.Middleware(searchH.SearchAPI())))

	port := config.Get("WEB_PORT", "8080")
	slog.Info("web starting", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("web failed", "err", err)
		os.Exit(1)
	}
}
