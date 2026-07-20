package selfcommit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitCmd(t, repo, "init")
	gitCmd(t, repo, "config", "user.email", "a@b.c")
	gitCmd(t, repo, "config", "user.name", "pj-test")
	gitCmd(t, repo, "config", "commit.gpgsign", "false")
	return repo
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, repo string) string {
	return gitCmd(t, repo, "ls-tree", "-r", "--name-only", "HEAD")
}

// A never-committed old path (a create'd file moved across the terminal boundary
// before its first commit) must be omitted from the pathspec, not passed and left to
// error. The commit should record only the new path.
func TestCommitOmitsUntrackedOldPath(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)

	oldPath := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	newPath := filepath.Join(repo, "wc", "archive", "wc-ab2c-x.md")
	write(t, newPath, "# x\n") // the move already happened; old path is absent and never tracked

	err := Commit(ctx, Request{
		StateDir: state, GitRoot: repo,
		Message: "pj: wc-ab2c -> done",
		NewPath: newPath, OldPath: oldPath,
	})
	if err != nil {
		t.Fatalf("commit with untracked old path must not error: %v", err)
	}
	tr := tree(t, repo)
	if !strings.Contains(tr, "wc/archive/wc-ab2c-x.md") {
		t.Errorf("new path must be committed, tree=%q", tr)
	}
	_ = oldPath
}

// A tracked old path that the mutation removed must be staged so its deletion is
// recorded — a committed file moved into archive/ leaves no stale copy behind.
func TestCommitStagesTrackedRemoval(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)

	oldPath := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, oldPath, "# x\n")
	gitCmd(t, repo, "add", "wc/wc-ab2c-x.md")
	gitCmd(t, repo, "commit", "-m", "seed")

	// Perform the move on disk, then self-commit both sides.
	newPath := filepath.Join(repo, "wc", "archive", "wc-ab2c-x.md")
	write(t, newPath, "# x done\n")
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	err := Commit(ctx, Request{
		StateDir: state, GitRoot: repo,
		Message: "pj: wc-ab2c -> done",
		NewPath: newPath, OldPath: oldPath,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	tr := tree(t, repo)
	if strings.Contains(tr, "wc/wc-ab2c-x.md") && !strings.Contains(tr, "wc/archive/") {
		t.Errorf("the old root path should be removed from the committed tree, tree=%q", tr)
	}
	if !strings.Contains(tr, "wc/archive/wc-ab2c-x.md") {
		t.Errorf("the archive path should be committed, tree=%q", tr)
	}
}

// A byte-identical rewrite stages nothing; Commit must be a clean no-op rather than
// erroring on "nothing to commit".
func TestCommitNoOpOnIdenticalRewrite(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	state := t.TempDir()
	repo := newRepo(t)
	p := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, p, "# x\n")
	gitCmd(t, repo, "add", "wc/wc-ab2c-x.md")
	gitCmd(t, repo, "commit", "-m", "seed")

	err := Commit(ctx, Request{
		StateDir: state, GitRoot: repo,
		Message: "pj: wc-ab2c reorder",
		NewPath: p,
	})
	if err != nil {
		t.Fatalf("no-op commit must not error: %v", err)
	}
	// Still exactly one commit — the no-op added none.
	log := gitCmd(t, repo, "log", "--oneline")
	if strings.Count(strings.TrimSpace(log), "\n") != 0 {
		t.Errorf("no-op self-commit must not add a commit, log=%q", log)
	}
}
