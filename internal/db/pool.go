package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool initializes a pgxpool with a Ping check.
// Phase 7 will extend AfterConnect with pgvector.RegisterTypes.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
