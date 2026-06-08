// Package config loads runtime configuration from environment variables with
// sensible defaults, so the binary runs with zero config in development and is
// fully tunable in production.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all tunables for the service. It is loaded once at startup and
// passed explicitly to the components that need it (no global state).
type Config struct {
	HTTPAddr        string
	BaseURL         string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration

	PostgresDSN      string
	PostgresMaxConns int32

	RedisAddr   string
	RedisDB     int
	CacheTTL    time.Duration
	CacheNegTTL time.Duration

	RateLimitRate  float64
	RateLimitBurst int

	AnalyticsBuffer        int
	AnalyticsWorkers       int
	AnalyticsBatchSize     int
	AnalyticsFlushInterval time.Duration
}

// Load reads configuration from the environment, applying defaults for any unset
// values. It returns an error only when a present value is malformed.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		BaseURL:         env("BASE_URL", "http://localhost:8080"),
		ReadTimeout:     envDur("READ_TIMEOUT", 5*time.Second),
		WriteTimeout:    envDur("WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:     envDur("IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout: envDur("SHUTDOWN_TIMEOUT", 15*time.Second),

		PostgresDSN:      env("POSTGRES_DSN", "postgres://linkforge:linkforge@localhost:5432/linkforge?sslmode=disable"),
		PostgresMaxConns: int32(envInt("POSTGRES_MAX_CONNS", 20)),

		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		RedisDB:     envInt("REDIS_DB", 0),
		CacheTTL:    envDur("CACHE_TTL", time.Hour),
		CacheNegTTL: envDur("CACHE_NEGATIVE_TTL", 30*time.Second),

		RateLimitRate:  envFloat("RATELIMIT_RATE", 10),
		RateLimitBurst: envInt("RATELIMIT_BURST", 20),

		AnalyticsBuffer:        envInt("ANALYTICS_BUFFER", 10000),
		AnalyticsWorkers:       envInt("ANALYTICS_WORKERS", 4),
		AnalyticsBatchSize:     envInt("ANALYTICS_BATCH_SIZE", 100),
		AnalyticsFlushInterval: envDur("ANALYTICS_FLUSH_INTERVAL", 2*time.Second),
	}
	if c.RateLimitBurst <= 0 || c.RateLimitRate <= 0 {
		return Config{}, fmt.Errorf("rate limit rate and burst must be positive")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
