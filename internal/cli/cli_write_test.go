package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/start-cli/pj/internal/git"
)

// requireGit skips a test when the git binary is unavailable — the git-backed modes
// cannot be exercised without it, but pj's pure-file behaviour is tested separately.
func requireGit(t *testing.T) {
	t.Helper()
	if !git.Available() {
		t.Skip("git not on PATH")
	}
}

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitLog returns the repo's one-line commit subjects, newest first.
func gitLog(t *testing.T, repo string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%s")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	return lines(string(out))
}

// initGitScope creates a git repo, registers a scope dir inside it with the given
// autoCommit, and returns (scopeDir, repoRoot). Commit signing is disabled locally so
// a signing global config cannot make self-commit flaky.
func initGitScope(t *testing.T, app *App, name string, autoCommit bool) (string, string) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "a@b.c")
	runGit(t, repo, "config", "user.name", "pj-test")
	runGit(t, repo, "config", "commit.gpgsign", "false")
	dir := filepath.Join(repo, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"scope", "init", dir, "--name", name}
	if autoCommit {
		args = append(args, "--auto-commit")
	}
	if _, _, err := run(t, app, args...); err != nil {
		t.Fatalf("init git scope %s: %v", name, err)
	}
	return dir, repo
}

// createID runs create and returns the printed path and the derived full id.
func createID(t *testing.T, app *App, scope string, args ...string) (string, string) {
	t.Helper()
	out, _, err := run(t, app, append([]string{"create"}, append(args, "--scope", scope)...)...)
	if err != nil {
		t.Fatalf("create %v: %v", args, err)
	}
	path := strings.TrimSpace(out)
	base := filepath.Base(path)
	fields := strings.SplitN(base, "-", 3)
	return path, fields[0] + "-" + fields[1]
}

