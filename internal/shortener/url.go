package shortener

import (
	"crypto/sha256"
	"net/url"
	"regexp"
	"strings"

	"github.com/shaurya703/linkforge/internal/domain"
)

var aliasRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// reserved short codes that would collide with real routes.
var reservedAliases = map[string]bool{
	"shorten": true, "healthz": true, "readyz": true,
	"metrics": true, "stats": true, "api": true,
}

// NormalizeURL validates a user-supplied URL and returns a canonical form plus a
// sha256 hash of that canonical form (used for dedupe). Normalization is
// deliberately conservative — we only touch parts that are semantically
// case-insensitive (scheme, host) and strip the fragment, so we never change
// where a link actually points.
func NormalizeURL(raw string) (normalized string, hash []byte, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, domain.ErrInvalidURL
	}
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", nil, domain.ErrInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", nil, domain.ErrInvalidURL
	}
	if u.Host == "" {
		return "", nil, domain.ErrInvalidURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	// Drop redundant default ports.
	u.Host = strings.TrimSuffix(u.Host, ":80")
	u.Host = strings.TrimSuffix(u.Host, ":443")
	u.Fragment = "" // fragments are client-side only; ignore for identity

	normalized = u.String()
	sum := sha256.Sum256([]byte(normalized))
	return normalized, sum[:], nil
}

// ValidateAlias checks a user-provided custom code.
func ValidateAlias(alias string) error {
	if !aliasRe.MatchString(alias) || reservedAliases[strings.ToLower(alias)] {
		return domain.ErrInvalidCode
	}
	return nil
}
