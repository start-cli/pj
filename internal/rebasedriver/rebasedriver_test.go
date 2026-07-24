package rebasedriver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/git"
	"github.com/start-cli/pj/internal/scopeconfig"
)

// --- git fixture primitives ---

func requireGit(t *testing.T) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
}

func gitRun(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, nil, "init")
	gitRun(t, repo, nil, "config", "user.email", "a@b.c")
	gitRun(t, repo, nil, "config", "user.name", "pj-test")
	gitRun(t, repo, nil, "config", "commit.gpgsign", "false")
	return repo
}

func writeF(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dateEnv(iso string) []string {
	if iso == "" {
		return nil
	}
	return []string{"GIT_AUTHOR_DATE=" + iso, "GIT_COMMITTER_DATE=" + iso}
}

func commitAll(t *testing.T, repo, msg, date string) string {
	t.Helper()
	gitRun(t, repo, nil, "add", "-A")
	gitRun(t, repo, dateEnv(date), "commit", "-m", msg)
	return gitCapture(t, repo, "rev-parse", "HEAD")
}

// startRebase runs `git rebase onto` non-interactively and reports whether it paused.
func startRebase(t *testing.T, repo, onto string) bool {
	t.Helper()
	cmd := exec.Command("git", "rebase", onto)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	_ = cmd.Run()
	return git.MidRebase(context.Background(), repo)
}

func freshLoader() SchemaLoader {
	cctx := cuecontext.New()
	return func(dir string) (*scopeconfig.Schema, error) {
		s, _, err := scopeconfig.LoadWithClosure(cctx, dir)
		return s, err
	}
}

func pjcue(extra string) string {
	return "name: \"wc\"\nautoCommit: true\n" + extra
}

func proj(fm, body string) string { return "---\n" + fm + "---\n" + body }

// setup builds a paused rebase over a single scope. cueBase is written on both branches
// unless cueMain differs. Each of base/main/feature is a whole project-file content for
// projRel (empty = the file is absent on that branch). It returns the fixture with the
// scope dir, the conflicted rel path, and the two side revs (main = ours/:2, feature =
// theirs/:3).
type fixture struct {
	repo, scopeDir, projRel, oursRev, theirsRev string
}

func setup(t *testing.T, cueBase, cueMain, base, main, feature, mainDate string) fixture {
	t.Helper()
	projRel := "wc/wc-ab2c-x.md"
	const baseDate = "2026-01-01T00:00:00Z"
	const featureDate = "2026-03-01T00:00:00Z"
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	cuePath := filepath.Join(scopeDir, "pj.cue")
	projPath := filepath.Join(repo, projRel)

	writeF(t, cuePath, cueBase)
	if base != "" {
		writeF(t, projPath, base)
	}
	commitAll(t, repo, "base", baseDate)
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	applyBranch(t, projPath, feature)
	featureTip := commitAll(t, repo, "feature", featureDate)

	gitRun(t, repo, nil, "checkout", mainBranch)
	if cueMain != "" {
		writeF(t, cuePath, cueMain)
	}
	applyBranch(t, projPath, main)
	mainTip := commitAll(t, repo, "main", mainDate)

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("setup: expected a paused rebase")
	}
	return fixture{repo: repo, scopeDir: scopeDir, projRel: projRel, oursRev: mainTip, theirsRev: featureTip}
}

// applyBranch writes content, or removes the file when content is empty (a deletion).
func applyBranch(t *testing.T, path, content string) {
	t.Helper()
	if content == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	writeF(t, path, content)
}

func (f fixture) conflict() Conflict {
	return Conflict{Path: f.projRel, ScopeDir: f.scopeDir, OursRev: f.oursRev, TheirsRev: f.theirsRev}
}

func resolve(t *testing.T, f fixture) Outcome {
	t.Helper()
	d := New(f.repo, freshLoader())
	out, err := d.Resolve(context.Background(), f.conflict())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return out
}

