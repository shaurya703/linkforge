// Package repository implements persistence with Postgres (via pgx). It exposes a
// concrete *LinkRepository whose methods satisfy the interfaces declared by the
// service and analytics layers, keeping infrastructure details out of the domain.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shaurya703/linkforge/internal/domain"
)

// LinkRepository is the Postgres-backed store for links and clicks.
type LinkRepository struct {
	pool *pgxpool.Pool
}

// NewPool opens a pgx connection pool with the given DSN and max connections.
func NewPool(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// New returns a LinkRepository over an existing pool.
func New(pool *pgxpool.Pool) *LinkRepository { return &LinkRepository{pool: pool} }

// NextID reserves the next sequence value used to derive an auto-generated code.
func (r *LinkRepository) NextID(ctx context.Context) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT nextval('links_id_seq')`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("nextval: %w", err)
	}
	return id, nil
}

const insertReturning = `created_at, click_count`

// InsertAuto inserts an auto-generated link, deduplicating non-expiring URLs via
// the partial unique index. Returns inserted=false (no error) when a concurrent
// request already created a row for the same URL, so the caller can fetch it.
func (r *LinkRepository) InsertAuto(ctx context.Context, l domain.Link, urlHash []byte) (inserted bool, err error) {
	var createdAt time.Time
	var clicks int64
	err = r.pool.QueryRow(ctx, `
		INSERT INTO links (id, code, long_url, url_hash, custom, expires_at)
		VALUES ($1, $2, $3, $4, FALSE, $5)
		ON CONFLICT (url_hash) WHERE custom = FALSE AND expires_at IS NULL
		DO NOTHING
		RETURNING `+insertReturning,
		l.ID, l.Code, l.LongURL, urlHash, l.ExpiresAt,
	).Scan(&createdAt, &clicks)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // dedupe conflict; caller re-reads the existing row
	}
	if err != nil {
		return false, fmt.Errorf("insert auto: %w", err)
	}
	return true, nil
}

// InsertCustom inserts a user-supplied alias. Returns domain.ErrAliasTaken if the
// code already exists.
func (r *LinkRepository) InsertCustom(ctx context.Context, l domain.Link, urlHash []byte) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO links (id, code, long_url, url_hash, custom, expires_at)
		VALUES ($1, $2, $3, $4, TRUE, $5)`,
		l.ID, l.Code, l.LongURL, urlHash, l.ExpiresAt,
	)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return domain.ErrAliasTaken
	}
	if err != nil {
		return fmt.Errorf("insert custom: %w", err)
	}
	return nil
}

// FindDuplicate returns the existing auto, non-expiring link for a URL hash.
func (r *LinkRepository) FindDuplicate(ctx context.Context, urlHash []byte) (domain.Link, bool, error) {
	l, err := r.scanOne(ctx, `
		SELECT id, code, long_url, custom, created_at, expires_at
		FROM links
		WHERE url_hash = $1 AND custom = FALSE AND expires_at IS NULL`, urlHash)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Link{}, false, nil
	}
	if err != nil {
		return domain.Link{}, false, err
	}
	return l, true, nil
}

// GetByCode loads a link by its short code. Returns domain.ErrNotFound if absent.
func (r *LinkRepository) GetByCode(ctx context.Context, code string) (domain.Link, error) {
	return r.scanOne(ctx, `
		SELECT id, code, long_url, custom, created_at, expires_at
		FROM links WHERE code = $1`, code)
}

func (r *LinkRepository) scanOne(ctx context.Context, query string, args ...any) (domain.Link, error) {
	var l domain.Link
	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&l.ID, &l.Code, &l.LongURL, &l.Custom, &l.CreatedAt, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Link{}, fmt.Errorf("query link: %w", err)
	}
	return l, nil
}

// Stats returns the click count and metadata for a code (used by an analytics
// read endpoint).
func (r *LinkRepository) Stats(ctx context.Context, code string) (domain.Link, int64, error) {
	var l domain.Link
	var clicks int64
	err := r.pool.QueryRow(ctx, `
		SELECT id, code, long_url, custom, created_at, expires_at, click_count
		FROM links WHERE code = $1`, code).Scan(
		&l.ID, &l.Code, &l.LongURL, &l.Custom, &l.CreatedAt, &l.ExpiresAt, &clicks)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, 0, domain.ErrNotFound
	}
	if err != nil {
		return domain.Link{}, 0, fmt.Errorf("query stats: %w", err)
	}
	return l, clicks, nil
}
