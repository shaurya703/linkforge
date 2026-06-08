package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shaurya703/linkforge/internal/domain"
)

// InsertClicks bulk-inserts click detail rows using Postgres COPY — far cheaper
// than N round-trips. Called by the analytics workers with a whole batch at once.
func (r *LinkRepository) InsertClicks(ctx context.Context, events []domain.ClickEvent) error {
	if len(events) == 0 {
		return nil
	}
	rows := make([][]any, len(events))
	for i, e := range events {
		rows[i] = []any{e.Code, e.Referrer, e.UserAgent, e.IP, e.Timestamp}
	}
	_, err := r.pool.CopyFrom(ctx,
		pgx.Identifier{"clicks"},
		[]string{"link_code", "referrer", "user_agent", "ip", "clicked_at"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy clicks: %w", err)
	}
	return nil
}

// IncrementClickCounts applies aggregated per-code increments to links.click_count
// in a single pipelined batch, giving O(1) reads of total clicks per link.
func (r *LinkRepository) IncrementClickCounts(ctx context.Context, counts map[string]int64) error {
	if len(counts) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for code, n := range counts {
		batch.Queue(`UPDATE links SET click_count = click_count + $1 WHERE code = $2`, n, code)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range counts {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("increment click counts: %w", err)
		}
	}
	return nil
}
