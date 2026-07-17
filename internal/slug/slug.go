// Package slug implements slugify, the create-time filename slug wire contract.
// It is a pure, deterministic function: no locale, no filesystem probe, no
// uniqueness pass — identical titles always yield identical slugs. The slug is
// frozen at pj create and mirrors the id in the filename <id>-<slug>.md.
//
// The closed grammar for a legal slug is ^[a-z0-9]+(-[a-z0-9]+)*$ with a byte
// length of 1 through SlugMax inclusive.
package slug

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// SlugMax is the maximum byte length of a legal slug.
const SlugMax = 48

// fallback is the slug used when a title reduces to no legal tokens.
const fallback = "x"

// Slugify converts a title into a legal slug by the closed deterministic
// algorithm: NFKC normalise, lowercase ASCII A-Z, keep ASCII alphanumerics,
// treat every other character as a separator, join non-empty tokens with a
// single '-', fall back to "x" when empty, and truncate to SlugMax bytes
// preferring a cut at the last '-' within the cap. The result always satisfies
// Valid; an empty input still yields "x" (callers reject an empty create title
// before calling, but the function stays total).
func Slugify(title string) string {
	normalised := norm.NFKC.String(title)

	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range normalised {
		switch {
		case r >= 'A' && r <= 'Z':
			cur.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()

	if len(tokens) == 0 {
		return fallback
	}
	s := strings.Join(tokens, "-")
	if len(s) > SlugMax {
		s = truncate(s)
	}
	if s == "" {
		return fallback
	}
	return s
}

// truncate reduces s to at most SlugMax bytes. It prefers cutting at the last
// '-' whose index is within the cap so the result stays valid; failing that
// (one long token) it hard-cuts to SlugMax bytes. s is ASCII at this stage, so
// a byte cut is a rune cut.
func truncate(s string) string {
	if cut := strings.LastIndexByte(s[:SlugMax+1], '-'); cut >= 0 {
		return s[:cut]
	}
	return strings.TrimRight(s[:SlugMax], "-")
}

// Valid reports whether s satisfies the closed slug grammar
// ^[a-z0-9]+(-[a-z0-9]+)*$ with byte length 1 through SlugMax.
func Valid(s string) bool {
	if len(s) < 1 || len(s) > SlugMax {
		return false
	}
	prevHyphen := true // guards against a leading '-'
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			prevHyphen = false
		case c == '-':
			if prevHyphen {
				return false // leading '-' or '--'
			}
			prevHyphen = true
		default:
			return false
		}
	}
	return !prevHyphen // trailing '-'
}
