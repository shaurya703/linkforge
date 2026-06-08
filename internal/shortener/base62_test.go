package shortener

import "testing"

func TestEncodeBase62_Known(t *testing.T) {
	cases := map[uint64]string{
		0:  "0",
		1:  "1",
		61: "Z",
		62: "10",
	}
	for in, want := range cases {
		if got := EncodeBase62(in); got != want {
			t.Errorf("EncodeBase62(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBase62_RoundTrip(t *testing.T) {
	for _, n := range []uint64{0, 1, 61, 62, 1000, 1_000_000_000, 1 << 40} {
		s := EncodeBase62(n)
		back, err := DecodeBase62(s)
		if err != nil {
			t.Fatalf("DecodeBase62(%q): %v", s, err)
		}
		if back != n {
			t.Errorf("round-trip %d -> %q -> %d", n, s, back)
		}
	}
}

func TestBase62_Distinct(t *testing.T) {
	// Sequential ids must yield distinct, non-empty codes (collision-free property).
	seen := make(map[string]bool)
	for id := int64(1_000_000_000); id < 1_000_010_000; id++ {
		code := (Base62Generator{}).Generate(id)
		if code == "" {
			t.Fatalf("empty code for id %d", id)
		}
		if seen[code] {
			t.Fatalf("collision at id %d -> %q", id, code)
		}
		seen[code] = true
	}
}

func TestDecodeBase62_Invalid(t *testing.T) {
	for _, s := range []string{"", "abc def", "hello!", "+/="} {
		if _, err := DecodeBase62(s); err == nil {
			t.Errorf("DecodeBase62(%q) expected error", s)
		}
	}
}
