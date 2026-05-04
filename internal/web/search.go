package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lnyarl/discordlogbot/internal/cache"
	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// PageSize, RecentLimit, AllLimit mirror search.py constants.
const (
	PageSize    = 20
	RecentLimit = 1000
	AllLimit    = 100
)

// LatestMessages is the DISTINCT-ON subquery that picks the newest row per
// message_id (handles edits / soft deletes). Mirrors search.py's
// LATEST_MESSAGES exactly.
const LatestMessages = `
	(SELECT DISTINCT ON (message_id)
		message_id, guild_id, channel_id, channel_name,
		author_id, author_name, content, attachments, action, created_at
	FROM messages
	ORDER BY message_id, id DESC) AS m
`

// EventLabels is a 1:1 copy of search.py's EVENT_LABELS dict so the
// rendered HTML carries identical Korean labels.
var EventLabels = map[string]string{
	"member_join":                 "멤버 입장",
	"member_leave":                "멤버 퇴장",
	"member_ban":                  "멤버 차단",
	"member_unban":                "멤버 차단 해제",
	"member_update":               "멤버 변경",
	"channel_create":              "채널 생성",
	"channel_delete":              "채널 삭제",
	"channel_update":              "채널 변경",
	"guild_update":                "서버 설정 변경",
	"role_create":                 "역할 생성",
	"role_delete":                 "역할 삭제",
	"role_update":                 "역할 변경",
	"voice_join":                  "음성 입장",
	"voice_leave":                 "음성 퇴장",
	"voice_move":                  "음성 이동",
	"thread_create":               "스레드 생성",
	"thread_delete":               "스레드 삭제",
	"reaction_add":                "반응 추가",
	"reaction_remove":             "반응 제거",
	"invite_create":               "초대 생성",
	"invite_delete":               "초대 삭제",
	"emojis_update":               "이모지 변경",
	"channel_pins_update":         "메시지 고정",
	"message_pin":                 "메시지 고정",
	"message_unpin":               "메시지 고정 해제",
	"bulk_message_delete":         "메시지 일괄 삭제",
	"reaction_clear":              "반응 전체 제거",
	"reaction_clear_emoji":        "반응 이모지 제거",
	"thread_update":               "스레드 변경",
	"thread_member_join":          "스레드 멤버 입장",
	"thread_member_remove":        "스레드 멤버 퇴장",
	"stickers_update":             "스티커 변경",
	"user_update":                 "유저 프로필 변경",
	"guild_join":                  "봇 서버 추가",
	"guild_remove":                "봇 서버 제거",
	"voice_state_change":          "음성 상태 변경",
	"automod_rule_create":         "AutoMod 규칙 생성",
	"automod_rule_update":         "AutoMod 규칙 수정",
	"automod_rule_delete":         "AutoMod 규칙 삭제",
	"automod_action":              "AutoMod 실행",
	"audit_log":                   "감사 로그",
	"scheduled_event_create":      "예약 이벤트 생성",
	"scheduled_event_update":      "예약 이벤트 수정",
	"scheduled_event_delete":      "예약 이벤트 삭제",
	"scheduled_event_user_add":    "예약 이벤트 참가",
	"scheduled_event_user_remove": "예약 이벤트 참가 취소",
	"stage_instance_create":       "스테이지 시작",
	"stage_instance_update":       "스테이지 변경",
	"stage_instance_delete":       "스테이지 종료",
	"integrations_update":         "연동 변경",
	"integration_create":          "연동 추가",
	"integration_update":          "연동 수정",
	"integration_delete":          "연동 삭제",
	"webhooks_update":             "웹훅 변경",
}

// SearchHandler bundles the dependencies the search routes need.
type SearchHandler struct {
	Pool         *pgxpool.Pool
	Permissions  *permissions.Client
	BotInviteURL string
}

// SearchPage renders /search as the SPA shell. The page itself is
// JS-driven; the Go side only fills username + bot invite URL into the
// header template.
func (h *SearchHandler) SearchPage(tpl *Templates) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := SessionFrom(r.Context())
		tpl.Render(w, "search.html", PageData{
			Username:     s.Username,
			BotInviteURL: h.BotInviteURL,
		})
	})
}

// channelOut is the per-channel record /api/channels emits — same JSON
// shape as Python.
type channelOut struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	GuildID     string `json:"guild_id"`
	GuildName   string `json:"guild_name"`
}

