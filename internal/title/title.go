// Package title implements the shared title-extraction helper used by list,
// meta, and search display. It reads the markdown body only and recognises an
// ATX H1 exclusively. It is pure and never fails: no match yields an empty
// string, never an error and never a slug or summary fallback.
package title

import (
	"bytes"
	"regexp"
	"strings"
)

// atxH1 matches a single-# ATX heading line: one '#' followed by whitespace and
// at least one further character. The required whitespace after the '#' means a
// '##' line never matches (its second '#' is not whitespace), so only true H1s
// are recognised. A line whose text is only whitespace can still match here, so
// Extract additionally checks that the stripped text is non-empty.
var atxH1 = regexp.MustCompile(`^#\s+.+`)

// Extract returns the text of the first ATX H1 in the body that carries actual
// text, with the leading '#' and surrounding whitespace stripped. It scans the
// body only (the caller passes the bytes after the closing frontmatter fence),
// ignores setext underlines, skips empty '#   ' headings so a later real H1 is
// still found, and returns "" when no such H1 is present.
func Extract(body []byte) string {
	for len(body) > 0 {
		var line []byte
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			line, body = body[:i], body[i+1:]
		} else {
			line, body = body, nil
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if atxH1.Match(line) {
			if text := strings.TrimSpace(strings.TrimPrefix(string(line), "#")); text != "" {
				return text
			}
		}
	}
	return ""
}
