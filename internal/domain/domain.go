// Package domain holds the core types and errors shared across layers. It has no
// dependencies on infrastructure (Postgres, Redis, HTTP) so it can be imported
// anywhere without creating cycles.
package domain

import (
	"errors"
	"time"
)

// Link is a shortened URL record.
type Link struct {
	ID        int64      `json:"-"`
	Code      string     `json:"code"`
	LongURL   string     `json:"long_url"`
	Custom    bool       `json:"custom"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// IsExpired reports whether the link has a TTL that has passed.
func (l Link) IsExpired(now time.Time) bool {
	return l.ExpiresAt != nil && now.After(*l.ExpiresAt)
}

// ClickEvent is an analytics record produced on every successful redirect. It is
// processed asynchronously off the hot path.
type ClickEvent struct {
	Code      string    `json:"code"`
	Referrer  string    `json:"referrer"`
	UserAgent string    `json:"user_agent"`
	IP        string    `json:"ip"`
	Timestamp time.Time `json:"timestamp"`
}

// Sentinel errors. Layers wrap or translate these into transport responses.
var (
	ErrNotFound    = errors.New("link not found")
	ErrExpired     = errors.New("link has expired")
	ErrAliasTaken  = errors.New("custom alias already in use")
	ErrInvalidURL  = errors.New("invalid url")
	ErrInvalidCode = errors.New("invalid short code")
)
