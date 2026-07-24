// Package git is pj's wrapper over the external git binary. It runs only the
// operations the write and self-commit paths need — path-scoped staging, a fixed
// commit, a dir-scoped porcelain status, tracked-path and staged-change probes,
// and mid-rebase detection — using the user's own git, credentials, and SSH
// config. It carries no git library; a subprocess is not cgo.
//
// Every command runs with cmd.Dir set to a derived git-root; callers pass that
// root explicitly rather than relying on the process working directory. pj never
// writes under <git-root>/.git/ — it only reads Git-owned state (rev-parse, status,
// rebase markers). git-root derivation itself lives in the gitroot package.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Available reports whether the git binary is on PATH. When it is not, callers
// treat self-commit exactly like a missing git-root: writes still land, commit is
// skipped, and repo-driven dirty health is skipped.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// run executes git with cmd.Dir set to gitRoot and returns its stdout. A non-zero
// exit becomes an error carrying git's own stderr, so the surfaced message is
// git's, not a generic "exit status 1".
func run(ctx context.Context, gitRoot string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = gitRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

// Add stages exactly the given pathspecs — never the whole tree. Staging specific
// paths (not git add -A) leaves unrelated dirty files untouched, and records a
// deletion when a staged path was tracked but is now gone (the terminal-move case).
// Paths may be absolute or relative to gitRoot.
func Add(ctx context.Context, gitRoot string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	_, err := run(ctx, gitRoot, args...)
	return err
}

// Commit records the staged changes with the given fixed message. It never pushes.
func Commit(ctx context.Context, gitRoot, message string) error {
	_, err := run(ctx, gitRoot, "commit", "-m", message)
	return err
}

// Tracked reports whether path is present in gitRoot's index. It is how the
// conditional pathspec selection decides to stage a now-absent old path: a tracked
// old path is staged so its deletion is recorded; a never-committed old path is
// omitted rather than passed to git add and left to error on "pathspec did not
// match any files".
func Tracked(ctx context.Context, gitRoot, path string) bool {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = gitRoot
	return cmd.Run() == nil
}

// HasStagedChanges reports whether the index differs from HEAD. Self-commit checks
// it before committing so a byte-identical rewrite (e.g. a same-status write) is a
// clean no-op rather than a "nothing to commit" failure.
func HasStagedChanges(ctx context.Context, gitRoot string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = gitRoot
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached: %w", err)
}

// HasUpstream reports whether gitRoot's current branch has a configured upstream —
// the precondition pj sync needs before a push is meaningful, and the signal doctor
// reads to distinguish an eligible auto-commit scope that can sync from one that
// still rides sync_disabled. It resolves the upstream via rev-parse; any error (no
// upstream, detached HEAD, no repo) is a false.
func HasUpstream(ctx context.Context, gitRoot string) bool {
	_, err := run(ctx, gitRoot, "rev-parse", "--abbrev-ref", "@{u}")
	return err == nil
}

// MidRebase reports whether gitRoot is mid-rebase — a rebase-merge or rebase-apply
// directory exists in its git dir. It resolves the git dir via rev-parse so it is
// correct for worktrees and submodules, not only a literal <root>/.git. A false is
// returned on any probe error (no repo, git absent): the caller only refuses when
// this is affirmatively true.
func MidRebase(ctx context.Context, gitRoot string) bool {
	for _, marker := range []string{"rebase-merge", "rebase-apply"} {
		out, err := run(ctx, gitRoot, "rev-parse", "--git-path", marker)
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(out))
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(gitRoot, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// UnmergedFiles lists the repo-relative paths git currently reports as unmerged
// (diff-filter=U) — the files a paused rebase left conflicted. Best-effort: an
// empty slice on any error, so the mid-rebase refuse still names the scope even
// when the conflicted file cannot be determined.
func UnmergedFiles(ctx context.Context, gitRoot string) []string {
	out, err := run(ctx, gitRoot, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	return nonEmptyLines(string(out))
}

// DirtyPaths returns the absolute paths under dir that git status reports as dirty
// (any working-tree or index change), scoped to dir so it never scans the whole
// working tree. It is the read behind the repo-driven uncommitted: signal; the caller
// filters to the allowlist. It is a thin projection of DirtyEntries, dropping the
// porcelain status code the code-carrying callers need.
func DirtyPaths(ctx context.Context, gitRoot, dir string) ([]string, error) {
	entries, err := DirtyEntries(ctx, gitRoot, dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return paths, nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
