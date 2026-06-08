package shortener

import (
	"strings"

	"github.com/shaurya703/linkforge/internal/domain"
)

// base62 alphabet. Order is arbitrary but fixed; codes are case-sensitive.
const base62Alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

const base = uint64(62)

// Generator turns a monotonic id into a short code.
//
// Approach: base62-encode a database sequence value. This is collision-free *by
// construction* — distinct ids map to distinct codes — so there is no retry loop
// and no probabilistic failure under load, unlike random-code-with-uniqueness-check
// schemes (which degrade as the keyspace fills) or hashing (which must handle
// collisions). The tradeoff is that codes are monotonic and therefore enumerable;
// the README documents how a reversible permutation over the id space would
// scramble them without sacrificing the collision-free property.
type Generator interface {
	Generate(id int64) string
}

// Base62Generator is the default Generator.
type Base62Generator struct{}

// Generate returns the base62 encoding of id.
func (Base62Generator) Generate(id int64) string {
	return EncodeBase62(uint64(id))
}

// EncodeBase62 encodes an unsigned integer to a base62 string.
func EncodeBase62(n uint64) string {
	if n == 0 {
		return string(base62Alphabet[0])
	}
	// Max uint64 is 11 base62 digits; build into a small buffer back-to-front.
	var buf [11]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = base62Alphabet[n%base]
		n /= base
	}
	return string(buf[i:])
}

// DecodeBase62 reverses EncodeBase62. Exposed mainly so tests can assert the
// round-trip property. Returns ErrInvalidCode on any out-of-alphabet character.
func DecodeBase62(s string) (uint64, error) {
	if s == "" {
		return 0, domain.ErrInvalidCode
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(base62Alphabet, s[i])
		if idx < 0 {
			return 0, domain.ErrInvalidCode
		}
		n = n*base + uint64(idx)
	}
	return n, nil
}
