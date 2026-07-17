// Package id implements the project-id wire contract: the shared predicates
// that every call site validates against, the crypto/rand short-id mint used
// by pj create, and the deterministic collision-repair extension used by the
// integrity paths. It performs no I/O; the mint takes its randomness source as
// an argument so it stays testable and deterministic under test.
//
// A project id is <scope>-<short-id>, e.g. wc-ab2c. The short-id is not a
// content hash; create always mints length 4 and collision repair may lengthen
// it, append-only, up to ShortIDMax.
package id

import (
	"fmt"
	"io"
	"strings"
)

const (
	// ShortIDMin is the shortest legal short-id (pj create always mints this).
	ShortIDMin = 4
	// ShortIDMax is the longest legal short-id; collision repair may grow a
	// short-id through this length but never beyond it.
	ShortIDMax = 8
	// ScopeNameMax is the longest legal scope name.
	ScopeNameMax = 12

	// LetterAlphabet is the 23-letter typeable subset (drops i, l, o). The
	// first character of every short-id is drawn from this set.
	LetterAlphabet = "abcdefghjkmnpqrstuvwxyz"
	// DigitAlphabet is the 8-digit typeable subset (drops 0, 1).
	DigitAlphabet = "23456789"
	// ShortIDAlphabet is the fixed 31-character ordered alphabet used for
	// short-id characters and, in this exact order, for collision-repair
	// enumeration. Order is load-bearing: two machines repairing the same
	// collision must enumerate identically.
	ShortIDAlphabet = LetterAlphabet + DigitAlphabet
)

// IsScopeName reports whether s is a legal scope name: ^[a-z0-9]{1,12}$. Scope
// names are chosen deliberately by a human, so the ambiguous characters that
// short-ids drop (i/l/o/0/1) are permitted here.
func IsScopeName(s string) bool {
	if len(s) < 1 || len(s) > ScopeNameMax {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isLowerAlnum(s[i]) {
			return false
		}
	}
	return true
}

// IsShortID reports whether s is a legal short-id. This is deliberately not
// equivalent to ^[a-z0-9]{4,8}$: every character must be in ShortIDAlphabet
// (excluding i/l/o/0/1) and the first character must be a letter, never a
// digit. Length is ShortIDMin through ShortIDMax inclusive.
func IsShortID(s string) bool {
	if len(s) < ShortIDMin || len(s) > ShortIDMax {
		return false
	}
	if !isShortIDLetter(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isShortIDChar(s[i]) {
			return false
		}
	}
	return true
}

// IsFullProjectID reports whether s is a legal full project id of the form
// <scope>-<short-id>: exactly one '-' at the split, the scope substring passes
// IsScopeName, and the remainder passes IsShortID (and contains no further
// '-'). This is the one authoritative full-id validator; there is no looser
// standalone regex.
func IsFullProjectID(s string) bool {
	i := strings.IndexByte(s, '-')
	if i < 0 {
		return false
	}
	scope, short := s[:i], s[i+1:]
	if strings.IndexByte(short, '-') >= 0 {
		return false
	}
	return IsScopeName(scope) && IsShortID(short)
}

// Mint draws a fresh length-4 short-id from r. The first character is a uniform
// letter from LetterAlphabet; each of positions 2-4 is a 50/50 coin flip
// between the letter and digit classes, uniform within the chosen class. It is
// a pure generator: the draw->check->redraw loop against existing ids lives in
// pj create under the scope flock, not here. r must supply enough entropy; a
// short read returns an error.
func Mint(r io.Reader) (string, error) {
	br := byteReader{r: r}
	out := make([]byte, ShortIDMin)

	first, err := br.uniform(len(LetterAlphabet))
	if err != nil {
		return "", fmt.Errorf("mint short-id: %w", err)
	}
	out[0] = LetterAlphabet[first]

	for i := 1; i < ShortIDMin; i++ {
		coin, err := br.uniform(2)
		if err != nil {
			return "", fmt.Errorf("mint short-id: %w", err)
		}
		if coin == 0 {
			n, err := br.uniform(len(LetterAlphabet))
			if err != nil {
				return "", fmt.Errorf("mint short-id: %w", err)
			}
			out[i] = LetterAlphabet[n]
		} else {
			n, err := br.uniform(len(DigitAlphabet))
			if err != nil {
				return "", fmt.Errorf("mint short-id: %w", err)
			}
			out[i] = DigitAlphabet[n]
		}
	}
	return string(out), nil
}

// Extend deterministically repairs a colliding short-id. Given the loser's
// current short-id prefix and the set of short-ids already occupied in the
// scope, it appends characters from ShortIDAlphabet — for each target length
// from len(prefix)+1 through ShortIDMax, it enumerates every extension of that
// length in lexicographic order and returns the first prefix+extension not
// occupied. It uses no randomness so two machines repairing the same collision
// mint the same id. If no free candidate exists at any length up to ShortIDMax
// it returns an error rather than inventing a non-prefix id.
func Extend(prefix string, occupied map[string]struct{}) (string, error) {
	if !IsShortID(prefix) {
		return "", fmt.Errorf("extend short-id: prefix %q is not a legal short-id", prefix)
	}
	n := len(prefix)
	for target := n + 1; target <= ShortIDMax; target++ {
		odometer := make([]int, target-n)
		for {
			var b strings.Builder
			b.Grow(target)
			b.WriteString(prefix)
			for _, d := range odometer {
				b.WriteByte(ShortIDAlphabet[d])
			}
			candidate := b.String()
			if _, taken := occupied[candidate]; !taken {
				return candidate, nil
			}
			if !incOdometer(odometer) {
				break
			}
		}
	}
	return "", fmt.Errorf("extend short-id: no free id for prefix %q within length %d", prefix, ShortIDMax)
}

// incOdometer advances the base-len(ShortIDAlphabet) counter in place, most
// significant digit first, returning false when it wraps past its maximum.
func incOdometer(digits []int) bool {
	base := len(ShortIDAlphabet)
	for i := len(digits) - 1; i >= 0; i-- {
		digits[i]++
		if digits[i] < base {
			return true
		}
		digits[i] = 0
	}
	return false
}

// byteReader draws uniform indices from an io.Reader via rejection sampling so
// the result is unbiased regardless of the source's byte distribution.
type byteReader struct {
	r io.Reader
}

func (br byteReader) uniform(n int) (int, error) {
	// n is always a small positive alphabet size in this package.
	limit := 256 - (256 % n)
	var buf [1]byte
	for {
		if _, err := io.ReadFull(br.r, buf[:]); err != nil {
			return 0, err
		}
		if int(buf[0]) < limit {
			return int(buf[0]) % n, nil
		}
	}
}

func isLowerAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func isShortIDChar(c byte) bool {
	return strings.IndexByte(ShortIDAlphabet, c) >= 0
}

func isShortIDLetter(c byte) bool {
	return strings.IndexByte(LetterAlphabet, c) >= 0
}
