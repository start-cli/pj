package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/term"

	"github.com/start-cli/pj/internal/token"
)

const (
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
)

// wantColor reports whether ANSI may be written to a stream that is (or is not) a
// TTY. Colour is auto: on only for a TTY. NO_COLOR (presence alone, any value)
// disables colour on every stream. v1 has no --color flag and does not honour
// FORCE_COLOR. Stdout is never coloured regardless (callers never ask about it),
// and closed token lines are never coloured (see PrintError).
func wantColor(isTTY bool) bool {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	return isTTY
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// PrintError writes a fatal error to stderr, colouring gated on stderr being a
// TTY with NO_COLOR unset.
func PrintError(err error) {
	fprintError(os.Stderr, err, wantColor(isTerminal(os.Stderr)))
}

// fprintError is the testable core. A line beginning with a closed token is
// printed verbatim at column 0 — never coloured, never given a label — so an
// agent can match the prefix. Any other message may carry a coloured "error:"
// label when colour is allowed.
func fprintError(w io.Writer, err error, colorAllowed bool) {
	msg := err.Error()
	if token.HasKnownPrefix(msg) || !colorAllowed {
		fmt.Fprintln(w, msg)
		return
	}
	fmt.Fprintln(w, ansiRed+"error:"+ansiReset+" "+msg)
}

// absPath resolves p to its canonical absolute path — for hand-off on stdout and
// for the paths the registry stores. It cleans and absolutises (no cwd-relative
// form, no ~), then resolves symlinks so the path names its one true location:
// the same spelling `git rev-parse` returns for a repo root. That single form is
// what lets every downstream path comparison — code-root containment, collision,
// dir disjointness, and longest-prefix resolution — hold on a symlinked tree
// (notably macOS, where /var and /tmp are symlinks).
func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", p, err)
	}
	return canonicalize(abs), nil
}

// canonicalize resolves symlinks in the longest existing prefix of an absolute,
// cleaned path and rejoins any not-yet-existing tail — pj scope init names a dir
// that does not exist yet, and the missing tail carries no symlinks precisely
// because it does not exist. It never errors: a prefix that cannot be resolved
// (permissions, a broken link) falls back to the cleaned absolute form so a
// comparison still has a well-formed path to work with.
func canonicalize(abs string) string {
	abs = filepath.Clean(abs)
	var tail []string
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}