// ChannelsAPI handles GET /api/channels.
func (h *SearchHandler) ChannelsAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := SessionFrom(r.Context())
		ctx := r.Context()
		accessible, err := h.accessibleChannelsForUser(ctx, s.UserID)
		if err != nil {
			slog.Error("accessibleChannelsForUser", "err", err, "user_id", s.UserID)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		if len(accessible) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"channels": []channelOut{}})
			return
		}
		ids := make([]string, 0, len(accessible))
		for _, c := range accessible {
			ids = append(ids, c.ChannelID)
		}
		rows, err := h.Pool.Query(ctx, `
			SELECT guild_id, channel_id, guild_name, channel_name
			  FROM log_channels
			 WHERE channel_id = ANY($1::text[])
		`, ids)
		if err != nil {
			slog.Error("log_channels query", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		defer rows.Close()
		out := make([]channelOut, 0, 16)
		for rows.Next() {
			var gid, cid, gn, cn string
			if err := rows.Scan(&gid, &cid, &gn, &cn); err != nil {
				continue
			}
			if cn == "" {
				cn = cid
			}
			if gn == "" {
				gn = gid
			}
			out = append(out, channelOut{ChannelID: cid, ChannelName: cn, GuildID: gid, GuildName: gn})
		}
		writeJSON(w, http.StatusOK, map[string]any{"channels": out})
	})
}

// accessibleChannelsForUser pulls the channel set the user has VIEW
// permission for. Cache hit → return; miss → recompute via Discord API +
// write back. Mirrors Python's get_or_compute_channels.
func (h *SearchHandler) accessibleChannelsForUser(ctx context.Context, userID string) ([]cache.Channel, error) {
	if h.Pool == nil {
		return nil, nil
	}
	cached, err := cache.Read(ctx, h.Pool, userID)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		return cached, nil
	}
	if h.Permissions == nil {
		return nil, nil
	}
	slog.Info("channel_access_cache miss; recomputing", "user_id", userID)
	channels, err := permissions.ComputeAccessibleChannels(ctx, h.Permissions, userID)
	if err != nil {
		return nil, err
	}
	out := make([]cache.Channel, len(channels))
	for i, c := range channels {
		out[i] = cache.Channel{
			ChannelID:    c.ChannelID,
			ChannelName:  c.ChannelName,
			CategoryID:   c.CategoryID,
			CategoryName: c.CategoryName,
			GuildID:      c.GuildID,
			GuildName:    c.GuildName,
		}
	}
	if err := cache.Write(ctx, h.Pool, userID, out); err != nil {
		slog.Error("cache write", "err", err, "user_id", userID)
	}
	return out, nil
}

// ── Search query types ────────────────────────────────────────────────

// messageRow is one normalized search hit (message kind).
type messageRow struct {
	Type        string             `json:"type"` // "message"
	MessageID   string             `json:"message_id"`
	GuildID     string             `json:"guild_id"`
	ChannelID   string             `json:"channel_id"`
	Action      string             `json:"action"`
	GuildName   string             `json:"guild_name"`
	ChannelName string             `json:"channel_name"`
	AuthorName  string             `json:"author_name"`
	Content     string             `json:"content"`
	Attachments []map[string]any   `json:"attachments"`
	CreatedAt   string             `json:"created_at"`
	Score       *float64           `json:"score"`
}

type eventRow struct {
	Type       string `json:"type"` // "event"
	EventType  string `json:"event_type"`
	EventLabel string `json:"event_label"`
	ActorName  string `json:"actor_name"`
	TargetName string `json:"target_name"`
	Details    any    `json:"details"`
	OccurredAt string `json:"occurred_at"`
}

type searchResponse struct {
	Results []any `json:"results"`
	Total   int   `json:"total"`
	Page    int   `json:"page"`
	Pages   int   `json:"pages"`
}

