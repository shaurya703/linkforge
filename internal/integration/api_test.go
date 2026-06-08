//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// noRedirectClient does not follow redirects, so we can assert on 302s.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func shorten(t *testing.T, base, body string, xff string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/shorten", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("shorten request: %v", err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestShortenAndRedirect(t *testing.T) {
	srv := newServer(t, 1000, 1000)
	resp, out := shorten(t, srv.URL, `{"url":"https://example.com/hello"}`, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	code, _ := out["code"].(string)
	if code == "" {
		t.Fatal("expected a code in response")
	}

	r2, err := noRedirectClient().Get(srv.URL + "/" + code)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", r2.StatusCode)
	}
	if loc := r2.Header.Get("Location"); loc != "https://example.com/hello" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestDedupeSameURL(t *testing.T) {
	srv := newServer(t, 1000, 1000)
	_, a := shorten(t, srv.URL, `{"url":"https://dedupe.example/x"}`, "")
	_, b := shorten(t, srv.URL, `{"url":"https://dedupe.example/x"}`, "")
	if a["code"] != b["code"] {
		t.Errorf("expected same code, got %v and %v", a["code"], b["code"])
	}
}

func TestCustomAliasAndConflict(t *testing.T) {
	srv := newServer(t, 1000, 1000)
	resp, out := shorten(t, srv.URL, `{"url":"https://example.com/a","alias":"mylink"}`, "")
	if resp.StatusCode != http.StatusCreated || out["code"] != "mylink" {
		t.Fatalf("custom alias: status=%d code=%v", resp.StatusCode, out["code"])
	}
	resp2, _ := shorten(t, srv.URL, `{"url":"https://example.com/b","alias":"mylink"}`, "")
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate alias status = %d, want 409", resp2.StatusCode)
	}
}

func TestInvalidURLAndNotFound(t *testing.T) {
	srv := newServer(t, 1000, 1000)
	resp, _ := shorten(t, srv.URL, `{"url":"ftp://nope"}`, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid url status = %d, want 400", resp.StatusCode)
	}
	r, _ := noRedirectClient().Get(srv.URL + "/doesnotexist")
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("missing code status = %d, want 404", r.StatusCode)
	}
	r.Body.Close()
}

func TestExpiringLink(t *testing.T) {
	srv := newServer(t, 1000, 1000)
	_, out := shorten(t, srv.URL, `{"url":"https://example.com/temp","ttl_seconds":1}`, "")
	code := out["code"].(string)
	time.Sleep(1500 * time.Millisecond)
	r, _ := noRedirectClient().Get(srv.URL + "/" + code)
	defer r.Body.Close()
	if r.StatusCode != http.StatusGone {
		t.Fatalf("expired link status = %d, want 410", r.StatusCode)
	}
}

func TestClickAnalyticsAsync(t *testing.T) {
	srv := newServer(t, 1000, 1000)
	_, out := shorten(t, srv.URL, `{"url":"https://example.com/clicks"}`, "")
	code := out["code"].(string)

	const hits = 3
	for i := 0; i < hits; i++ {
		r, _ := noRedirectClient().Get(srv.URL + "/" + code)
		r.Body.Close()
	}

	// Analytics are async; poll /stats until the count catches up.
	deadline := time.Now().Add(3 * time.Second)
	var clicks float64
	for time.Now().Before(deadline) {
		r, err := http.Get(srv.URL + "/stats/" + code)
		if err != nil {
			t.Fatal(err)
		}
		var s map[string]any
		json.NewDecoder(r.Body).Decode(&s)
		r.Body.Close()
		if c, ok := s["clicks"].(float64); ok {
			clicks = c
			if c >= hits {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if clicks < hits {
		t.Fatalf("clicks = %v, want >= %d", clicks, hits)
	}
}

func TestRateLimiting(t *testing.T) {
	srv := newServer(t, 1, 3) // burst 3, 1/sec
	ip := "203.0.113.7"       // unique identity for this test's bucket

	var got201, got429 int
	for i := 0; i < 8; i++ {
		url := fmt.Sprintf(`{"url":"https://example.com/rl%d"}`, i)
		resp, _ := shorten(t, srv.URL, url, ip)
		switch resp.StatusCode {
		case http.StatusCreated:
			got201++
		case http.StatusTooManyRequests:
			got429++
		}
	}
	if got201 < 3 {
		t.Errorf("expected at least the burst (3) to succeed, got %d", got201)
	}
	if got429 == 0 {
		t.Errorf("expected some requests to be rate-limited, got none")
	}
}
