package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// Config bundles every tunable the worker reads from the environment.
// Mirrors the env keys in workers/embedding_worker.py.
type Config struct {
	OllamaHost     string
	OllamaModel    string
	BatchSize      int
	Concurrency    int
	PollInterval   time.Duration
	MaxTokens      int
	ContextBefore  int
	ContextAfter   int
	MaxGap         time.Duration
}

// DefaultConfig returns the same defaults the Python worker uses.
func DefaultConfig() Config {
	return Config{
		OllamaHost:    "http://localhost:11434",
		OllamaModel:   "bge-m3",
		BatchSize:     32,
		Concurrency:   4,
		PollInterval:  10 * time.Second,
		MaxTokens:     8000,
		ContextBefore: 10,
		ContextAfter:  5,
		MaxGap:        2 * time.Hour,
	}
}

// MessageRow is one batch row: the message that needs embedding.
type MessageRow struct {
	ID         int64
	ChannelID  string
	AuthorName string
	Content    string
	CreatedAt  string
}

// Run is the worker's main loop. Cancel ctx to stop. Each iteration:
//
//  1. Begin a transaction with FOR UPDATE SKIP LOCKED on the candidate
//     batch, so multiple workers (e.g. Python+Go during Phase 8 cutover)
//     don't double-process the same rows.
//  2. For each row, fetch chronological neighbours, build the context
//     text, call Ollama in parallel up to Concurrency.
//  3. UPDATE every row's embedding inside the same transaction → COMMIT.
//
// Empty batches drain into PollInterval sleeps (gateway-style backoff).
func Run(ctx context.Context, pool *pgxpool.Pool, ollama *OllamaClient, cfg Config) error {
	remaining, err := countRemaining(ctx, pool)
	if err != nil {
		return fmt.Errorf("initial count: %w", err)
	}
	slog.Info("embedding worker starting", "model", cfg.OllamaModel, "remaining", remaining)

	totalProcessed := 0
	sem := semaphore.NewWeighted(int64(cfg.Concurrency))

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		processed, err := processBatch(ctx, pool, ollama, cfg, sem)
		if err != nil {
			slog.Error("batch failed", "err", err)
			// Pause briefly so we don't hammer the DB on a hard error.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.PollInterval):
			}
			continue
		}
		if processed == 0 {
			slog.Info("queue empty, sleeping", "interval", cfg.PollInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.PollInterval):
			}
			continue
		}
		totalProcessed += processed
		slog.Info("batch complete", "processed", processed, "total", totalProcessed)
	}
}

