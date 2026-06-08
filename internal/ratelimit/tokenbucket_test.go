package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T, rate float64, burst int) (*Limiter, *time.Time) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	now := time.Unix(1_700_000_000, 0)
	l := New(client, rate, burst)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestTokenBucket_AllowsBurstThenBlocks(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 5) // 1 token/sec, burst 5
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		allowed, _, err := l.Allow(ctx, "ip-1")
		if err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Fatalf("request %d in burst should be allowed", i+1)
		}
	}
	// 6th immediately should be denied (bucket empty).
	allowed, retry, err := l.Allow(ctx, "ip-1")
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("6th request should be rate-limited")
	}
	if retry <= 0 {
		t.Errorf("expected a positive Retry-After, got %v", retry)
	}
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	l, now := newTestLimiter(t, 2, 2) // 2 tokens/sec, burst 2
	ctx := context.Background()

	// Drain the bucket.
	l.Allow(ctx, "ip-2")
	l.Allow(ctx, "ip-2")
	if allowed, _, _ := l.Allow(ctx, "ip-2"); allowed {
		t.Fatal("bucket should be empty")
	}

	// Advance 1 second -> +2 tokens.
	*now = now.Add(time.Second)
	if allowed, _, _ := l.Allow(ctx, "ip-2"); !allowed {
		t.Fatal("should be allowed after refill")
	}
}

func TestTokenBucket_IsolatesIdentities(t *testing.T) {
	l, _ := newTestLimiter(t, 1, 1)
	ctx := context.Background()
	if allowed, _, _ := l.Allow(ctx, "a"); !allowed {
		t.Fatal("a first request allowed")
	}
	// Different identity has its own bucket.
	if allowed, _, _ := l.Allow(ctx, "b"); !allowed {
		t.Fatal("b should not be affected by a")
	}
}
