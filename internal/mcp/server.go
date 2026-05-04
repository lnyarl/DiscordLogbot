package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lnyarl/discordlogbot/internal/auth"
	"github.com/lnyarl/discordlogbot/internal/cache"
	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// ChannelLister is the data dependency of the list_channels tool. The
// indirection lets tests inject a stub instead of hitting Discord.
type ChannelLister interface {
	ListChannels(ctx context.Context, userID string) ([]permissions.AccessibleChannel, error)
}

// DiscordLister adapts permissions.ComputeAccessibleChannels into the
// ChannelLister interface for production use.
type DiscordLister struct {
	Client *permissions.Client
}

func NewDiscordLister(c *permissions.Client) *DiscordLister {
	return &DiscordLister{Client: c}
}

func (d *DiscordLister) ListChannels(ctx context.Context, userID string) ([]permissions.AccessibleChannel, error) {
	return permissions.ComputeAccessibleChannels(ctx, d.Client, userID)
}

// Server bundles an MCP SDK server pre-loaded with our 4 tools.
type Server struct {
	sdk    *mcpsdk.Server
	lister ChannelLister
	pool   *pgxpool.Pool // optional — required by the 3 DB-backed tools
}

func (s *Server) SDK() *mcpsdk.Server { return s.sdk }

// NewServer constructs the SDK server with list_channels only — backwards-
// compatible with the Phase 2 wiring. Tests + the Phase 6 web binary use
// NewServerWithPool for the full 4-tool surface.
func NewServer(lister ChannelLister) *Server {
	return NewServerWithPool(lister, nil)
}

// NewServerWithPool registers all 4 MCP tools, including the DB-backed
// search_messages / get_messages / get_guild_events. When pool is nil
// only list_channels is registered (Phase 2 / unit-test mode).
func NewServerWithPool(lister ChannelLister, pool *pgxpool.Pool) *Server {
	sdk := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "discord-logbot", Version: "0.1.0"},
		nil,
	)
	s := &Server{sdk: sdk, lister: lister, pool: pool}

	mcpsdk.AddTool(sdk, &mcpsdk.Tool{
		Name:        "list_channels",
		Description: "접근 가능한 Discord 서버/채널 목록 반환",
	}, s.handleListChannels)

	if pool != nil {
		mcpsdk.AddTool(sdk, &mcpsdk.Tool{
			Name:        "search_messages",
			Description: "채널에서 키워드로 메시지 검색 (부분 일치). since/until로 기간 제한 가능.",
		}, s.handleSearchMessages)

		mcpsdk.AddTool(sdk, &mcpsdk.Tool{
			Name:        "get_messages",
			Description: "채널의 메시지 조회. since/until 미지정 시 최신순.",
		}, s.handleGetMessages)

		mcpsdk.AddTool(sdk, &mcpsdk.Tool{
			Name:        "get_guild_events",
			Description: "서버 이벤트(입퇴장, 밴, 역할 변경 등) 조회. since/until로 기간 제한 가능.",
		}, s.handleGetGuildEvents)
	}

	return s
}

// listChannelsArgs has no fields; the SDK still requires a struct type
// for the generic AddTool call so it can synthesise an input schema.
type listChannelsArgs struct{}

func (s *Server) handleListChannels(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	_ listChannelsArgs,
) (*mcpsdk.CallToolResult, any, error) {
	userID, ok := auth.UserIDFrom(ctx)
	if !ok {
		return nil, nil, errors.New("missing user_id in context")
	}
	channels, err := s.resolveChannels(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if channels == nil {
		channels = []permissions.AccessibleChannel{}
	}
	body, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(body)), nil, nil
}

