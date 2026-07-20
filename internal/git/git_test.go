package git

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
	if !Available() {
		t.Skip("git not on PATH")
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
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

func TestAddCommitAndStagedChanges(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	p := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, p, "# x\n")

	staged, err := HasStagedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		t.Error("nothing should be staged before add")
	}
	if err := Add(ctx, repo, []string{p}); err != nil {
		t.Fatal(err)
	}
	staged, err = HasStagedChanges(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !staged {
		t.Error("the added path should be staged")
	}
	if err := Commit(ctx, repo, "pj: wc-ab2c -> todo"); err != nil {
		t.Fatal(err)
	}
	if !Tracked(ctx, repo, p) {
		t.Error("committed path must be tracked")
	}
}

func TestTrackedFalseForUntracked(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	p := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, p, "# x\n")
	if Tracked(ctx, repo, p) {
		t.Error("a never-added path must not be tracked")
	}
}

func TestDirtyPathsRenameReportsDestination(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	// Commit a file, then stage a rename inside the scope dir. The -z porcelain emits
	// "R  <new>\0<old>\0": DirtyPaths must report the destination and consume (not
	// report) the source field.
	oldPath := filepath.Join(repo, "wc", "wc-ab2c-x.md")
	write(t, oldPath, "# x\n")
	gitCmd(t, repo, "add", "wc/wc-ab2c-x.md")
	gitCmd(t, repo, "commit", "-m", "seed")
	gitCmd(t, repo, "mv", "wc/wc-ab2c-x.md", "wc/wc-ab2c-y.md")

	dir := filepath.Join(repo, "wc")
	paths, err := DirtyPaths(ctx, repo, dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range paths {
		found[filepath.Base(p)] = true
	}
	if !found["wc-ab2c-y.md"] {
		t.Errorf("a rename must report its destination path, got %v", paths)
	}
	if found["wc-ab2c-x.md"] {
		t.Errorf("the rename source field must be consumed, not reported, got %v", paths)
	}
}

func TestMidRebaseDetection(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	if MidRebase(ctx, repo) {
		t.Error("a fresh repo is not mid-rebase")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-apply"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !MidRebase(ctx, repo) {
		t.Error("a rebase-apply dir should read as mid-rebase")
	}
}

func TestDirtyPathsScopedAndExpanded(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	// An entirely-untracked scope dir must expand to individual files, and files
	// outside the scoped dir must not appear.
	write(t, filepath.Join(repo, "wc", "pj.cue"), "name: \"wc\"\n")
	write(t, filepath.Join(repo, "wc", "wc-ab2c-x.md"), "# x\n")
	write(t, filepath.Join(repo, "other", "unrelated.md"), "# y\n")

	dir := filepath.Join(repo, "wc")
	paths, err := DirtyPaths(ctx, repo, dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range paths {
		found[filepath.Base(p)] = true
	}
	if !found["pj.cue"] || !found["wc-ab2c-x.md"] {
		t.Errorf("scoped dirty paths should list the scope's files, got %v", paths)
	}
	if found["unrelated.md"] {
		t.Errorf("dirty paths must stay scoped to dir, got %v", paths)
	}
}
