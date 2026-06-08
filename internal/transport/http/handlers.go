package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shaurya703/linkforge/internal/domain"
	"github.com/shaurya703/linkforge/internal/shortener"
)

// Service is the business-logic dependency of the handlers (the shortener service).
type Service interface {
	Shorten(ctx context.Context, in shortener.ShortenInput) (domain.Link, error)
	Resolve(ctx context.Context, code string) (domain.Link, error)
}

// StatsProvider exposes per-link click stats.
type StatsProvider interface {
	Stats(ctx context.Context, code string) (domain.Link, int64, error)
}

// Enqueuer receives click events for async processing.
type Enqueuer interface {
	Enqueue(e domain.ClickEvent)
}

// Handlers holds the HTTP handler dependencies.
type Handlers struct {
	svc     Service
	stats   StatsProvider
	clicks  Enqueuer
	baseURL string
	log     *slog.Logger
}

// NewHandlers constructs the handler set.
func NewHandlers(svc Service, stats StatsProvider, clicks Enqueuer, baseURL string, log *slog.Logger) *Handlers {
	return &Handlers{svc: svc, stats: stats, clicks: clicks, baseURL: strings.TrimRight(baseURL, "/"), log: log}
}

type shortenRequest struct {
	URL        string `json:"url"`
	Alias      string `json:"alias,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

type shortenResponse struct {
	Code      string     `json:"code"`
	ShortURL  string     `json:"short_url"`
	LongURL   string     `json:"long_url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Shorten handles POST /shorten.
func (h *Handlers) Shorten(w http.ResponseWriter, r *http.Request) {
	var req shortenRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)) // cap body at 1MB
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "request body must be valid JSON with a 'url' field")
		return
	}

	in := shortener.ShortenInput{URL: req.URL, Alias: req.Alias}
	if req.TTLSeconds > 0 {
		ttl := time.Duration(req.TTLSeconds) * time.Second
		in.TTL = &ttl
	}

	link, err := h.svc.Shorten(r.Context(), in)
	if err != nil {
		writeDomainError(w, h.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, shortenResponse{
		Code:      link.Code,
		ShortURL:  h.baseURL + "/" + link.Code,
		LongURL:   link.LongURL,
		ExpiresAt: link.ExpiresAt,
	})
}

// Redirect handles GET /{code} — the hot path. It resolves via cache-aside, fires
// a non-blocking analytics event, and 302-redirects.
func (h *Handlers) Redirect(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if code == "" {
		writeError(w, http.StatusNotFound, "not_found", "no link found for that code")
		return
	}

	link, err := h.svc.Resolve(r.Context(), code)
	if err != nil {
		writeDomainError(w, h.log, err)
		return
	}

	// Hand off analytics without blocking the redirect.
	h.clicks.Enqueue(domain.ClickEvent{
		Code:      code,
		Referrer:  r.Referer(),
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
		Timestamp: time.Now().UTC(),
	})

	// 302 (not 301) so analytics see every click and we keep control of the mapping.
	http.Redirect(w, r, link.LongURL, http.StatusFound)
}

type statsResponse struct {
	Code      string     `json:"code"`
	LongURL   string     `json:"long_url"`
	Clicks    int64      `json:"clicks"`
	Custom    bool       `json:"custom"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Stats handles GET /stats/{code}.
func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	link, clicks, err := h.stats.Stats(r.Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeDomainError(w, h.log, err)
			return
		}
		writeDomainError(w, h.log, err)
		return
	}
	writeJSON(w, http.StatusOK, statsResponse{
		Code:      link.Code,
		LongURL:   link.LongURL,
		Clicks:    clicks,
		Custom:    link.Custom,
		CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt,
	})
}

// Healthz is a liveness probe.
func (h *Handlers) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clientIP extracts the best-guess client IP, honoring X-Forwarded-For when present.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
