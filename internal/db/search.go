package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SearchResult is the row shape /logbot search returns to the user.
type SearchResult struct {
	MessageID   string `json:"message_id"`
	ChannelName string `json:"channel_name"`
	AuthorName  string `json:"author_name"`
	Content     string `json:"content"`
	CreatedAt   string `json:"created_at"`
}

// escapeLike replicates Python's escape: backslash → backslash backslash,
// then % and _ become literals via the LIKE ESCAPE clause.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// SearchMessages performs a case-insensitive substring search over the
// guild's message content (no pg_trgm — that's web/search.go's domain).
func SearchMessages(
	ctx context.Context, pool *pgxpool.Pool, guildID, keyword string, limit int,
) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 200
	}
	pattern := "%" + escapeLike(keyword) + "%"
	rows, err := pool.Query(ctx, `
		SELECT message_id, channel_name, author_name, content, created_at
		FROM messages
		WHERE guild_id = $1 AND lower(content) LIKE lower($2) ESCAPE '\'
		ORDER BY created_at DESC LIMIT $3
	`, guildID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.MessageID, &r.ChannelName, &r.AuthorName, &r.Content, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
