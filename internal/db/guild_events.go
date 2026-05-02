package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GuildEventInput mirrors PostgreSQLDatabase.save_guild_event's keyword args.
type GuildEventInput struct {
	EventType  string
	GuildID    string
	ActorID    string // "" → NULL
	ActorName  string // "" → NULL
	TargetID   string // "" → NULL
	TargetName string // "" → NULL
	Details    map[string]any
	OccurredAt time.Time
}

// nullable converts "" to nil so pgx writes SQL NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SaveGuildEvent inserts a single row into guild_events.
func SaveGuildEvent(ctx context.Context, pool *pgxpool.Pool, in GuildEventInput) error {
	if in.Details == nil {
		in.Details = map[string]any{}
	}
	details, err := json.Marshal(in.Details)
	if err != nil {
		return fmt.Errorf("marshal details: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO guild_events
			(event_type, guild_id, actor_id, actor_name, target_id, target_name, details, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		in.EventType, in.GuildID,
		nullable(in.ActorID), nullable(in.ActorName),
		nullable(in.TargetID), nullable(in.TargetName),
		string(details), formatTime(in.OccurredAt),
	)
	return err
}
