// Package selfcommit is the single reusable self-commit step for pj's auto-commit
// scopes. A complete-state verb that has already written its file(s) and
// write-through the index calls Commit with the touched paths, a fixed message, and
// the derived git-root; the same step will back P5's --repair self-commit and P6's
// sync snapshot commit, so commit discipline lives in one place rather than forking
// three ways.
//
// It is mechanism only. The policy decisions — whether the scope is auto-commit,
// whether a git-root exists, emitting sync_disabled when it does not — belong to the
// caller. Commit assumes a git-root exists and git is available; it takes the
// git-root commit lock, stages exactly the matchable touched paths, and commits.
package selfcommit

import (
	"context"
	"fmt"
	"os"

	"github.com/start-cli/pj/internal/git"
	"github.com/start-cli/pj/internal/gitstate"
)

// Request is one self-commit: the fixed message, the always-staged post-write path,
// and an optional old path removed by the same mutation (the terminal-boundary
// move). GitRoot is the derived commit unit; StateDir locates its commit lock.
type Request struct {
	StateDir string
	GitRoot  string
	Message  string
	// NewPath is the post-write path; always staged.
	NewPath string
	// OldPath is a path the mutation removed (empty when the verb rewrote a single
	// in-place file). It is staged only when git can match it — it was tracked, or is
	// still present — so a never-committed old path is omitted rather than passed to
	// git add and left to error on "pathspec did not match any files".
	OldPath string
}

// Commit takes the git-root commit lock, stages the matchable touched paths, and
// commits under the fixed message — synchronously, no push. A byte-identical
// rewrite that stages nothing is a clean no-op. The caller must have confirmed a
// git-root exists and git is available; a git failure here is returned so the verb
// can surface it non-zero, with the file write and index write-through left standing.
func Commit(ctx context.Context, req Request) error {
	lock, err := gitstate.AcquireCommitLock(req.StateDir, req.GitRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	paths := []string{req.NewPath}
	if req.OldPath != "" && req.OldPath != req.NewPath && matchable(ctx, req.GitRoot, req.OldPath) {
		paths = append(paths, req.OldPath)
	}
	if err := git.Add(ctx, req.GitRoot, paths); err != nil {
		return err
	}
	staged, err := git.HasStagedChanges(ctx, req.GitRoot)
	if err != nil {
		return err
	}
	if !staged {
		return nil
	}
	if err := git.Commit(ctx, req.GitRoot, req.Message); err != nil {
		return fmt.Errorf("commit %s: %w", req.Message, err)
	}
	return nil
}

// matchable reports whether git add can name path without erroring: the file is
// still present, or it is tracked in the index (so staging it records its deletion).
func matchable(ctx context.Context, gitRoot, path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return git.Tracked(ctx, gitRoot, path)
}
