package shortener

import (
	"context"
	"errors"
	"time"

	"github.com/shaurya703/linkforge/internal/domain"
)

// LinkStore is the persistence contract the service depends on. Declaring it here
// (at the consumer) keeps the service decoupled from Postgres and trivially mockable.
type LinkStore interface {
	NextID(ctx context.Context) (int64, error)
	InsertAuto(ctx context.Context, l domain.Link, urlHash []byte) (inserted bool, err error)
	InsertCustom(ctx context.Context, l domain.Link, urlHash []byte) error
	FindDuplicate(ctx context.Context, urlHash []byte) (domain.Link, bool, error)
	GetByCode(ctx context.Context, code string) (domain.Link, error)
}

// Cache is the read-through cache contract for the redirect hot path. All writes
// are best-effort: a cache outage must never fail a request, so these return nothing.
type Cache interface {
	Lookup(ctx context.Context, code string) (l domain.Link, hit bool, negative bool)
	Store(ctx context.Context, l domain.Link)
	StoreNegative(ctx context.Context, code string)
	Invalidate(ctx context.Context, code string)
}

// Clock lets tests control "now"; production uses time.Now.
type Clock func() time.Time

// Service holds the core business logic for shortening and resolving links.
type Service struct {
	store LinkStore
	cache Cache
	gen   Generator
	now   Clock
}

// NewService wires the service. A nil cache disables caching (useful in tests).
func NewService(store LinkStore, cache Cache, gen Generator) *Service {
	if cache == nil {
		cache = noopCache{}
	}
	return &Service{store: store, cache: cache, gen: gen, now: time.Now}
}

// ShortenInput is the validated request to create a link.
type ShortenInput struct {
	URL   string
	Alias string         // optional custom code
	TTL   *time.Duration // optional expiry
}

// Shorten validates, normalizes, and persists a link, returning the stored record.
func (s *Service) Shorten(ctx context.Context, in ShortenInput) (domain.Link, error) {
	normalized, hash, err := NormalizeURL(in.URL)
	if err != nil {
		return domain.Link{}, err
	}

	var expiresAt *time.Time
	if in.TTL != nil {
		if *in.TTL <= 0 {
			return domain.Link{}, domain.ErrInvalidURL
		}
		t := s.now().Add(*in.TTL).UTC()
		expiresAt = &t
	}

	// Custom alias path: no dedupe, explicit collision handling.
	if in.Alias != "" {
		if verr := ValidateAlias(in.Alias); verr != nil {
			return domain.Link{}, verr
		}
		id, nerr := s.store.NextID(ctx)
		if nerr != nil {
			return domain.Link{}, nerr
		}
		link := domain.Link{ID: id, Code: in.Alias, LongURL: normalized, Custom: true, ExpiresAt: expiresAt}
		if ierr := s.store.InsertCustom(ctx, link, hash); ierr != nil {
			return domain.Link{}, ierr
		}
		link.CreatedAt = s.now().UTC()
		return link, nil
	}

	// Auto path: dedupe non-expiring URLs so the same link maps to one code.
	if expiresAt == nil {
		if dup, found, derr := s.store.FindDuplicate(ctx, hash); derr != nil {
			return domain.Link{}, derr
		} else if found {
			return dup, nil
		}
	}

	id, nerr := s.store.NextID(ctx)
	if nerr != nil {
		return domain.Link{}, nerr
	}
	link := domain.Link{ID: id, Code: s.gen.Generate(id), LongURL: normalized, Custom: false, ExpiresAt: expiresAt}
	inserted, ierr := s.store.InsertAuto(ctx, link, hash)
	if ierr != nil {
		return domain.Link{}, ierr
	}
	if !inserted {
		// Lost a dedupe race; the winning row is now present.
		dup, found, derr := s.store.FindDuplicate(ctx, hash)
		if derr != nil {
			return domain.Link{}, derr
		}
		if found {
			return dup, nil
		}
		return domain.Link{}, domain.ErrNotFound // should not happen
	}
	link.CreatedAt = s.now().UTC()
	return link, nil
}

// Resolve returns the destination URL for a code using a cache-aside strategy.
// Flow: Redis -> (miss) Postgres -> populate cache. Misses are negatively cached
// to shield the database from floods of lookups for codes that don't exist.
func (s *Service) Resolve(ctx context.Context, code string) (domain.Link, error) {
	if l, hit, negative := s.cache.Lookup(ctx, code); hit {
		if negative {
			return domain.Link{}, domain.ErrNotFound
		}
		if l.IsExpired(s.now()) {
			s.cache.Invalidate(ctx, code)
			return domain.Link{}, domain.ErrExpired
		}
		return l, nil
	}

	l, err := s.store.GetByCode(ctx, code)
	if errors.Is(err, domain.ErrNotFound) {
		s.cache.StoreNegative(ctx, code)
		return domain.Link{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Link{}, err
	}
	if l.IsExpired(s.now()) {
		return domain.Link{}, domain.ErrExpired
	}
	s.cache.Store(ctx, l)
	return l, nil
}

// noopCache is used when caching is disabled.
type noopCache struct{}

func (noopCache) Lookup(context.Context, string) (domain.Link, bool, bool) {
	return domain.Link{}, false, false
}
func (noopCache) Store(context.Context, domain.Link)    {}
func (noopCache) StoreNegative(context.Context, string) {}
func (noopCache) Invalidate(context.Context, string)    {}
