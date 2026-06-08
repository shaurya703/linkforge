package shortener

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shaurya703/linkforge/internal/domain"
)

// fakeStore is an in-memory LinkStore for unit tests.
type fakeStore struct {
	seq    int64
	byCode map[string]domain.Link
	byHash map[string]domain.Link // only non-expiring auto links (dedupe view)
}

func newFakeStore() *fakeStore {
	return &fakeStore{seq: 1_000_000_000, byCode: map[string]domain.Link{}, byHash: map[string]domain.Link{}}
}

func (f *fakeStore) NextID(context.Context) (int64, error) { f.seq++; return f.seq, nil }

func (f *fakeStore) InsertAuto(_ context.Context, l domain.Link, urlHash []byte) (bool, error) {
	// Mirror the partial unique index: only non-expiring auto links dedupe.
	if l.ExpiresAt == nil {
		if _, ok := f.byHash[string(urlHash)]; ok {
			return false, nil // dedupe conflict
		}
		f.byHash[string(urlHash)] = l
	}
	f.byCode[l.Code] = l
	return true, nil
}

func (f *fakeStore) InsertCustom(_ context.Context, l domain.Link, _ []byte) error {
	if _, ok := f.byCode[l.Code]; ok {
		return domain.ErrAliasTaken
	}
	f.byCode[l.Code] = l
	return nil
}

func (f *fakeStore) FindDuplicate(_ context.Context, urlHash []byte) (domain.Link, bool, error) {
	l, ok := f.byHash[string(urlHash)]
	return l, ok, nil
}

func (f *fakeStore) GetByCode(_ context.Context, code string) (domain.Link, error) {
	l, ok := f.byCode[code]
	if !ok {
		return domain.Link{}, domain.ErrNotFound
	}
	return l, nil
}

// fakeCache records interactions to assert cache-aside behaviour.
type fakeCache struct {
	entries  map[string]domain.Link
	negative map[string]bool
	stores   int
}

func newFakeCache() *fakeCache {
	return &fakeCache{entries: map[string]domain.Link{}, negative: map[string]bool{}}
}

func (c *fakeCache) Lookup(_ context.Context, code string) (domain.Link, bool, bool) {
	if c.negative[code] {
		return domain.Link{}, true, true
	}
	if l, ok := c.entries[code]; ok {
		return l, true, false
	}
	return domain.Link{}, false, false
}
func (c *fakeCache) Store(_ context.Context, l domain.Link)       { c.entries[l.Code] = l; c.stores++ }
func (c *fakeCache) StoreNegative(_ context.Context, code string) { c.negative[code] = true }
func (c *fakeCache) Invalidate(_ context.Context, code string) {
	delete(c.entries, code)
	delete(c.negative, code)
}

func TestShorten_DedupesSameURL(t *testing.T) {
	svc := NewService(newFakeStore(), newFakeCache(), Base62Generator{})
	a, err := svc.Shorten(context.Background(), ShortenInput{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := svc.Shorten(context.Background(), ShortenInput{URL: "https://example.com"})
	if a.Code != b.Code {
		t.Errorf("expected same code for duplicate URL, got %q and %q", a.Code, b.Code)
	}
}

func TestShorten_TTLBypassesDedupe(t *testing.T) {
	svc := NewService(newFakeStore(), nil, Base62Generator{})
	ttl := time.Hour
	a, _ := svc.Shorten(context.Background(), ShortenInput{URL: "https://example.com", TTL: &ttl})
	b, _ := svc.Shorten(context.Background(), ShortenInput{URL: "https://example.com", TTL: &ttl})
	if a.Code == b.Code {
		t.Error("TTL links should not be deduped")
	}
	if a.ExpiresAt == nil {
		t.Error("expected ExpiresAt to be set")
	}
}

func TestShorten_CustomAlias(t *testing.T) {
	svc := NewService(newFakeStore(), nil, Base62Generator{})
	l, err := svc.Shorten(context.Background(), ShortenInput{URL: "https://example.com", Alias: "promo"})
	if err != nil || l.Code != "promo" {
		t.Fatalf("custom alias: code=%q err=%v", l.Code, err)
	}
	_, err = svc.Shorten(context.Background(), ShortenInput{URL: "https://other.com", Alias: "promo"})
	if !errors.Is(err, domain.ErrAliasTaken) {
		t.Errorf("expected ErrAliasTaken, got %v", err)
	}
}

func TestShorten_InvalidInputs(t *testing.T) {
	svc := NewService(newFakeStore(), nil, Base62Generator{})
	if _, err := svc.Shorten(context.Background(), ShortenInput{URL: "ftp://x"}); !errors.Is(err, domain.ErrInvalidURL) {
		t.Errorf("bad url: got %v", err)
	}
	if _, err := svc.Shorten(context.Background(), ShortenInput{URL: "https://ok.com", Alias: "no"}); !errors.Is(err, domain.ErrInvalidCode) {
		t.Errorf("bad alias: got %v", err)
	}
}

func TestResolve_CacheAside(t *testing.T) {
	store := newFakeStore()
	cache := newFakeCache()
	svc := NewService(store, cache, Base62Generator{})
	created, _ := svc.Shorten(context.Background(), ShortenInput{URL: "https://example.com"})

	// First resolve: cache miss -> DB -> populate cache.
	got, err := svc.Resolve(context.Background(), created.Code)
	if err != nil || got.LongURL != "https://example.com" {
		t.Fatalf("resolve: %v / %q", err, got.LongURL)
	}
	if cache.stores != 1 {
		t.Errorf("expected 1 cache store after miss, got %d", cache.stores)
	}
	// Second resolve served from cache (no extra store).
	if _, err := svc.Resolve(context.Background(), created.Code); err != nil {
		t.Fatal(err)
	}
	if cache.stores != 1 {
		t.Errorf("expected resolve to be served from cache, stores=%d", cache.stores)
	}
}

func TestResolve_NegativeCaching(t *testing.T) {
	cache := newFakeCache()
	svc := NewService(newFakeStore(), cache, Base62Generator{})
	if _, err := svc.Resolve(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if !cache.negative["missing"] {
		t.Error("expected missing code to be negatively cached")
	}
}

func TestResolve_Expired(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, nil, Base62Generator{})
	past := time.Now().Add(-time.Hour)
	store.byCode["old"] = domain.Link{Code: "old", LongURL: "https://x.com", ExpiresAt: &past}
	if _, err := svc.Resolve(context.Background(), "old"); !errors.Is(err, domain.ErrExpired) {
		t.Errorf("expected ErrExpired, got %v", err)
	}
}
