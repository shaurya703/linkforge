package shortener

import (
	"errors"
	"testing"

	"github.com/shaurya703/linkforge/internal/domain"
)

func TestNormalizeURL(t *testing.T) {
	valid := []struct{ in, want string }{
		{"https://Example.com/Path", "https://example.com/Path"},
		{"http://HOST:80/a", "http://host/a"},
		{"https://host:443/", "https://host/"},
		{"https://example.com/p#frag", "https://example.com/p"},
	}
	for _, c := range valid {
		got, hash, err := NormalizeURL(c.in)
		if err != nil {
			t.Fatalf("NormalizeURL(%q) unexpected err: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
		if len(hash) != 32 {
			t.Errorf("hash length = %d, want 32", len(hash))
		}
	}

	invalid := []string{"", "  ", "ftp://x", "not a url", "//no-scheme", "http://"}
	for _, in := range invalid {
		if _, _, err := NormalizeURL(in); !errors.Is(err, domain.ErrInvalidURL) {
			t.Errorf("NormalizeURL(%q) err = %v, want ErrInvalidURL", in, err)
		}
	}
}

func TestNormalizeURL_DedupeHashStable(t *testing.T) {
	_, h1, _ := NormalizeURL("https://Example.com/x")
	_, h2, _ := NormalizeURL("https://example.com/x")
	if string(h1) != string(h2) {
		t.Error("case-different hosts should normalize to the same hash")
	}
}

func TestValidateAlias(t *testing.T) {
	ok := []string{"abc", "my_link-1", "ABCdef123"}
	for _, a := range ok {
		if err := ValidateAlias(a); err != nil {
			t.Errorf("ValidateAlias(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{"ab", "has space", "way-too-long-alias-way-too-long-alias-xx", "metrics", "shorten", "bad/slash"}
	for _, a := range bad {
		if err := ValidateAlias(a); !errors.Is(err, domain.ErrInvalidCode) {
			t.Errorf("ValidateAlias(%q) = %v, want ErrInvalidCode", a, err)
		}
	}
}