// SearchAPI handles GET /api/search. Heavy logic — kept inline to mirror
// Python's branching for readability, but each query is built with the
// dynamic conditions/params slice pattern called out in migration plan §5.
func (h *SearchHandler) SearchAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := SessionFrom(r.Context())
		ctx := r.Context()
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(q) > 200 {
			q = q[:200]
		}
		channelID := r.URL.Query().Get("channel_id")
		guildID := r.URL.Query().Get("guild_id")
		author := r.URL.Query().Get("author")
		page := parsePositiveInt(r.URL.Query().Get("page"), 1)
		includeEvents := r.URL.Query().Get("include_events") != ""

		empty := searchResponse{Results: []any{}, Total: 0, Page: page, Pages: 0}

		accessible, err := h.accessibleChannelsForUser(ctx, s.UserID)
		if err != nil {
			slog.Error("accessibleChannelsForUser", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		if len(accessible) == 0 {
			writeJSON(w, http.StatusOK, empty)
			return
		}
		viewIDs := make([]string, 0, len(accessible))
		for _, c := range accessible {
			viewIDs = append(viewIDs, c.ChannelID)
		}

		// Intersect with log_channels — only logged channels appear in messages.
		logRows, err := h.Pool.Query(ctx,
			"SELECT guild_id, channel_id FROM log_channels WHERE channel_id = ANY($1::text[])",
			viewIDs)
		if err != nil {
			slog.Error("log_channels intersect", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		guildMap := map[string]string{}
		for logRows.Next() {
			var gid, cid string
			if err := logRows.Scan(&gid, &cid); err == nil {
				guildMap[cid] = gid
			}
		}
		logRows.Close()
		if len(guildMap) == 0 {
			writeJSON(w, http.StatusOK, empty)
			return
		}

		// Filter selection: explicit channel → guild → all logged channels.
		var targetIDs []string
		if channelID != "" {
			if _, ok := guildMap[channelID]; !ok {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Forbidden"})
				return
			}
			targetIDs = []string{channelID}
		} else if guildID != "" {
			for cid, gid := range guildMap {
				if gid == guildID {
					targetIDs = append(targetIDs, cid)
				}
			}
			if len(targetIDs) == 0 {
				writeJSON(w, http.StatusOK, empty)
				return
			}
		} else {
			targetIDs = make([]string, 0, len(guildMap))
			for cid := range guildMap {
				targetIDs = append(targetIDs, cid)
			}
		}

		offset := (page - 1) * PageSize

		targetGuildSet := map[string]struct{}{}
		for _, cid := range targetIDs {
			if gid, ok := guildMap[cid]; ok {
				targetGuildSet[gid] = struct{}{}
			}
		}
		targetGuildIDs := make([]string, 0, len(targetGuildSet))
		for gid := range targetGuildSet {
			targetGuildIDs = append(targetGuildIDs, gid)
		}

		var resp searchResponse
		resp.Page = page

		if q == "" {
			h.searchEmptyQuery(ctx, w, &resp, targetIDs, channelID, guildMap, author, includeEvents, offset)
			return
		}
		h.searchWithQuery(ctx, w, &resp, targetIDs, targetGuildIDs, q, author, includeEvents, offset)
	})
}

// searchEmptyQuery handles the q=="" branch: most-recent messages for the
// channel set, optionally interleaved with guild_events for an explicit
// channel filter (the only branch where Python supports include_events
// without a query).
func (h *SearchHandler) searchEmptyQuery(
	ctx context.Context, w http.ResponseWriter, resp *searchResponse,
	targetIDs []string, channelID string, guildMap map[string]string,
	author string, includeEvents bool, offset int,
) {
	if includeEvents && channelID != "" {
		gid := guildMap[channelID]
		var total int
		err := h.Pool.QueryRow(ctx, fmt.Sprintf(`
			SELECT LEAST(cnt, $3) FROM (
				SELECT (
					SELECT COUNT(*) FROM %s WHERE channel_id = $1
				) + (
					SELECT COUNT(*) FROM guild_events
					WHERE guild_id = $2 AND details::jsonb->>'channel_id' = $1
				) AS cnt
			) sub
		`, LatestMessages), channelID, gid, RecentLimit).Scan(&total)
		if err != nil {
			slog.Error("count combined", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		rows, err := h.Pool.Query(ctx, fmt.Sprintf(`
			SELECT * FROM (
				SELECT
					'message' AS type,
					message_id, guild_id, channel_id, channel_name,
					author_name, content, attachments, created_at AS ts,
					action, NULL::text AS event_type, NULL::text AS target_name, NULL::text AS details
				FROM %s
				WHERE channel_id = $1
				UNION ALL
				SELECT
					'event' AS type,
					NULL, guild_id, NULL, NULL,
					actor_name, NULL, '[]', occurred_at AS ts,
					NULL, event_type, target_name, details
				FROM guild_events
				WHERE guild_id = $2
				  AND details::jsonb->>'channel_id' = $1
			) combined
			ORDER BY ts DESC
			LIMIT $3 OFFSET $4
		`, LatestMessages), channelID, gid, PageSize, offset)
		if err != nil {
			slog.Error("combined query", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
			return
		}
		defer rows.Close()
		results := []any{}
		for rows.Next() {
			var typ, mid, gid2, cid, cn, an, content, attach, ts, action string
			var et, tn, det *string
			if err := rows.Scan(&typ, &mid, &gid2, &cid, &cn, &an, &content, &attach, &ts, &action, &et, &tn, &det); err != nil {
				continue
			}
			if typ == "event" {
				results = append(results, eventRow{
					Type:       "event",
					EventType:  derefStr(et),
					EventLabel: labelFor(derefStr(et)),
					ActorName:  an,
					TargetName: derefStr(tn),
					Details:    parseDetails(derefStr(det)),
					OccurredAt: ts,
				})
			} else {
				results = append(results, messageRow{
					Type:        "message",
					MessageID:   mid,
					GuildID:     gid2,
					ChannelID:   cid,
					Action:      action,
					ChannelName: cn,
					AuthorName:  an,
					Content:     content,
					Attachments: parseAttachments(attach),
					CreatedAt:   ts,
				})
			}
		}
		resp.Results = results
		resp.Total = total
		if total > 0 {
			resp.Pages = int(math.Ceil(float64(total) / float64(PageSize)))
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// No combined branch — recent messages only, with optional author filter.
	conditions := []string{"channel_id = ANY($1::text[])"}
	params := []any{targetIDs}
	if author != "" {
		params = append(params, "%"+author+"%")
		conditions = append(conditions, fmt.Sprintf("author_name ILIKE $%d", len(params)))
	}
	whereClause := strings.Join(conditions, " AND ")

	limitParam := fmt.Sprintf("$%d", len(params)+1)
	limitArg := AllLimit
	var total int
	if err := h.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT LEAST(COUNT(*), %s) FROM %s WHERE %s", limitParam, LatestMessages, whereClause),
		append(append([]any{}, params...), limitArg)...).Scan(&total); err != nil {
		slog.Error("count recent", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}

	pageParam := fmt.Sprintf("$%d", len(params)+1)
	offsetParam := fmt.Sprintf("$%d", len(params)+2)
	rowsQuery := fmt.Sprintf(`
		SELECT message_id, guild_id, channel_id, channel_name,
		       author_name, content, attachments, action, created_at, 1.0::float AS score
		  FROM %s
		 WHERE %s
		 ORDER BY created_at DESC
		 LIMIT %s OFFSET %s
	`, LatestMessages, whereClause, pageParam, offsetParam)
	rows, err := h.Pool.Query(ctx, rowsQuery, append(append([]any{}, params...), PageSize, offset)...)
	if err != nil {
		slog.Error("rows recent", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	defer rows.Close()
	results := scanMessageRows(rows)
	resp.Results = make([]any, 0, len(results))
	for _, r := range results {
		resp.Results = append(resp.Results, r)
	}
	resp.Total = total
	if total > 0 {
		resp.Pages = int(math.Ceil(float64(total) / float64(PageSize)))
	}
	writeJSON(w, http.StatusOK, resp)
}

// searchWithQuery handles q != "" — trgm similarity for q≥3 chars,
// ESCAPE'd ILIKE otherwise. Optional author filter and event search.
func (h *SearchHandler) searchWithQuery(
	ctx context.Context, w http.ResponseWriter, resp *searchResponse,
	targetIDs []string, targetGuildIDs []string,
	q, author string, includeEvents bool, offset int,
) {
	useTrgm := len(q) >= 3
	escaped := likeEscape(q)
	likeQ := "%" + escaped + "%"

	conditions := []string{"channel_id = ANY($1::text[])"}
	params := []any{targetIDs}

	var matchSQL string
	if useTrgm {
		params = append(params, q)
		conditions = append(conditions, fmt.Sprintf("content %% $%d", len(params)))
		matchSQL = fmt.Sprintf("similarity(content, $%d) AS score", indexOf(params, q)+1)
	} else {
		params = append(params, likeQ)
		conditions = append(conditions, fmt.Sprintf("content ILIKE $%d ESCAPE '\\'", len(params)))
		matchSQL = "1.0::float AS score"
	}

	if author != "" {
		params = append(params, "%"+author+"%")
		conditions = append(conditions, fmt.Sprintf("author_name ILIKE $%d", len(params)))
	}

	whereClause := strings.Join(conditions, " AND ")

	// COUNT
	var total int
	if err := h.Pool.QueryRow(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", LatestMessages, whereClause),
		params...).Scan(&total); err != nil {
		slog.Error("count search", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}

	// rows
	pageParam := fmt.Sprintf("$%d", len(params)+1)
	offsetParam := fmt.Sprintf("$%d", len(params)+2)
	orderBy := "created_at DESC"
	if useTrgm {
		orderBy = "score DESC, created_at DESC"
	}
	rowsQuery := fmt.Sprintf(`
		SELECT message_id, guild_id, channel_id, channel_name,
		       author_name, content, attachments, action, created_at, %s
		  FROM %s
		 WHERE %s
		 ORDER BY %s
		 LIMIT %s OFFSET %s
	`, matchSQL, LatestMessages, whereClause, orderBy, pageParam, offsetParam)

	queryArgs := append(append([]any{}, params...), PageSize, offset)
	rows, err := h.Pool.Query(ctx, rowsQuery, queryArgs...)
	if err != nil {
		slog.Error("rows search", "err", err, "query", rowsQuery)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	defer rows.Close()
	results := scanMessageRows(rows)
	out := make([]any, 0, len(results))
	for _, r := range results {
		out = append(out, r)
	}

	// Optional events
	var eventResults []eventRow
	if includeEvents && len(targetGuildIDs) > 0 {
		erows, err := h.Pool.Query(ctx, `
			SELECT event_type, guild_id, actor_name, target_name, details, occurred_at
			  FROM guild_events
			 WHERE guild_id = ANY($1::text[])
			   AND details ILIKE $2 ESCAPE '\'
			 ORDER BY occurred_at DESC
			 LIMIT $3
		`, targetGuildIDs, likeQ, PageSize)
		if err != nil {
			slog.Error("events query", "err", err)
		} else {
			defer erows.Close()
			for erows.Next() {
				var et, gid string
				var an, tn, det, ts string
				if err := erows.Scan(&et, &gid, &an, &tn, &det, &ts); err == nil {
					eventResults = append(eventResults, eventRow{
						Type:       "event",
						EventType:  et,
						EventLabel: labelFor(et),
						ActorName:  an,
						TargetName: tn,
						Details:    parseDetails(det),
						OccurredAt: ts,
					})
				}
			}
		}
	}
	for _, er := range eventResults {
		out = append(out, er)
	}

	resp.Results = out
	resp.Total = total + len(eventResults)
	if resp.Total > 0 {
		resp.Pages = int(math.Ceil(float64(resp.Total) / float64(PageSize)))
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Helpers ──────────────────────────────────────────────────────────────

// likeEscape mirrors search.py:
//
//	q.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
//
// so user-supplied `_` and `%` don't act as ILIKE wildcards. We pair this
// with `ESCAPE '\'` at the call site.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func parsePositiveInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// indexOf returns the first index in params equal to v (used to find
// the placeholder number assigned to a value). Returns -1 if not found.
func indexOf(params []any, v any) int {
	for i, p := range params {
		if p == v {
			return i
		}
	}
	return -1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("json encode failed", "err", err)
	}
}

func labelFor(eventType string) string {
	if v, ok := EventLabels[eventType]; ok {
		return v
	}
	return eventType
}

func parseAttachments(raw string) []map[string]any {
	if raw == "" {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []map[string]any{}
	}
	return out
}

// parseDetails returns the JSON details payload as a generic value. Unlike
// attachments it can be either an object or a string fallback (Python's
// search.py emits it raw and the JS-side parses).
func parseDetails(raw string) any {
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		// Python returns the raw string when JSON parsing fails — preserve.
		return raw
	}
	return v
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// pgxRowsLike abstracts pgx.Rows for scanMessageRows so tests can feed a
// fake. We only need Next + Scan.
type pgxRowsLike interface {
	Next() bool
	Scan(dest ...any) error
}

func scanMessageRows(rows pgxRowsLike) []messageRow {
	out := make([]messageRow, 0, 16)
	for rows.Next() {
		var mid, gid, cid, cn, an, content, attach, action, ts string
		var score float64
		if err := rows.Scan(&mid, &gid, &cid, &cn, &an, &content, &attach, &action, &ts, &score); err != nil {
			continue
		}
		s := round3(score)
		out = append(out, messageRow{
			Type:        "message",
			MessageID:   mid,
			GuildID:     gid,
			ChannelID:   cid,
			Action:      action,
			ChannelName: cn,
			AuthorName:  an,
			Content:     content,
			Attachments: parseAttachments(attach),
			CreatedAt:   ts,
			Score:       &s,
		})
	}
	return out
}

func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}

// Suppress unused-helper warning on time package while keeping it for
// future use (timed-out request contexts).
var _ = time.Duration(0)
