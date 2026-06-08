// Command server is the linkforge entrypoint: it loads config, runs migrations,
// wires every component via constructor injection, and manages a graceful
// lifecycle (drain in-flight requests, flush analytics, close pools).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shaurya703/linkforge/internal/analytics"
	"github.com/shaurya703/linkforge/internal/cache"
	"github.com/shaurya703/linkforge/internal/config"
	"github.com/shaurya703/linkforge/internal/observability"
	"github.com/shaurya703/linkforge/internal/ratelimit"
	"github.com/shaurya703/linkforge/internal/repository"
	"github.com/shaurya703/linkforge/internal/shortener"
	transport "github.com/shaurya703/linkforge/internal/transport/http"
	"github.com/shaurya703/linkforge/migrations"
)

func main() {
	log := observability.NewLogger(os.Getenv("LOG_LEVEL"))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Apply schema before anything touches the database.
	log.Info("running migrations")
	if err := migrations.Run(cfg.PostgresDSN); err != nil {
		return err
	}

	startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- infrastructure ---
	pool, err := repository.NewPool(startCtx, cfg.PostgresDSN, cfg.PostgresMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb, err := cache.NewClient(startCtx, cfg.RedisAddr, cfg.RedisDB)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	metrics := observability.NewMetrics()

	// --- components (constructor injection) ---
	repo := repository.New(pool)
	redisCache := cache.New(rdb, cfg.CacheTTL, cfg.CacheNegTTL, log, metrics.CacheHits, metrics.CacheMisses)
	limiter := ratelimit.New(rdb, cfg.RateLimitRate, cfg.RateLimitBurst)
	svc := shortener.NewService(repo, redisCache, shortener.Base62Generator{})

	pipeline := analytics.New(repo, analytics.Config{
		Buffer:        cfg.AnalyticsBuffer,
		Workers:       cfg.AnalyticsWorkers,
		BatchSize:     cfg.AnalyticsBatchSize,
		FlushInterval: cfg.AnalyticsFlushInterval,
	}, log, metrics.ClicksDropped, metrics.ClicksQueued)
	pipeline.Start()

	handlers := transport.NewHandlers(svc, repo, pipeline, cfg.BaseURL, log)
	router := transport.NewRouter(transport.RouterDeps{
		Handlers: handlers,
		Metrics:  metrics,
		Limiter:  limiter,
		Logger:   log,
		Ready: func(ctx context.Context) error {
			if perr := pool.Ping(ctx); perr != nil {
				return perr
			}
			return rdb.Ping(ctx).Err()
		},
	})

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// --- lifecycle ---
	serverErr := make(chan error, 1)
	go func() {
		log.Info("server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return err
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// Graceful shutdown: stop accepting, drain in-flight requests, then flush the
	// analytics buffer and let deferred closes release the pools.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown timed out", "err", err)
		_ = srv.Close()
	}
	log.Info("draining analytics pipeline")
	pipeline.Stop()
	log.Info("shutdown complete")
	return nil
}
