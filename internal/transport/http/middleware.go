package http

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shaurya703/linkforge/internal/observability"
	"github.com/shaurya703/linkforge/internal/ratelimit"
)

// statusRecorder captures the status code and bytes written for logging/metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// RequestLogger emits one structured JSON line per request.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", float64(time.Since(start).Microseconds())/1000,
				"ip", clientIP(r),
			)
		})
	}
}

// Metrics records request counts and latency, labelled by the chi route pattern
// (bounded cardinality — "/{code}", not the millions of distinct codes).
func Metrics(m *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			m.HTTPDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
			m.HTTPRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		})
	}
}

// Recoverer turns a panic into a 500 JSON response instead of crashing the server.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "err", rec, "path", r.URL.Path)
					writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit applies the token-bucket limiter keyed by client IP. On a limiter
// (Redis) error it fails open — a limiter outage must not take down the endpoint.
func RateLimit(limiter *ratelimit.Limiter, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, retry, err := limiter.Allow(r.Context(), clientIP(r))
			if err != nil {
				log.Warn("rate limiter error; failing open", "err", err)
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				secs := int(retry.Seconds())
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ReadyFunc reports service readiness (dependencies reachable).
type ReadyFunc func(ctx context.Context) error
