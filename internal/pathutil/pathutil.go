// Package pathutil holds the boundary-safe path predicates the registration
// checks and the ambient resolver share. Every function assumes its inputs are
// already cleaned absolute paths (the registry stores paths that way and the CLI
// resolves them at the edge), so comparisons are pure string operations with an
// explicit separator boundary — "/a/bc" is never treated as nested under "/a/b".
package pathutil

import (
	"os"
	"strings"
)

// UnderOrEqual reports whether child is ancestor or lies within it, comparing on
// a path-separator boundary so a shared textual prefix that is not a directory
// boundary (e.g. /a/bc under /a/b) does not count.
func UnderOrEqual(child, ancestor string) bool {
	if child == ancestor {
		return true
	}
	sep := string(os.PathSeparator)
	if !strings.HasSuffix(ancestor, sep) {
		ancestor += sep
	}
	return strings.HasPrefix(child, ancestor)
}

// Overlap reports whether a and b are equal or one is nested within the other.
// It is the dir-disjointness test: two scope dirs may never overlap, unlike
// code-roots, which nest cleanly under longest-prefix resolution.
func Overlap(a, b string) bool {
	return UnderOrEqual(a, b) || UnderOrEqual(b, a)
}
