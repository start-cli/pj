package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitCmdEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// conflictRepo builds a repo paused on a rebase with one 3-way-conflicted file. It
// returns the repo, the conflicted path, the main-tip and feature-tip SHAs, and the
// content each branch committed, so tests can assert the stage-to-side mapping.
func conflictRepo(t *testing.T) (repo, path, mainTip, featureTip, mainBody, featureBody string) {
	t.Helper()
	repo = newRepo(t)
	path = "wc/wc-ab2c-x.md"
	abs := filepath.Join(repo, path)

	write(t, abs, "a\nCOMMON\nc\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	featureBody = "a\nFEATURE\nc\n"
	write(t, abs, featureBody)
	gitCmd(t, repo, "commit", "-am", "feature edit")
	featureTip = gitOut(t, repo, "rev-parse", "HEAD")

	gitCmd(t, repo, "checkout", main)
	mainBody = "a\nMAIN\nc\n"
	write(t, abs, mainBody)
	gitCmd(t, repo, "commit", "-am", "main edit")
	mainTip = gitOut(t, repo, "rev-parse", "HEAD")

	gitCmd(t, repo, "checkout", "feature")
	// Rebase feature onto main: replaying feature's commit conflicts.
	cmd := exec.Command("git", "rebase", main)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	_ = cmd.Run() // expected non-zero: paused on conflict
	if !MidRebase(context.Background(), repo) {
		t.Fatal("setup: expected a paused rebase")
	}
	return repo, path, mainTip, featureTip, mainBody, featureBody
}

func TestConflictStagesAndShowMapping(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo, path, _, _, mainBody, featureBody := conflictRepo(t)

	stages, err := ConflictStages(ctx, repo, path)
	if err != nil {
		t.Fatal(err)
	}
	if !stages.Base || !stages.Ours || !stages.Theirs {
		t.Fatalf("3-way conflict must list all stages: %+v", stages)
	}

	// Stage :2 is the rebase target (main); stage :3 is the commit being replayed
	// (feature). This pins the inverted stage-to-side mapping.
	ours, err := ShowStage(ctx, repo, 2, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(ours) != mainBody {
		t.Errorf("stage :2 = %q, want main body %q", ours, mainBody)
	}
	theirs, err := ShowStage(ctx, repo, 3, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(theirs) != featureBody {
		t.Errorf("stage :3 = %q, want feature body %q", theirs, featureBody)
	}
}

func TestConflictStagesAddAdd(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	seed := filepath.Join(repo, "seed.txt")
	write(t, seed, "seed\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "seed")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	path := "wc/wc-ab2c-x.md"
	abs := filepath.Join(repo, path)
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, abs, "feature add\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "feature add")

	gitCmd(t, repo, "checkout", main)
	write(t, abs, "main add\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "main add")

	gitCmd(t, repo, "checkout", "feature")
	cmd := exec.Command("git", "rebase", main)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	_ = cmd.Run()

	stages, err := ConflictStages(ctx, repo, path)
	if err != nil {
		t.Fatal(err)
	}
	if stages.Base {
		t.Error("add/add must have no base stage")
	}
	if !stages.Ours || !stages.Theirs {
		t.Errorf("add/add must have both side stages: %+v", stages)
	}
}

func TestConflictStagesDeleteModify(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := "wc/wc-ab2c-x.md"
	abs := filepath.Join(repo, path)
	write(t, abs, "base\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "commit", "-am", "delete")

	gitCmd(t, repo, "checkout", main)
	write(t, abs, "modified\n")
	gitCmd(t, repo, "commit", "-am", "modify")

	gitCmd(t, repo, "checkout", "feature")
	cmd := exec.Command("git", "rebase", main)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	_ = cmd.Run()

	stages, err := ConflictStages(ctx, repo, path)
	if err != nil {
		t.Fatal(err)
	}
	if !stages.Base {
		t.Error("delete/modify must keep a base stage")
	}
	// Exactly one side's stage is absent (the deleting side).
	if stages.Ours == stages.Theirs {
		t.Errorf("delete/modify must have exactly one side stage: %+v", stages)
	}
}

func TestMergeBlobsCleanAndConflict(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	base := []byte("a\nb\nc\nd\ne\nf\ng\n")
	ours := []byte("X\nb\nc\nd\ne\nf\ng\n")   // first line
	theirs := []byte("a\nb\nc\nd\ne\nf\nY\n") // last line
	merged, conflicted, err := MergeBlobs(ctx, base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted {
		t.Errorf("well-separated edits must merge clean: %q", merged)
	}
	if string(merged) != "X\nb\nc\nd\ne\nf\nY\n" {
		t.Errorf("clean merge = %q", merged)
	}

	out, conflicted, err := MergeBlobs(ctx, []byte("a\nb\nc\n"), []byte("a\nX\nc\n"), []byte("a\nZ\nc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Error("overlapping edits must conflict")
	}
	if !strings.Contains(string(out), "<<<<<<<") {
		t.Errorf("conflicted merge must carry markers: %q", out)
	}
}

func TestListFiles(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "wc", "wc-ab2c-x.md"), "x\n")
	write(t, filepath.Join(repo, "wc", "wc-cd3e-y.md"), "y\n")
	write(t, filepath.Join(repo, "other", "z.md"), "z\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "files")

	files, err := ListFiles(ctx, repo, "wc")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files under wc, got %v", files)
	}
}

func TestAuthorDatePerFile(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := "wc/wc-ab2c-x.md"
	write(t, filepath.Join(repo, path), "v1\n")
	gitCmd(t, repo, "add", "-A")
	when := "2021-05-05T10:00:00+00:00"
	gitCmdEnv(t, repo, []string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, "commit", "-m", "v1")

	// A later, unrelated commit that does not touch path must not move path's date.
	write(t, filepath.Join(repo, "other.txt"), "u\n")
	gitCmd(t, repo, "add", "-A")
	gitCmdEnv(t, repo, []string{"GIT_AUTHOR_DATE=2099-01-01T00:00:00+00:00", "GIT_COMMITTER_DATE=2099-01-01T00:00:00+00:00"}, "commit", "-m", "unrelated")

	got, err := AuthorDate(ctx, repo, "HEAD", path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339, when)
	if !got.Equal(want) {
		t.Errorf("author date = %v, want %v (per-file, not branch tip)", got, want)
	}
}

func TestRebaseSidesResolvesHeadAndRebaseHead(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo, _, mainTip, featureTip, _, _ := conflictRepo(t)

	head, rebaseHead, err := RebaseSides(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if head != mainTip {
		t.Errorf("HEAD (stage :2 side) = %s, want main tip %s", head, mainTip)
	}
	if rebaseHead != featureTip {
		t.Errorf("REBASE_HEAD (stage :3 side) = %s, want feature tip %s", rebaseHead, featureTip)
	}
}

func TestRebaseAbortRestores(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo, _, _, _, _, _ := conflictRepo(t)

	if err := RebaseAbort(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if MidRebase(ctx, repo) {
		t.Error("abort must leave the rebase state clean")
	}
}

func TestRebasePausesOnConflict(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	path := filepath.Join(repo, "f.txt")
	write(t, path, "a\nCOMMON\nc\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")
	main := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, path, "a\nFEATURE\nc\n")
	gitCmd(t, repo, "commit", "-am", "feature")
	gitCmd(t, repo, "checkout", main)
	write(t, path, "a\nMAIN\nc\n")
	gitCmd(t, repo, "commit", "-am", "main")
	gitCmd(t, repo, "checkout", "feature")

	paused, err := Rebase(ctx, repo, main)
	if err != nil {
		t.Fatalf("rebase must pause, not error: %v", err)
	}
	if !paused {
		t.Fatal("rebase must report paused on conflict")
	}
	if err := RebaseAbort(ctx, repo); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPushUnpushed(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	remote := t.TempDir()
	gitCmd(t, remote, "init", "--bare")

	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "one\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "one")
	gitCmd(t, repo, "remote", "add", "origin", remote)
	gitCmd(t, repo, "push", "-u", "origin", "HEAD")

	n, err := UnpushedCount(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("unpushed after push = %d, want 0", n)
	}

	write(t, filepath.Join(repo, "f.txt"), "two\n")
	gitCmd(t, repo, "commit", "-am", "two")
	if n, _ := UnpushedCount(ctx, repo); n != 1 {
		t.Errorf("unpushed after new commit = %d, want 1", n)
	}

	if err := Push(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if n, _ := UnpushedCount(ctx, repo); n != 0 {
		t.Errorf("unpushed after Push = %d, want 0", n)
	}
	if err := Fetch(ctx, repo); err != nil {
		t.Fatal(err)
	}
}

func TestDirtyEntriesCarryCode(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	repo := newRepo(t)
	tracked := "wc/wc-ab2c-x.md"
	write(t, filepath.Join(repo, tracked), "v1\n")
	gitCmd(t, repo, "add", "-A")
	gitCmd(t, repo, "commit", "-m", "base")

	write(t, filepath.Join(repo, tracked), "v2\n")               // modified
	write(t, filepath.Join(repo, "wc", "wc-cd3e-new.md"), "n\n") // untracked add

	entries, err := DirtyEntries(ctx, repo, "wc")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	for _, e := range entries {
		codes[filepath.Base(e.Path)] = e.Code
	}
	if !strings.Contains(codes["wc-ab2c-x.md"], "M") {
		t.Errorf("modified file code = %q, want an M", codes["wc-ab2c-x.md"])
	}
	if codes["wc-cd3e-new.md"] != "??" {
		t.Errorf("untracked file code = %q, want ??", codes["wc-cd3e-new.md"])
	}
}
