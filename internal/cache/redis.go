// Package cache implements the redirect-path read cache over Redis. It realises a
// cache-aside pattern: the service asks the cache first and falls back to Postgres
// on a miss, then populates the cache. All writes here are best-effort — a Redis
// failure degrades to a database hit, never an error to the client.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shaurya703/linkforge/internal/domain"
)

const (
	keyPrefix      = "lf:link:"
	negativeMarker = "\x00nf" // sentinel value marking a known-missing code
)

// Redis is a go-redis-backed implementation of shortener.Cache.
type Redis struct {
	client *redis.Client
	ttl    time.Duration
	negTTL time.Duration
	log    *slog.Logger

	hits, misses interface{ Inc() } // prometheus counters (decoupled via tiny interface)
}

// NewClient builds a go-redis client and verifies connectivity. When url is
// non-empty it is parsed as a full connection URL (redis:// or rediss://),
// carrying host, password, db, and TLS — the form managed providers hand out.
// Otherwise it falls back to a plain host:port addr with the given db (local dev
// and docker-compose).
func NewClient(ctx context.Context, url, addr string, db int) (*redis.Client, error) {
	var opts *redis.Options
	if url != "" {
		parsed, err := redis.ParseURL(url)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		opts = parsed
	} else {
		opts = &redis.Options{Addr: addr, DB: db}
	}
	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// New constructs the cache. hits/misses are Prometheus counters (anything with Inc()).
func New(client *redis.Client, ttl, negTTL time.Duration, log *slog.Logger, hits, misses interface{ Inc() }) *Redis {
	return &Redis{client: client, ttl: ttl, negTTL: negTTL, log: log, hits: hits, misses: misses}
}

func key(code string) string { return keyPrefix + code }

// Lookup checks Redis. hit=false means a cache miss (caller should hit the DB);
// hit=true with negative=true means the code is cached as known-missing.
func (r *Redis) Lookup(ctx context.Context, code string) (domain.Link, bool, bool) {
	val, err := r.client.Get(ctx, key(code)).Result()
	if errors.Is(err, redis.Nil) {
		r.misses.Inc()
		return domain.Link{}, false, false
	}
	if err != nil {
		// Treat any Redis error as a miss so the request still succeeds via DB.
		r.log.Warn("cache lookup failed", "code", code, "err", err)
		r.misses.Inc()
		return domain.Link{}, false, false
	}
	r.hits.Inc()
	if val == negativeMarker {
		return domain.Link{}, true, true
	}
	var l domain.Link
	if uerr := json.Unmarshal([]byte(val), &l); uerr != nil {
		r.log.Warn("cache decode failed; treating as miss", "code", code, "err", uerr)
		r.misses.Inc()
		return domain.Link{}, false, false
	}
	return l, true, false
}

// Store caches a resolved link, capping the TTL so a cache entry never outlives
// the link's own expiry.
func (r *Redis) Store(ctx context.Context, l domain.Link) {
	ttl := r.ttl
	if l.ExpiresAt != nil {
		remaining := time.Until(*l.ExpiresAt)
		if remaining <= 0 {
			return // already expired; nothing to cache
		}
		if remaining < ttl {
			ttl = remaining
		}
	}
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	if err := r.client.Set(ctx, key(l.Code), b, ttl).Err(); err != nil {
		r.log.Warn("cache store failed", "code", l.Code, "err", err)
	}
}

// StoreNegative caches a known-missing code for a short TTL to absorb floods of
// lookups for nonexistent codes (cache-penetration defense).
func (r *Redis) StoreNegative(ctx context.Context, code string) {
	if err := r.client.Set(ctx, key(code), negativeMarker, r.negTTL).Err(); err != nil {
		r.log.Warn("cache store-negative failed", "code", code, "err", err)
	}
}

// Invalidate removes a code from the cache (on update/delete/expiry).
func (r *Redis) Invalidate(ctx context.Context, code string) {
	if err := r.client.Del(ctx, key(code)).Err(); err != nil {
		r.log.Warn("cache invalidate failed", "code", code, "err", err)
	}
}
