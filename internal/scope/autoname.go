// Package scope implements the --auto-name derivation: a pure procedure that
// proposes a scope name from a code-root basename. It performs no I/O and never
// invents a fallback — a basename that cannot yield a legal name is a hard
// error telling the caller to pass --name. The registration-collision check
// (derived name already registered) belongs to P2, not this function.
package scope

import (
	"errors"
	"strings"
	"unicode"

	"github.com/start-cli/pj/internal/id"
)

const (
	// nameMax is the maximum length of a derived scope name. A derived name must
	// be a legal scope name, so this tracks the id package's scope-name cap.
	nameMax = id.ScopeNameMax

	// autoNameLetters is the letter subset a derived name may use (drops
	// i/l/o), matching the short-id typeability bias. It is a strict subset of
	// the full legal scope alphabet; an explicit --name may use i/l/o/0/1. It is
	// the same typeable letter set the id package draws short-ids from.
	autoNameLetters = id.LetterAlphabet
	// autoNameDigits is the digit subset a derived name may use (drops 0/1),
	// the same typeable digit set the id package uses.
	autoNameDigits = id.DigitAlphabet
)

// ErrCannotDerive signals that no legal scope name can be derived from the
// given basename; the caller should ask the user to pass --name.
var ErrCannotDerive = errors.New("cannot derive scope name from code-root basename; pass --name")

// AutoName derives a proposed scope name from a code-root basename by the
// closed procedure: reduce to the last path segment, tokenise on [-_. ] runs
// and camelCase boundaries, seed from token initials (two or more tokens) or
// the first two characters (one opaque token), restrict to the auto-name
// alphabet, ensure a letter leads, cap at nameMax, and accept only a result
// matching ^[a-z][a-z0-9]{0,11}$ within that alphabet. Any other outcome
// returns ErrCannotDerive.
func AutoName(basename string) (string, error) {
	tokens := tokenise(lastSegment(basename))

	var seed string
	switch {
	case len(tokens) >= 2:
		var b strings.Builder
		for _, t := range tokens {
			b.WriteString(string([]rune(t)[:1]))
		}
		seed = b.String()
	case len(tokens) == 1:
		seed = firstRunes(tokens[0], 2)
	default:
		seed = ""
	}

	name := sanitize(seed)
	if !accept(name) {
		return "", ErrCannotDerive
	}
	return name, nil
}

// lastSegment returns the final '/'-separated element of s, so a full path
// reduces to its basename per step 1 of the procedure.
func lastSegment(s string) string {
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// tokenise splits on [-_. ] separator runs and camelCase boundaries, lowercases
// ASCII letters, keeps digits, and drops empty tokens.
func tokenise(s string) []string {
	var tokens []string
	for _, piece := range strings.FieldsFunc(s, isSeparator) {
		tokens = append(tokens, splitCamel(piece)...)
	}
	for i, t := range tokens {
		tokens[i] = strings.ToLower(t)
	}
	return tokens
}

func isSeparator(r rune) bool {
	return r == '-' || r == '_' || r == '.' || r == ' '
}

// splitCamel breaks a separator-free piece at camelCase boundaries: before an
// uppercase letter that follows a lowercase letter or digit, and before an
// uppercase letter that begins a word after an acronym run (e.g. HTMLParser ->
// HTML, Parser). Digits stay attached to the surrounding token.
func splitCamel(piece string) []string {
	runes := []rune(piece)
	if len(runes) == 0 {
		return nil
	}
	var tokens []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := false
		if unicode.IsUpper(cur) {
			if !unicode.IsUpper(prev) {
				boundary = true
			} else if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				boundary = true
			}
		}
		if boundary {
			tokens = append(tokens, string(runes[start:i]))
			start = i
		}
	}
	return append(tokens, string(runes[start:]))
}

// sanitize drops characters outside the auto-name alphabet, then strips leading
// non-letters so a letter leads, then caps the length.
func sanitize(seed string) string {
	var kept []byte
	for i := 0; i < len(seed); i++ {
		c := seed[i]
		if inAlphabet(c) {
			kept = append(kept, c)
		}
	}
	for len(kept) > 0 && !isAutoLetter(kept[0]) {
		kept = kept[1:]
	}
	if len(kept) > nameMax {
		kept = kept[:nameMax]
	}
	return string(kept)
}

// accept enforces the final contract: ^[a-z][a-z0-9]{0,11}$ within the
// auto-name alphabet, length at least one.
func accept(name string) bool {
	if len(name) < 1 || len(name) > nameMax {
		return false
	}
	if !isAutoLetter(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !inAlphabet(name[i]) {
			return false
		}
	}
	return true
}

func inAlphabet(c byte) bool {
	return isAutoLetter(c) || strings.IndexByte(autoNameDigits, c) >= 0
}

func isAutoLetter(c byte) bool {
	return strings.IndexByte(autoNameLetters, c) >= 0
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