func fmValue(t *testing.T, path, key string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines(string(data)) {
		if strings.HasPrefix(line, key+":") {
			v := strings.TrimSpace(strings.TrimPrefix(line, key+":"))
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func TestCreateScaffoldPlainFiles(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	path, id := createID(t, app, "wc", "Network redesign")
	if !strings.HasSuffix(path, ".md") || !filepath.IsAbs(path) {
		t.Fatalf("create should print an absolute .md path, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if fmValue(t, path, "id") != id {
		t.Errorf("frontmatter id = %q want %q", fmValue(t, path, "id"), id)
	}
	if got := fmValue(t, path, "status"); got != "draft" {
		t.Errorf("default status = %q want draft", got)
	}
	if got := fmValue(t, path, "order"); got != "a0" {
		t.Errorf("first order = %q want a0", got)
	}
	if fmValue(t, path, "created") == "" {
		t.Error("created must be set")
	}
	if !strings.Contains(body, "\norder: \"a0\"\n") {
		t.Errorf("order must be quoted in the file: %q", body)
	}
	if !strings.HasSuffix(body, "# Network redesign\n") {
		t.Errorf("body must be a single H1 from the title: %q", body)
	}
	// The frozen slug rides the filename.
	if !strings.HasSuffix(path, id+"-network-redesign.md") {
		t.Errorf("filename slug not frozen from title: %q", path)
	}
}

func TestCreateEmptyTitleAndUnknownStatus(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	if _, _, err := run(t, app, "create", "   ", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("empty title should exit 2, got %v", err)
	}
	if _, _, err := run(t, app, "create", "X", "nope", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("unknown status should exit 2, got %v", err)
	}
}

func TestCreateAppendsAfterScopeWideMax(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	pA, _ := createID(t, app, "wc", "A")
	pB, _ := createID(t, app, "wc", "B")
	pC, _ := createID(t, app, "wc", "C")
	a, b, c := fmValue(t, pA, "order"), fmValue(t, pB, "order"), fmValue(t, pC, "order")
	if a >= b || b >= c {
		t.Errorf("append orders must strictly increase, got %q %q %q", a, b, c)
	}
}

func TestCreateTerminalUnderArchive(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	out, errOut, err := run(t, app, "create", "Already done", "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("terminal create: %v", err)
	}
	path := strings.TrimSpace(out)
	if !strings.Contains(path, string(os.PathSeparator)+"archive"+string(os.PathSeparator)) {
		t.Errorf("terminal create must live under archive/, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("terminal scaffold file must exist: %v", err)
	}
	// A terse durability note (not a closed token) rides stderr.
	if !strings.Contains(errOut, "not git-durable") {
		t.Errorf("terminal create should ride a scaffold-durability note, got %q", errOut)
	}
}

func TestStatusTerminalBoundaryMovePlainFiles(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")

	if _, _, err := run(t, app, "status", id, "todo"); err != nil {
		t.Fatalf("status todo: %v", err)
	}
	out, _, err := run(t, app, "status", id, "done")
	if err != nil {
		t.Fatalf("status done: %v", err)
	}
	movedPath := strings.TrimSpace(out)
	if filepath.Dir(movedPath) != filepath.Join(dir, "archive") {
		t.Errorf("done should print the post-move archive path, got %q", movedPath)
	}
	if _, err := os.Stat(movedPath); err != nil {
		t.Errorf("moved file must exist under archive/: %v", err)
	}
	// The old root location is gone.
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(movedPath))); !os.IsNotExist(err) {
		t.Errorf("old root path must be removed after the terminal move")
	}
	if got := fmValue(t, movedPath, "status"); got != "done" {
		t.Errorf("status not rewritten: %q", got)
	}

	// Reopening moves it back to the dir root.
	out, _, err = run(t, app, "status", id, "todo")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened := strings.TrimSpace(out)
	if filepath.Dir(reopened) != dir {
		t.Errorf("reopen should move the file back to the dir root, got %q", reopened)
	}
}

func TestStatusRefusals(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")

	// Unknown status → exit 2.
	if _, _, err := run(t, app, "status", id, "nope"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("unknown status should exit 2, got %v", err)
	}
	// Malformed id → exit 2.
	if _, _, err := run(t, app, "status", "bad!", "done", "--scope", "wc"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("malformed id should exit 2, got %v", err)
	}
	// Well-formed unknown id → generic non-zero.
	if _, _, err := run(t, app, "status", "wc-zzzz", "done"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("unknown well-formed id should exit 1, got %v", err)
	}
	// parse_error quarantine → refuse, no write.
	bad := "---\nid: wc-abcd\nstatus: [unterminated\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-abcd-x.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(t, app, "status", "wc-abcd", "done")
	if ExitCodeFromError(err) != exitFailure {
		t.Errorf("parse_error status should be non-zero, got %v", err)
	}
	if !strings.Contains(err.Error()+errOut, "parse_error:") {
		t.Errorf("expected parse_error token, got err=%v stderr=%q", err, errOut)
	}
	// Duplicate id → refuse, no write to either side.
	first := filepath.Join(dir, "wc-"+strings.SplitN(id, "-", 2)[1]+"-work.md")
	if _, err := os.Stat(first); err == nil {
		if err := copyFile(first, filepath.Join(dir, id+"-dup.md")); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := run(t, app, "status", id, "done"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("duplicate id should refuse non-zero, got %v", err)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func TestReorderPlacements(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	pA, idA := createID(t, app, "wc", "A")
	pB, idB := createID(t, app, "wc", "B")
	pC, idC := createID(t, app, "wc", "C")

	// --first: C sorts before everyone.
	if _, _, err := run(t, app, "reorder", idC, "--first"); err != nil {
		t.Fatalf("reorder --first: %v", err)
	}
	if fmValue(t, pC, "order") >= fmValue(t, pA, "order") {
		t.Errorf("--first should place C before A")
	}
	// --last: A sorts after everyone.
	if _, _, err := run(t, app, "reorder", idA, "--last"); err != nil {
		t.Fatalf("reorder --last: %v", err)
	}
	if fmValue(t, pA, "order") <= fmValue(t, pB, "order") {
		t.Errorf("--last should place A after B")
	}
	// --before C: B lands before C.
	if _, _, err := run(t, app, "reorder", idB, "--before", idC); err != nil {
		t.Fatalf("reorder --before: %v", err)
	}
	if fmValue(t, pB, "order") >= fmValue(t, pC, "order") {
		t.Errorf("--before C should place B before C")
	}
	// --after C: B lands after C (and after A, the current back).
	if _, _, err := run(t, app, "reorder", idB, "--after", idC); err != nil {
		t.Fatalf("reorder --after: %v", err)
	}
	if fmValue(t, pB, "order") <= fmValue(t, pC, "order") {
		t.Errorf("--after C should place B after C")
	}
}

func TestReorderArgErrors(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "A")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no destination", []string{"reorder", id}, exitUsage},
		{"two destinations", []string{"reorder", id, "--first", "--last"}, exitUsage},
		{"malformed neighbour", []string{"reorder", id, "--before", "bad!"}, exitUsage},
		{"unknown neighbour", []string{"reorder", id, "--before", "wc-zzzz"}, exitFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != c.want {
				t.Errorf("exit = %d want %d (err=%v)", got, c.want, err)
			}
		})
	}
}

func TestClaimWritesInProgress(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addProject(t, dir, "wc-de34", "two", "todo", "a1", "# Two\n", false, "")

	out, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	claimed := strings.TrimSpace(out)
	if !strings.HasSuffix(claimed, "wc-ab2c-one.md") {
		t.Errorf("claim should take the first todo, got %q", claimed)
	}
	if got := fmValue(t, claimed, "status"); got != "in-progress" {
		t.Errorf("claim must set in-progress, got %q", got)
	}
	// The next claim serialises and takes the next todo — never the same id twice.
	out2, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if strings.TrimSpace(out2) == claimed {
		t.Errorf("a second claim must not hand off the same id")
	}
	if !strings.HasSuffix(strings.TrimSpace(out2), "wc-de34-two.md") {
		t.Errorf("second claim should take de34, got %q", out2)
	}
	// Now the queue is empty: non-zero, no path.
	out3, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err == nil || out3 != "" {
		t.Errorf("empty claim queue should be non-zero with no path: out=%q err=%v", out3, err)
	}
}

func TestClaimSkipsParseErrorCandidate(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// The first-by-order candidate is quarantined; claim skips it and takes the next.
	bad := "---\nid: wc-ab2c\nstatus: [x\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broke.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	addProject(t, dir, "wc-de34", "ok", "todo", "a1", "# Ok\n", false, "")

	out, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-de34-ok.md") {
		t.Errorf("claim should skip the parse_error candidate and take de34, got %q", out)
	}
}

func TestWriteVerbsRefuseUnparseableConfig(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	_, id := createID(t, app, "wc", "Work")
	addProject(t, dir, "wc-nb01", "n", "todo", "a5", "# N\n", false, "")

	// A schema-invalid but compilable config: the name reads (scope resolves), but the
	// schema fails, so writes refuse while reads stay available.
	bad := "name: \"wc\"\nautoCommit: false\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := [][]string{
		{"create", "New", "--scope", "wc"},
		{"status", id, "done"},
		{"reorder", id, "--first"},
		{"next", "--claim", "--scope", "wc"},
	}
	for _, args := range checks {
		_, errOut, err := run(t, app, args...)
		if ExitCodeFromError(err) != exitFailure {
			t.Errorf("%v under unparseable config should be non-zero, got %v", args, err)
		}
		if !strings.Contains(err.Error()+errOut, "config_unparseable:") {
			t.Errorf("%v should ride config_unparseable, got err=%v stderr=%q", args, err, errOut)
		}
	}

	// Reads of the same scope stay available.
	if _, _, err := run(t, app, "list", "--scope", "wc"); err != nil {
		t.Errorf("reads must stay available under an unusable config: %v", err)
	}
}

// The complete-state write verbs must ride the same reconcile integrity warnings a
// read (or next --claim) surfaces — a mutation is exactly when the pj doctor nudge
// matters. A quarantined sibling makes the scope carry a parse_error count; create,
// status, and reorder on a healthy row must each echo it on stderr.
func TestWriteVerbsSurfaceIntegrityWarnings(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-de34", "ok", "todo", "a1", "# Ok\n", false, "")
	bad := "---\nid: wc-ab2c\nstatus: [x\n---\n# broke\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broke.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := [][]string{
		{"create", "New thing", "--scope", "wc"},
		{"status", "wc-de34", "review"},
		{"reorder", "wc-de34", "--first"},
	}
	for _, args := range cases {
		_, errOut, err := run(t, app, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(errOut, "parse_error:") {
			t.Errorf("%v should ride the parse_error integrity warning, got stderr=%q", args, errOut)
		}
	}
}

func TestAutoCommitSelfCommitLifecycle(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)

	_, id := createID(t, app, "wc", "Work")
	// create never self-commits.
	if n := len(gitLog(t, repo)); n != 0 {
		t.Fatalf("create must not self-commit, got %d commits", n)
	}
	if _, _, err := run(t, app, "status", id, "todo"); err != nil {
		t.Fatalf("status todo: %v", err)
	}
	out, _, err := run(t, app, "status", id, "done")
	if err != nil {
		t.Fatalf("status done: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) != 2 || log[0] != "pj: "+id+" -> done" || log[1] != "pj: "+id+" -> todo" {
		t.Fatalf("unexpected commit log: %v", log)
	}
	// The done commit records the move: the archive path is present, the old root path
	// is gone in the committed tree.
	moved := strings.TrimSpace(out)
	rel, _ := filepath.Rel(repo, moved)
	tree := gitTree(t, repo)
	if !containsPath(tree, rel) {
		t.Errorf("archive path %q must be committed, tree=%v", rel, tree)
	}
	oldRel, _ := filepath.Rel(repo, filepath.Join(dir, filepath.Base(moved)))
	if containsPath(tree, oldRel) {
		t.Errorf("old root path %q must not remain in the committed tree", oldRel)
	}
	// Only the project file was staged: pj.cue stays untracked (committed later by sync).
	if containsPath(tree, filepath.Join("wc", "pj.cue")) {
		t.Errorf("self-commit must stage only the touched project path, not pj.cue")
	}
}

func gitTree(t *testing.T, repo string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", "HEAD")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	return lines(string(out))
}

func containsPath(tree []string, p string) bool {
	p = filepath.ToSlash(p)
	for _, e := range tree {
		if e == p {
			return true
		}
	}
	return false
}

func TestClaimSelfCommitsInProgress(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")

	out, _, err := run(t, app, "next", "--claim", "--scope", "wc")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := fmValue(t, strings.TrimSpace(out), "status"); got != "in-progress" {
		t.Errorf("claim must set in-progress, got %q", got)
	}
	// The claim self-commits its own change with the fixed in-progress message.
	log := gitLog(t, repo)
	if len(log) != 1 || log[0] != "pj: wc-ab2c -> in-progress" {
		t.Fatalf("claim should self-commit one in-progress commit, got %v", log)
	}
}

func TestAutoCommitTerminalCreateNeverCommits(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	_, repo := initGitScope(t, app, "wc", true)

	out, errOut, err := run(t, app, "create", "Already done", "done", "--scope", "wc")
	if err != nil {
		t.Fatalf("terminal create: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(out), string(os.PathSeparator)+"archive"+string(os.PathSeparator)) {
		t.Errorf("terminal create must scaffold under archive/, got %q", out)
	}
	// Even on an auto-commit scope with a git-root, a terminal create never self-commits.
	if n := len(gitLog(t, repo)); n != 0 {
		t.Errorf("terminal create must not self-commit, got %d commits", n)
	}
	if !strings.Contains(errOut, "not git-durable") {
		t.Errorf("terminal create should ride the scaffold-durability note, got %q", errOut)
	}
}

func TestAutoCommitPlannedRidesSyncDisabled(t *testing.T) {
	app := newApp(t)
	// An auto-commit scope with no git-root (planned repo): writes land, self-commit is
	// skipped with sync_disabled.
	dir := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init planned auto-commit: %v", err)
	}
	_, id := createID(t, app, "wc", "Work")
	_, errOut, err := run(t, app, "status", id, "todo")
	if err != nil {
		t.Fatalf("planned status should still land: %v", err)
	}
	if !strings.Contains(errOut, "sync_disabled:") {
		t.Errorf("planned auto-commit write should ride sync_disabled, got %q", errOut)
	}
}

func TestRepoDrivenUncommitted(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, _ := initGitScope(t, app, "rd", false)

	_, id := createID(t, app, "rd", "Host thing")
	_, errOut, err := run(t, app, "status", id, "todo")
	if err != nil {
		t.Fatalf("repo-driven status: %v", err)
	}
	if !strings.Contains(errOut, "uncommitted:") {
		t.Errorf("repo-driven write should ride uncommitted, got %q", errOut)
	}
	// Pure reads never carry the token.
	if _, readErr, _ := run(t, app, "get", id); strings.Contains(readErr, "uncommitted:") {
		t.Errorf("reads must never ride uncommitted, got %q", readErr)
	}
	// Non-allowlist residue does not count toward the signal.
	if err := os.WriteFile(filepath.Join(dir, "residue.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut2, _ := run(t, app, "status", id, "review")
	if strings.Contains(errOut2, "residue.txt") {
		t.Errorf("residue must not be flagged as uncommitted, got %q", errOut2)
	}
}

func TestMidRebaseRefusesWrites(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	_, id := createID(t, app, "wc", "Work")

	// Simulate a paused rebase.
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "status", id, "todo"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase status should refuse non-zero, got %v", err)
	}
	if _, _, err := run(t, app, "create", "New", "--scope", "wc"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase create should refuse non-zero, got %v", err)
	}
	// Reads stay allowed mid-rebase.
	if _, _, err := run(t, app, "get", id); err != nil {
		t.Errorf("reads must stay allowed mid-rebase, got %v", err)
	}
	_ = dir
}