// resolveChannels prefers the channel_access_cache (lazy-fill on miss)
// and falls back to direct Discord API computation when no pool is wired.
// Mirrors web/permissions.py:get_or_compute_channels.
func (s *Server) resolveChannels(ctx context.Context, userID string) ([]permissions.AccessibleChannel, error) {
	if s.pool != nil {
		cached, err := cache.Read(ctx, s.pool, userID)
		if err != nil {
			return nil, err
		}
		if cached != nil {
			return cacheChannelsToPermissions(cached), nil
		}
		slog.Info("MCP channel cache miss; recomputing", "user_id", userID)
	}
	fresh, err := s.lister.ListChannels(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.pool != nil {
		out := make([]cache.Channel, len(fresh))
		for i, c := range fresh {
			out[i] = cache.Channel{
				ChannelID:    c.ChannelID,
				ChannelName:  c.ChannelName,
				CategoryID:   c.CategoryID,
				CategoryName: c.CategoryName,
				GuildID:      c.GuildID,
				GuildName:    c.GuildName,
			}
		}
		if err := cache.Write(ctx, s.pool, userID, out); err != nil {
			slog.Error("MCP cache write", "err", err, "user_id", userID)
		}
	}
	return fresh, nil
}

func cacheChannelsToPermissions(in []cache.Channel) []permissions.AccessibleChannel {
	out := make([]permissions.AccessibleChannel, len(in))
	for i, c := range in {
		out[i] = permissions.AccessibleChannel{
			ChannelID:    c.ChannelID,
			ChannelName:  c.ChannelName,
			CategoryID:   c.CategoryID,
			CategoryName: c.CategoryName,
			GuildID:      c.GuildID,
			GuildName:    c.GuildName,
		}
	}
	return out
}

// ── Access checks ────────────────────────────────────────────────────────

// channelRequired reports whether channel_id must be provided + accessible
// (search_messages / get_messages). guild-only tools (get_guild_events,
// list_channels) pass channelID = "" with required=false.
func checkAccess(channels []permissions.AccessibleChannel, guildID, channelID string, channelRequired bool) string {
	guildChannels := map[string]map[string]struct{}{}
	for _, c := range channels {
		m := guildChannels[c.GuildID]
		if m == nil {
			m = map[string]struct{}{}
			guildChannels[c.GuildID] = m
		}
		m[c.ChannelID] = struct{}{}
	}
	chans, ok := guildChannels[guildID]
	if !ok {
		return "접근 거부: 해당 서버에 접근 권한이 없습니다."
	}
	if channelRequired {
		if channelID == "" {
			return "접근 거부: 해당 채널에 접근 권한이 없습니다."
		}
		if _, ok := chans[channelID]; !ok {
			return "접근 거부: 해당 채널에 접근 권한이 없습니다."
		}
	}
	return ""
}

// ── Time filter ──────────────────────────────────────────────────────────

// parseISOToDBString mirrors web/mcp_router.py:_parse_iso_to_db_string.
// DB stores created_at/occurred_at as ISO 8601 text in UTC with +00:00
// offset; lexicographic comparison only matches chronological order if
// every value carries the same offset string. Naive datetimes are
// assumed UTC.
func parseISOToDBString(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	// Try a sequence of common ISO 8601 layouts; mirrors Python
	// datetime.fromisoformat which accepts both Z and +00:00 forms.
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000000",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	var t time.Time
	var err error
	for _, layout := range layouts {
		t, err = time.Parse(layout, s)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("잘못된 ISO 8601 datetime: %q", s)
	}
	t = t.UTC()
	// Truncate to microseconds: Python's isoformat() prints exactly 6
	// fractional digits when non-zero, so any sub-microsecond precision
	// would diverge from a Python-written row. Truncation (not rounding)
	// keeps lexicographic comparisons monotonic.
	t = t.Truncate(time.Microsecond)
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00"), nil
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00"), nil
}

// appendTimeFilter pushes since/until conditions and corresponding params
// onto the slice builder (migration plan §5 pattern).
func appendTimeFilter(conditions []string, params []any, column, since, until string) ([]string, []any) {
	if since != "" {
		params = append(params, since)
		conditions = append(conditions, fmt.Sprintf("%s >= $%d", column, len(params)))
	}
	if until != "" {
		params = append(params, until)
		conditions = append(conditions, fmt.Sprintf("%s <= $%d", column, len(params)))
	}
	return conditions, params
}

// likeEscapeMCP duplicates web.likeEscape — the LIKE escape rule is
// identical, but the import path would create a cycle so we keep a copy.
func likeEscapeMCP(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// ── search_messages ──────────────────────────────────────────────────────

type searchMessagesArgs struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Keyword   string `json:"keyword"`
	Limit     int    `json:"limit,omitempty"`
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
}

type messageRow struct {
	ChannelName string `json:"channel_name"`
	AuthorName  string `json:"author_name"`
	Content     string `json:"content"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) handleSearchMessages(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args searchMessagesArgs,
) (*mcpsdk.CallToolResult, any, error) {
	userID, ok := auth.UserIDFrom(ctx)
	if !ok {
		return nil, nil, errors.New("missing user_id")
	}
	channels, err := s.resolveChannels(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if msg := checkAccess(channels, args.GuildID, args.ChannelID, true); msg != "" {
		return textResult(msg), nil, nil
	}
	since, err := parseISOToDBString(args.Since)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	until, err := parseISOToDBString(args.Until)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	limit := clampInt(args.Limit, 100, 500)

	escaped := likeEscapeMCP(args.Keyword)
	params := []any{args.GuildID, args.ChannelID, "%" + escaped + "%"}
	conditions := []string{
		"guild_id = $1",
		"channel_id = $2",
		"lower(content) LIKE lower($3) ESCAPE '\\'",
	}
	conditions, params = appendTimeFilter(conditions, params, "created_at", since, until)
	params = append(params, limit)

	q := fmt.Sprintf(`
		SELECT channel_name, author_name, content, created_at
		  FROM messages
		 WHERE %s
		 ORDER BY created_at DESC LIMIT $%d
	`, strings.Join(conditions, " AND "), len(params))
	rows, err := s.queryMessages(ctx, q, params)
	if err != nil {
		return nil, nil, err
	}
	body, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(body)), nil, nil
}

// ── get_messages ─────────────────────────────────────────────────────────

type getMessagesArgs struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Limit     int    `json:"limit,omitempty"`
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
}

func (s *Server) handleGetMessages(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args getMessagesArgs,
) (*mcpsdk.CallToolResult, any, error) {
	userID, ok := auth.UserIDFrom(ctx)
	if !ok {
		return nil, nil, errors.New("missing user_id")
	}
	channels, err := s.resolveChannels(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if msg := checkAccess(channels, args.GuildID, args.ChannelID, true); msg != "" {
		return textResult(msg), nil, nil
	}
	since, err := parseISOToDBString(args.Since)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	until, err := parseISOToDBString(args.Until)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	limit := clampInt(args.Limit, 100, 500)

	params := []any{args.GuildID, args.ChannelID}
	conditions := []string{"guild_id = $1", "channel_id = $2"}
	conditions, params = appendTimeFilter(conditions, params, "created_at", since, until)
	params = append(params, limit)

	q := fmt.Sprintf(`
		SELECT channel_name, author_name, content, created_at
		  FROM messages
		 WHERE %s
		 ORDER BY created_at DESC LIMIT $%d
	`, strings.Join(conditions, " AND "), len(params))
	rows, err := s.queryMessages(ctx, q, params)
	if err != nil {
		return nil, nil, err
	}
	body, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(body)), nil, nil
}

// ── get_guild_events ─────────────────────────────────────────────────────

type getGuildEventsArgs struct {
	GuildID   string `json:"guild_id"`
	EventType string `json:"event_type,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
}

type guildEventRow struct {
	EventType  string `json:"event_type"`
	ActorName  string `json:"actor_name"`
	TargetName string `json:"target_name"`
	Details    any    `json:"details"`
	OccurredAt string `json:"occurred_at"`
}

func (s *Server) handleGetGuildEvents(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	args getGuildEventsArgs,
) (*mcpsdk.CallToolResult, any, error) {
	userID, ok := auth.UserIDFrom(ctx)
	if !ok {
		return nil, nil, errors.New("missing user_id")
	}
	channels, err := s.resolveChannels(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if msg := checkAccess(channels, args.GuildID, "", false); msg != "" {
		return textResult(msg), nil, nil
	}
	since, err := parseISOToDBString(args.Since)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	until, err := parseISOToDBString(args.Until)
	if err != nil {
		return textResult(err.Error()), nil, nil
	}
	limit := clampInt(args.Limit, 50, 200)

	params := []any{args.GuildID}
	conditions := []string{"guild_id = $1"}
	if args.EventType != "" {
		params = append(params, args.EventType)
		conditions = append(conditions, fmt.Sprintf("event_type = $%d", len(params)))
	}
	conditions, params = appendTimeFilter(conditions, params, "occurred_at", since, until)
	params = append(params, limit)

	q := fmt.Sprintf(`
		SELECT event_type, actor_name, target_name, details, occurred_at
		  FROM guild_events
		 WHERE %s
		 ORDER BY occurred_at DESC LIMIT $%d
	`, strings.Join(conditions, " AND "), len(params))

	rows, err := s.pool.Query(ctx, q, params...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]guildEventRow, 0, limit)
	for rows.Next() {
		var ev, an, tn, det, ts string
		if err := rows.Scan(&ev, &an, &tn, &det, &ts); err != nil {
			// Log + skip rather than aborting — a malformed row should
			// not fail the whole tool call, but it MUST not be invisible
			// either, since silent truncation breaks debugging.
			slog.Error("get_guild_events scan", "err", err)
			continue
		}
		out = append(out, guildEventRow{
			EventType:  ev,
			ActorName:  an,
			TargetName: tn,
			Details:    parseJSONDetails(det),
			OccurredAt: ts,
		})
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(body)), nil, nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func (s *Server) queryMessages(ctx context.Context, q string, params []any) ([]messageRow, error) {
	rows, err := s.pool.Query(ctx, q, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []messageRow
	for rows.Next() {
		var cn, an, content, ts string
		if err := rows.Scan(&cn, &an, &content, &ts); err != nil {
			// Log + skip — a single malformed row shouldn't fail the
			// whole tool call, but silent truncation hides debugging
			// signal for schema drift / null mismatches.
			slog.Error("queryMessages scan", "err", err)
			continue
		}
		out = append(out, messageRow{
			ChannelName: cn,
			AuthorName:  an,
			Content:     content,
			CreatedAt:   ts,
		})
	}
	if out == nil {
		out = []messageRow{}
	}
	return out, nil
}

func parseJSONDetails(raw string) any {
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func textResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
	}
}
