package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/flock"
	"github.com/start-cli/pj/internal/git"
	"github.com/start-cli/pj/internal/gitroot"
	"github.com/start-cli/pj/internal/gitstate"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/selfcommit"
	"github.com/start-cli/pj/internal/slug"
	"github.com/start-cli/pj/internal/token"
)

// scopeLockName is the per-scope flock file at the dir root. pj scope init
// gitignores it; the write verbs take it exclusively across their whole
// reconcile→read→write span so concurrent writers in one scope serialise.
const scopeLockName = ".pj.lock"

// acquireScopeLock takes the exclusive per-scope flock at <dir>/.pj.lock. The
// registered dir already exists, so the lock file is created there on first need.
func acquireScopeLock(dir string) (*flock.Lock, error) {
	return flock.Acquire(filepath.Join(dir, scopeLockName))
}

// refuseUnusableScope is the shared write precondition for an unreachable dir or an
// unparseable pj.cue: either refuses the write non-zero with its own token line and
// no file write. A healthy sibling scope and every read are unaffected. It is
// distinct from the per-project parse_error quarantine the verbs check per row.
func refuseUnusableScope(res *reconcile.Result, scope, dir string) error {
	if res.Unreachable[scope] {
		return fmt.Errorf("%s", token.Line(token.UnreachableScope,
			fmt.Sprintf("%s: dir %s is not reachable", scope, dir)))
	}
	if cfgErr, ok := res.ConfigErrs[scope]; ok {
		return fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
			fmt.Sprintf("%s (%s): %s — fix pj.cue before writing", scope, cfgErr.Dir, cfgErr.Reason)))
	}
	return nil
}

// gitRootFor resolves the derived git-root for dir once per write command. hasRoot
// is false when git is unavailable or no git-root is derivable — the durability
// helpers treat both the same (no self-commit; sync_disabled / repo health skipped).
// A write command resolves this once and threads it through so the root is derived a
// single time and every helper agrees on the same value for the whole command.
func gitRootFor(dir string) (root string, hasRoot bool) {
	if !git.Available() {
		return "", false
	}
	return gitroot.RepoRoot(dir)
}

// checkMidRebase fails fast when an auto-commit scope's git-root is mid-rebase: a
// complete-state or scaffold write must not land a pj commit on git's temporary
// HEAD. The refuse is repo-granular — it fires for every auto-commit sibling sharing
// the root — and names the scope and the conflicted file so the block is legible
// even from a sibling scope. Non-auto-commit scopes never self-commit and so are
// never frozen; reads and pj edit stay allowed (handled by not calling this).
func checkMidRebase(ctx context.Context, scope string, autoCommit bool, root string, hasRoot bool) error {
	if !autoCommit || !hasRoot {
		return nil
	}
	if !git.MidRebase(ctx, root) {
		return nil
	}
	where := "the conflicted file"
	if files := git.UnmergedFiles(ctx, root); len(files) > 0 {
		where = strings.Join(files, ", ")
	}
	return fmt.Errorf("%s is mid-sync-conflict in shared repo %s — resolve %s then run pj sync",
		scope, root, where)
}

// completeStateDurability applies the post-write durability policy for a
// complete-state verb (status / reorder / next --claim). On an auto-commit scope it
// self-commits the touched paths when a git-root exists, or rides sync_disabled and
// skips the commit when none does (or git is absent); the file and index writes
// stand either way. On a non-auto-commit scope it never commits and instead rides
// the repo-driven uncommitted: signal. A git failure during self-commit is returned
// non-zero, leaving the write in place.
func (e *engine) completeStateDurability(ctx context.Context, c *cobra.Command, scope, dir string, autoCommit bool, message, newPath, oldPath, root string, hasRoot bool) error {
	if !autoCommit {
		e.repoDirtyHealth(ctx, c, dir, root, hasRoot)
		return nil
	}
	if !hasRoot {
		stderrln(c, token.Line(token.SyncDisabled,
			fmt.Sprintf("%s: no git repository for %s — files written but not committed", scope, dir)))
		return nil
	}
	req := selfcommit.Request{
		StateDir: e.app.StateDir,
		GitRoot:  root,
		Message:  message,
		NewPath:  newPath,
		OldPath:  oldPath,
	}
	if err := selfcommit.Commit(ctx, req); err != nil {
		return fmt.Errorf("self-commit %s: %w", scope, err)
	}
	if detail, present := gitstate.ReadLastPushError(e.app.StateDir, root); present {
		stderrln(c, fmt.Sprintf("note: %s has a failed push on record (%s) — run pj sync", scope, detail))
	}
	return nil
}

// createDurability applies the create-specific policy: create never self-commits in
// any mode. A terminal create rides a terse durability note (not a closed token)
// that the scaffold under archive/ is not git-durable until the sync/host boundary.
// On a non-auto-commit scope the scaffold is disk-dirty too, so the repo-driven
// uncommitted: signal still runs.
func (e *engine) createDurability(ctx context.Context, c *cobra.Command, dir string, autoCommit, terminal bool, fullID, root string, hasRoot bool) {
	if terminal {
		stderrln(c, fmt.Sprintf("note: %s scaffolded under archive/ — a terminal create is not git-durable until pj sync (auto-commit) or a host commit", fullID))
	}
	if !autoCommit {
		e.repoDirtyHealth(ctx, c, dir, root, hasRoot)
	}
}

// repoDirtyHealth rides the repo-driven uncommitted: signal: on a scope inside git
// with autoCommit false, count the dirty paths under dir that match the auto-commit
// allowlist shape and warn with a short count. Detect-only — pj never stages,
// commits, or pushes. It skips silently when there is no git-root (plain files),
// git is absent, or status fails; pure reads never reach here.
func (e *engine) repoDirtyHealth(ctx context.Context, c *cobra.Command, dir, root string, hasRoot bool) {
	if !hasRoot {
		return
	}
	dirty, err := git.DirtyPaths(ctx, root, dir)
	if err != nil {
		return
	}
	n := 0
	for _, p := range dirty {
		if isAllowlistedScopeFile(p, dir) {
			n++
		}
	}
	if n > 0 {
		stderrln(c, token.Line(token.Uncommitted,
			fmt.Sprintf("%d allowlisted path(s) under %s uncommitted — commit with the host repo", n, dir)))
	}
}

// isAllowlistedScopeFile reports whether an absolute path under dir is a first-class
// scope file pj's auto-commit allowlist would carry: a project <id>-<slug>.md at the
// dir root or as an immediate child of archive/, or pj.cue / .gitignore at the dir
// root. Anything deeper than archive/, or any other name, is non-allowlist residue.
func isAllowlistedScopeFile(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	base := filepath.Base(rel)
	switch filepath.Dir(rel) {
	case ".":
		return base == "pj.cue" || base == ".gitignore" || looksLikeProjectFile(base)
	case "archive":
		return looksLikeProjectFile(base)
	default:
		return false
	}
}

// looksLikeProjectFile reports whether base matches the project filename shape
// <scope>-<short-id>[-<slug>].md: the first two hyphen segments form a legal full
// id and any remaining tail is a legal slug. Scope names and short-ids never contain
// a hyphen, so the id is exactly the first two segments.
func looksLikeProjectFile(base string) bool {
	stem, ok := strings.CutSuffix(base, ".md")
	if !ok {
		return false
	}
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 2 {
		return false
	}
	if !id.IsFullProjectID(parts[0] + "-" + parts[1]) {
		return false
	}
	if len(parts) == 3 {
		return slug.Valid(parts[2])
	}
	return true
}
