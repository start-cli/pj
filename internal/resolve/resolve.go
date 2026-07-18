// Package resolve implements ambient scope resolution: the shared mechanism the
// later ambient project verbs consume. No P2 verb resolves ambiently (pj scope
// list enumerates every scope and takes no --scope), so this package ships with
// unit-test coverage rather than a driving command.
//
// Precedence is --scope > PJ_SCOPE > longest-prefix code-root, all against the
// registry only — never a filesystem probe for an unregistered pj.cue. An
// explicit override naming an unregistered scope fails closed with an
// unknown-scope error rather than falling through to the code-root tier; a
// deliberate target that misses is a mistake, not a request to auto-resolve.
// A resolved scope whose registry key disagrees with its on-disk pj.cue name is
// name-drift: unusable until deliberate re-registration, hard-erroring on every
// using path.
package resolve

import (
	"errors"
	"fmt"

	"cuelang.org/go/cue"
	"github.com/start-cli/pj/internal/gitroot"
	"github.com/start-cli/pj/internal/pathutil"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/token"
)

// Resolved is a successfully resolved ambient scope.
type Resolved struct {
	Name  string
	Entry registry.Entry
}

// Options carries the ambient inputs, read once at the CLI edge and passed in so
// resolution is a pure function of the registry plus these values.
type Options struct {
	// ScopeFlag is the --scope override (highest precedence). Empty when unset.
	ScopeFlag string
	// EnvScope is the PJ_SCOPE override. Empty when unset.
	EnvScope string
	// Cwd is the current working directory for longest-prefix code-root matching.
	// It must be canonical — absolute and symlink-resolved — to match the stored
	// roots (the CLI canonicalises it at the edge); a symlinked cwd would not
	// prefix a git-resolved root.
	Cwd string
}

// ErrNoScope is returned when no override is set and no code-root matches cwd.
// It fails registry-only: no tree probe for an unregistered scope. Scope-
// requiring commands surface guidance; discovery commands run without a scope.
var ErrNoScope = errors.New("no ambient scope: pass --scope <name>, set PJ_SCOPE, or run inside a registered code-root (see pj scope init/import)")

// UnknownScopeError is returned when an explicit --scope/PJ_SCOPE override names
// a scope with no registry entry. It never falls through to the code-root tier.
type UnknownScopeError struct {
	Name string
}

func (e *UnknownScopeError) Error() string {
	return fmt.Sprintf("unknown scope: %q is not registered (see pj scope import)", e.Name)
}

// DriftError is returned for a resolved scope whose registry key disagrees with
// its on-disk pj.cue name. Its message carries the name_drift token and the
// exact forget+import recovery.
type DriftError struct {
	Key    string
	PjName string
	Dir    string
	line   string
}

func (e *DriftError) Error() string { return e.line }

// Resolve resolves the ambient scope by the precedence chain. An override that
// names an unregistered scope returns *UnknownScopeError; a resolved scope in
// name-drift returns *DriftError; no match with no override returns ErrNoScope.
func Resolve(ctx *cue.Context, reg *registry.Registry, opts Options) (*Resolved, error) {
	if name := override(opts); name != "" {
		entry, ok := reg.Scopes[name]
		if !ok {
			return nil, &UnknownScopeError{Name: name}
		}
		return resolvedOrDrift(ctx, name, entry)
	}

	name, entry, ok := longestPrefix(reg, opts.Cwd)
	if !ok {
		return nil, ErrNoScope
	}
	return resolvedOrDrift(ctx, name, entry)
}

// override returns the winning explicit override name (--scope over PJ_SCOPE),
// or "" when neither is set.
func override(opts Options) string {
	if opts.ScopeFlag != "" {
		return opts.ScopeFlag
	}
	return opts.EnvScope
}

// longestPrefix returns the registered scope whose code-root is the longest
// prefix of cwd. Nested code-roots resolve deterministically to the most
// specific.
func longestPrefix(reg *registry.Registry, cwd string) (string, registry.Entry, bool) {
	var bestName string
	var bestEntry registry.Entry
	bestLen := -1
	for name, entry := range reg.Scopes {
		if pathutil.UnderOrEqual(cwd, entry.Root) && len(entry.Root) > bestLen {
			bestName, bestEntry, bestLen = name, entry, len(entry.Root)
		}
	}
	if bestLen < 0 {
		return "", registry.Entry{}, false
	}
	return bestName, bestEntry, true
}

// resolvedOrDrift returns the scope unless its on-disk pj.cue name disagrees with
// the registry key. When the pj.cue name cannot be read (absent/unparseable), the
// scope still resolves — reads stay available and the write path enforces the
// config_unparseable gate; drift is specifically a name mismatch.
func resolvedOrDrift(ctx *cue.Context, name string, entry registry.Entry) (*Resolved, error) {
	pjName, err := scopeconfig.ReadName(ctx, entry.Dir)
	if err == nil && pjName != name {
		return nil, &DriftError{
			Key:    name,
			PjName: pjName,
			Dir:    entry.Dir,
			line:   DriftLine(name, pjName, entry.Dir, SuggestCodeRoot(entry.Dir, entry.Root)),
		}
	}
	return &Resolved{Name: name, Entry: entry}, nil
}

// DriftLine formats the stderr line for a name-drift condition: the name_drift
// token, both names, the dir, and the exact forget+import recovery. codeRoot is
// appended as --code-root only when non-empty (a non-default binding to
// reproduce). It is shared by the resolver's hard error and pj scope list's soft
// diagnostic so the recovery wording never forks.
func DriftLine(key, pjName, dir, codeRoot string) string {
	rec := fmt.Sprintf("pj scope forget %s && pj scope import %s", key, dir)
	if codeRoot != "" {
		rec += " --code-root " + codeRoot
	}
	return token.Line(token.NameDrift, fmt.Sprintf("registry key %q but pj.cue name is %q at %s — run: %s", key, pjName, dir, rec))
}

// SuggestCodeRoot returns the code-root to include in a recovery suggestion, or
// "" when root is the default for dir (git-root inside a repo, else dir), so the
// suggested re-import reproduces the exact binding without a redundant flag. It
// derives the git-root itself; callers that already hold one use
// SuggestCodeRootWith to avoid a second derivation.
func SuggestCodeRoot(dir, root string) string {
	gitRoot, inRepo := gitroot.RepoRoot(dir)
	return SuggestCodeRootWith(dir, root, gitRoot, inRepo)
}

// SuggestCodeRootWith is SuggestCodeRoot over a pre-derived git-root. The default
// code-root is the git-root when the dir is inside a repo, else the dir itself; a
// root equal to that default needs no explicit --code-root in the suggestion.
func SuggestCodeRootWith(dir, root, gitRoot string, inRepo bool) string {
	def := dir
	if inRepo {
		def = gitRoot
	}
	if root == def {
		return ""
	}
	return root
}
