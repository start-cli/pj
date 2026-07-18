package gitroot

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoRootInsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := exec.Command("mkdir", "-p", sub).Run(); err != nil {
		t.Fatal(err)
	}

	root, ok := RepoRoot(sub)
	if !ok {
		t.Fatal("expected a git-root")
	}
	// macOS /tmp is a symlink; compare the resolved forms.
	wantResolved, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("RepoRoot=%q want %q", gotResolved, wantResolved)
	}
}

func TestRepoRootOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	if root, ok := RepoRoot(dir); ok {
		t.Errorf("expected no git-root outside a repo, got %q", root)
	}
}

func TestRepoRootMissingDir(t *testing.T) {
	if root, ok := RepoRoot(filepath.Join(t.TempDir(), "does-not-exist")); ok {
		t.Errorf("expected no git-root for a missing dir, got %q", root)
	}
}

func TestRepoRootForNewMissingDescendant(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// A not-yet-created dir several levels below the repo derives the repo root
	// from its nearest existing ancestor.
	missing := filepath.Join(repo, "a", "b", "c")
	root, ok := RepoRootForNew(missing)
	if !ok {
		t.Fatal("expected a git-root for a missing descendant of a repo")
	}
	wantResolved, _ := filepath.EvalSymlinks(repo)
	gotResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("RepoRootForNew=%q want %q", gotResolved, wantResolved)
	}
}

func TestRepoRootForNewOutsideRepo(t *testing.T) {
	// A missing dir whose existing ancestors are not a repo yields no git-root.
	if root, ok := RepoRootForNew(filepath.Join(t.TempDir(), "x", "y")); ok {
		t.Errorf("expected no git-root, got %q", root)
	}
}