func parseFile(t *testing.T, path string) *frontmatter.Model {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	interior, _, present := frontmatter.Split(raw)
	if !present {
		t.Fatalf("no frontmatter fence in %s", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// --- tests ---

// Both sides change frontmatter and the bodies merge cleanly: the driver stages a fully
// clean, parseable file — where the working-tree file git left would not parse.
func TestCleanTwoSidedFrontmatterStaged(t *testing.T) {
	requireGit(t)
	fmBase := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: BASE\ncreated: 2026-01-01T00:00:00Z\n"
	fmMain := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: MAIN\ncreated: 2026-01-01T00:00:00Z\n"
	fmFeat := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: FEAT\ncreated: 2026-01-01T00:00:00Z\n"
	body := "L1\nL2\nL3\nL4\nL5\nL6\nL7\n"
	mainBody := "M1\nL2\nL3\nL4\nL5\nL6\nL7\n" // first line
	featBody := "L1\nL2\nL3\nL4\nL5\nL6\nF7\n" // last line

	f := setup(t,
		pjcue(""), "",
		proj(fmBase, body), proj(fmMain, mainBody), proj(fmFeat, featBody),
		"2026-06-01T00:00:00Z")

	projPath := filepath.Join(f.repo, f.projRel)
	// The working-tree file git left does not parse cleanly (markers in the frontmatter).
	rawLeft, _ := os.ReadFile(projPath)
	if interior, _, present := frontmatter.Split(rawLeft); present {
		if _, err := frontmatter.Parse(interior); err == nil {
			t.Fatal("precondition: git's working-tree file should not have parseable frontmatter")
		}
	}

	out := resolve(t, f)
	if out.Class != ClassClean || !out.Staged {
		t.Fatalf("want clean+staged, got class=%v staged=%v", out.Class, out.Staged)
	}
	m := parseFile(t, projPath)
	if m.Summary != "MAIN" { // ours (main) is newer → LWW
		t.Errorf("summary = %q, want MAIN (LWW)", m.Summary)
	}
	raw, _ := os.ReadFile(projPath)
	if !strings.Contains(string(raw), "M1") || !strings.Contains(string(raw), "F7") {
		t.Errorf("both clean body edits must survive: %q", raw)
	}
	if u, _ := git.ConflictStages(context.Background(), f.repo, f.projRel); u.Any() {
		t.Error("staged clean file must no longer be unmerged")
	}
}

// A body-only conflict: clean field-merged frontmatter, markers only in the body, left
// unstaged, reported as a body-conflict handoff.
func TestBodyConflictUnstaged(t *testing.T) {
	requireGit(t)
	fmBase := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\n"
	fmMain := "id: wc-ab2c\nstatus: done\norder: \"a0\"\n" // one-side status change
	base := proj(fmBase, "a\nCOMMON\nc\n")
	main := proj(fmMain, "a\nMAIN\nc\n")
	feat := proj(fmBase, "a\nFEAT\nc\n")

	f := setup(t, pjcue(""), "", base, main, feat,
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassBodyConflict || out.Staged {
		t.Fatalf("want body-conflict unstaged, got class=%v staged=%v", out.Class, out.Staged)
	}
	projPath := filepath.Join(f.repo, f.projRel)
	m := parseFile(t, projPath)
	if m.Status != "done" {
		t.Errorf("frontmatter must field-merge cleanly (status done): got %q", m.Status)
	}
	raw, _ := os.ReadFile(projPath)
	if !strings.Contains(string(raw), "<<<<<<<") {
		t.Error("body must carry conflict markers")
	}
}

// Delete/edit: the upstream side (:2 / ours) deletes, the replayed side edits. The driver
// reports the deleting side — proving the :2=ours mapping — stages nothing, and returns
// the surviving status.
func TestDeleteEditHandoffMapping(t *testing.T) {
	requireGit(t)
	base := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "body\n")
	feat := proj("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "edited\n")

	// main (ours/:2) deletes; feature (theirs/:3) edits.
	f := setup(t, pjcue(""), "", base, "", feat,
		"2026-02-01T00:00:00Z")

	before, _ := os.ReadFile(filepath.Join(f.repo, f.projRel))
	out := resolve(t, f)
	if out.Class != ClassDeleteEdit || out.Staged {
		t.Fatalf("want delete/edit unstaged, got class=%v staged=%v", out.Class, out.Staged)
	}
	if out.DeleteEdit.Deleted.String() != "ours" {
		t.Errorf("upstream side deleted; want ours, got %s", out.DeleteEdit.Deleted)
	}
	if out.DeleteEdit.SurvivingStatus != "done" {
		t.Errorf("surviving status = %q, want done", out.DeleteEdit.SurvivingStatus)
	}
	after, _ := os.ReadFile(filepath.Join(f.repo, f.projRel))
	if string(before) != string(after) {
		t.Error("delete/edit must not rewrite the working-tree file")
	}
}

// A U21 fail-closed merge (both-sides immutable disagreement) comes back as a fail-closed
// handoff naming the key, unstaged, distinct from an operational error.
func TestFailClosedImmutableDisagreement(t *testing.T) {
	requireGit(t)
	base := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	main := proj("id: wc-zzzz\nstatus: todo\norder: \"a0\"\n", "b\n")
	feat := proj("id: wc-yyyy\nstatus: todo\norder: \"a0\"\n", "b\n")

	f := setup(t, pjcue(""), "", base, main, feat,
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassFailClosed || out.Staged {
		t.Fatalf("want fail-closed unstaged, got class=%v staged=%v", out.Class, out.Staged)
	}
	if out.FailClosed.Key != frontmatter.KeyID {
		t.Errorf("fail-closed key = %q, want id", out.FailClosed.Key)
	}
}

// A same-id add/add is resolved by renaming one side: two files written and staged, the
// rename pair returned — never a field-merge.
func TestAddAddRenameTwoFiles(t *testing.T) {
	requireGit(t)
	// Both branches add the same path with the same id; main is older (kept), feature
	// newer (renamed).
	mainFile := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n", "MAIN body\n")
	featFile := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-06-01T00:00:00Z\n", "FEAT body\n")

	f := setup(t, pjcue(""), "", "", mainFile, featFile,
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassRename || !out.Staged {
		t.Fatalf("want rename+staged, got class=%v staged=%v", out.Class, out.Staged)
	}
	if out.Rename.OldID != "wc-ab2c" || !strings.HasPrefix(out.Rename.NewID, "wc-ab2c") || out.Rename.NewID == "wc-ab2c" {
		t.Errorf("rename ids = %+v", out.Rename)
	}
	keep := parseFile(t, filepath.Join(f.repo, out.Rename.KeepPath))
	if keep.ID != "wc-ab2c" {
		t.Errorf("keep side id = %q, want wc-ab2c", keep.ID)
	}
	newM := parseFile(t, filepath.Join(f.repo, out.Rename.NewPath))
	if newM.ID != out.Rename.NewID {
		t.Errorf("renamed file id = %q, want %q", newM.ID, out.Rename.NewID)
	}
	// The kept side is main (older); the renamed loser carries feature's body.
	keepRaw, _ := os.ReadFile(filepath.Join(f.repo, out.Rename.KeepPath))
	newRaw, _ := os.ReadFile(filepath.Join(f.repo, out.Rename.NewPath))
	if !strings.Contains(string(keepRaw), "MAIN body") {
		t.Errorf("keep path must hold the older (main) body")
	}
	if !strings.Contains(string(newRaw), "FEAT body") {
		t.Errorf("new path must hold the renamed (feature) body")
	}
}

// Per-file author dates, not branch-tip dates: a newer unrelated commit on one branch
// must not decide another project's fields.
func TestPerFileAuthorDateNotBranchTip(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "pj.cue"), pjcue(""))
	aPath := filepath.Join(repo, "wc", "wc-ab2c-a.md")
	bPath := filepath.Join(repo, "wc", "wc-cd3e-b.md")
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: BASE\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	writeF(t, bPath, proj("id: wc-cd3e\nstatus: todo\norder: \"a0\"\n", "y\n"))
	commitAll(t, repo, "base", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	// feature: edit A's summary at D2 (the later of the two A-edits).
	gitRun(t, repo, nil, "checkout", "-b", "feature")
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: FEAT\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	featTip := commitAll(t, repo, "feature A edit", "2026-05-01T00:00:00Z")

	// main: edit A's summary at D1 (earlier than feature's A edit) ...
	gitRun(t, repo, nil, "checkout", mainBranch)
	writeF(t, aPath, proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\nsummary: MAIN\ncreated: 2026-01-01T00:00:00Z\n", "x\n"))
	commitAll(t, repo, "main A edit", "2026-03-01T00:00:00Z")
	// ... then an unrelated later commit touching only B at D3 (the branch tip).
	writeF(t, bPath, proj("id: wc-cd3e\nstatus: done\norder: \"a0\"\n", "y\n"))
	mainTip := commitAll(t, repo, "main B edit", "2026-09-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}

	d := New(repo, freshLoader())
	out, err := d.Resolve(context.Background(), Conflict{
		Path: "wc/wc-ab2c-a.md", ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := parseFile(t, aPath)
	// Per-file: ours(A)=D1 < theirs(A)=D2 → theirs wins → FEAT. Branch-tip would give
	// ours=D3 > theirs=D2 → MAIN, the wrong answer.
	if m.Summary != "FEAT" {
		t.Errorf("summary = %q, want FEAT (per-file date, not branch tip)", m.Summary)
	}
	_ = out
}

// The driver types the merge from the on-disk schema at driver time: a key only the
// incoming pj.cue declares as strings is set-merged, not LWW'd.
func TestSchemaFromOnDiskStrings(t *testing.T) {
	requireGit(t)
	fmBase := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nreviewers: [alice]\n"
	fmMain := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nreviewers: [alice, bob]\n"
	fmFeat := "id: wc-ab2c\nstatus: todo\norder: \"a0\"\nreviewers: [alice, carol]\n"
	// Only main's pj.cue declares reviewers as a strings field; it auto-merges into the
	// on-disk pj.cue the driver evaluates.
	cueMain := pjcue("fields: reviewers: type: \"strings\"\n")

	f := setup(t, pjcue(""), cueMain,
		proj(fmBase, "b\n"), proj(fmMain, "b\n"), proj(fmFeat, "b\n"),
		"2026-02-01T00:00:00Z")

	out := resolve(t, f)
	if out.Class != ClassClean {
		t.Fatalf("want clean, got %v", out.Class)
	}
	m := parseFile(t, filepath.Join(f.repo, f.projRel))
	var reviewers []string
	for _, fld := range m.Custom {
		if fld.Key == "reviewers" {
			if list, ok := fld.Value.([]any); ok {
				for _, e := range list {
					reviewers = append(reviewers, e.(string))
				}
			}
		}
	}
	if strings.Join(reviewers, ",") != "alice,bob,carol" {
		t.Errorf("strings field must set-merge from on-disk schema: got %v", reviewers)
	}
}

// Across two add/add conflicts in one rebase, the id minted for the first is in the
// occupied set the second uses, so the extensions never collide.
func TestOccupiedAccumulatesAcrossAddAdds(t *testing.T) {
	requireGit(t)
	repo := newRepo(t)
	scopeDir := filepath.Join(repo, "wc")
	writeF(t, filepath.Join(scopeDir, "pj.cue"), pjcue(""))
	writeF(t, filepath.Join(repo, "seed.txt"), "seed\n")
	commitAll(t, repo, "seed", "2026-01-01T00:00:00Z")
	mainBranch := gitCapture(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

	pathA := "wc/wc-ab2c-alpha.md"
	pathB := "wc/wc-ab2c-beta.md"
	fm := func(created string) string {
		return "id: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: " + created + "\n"
	}

	gitRun(t, repo, nil, "checkout", "-b", "feature")
	writeF(t, filepath.Join(repo, pathA), proj(fm("2026-06-01T00:00:00Z"), "featA\n"))
	writeF(t, filepath.Join(repo, pathB), proj(fm("2026-06-01T00:00:00Z"), "featB\n"))
	featTip := commitAll(t, repo, "feature adds", "2026-06-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", mainBranch)
	writeF(t, filepath.Join(repo, pathA), proj(fm("2026-01-01T00:00:00Z"), "mainA\n"))
	writeF(t, filepath.Join(repo, pathB), proj(fm("2026-01-01T00:00:00Z"), "mainB\n"))
	mainTip := commitAll(t, repo, "main adds", "2026-01-01T00:00:00Z")

	gitRun(t, repo, nil, "checkout", "feature")
	if !startRebase(t, repo, mainBranch) {
		t.Fatal("expected paused rebase")
	}

	d := New(repo, freshLoader())
	ctx := context.Background()
	outA, err := d.Resolve(ctx, Conflict{Path: pathA, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip})
	if err != nil {
		t.Fatal(err)
	}
	outB, err := d.Resolve(ctx, Conflict{Path: pathB, ScopeDir: scopeDir, OursRev: mainTip, TheirsRev: featTip})
	if err != nil {
		t.Fatal(err)
	}
	if outA.Class != ClassRename || outB.Class != ClassRename {
		t.Fatalf("both must be renames: %v %v", outA.Class, outB.Class)
	}
	if outA.Rename.NewID == outB.Rename.NewID {
		t.Errorf("second extension must not collide with the first: both %s", outA.Rename.NewID)
	}
}

// An operational fault (unreadable scope schema) is an ordinary error, distinct from a
// fail-closed data condition; the driver stages nothing.
func TestOperationalErrorDistinctFromFailClosed(t *testing.T) {
	requireGit(t)
	base := proj("id: wc-ab2c\nstatus: todo\norder: \"a0\"\n", "b\n")
	main := proj("id: wc-ab2c\nstatus: done\norder: \"a0\"\n", "b\n")
	feat := proj("id: wc-ab2c\nstatus: in-progress\norder: \"a0\"\n", "b\n")
	f := setup(t, pjcue(""), "", base, main, feat,
		"2026-02-01T00:00:00Z")

	d := New(f.repo, func(string) (*scopeconfig.Schema, error) {
		return nil, os.ErrPermission // simulate an unreadable schema
	})
	_, err := d.Resolve(context.Background(), f.conflict())
	if err == nil {
		t.Fatal("an unreadable schema must be an operational error, not a fail-closed outcome")
	}
}
