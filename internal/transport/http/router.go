package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shaurya703/linkforge/internal/observability"
	"github.com/shaurya703/linkforge/internal/ratelimit"
)

// RouterDeps are the dependencies needed to assemble the HTTP router.
type RouterDeps struct {
	Handlers *Handlers
	Metrics  *observability.Metrics
	Limiter  *ratelimit.Limiter
	Logger   *slog.Logger
	Ready    ReadyFunc
}

// NewRouter assembles the chi router with the full middleware stack and routes.
func NewRouter(d RouterDeps) http.Handler {
	r := chi.NewRouter()

	// Global middleware: recover -> log -> metrics (outermost first).
	r.Use(Recoverer(d.Logger))
	r.Use(RequestLogger(d.Logger))
	r.Use(Metrics(d.Metrics))

	// Landing page (self-contained embedded UI). Registered before the /{code}
	// catch-all; chi matches the exact root "/" here and single segments below.
	r.Get("/", d.Handlers.Index)

	// Operational endpoints (no rate limit).
	r.Get("/healthz", d.Handlers.Healthz)
	r.Get("/readyz", readyHandler(d.Ready))
	r.Handle("/metrics", d.Metrics.Handler())

	// Write path: rate-limited.
	r.Group(func(r chi.Router) {
		r.Use(RateLimit(d.Limiter, d.Logger))
		r.Post("/shorten", d.Handlers.Shorten)
	})

	// Read paths.
	r.Get("/stats/{code}", d.Handlers.Stats)
	r.Get("/{code}", d.Handlers.Redirect) // hot path; registered last as a catch-all

	return r
}

func readyHandler(ready ReadyFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