// processBatch runs one fetch→embed→save cycle inside a single transaction.
// Returns the number of rows updated (0 = empty queue).
func processBatch(
	ctx context.Context,
	pool *pgxpool.Pool,
	ollama *OllamaClient,
	cfg Config,
	sem *semaphore.Weighted,
) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	// Rollback uses an uncancellable context so a SIGTERM mid-batch can
	// still clean up the row locks instead of leaving the tx open. Same
	// reasoning applies to the Commit below — the actual happy-path
	// commit must NOT inherit the cancelled signal context.
	defer func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(bg)
	}()

	rows, err := tx.Query(ctx, `
		SELECT id, channel_id, author_name, content, created_at
		  FROM messages
		 WHERE embedding IS NULL
		   AND action != 'delete'
		   AND length(content) >= 1
		 ORDER BY id DESC
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED
	`, cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("fetch_batch: %w", err)
	}
	var batch []MessageRow
	for rows.Next() {
		var r MessageRow
		if err := rows.Scan(&r.ID, &r.ChannelID, &r.AuthorName, &r.Content, &r.CreatedAt); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if len(batch) == 0 {
		return 0, tx.Commit(ctx) // commit the (empty) read tx so locks release
	}

	// Build prompts SEQUENTIALLY — pgx.Tx is documented as not
	// goroutine-safe, so concurrent tx.Query() calls would race the
	// underlying connection. Python's worker also ran fetch_context in
	// a sequential `for` loop. Per-row context queries are cheap; the
	// real parallelism win is in the Ollama call below.
	prompts := make([]string, len(batch))
	for i := range batch {
		before, after, err := fetchContext(ctx, tx, batch[i].ChannelID, batch[i].CreatedAt, cfg)
		if err != nil {
			return 0, fmt.Errorf("fetch_context: %w", err)
		}
		prompts[i] = BuildContextText(
			ContextMessage{
				AuthorName: batch[i].AuthorName,
				Content:    batch[i].Content,
				CreatedAt:  batch[i].CreatedAt,
			},
			before, after, cfg.MaxTokens,
		)
	}

	// Call Ollama in parallel up to cfg.Concurrency. The semaphore caps
	// inflight requests so a flaky local Ollama doesn't get stampeded.
	t0 := time.Now()
	embeddings := make([][]float32, len(batch))
	g2, gctx2 := errgroup.WithContext(ctx)
	for i := range batch {
		i := i
		g2.Go(func() error {
			if err := sem.Acquire(gctx2, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			emb, err := ollama.Embed(gctx2, prompts[i])
			if err != nil {
				return fmt.Errorf("embed id=%d: %w", batch[i].ID, err)
			}
			embeddings[i] = emb
			return nil
		})
	}
	if err := g2.Wait(); err != nil {
		return 0, err
	}
	elapsed := time.Since(t0)

	// Save inside the same transaction so the FOR UPDATE locks aren't
	// released until the embedding is committed. Use a commit context
	// that ignores ctx cancellation — once the embeddings are computed,
	// rolling back on SIGTERM would silently re-queue the batch on the
	// next worker restart, wasting the Ollama call we already paid for.
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for i, row := range batch {
		_, err := tx.Exec(commitCtx,
			"UPDATE messages SET embedding = $1 WHERE id = $2",
			pgvector.NewVector(embeddings[i]), row.ID,
		)
		if err != nil {
			return 0, fmt.Errorf("update id=%d: %w", row.ID, err)
		}
	}
	if err := tx.Commit(commitCtx); err != nil {
		return 0, err
	}

	if elapsed > 0 {
		slog.Info("ollama batch", "n", len(batch), "elapsed", elapsed,
			"per_sec", float64(len(batch))/elapsed.Seconds())
	}
	return len(batch), nil
}

// fetchContext loads up to ContextBefore previous + ContextAfter following
// messages in the same channel, filtered by the same action!='delete'
// + non-empty content predicate the batch query uses. Then trim_by_gap
// is applied to drop neighbours separated by a long silence.
func fetchContext(
	ctx context.Context, tx pgx.Tx,
	channelID, createdAt string, cfg Config,
) ([]ContextMessage, []ContextMessage, error) {
	beforeRows, err := tx.Query(ctx, `
		SELECT author_name, content, created_at
		  FROM messages
		 WHERE channel_id = $1 AND created_at < $2
		   AND action != 'delete' AND length(content) >= 1
		 ORDER BY created_at DESC LIMIT $3
	`, channelID, createdAt, cfg.ContextBefore)
	if err != nil {
		return nil, nil, err
	}
	before, err := scanContextRows(beforeRows)
	beforeRows.Close()
	if err != nil {
		return nil, nil, err
	}

	afterRows, err := tx.Query(ctx, `
		SELECT author_name, content, created_at
		  FROM messages
		 WHERE channel_id = $1 AND created_at > $2
		   AND action != 'delete' AND length(content) >= 1
		 ORDER BY created_at ASC LIMIT $3
	`, channelID, createdAt, cfg.ContextAfter)
	if err != nil {
		return nil, nil, err
	}
	after, err := scanContextRows(afterRows)
	afterRows.Close()
	if err != nil {
		return nil, nil, err
	}

	anchor, err := ParseDT(createdAt)
	if err != nil {
		return nil, nil, err
	}
	return TrimByGap(before, anchor, cfg.MaxGap), TrimByGap(after, anchor, cfg.MaxGap), nil
}

func scanContextRows(rows pgx.Rows) ([]ContextMessage, error) {
	var out []ContextMessage
	for rows.Next() {
		var m ContextMessage
		if err := rows.Scan(&m.AuthorName, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func countRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM messages WHERE embedding IS NULL AND action != 'delete'").
		Scan(&n)
	return n, err
}

