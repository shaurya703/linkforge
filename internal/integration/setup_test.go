//go:build integration

// Package integration spins up real Postgres and Redis containers and exercises
// the full HTTP API end-to-end. Run with: go test -tags=integration ./internal/integration/...
package integration

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/shaurya703/linkforge/internal/analytics"
	"github.com/shaurya703/linkforge/internal/cache"
	"github.com/shaurya703/linkforge/internal/observability"
	"github.com/shaurya703/linkforge/internal/ratelimit"
	"github.com/shaurya703/linkforge/internal/repository"
	"github.com/shaurya703/linkforge/internal/shortener"
	transport "github.com/shaurya703/linkforge/internal/transport/http"
	"github.com/shaurya703/linkforge/migrations"
)

var (
	pgDSN     string
	redisAddr string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("linkforge"),
		tcpostgres.WithUsername("linkforge"),
		tcpostgres.WithPassword("linkforge"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		panic("start postgres: " + err.Error())
	}
	pgDSN, err = pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(err)
	}

	rC, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		panic("start redis: " + err.Error())
	}
	redisAddr, err = rC.Endpoint(ctx, "")
	if err != nil {
		panic(err)
	}

	if err := migrations.Run(pgDSN); err != nil {
		panic("migrate: " + err.Error())
	}

	code := m.Run()

	_ = pgC.Terminate(ctx)
	_ = rC.Terminate(ctx)
	os.Exit(code)
}

// newServer builds a full API server backed by the shared containers, with the
// given rate-limit parameters. Everything is cleaned up when the test ends.
func newServer(t *testing.T, rate float64, burst int) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	log := observability.NewLogger("error")

	pool, err := repository.NewPool(ctx, pgDSN, 10)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	// Isolate cache/rate-limit state between tests.
	rdb.FlushDB(ctx)

	metrics := observability.NewMetrics()
	repo := repository.New(pool)
	redisCache := cache.New(rdb, time.Hour, 30*time.Second, log, metrics.CacheHits, metrics.CacheMisses)
	limiter := ratelimit.New(rdb, rate, burst)
	svc := shortener.NewService(repo, redisCache, shortener.Base62Generator{})

	pipe := analytics.New(repo, analytics.Config{
		Buffer: 1000, Workers: 2, BatchSize: 5, FlushInterval: 150 * time.Millisecond,
	}, log, metrics.ClicksDropped, metrics.ClicksQueued)
	pipe.Start()
	t.Cleanup(pipe.Stop)

	router := transport.NewRouter(transport.RouterDeps{
		Handlers: transport.NewHandlers(svc, repo, pipe, "http://short.test", log),
		Metrics:  metrics,
		Limiter:  limiter,
		Logger:   log,
		Ready:    func(ctx context.Context) error { return pool.Ping(ctx) },
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}
