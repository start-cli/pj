package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initScope creates and registers a scope named name and returns its on-disk dir
// (the cleaned absolute path init printed).
func initScope(t *testing.T, app *App, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	out, _, err := run(t, app, "scope", "init", dir, "--name", name)
	if err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
	return strings.TrimSpace(out)
}

// addProject writes a project file into a scope dir. archived places it under
// archive/. The filename embeds the id, matching reconcile's grammar.
func addProject(t *testing.T, dir, id, slug, status, order, body string, archived bool, extraFM string) {
	t.Helper()
	name := id + "-" + slug + ".md"
	target := dir
	if archived {
		target = filepath.Join(dir, "archive")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: 2026-01-01T00:00:00Z\n" + extraFM + "---\n"
	if err := os.WriteFile(filepath.Join(target, name), []byte(fm+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestListBoardContract(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nbody", false, "")
	addProject(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth flow\n", false, "depends: [wc-ab2c]\n")
	addProject(t, dir, "wc-gh56", "old", "done", "a2", "# Old work\n", true, "")

	out, _, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	rows := lines(out)
	if len(rows) != 2 {
		t.Fatalf("default board should show 2 active rows, got %d: %q", len(rows), out)
	}
	// Sorted by (order, id): ab2c then de34.
	if !strings.HasPrefix(rows[0], "wc-ab2c\ttodo\tNetwork redesign\t\t") {
		t.Errorf("row0 = %q", rows[0])
	}
	// de34 waits on the still-todo ab2c.
	if rows[1] != "wc-de34\ttodo\tAuth flow\t\twc-ab2c" {
		t.Errorf("row1 waiting-on wrong: %q", rows[1])
	}
	for _, r := range rows {
		if strings.Contains(r, "\x1b") {
			t.Errorf("TSV must never carry ANSI: %q", r)
		}
	}

	// --all restores the archived done project.
	out, _, _ = run(t, app, "list", "--scope", "wc", "--all")
	if len(lines(out)) != 3 {
		t.Errorf("--all should include archived done, got %q", out)
	}

	// Unknown status positional → exit 2.
	_, _, err = run(t, app, "list", "--scope", "wc", "bogus")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Errorf("unknown status exit = %d want %d", got, exitUsage)
	}
}

func TestListTSVFlattensControlCharsInFields(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "tv")
	// A YAML double-quoted summary carrying a literal tab and an escaped newline:
	// left raw these would split the record into extra columns and lines.
	addProject(t, dir, "tv-ab2c", "note", "todo", "a0", "# Title\n", false,
		"summary: \"col1\tcol2\\nline2\"\n")

	out, _, err := run(t, app, "list", "--scope", "tv")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 {
		t.Fatalf("a single project must stay one TSV line, got %d: %q", len(rows), out)
	}
	if got := strings.Count(rows[0], "\t"); got != 4 {
		t.Errorf("row must have exactly 5 fields (4 tabs), got %d: %q", got, rows[0])
	}
	if strings.Contains(rows[0], "col1\tcol2") {
		t.Errorf("summary tab leaked into the TSV: %q", rows[0])
	}
	if !strings.Contains(rows[0], "col1 col2 line2") {
		t.Errorf("control chars should flatten to spaces: %q", rows[0])
	}
}

func TestNextSelectionAndBlocked(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	addProject(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-network.md") {
		t.Errorf("next should pick ab2c, got %q", out)
	}

	// Finish ab2c → de34 becomes runnable; ab2c moves under archive so it drops out.
	if err := os.Remove(filepath.Join(dir, "wc-ab2c-network.md")); err != nil {
		t.Fatal(err)
	}
	addProject(t, dir, "wc-ab2c", "network", "done", "a0", "# Network\n", true, "")
	out, _, err = run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next after unblock: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-de34-auth.md") {
		t.Errorf("next should pick de34 after ab2c done, got %q", out)
	}
}

func TestNextEmptyBecauseBlocked(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// One todo blocked by a missing same-scope target → held, empty-because-blocked.
	addProject(t, dir, "wc-de34", "auth", "todo", "a0", "# Auth\n", false, "depends: [wc-zz99]\n")

	out, errOut, err := run(t, app, "next", "--scope", "wc")
	if err == nil {
		t.Fatal("blocked queue should be non-zero")
	}
	if out != "" {
		t.Errorf("blocked next must print no path, got %q", out)
	}
	if !strings.Contains(err.Error(), "waiting on unmet deps") {
		t.Errorf("expected blocked diagnostic, got %v", err)
	}
	if !strings.Contains(errOut, "depends_dangling:") {
		t.Errorf("expected depends_dangling token on stderr, got %q", errOut)
	}
}

func TestGetMetaAndDuplicate(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nbody", false, "summary: short one\n")

	// get full id → path.
	out, _, err := run(t, app, "get", "wc-ab2c")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-network.md") {
		t.Fatalf("get = %q err=%v", out, err)
	}
	// get short id needs --scope.
	out, _, err = run(t, app, "get", "ab2c", "--scope", "wc")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("get short = %q err=%v", out, err)
	}

	// meta prints preamble + raw FM including summary.
	out, _, err = run(t, app, "meta", "wc-ab2c")
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if !strings.HasPrefix(out, "id: wc-ab2c\ntitle: Network redesign\npath: ") {
		t.Errorf("meta preamble wrong: %q", out)
	}
	if !strings.Contains(out, "summary: short one") {
		t.Errorf("meta should carry raw summary: %q", out)
	}

	// Duplicate id → get refuses with the token, and the condition rides exactly once:
	// the verb's actionable refusal, not also reconcile's generic integrity echo.
	addProject(t, dir, "wc-ab2c", "dup", "todo", "a3", "# Dup\n", false, "")
	_, errOut, err := run(t, app, "get", "wc-ab2c")
	if err == nil {
		t.Fatal("duplicate id must refuse")
	}
	if !strings.Contains(err.Error(), "duplicate_id:") {
		t.Errorf("expected the duplicate_id refusal on the error, got err=%v", err)
	}
	if strings.Contains(errOut, "duplicate_id:") {
		t.Errorf("reconcile's duplicate_id echo must be suppressed when the verb refuses, got stderr=%q", errOut)
	}
}

func TestDuplicateSuppressionKeepsOtherIDs(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Two independent duplicate-id collisions in one scope.
	addProject(t, dir, "wc-ab2c", "one", "todo", "a0", "# Ab One\n", false, "")
	addProject(t, dir, "wc-ab2c", "two", "todo", "a1", "# Ab Two\n", false, "")
	addProject(t, dir, "wc-de34", "one", "todo", "a2", "# De One\n", false, "")
	addProject(t, dir, "wc-de34", "two", "todo", "a3", "# De Two\n", false, "")

	// get refuses on ab2c: its own duplicate_id line carries the refusal, the generic
	// integrity echo for ab2c is suppressed, but de34's integrity echo still rides.
	_, errOut, err := run(t, app, "get", "wc-ab2c")
	if err == nil {
		t.Fatal("duplicate id must refuse")
	}
	if !strings.Contains(err.Error(), "duplicate_id:") || !strings.Contains(err.Error(), "wc-ab2c") {
		t.Errorf("refusal should name wc-ab2c, got %v", err)
	}
	if strings.Contains(errOut, "wc-ab2c claimed by") {
		t.Errorf("ab2c's integrity echo must be suppressed when the verb refuses on it: %q", errOut)
	}
	if !strings.Contains(errOut, "wc-de34 claimed by") {
		t.Errorf("de34's unrelated duplicate_id must still ride: %q", errOut)
	}
}

func TestParseErrorLocatable(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Conflict markers inside the frontmatter → quarantine.
	bad := "---\nid: wc-ab2c\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> x\n---\n# T\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	// get exits 0 with the path and a parse_error line on stderr.
	out, errOut, err := run(t, app, "get", "wc-ab2c")
	if err != nil {
		t.Fatalf("get on quarantine should exit 0: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("get on quarantine should still print the path")
	}
	if !strings.Contains(errOut, "parse_error:") {
		t.Errorf("expected parse_error on stderr, got %q", errOut)
	}
}

func TestSearchAndDeps(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network redesign\n\nsockets and buffers", false, "")
	addProject(t, dir, "wc-de34", "auth", "todo", "a1", "# Auth\n", false, "depends: [wc-ab2c]\nrelated: [wc-ab2c]\n")

	out, _, err := run(t, app, "search", "sockets", "--scope", "wc")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "wc-ab2c\ttodo\tNetwork redesign\t\t") || !strings.HasSuffix(rows[0], ".md") {
		t.Errorf("search hit wrong: %q", out)
	}

	// deps: de34 depends on ab2c; ab2c is depended on by de34.
	out, _, err = run(t, app, "deps", "wc-de34")
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if !strings.Contains(out, "depends on:\n  wc-ab2c\ttodo\tNetwork redesign") {
		t.Errorf("deps depends-on section wrong: %q", out)
	}
	if !strings.Contains(out, "related:\n  wc-ab2c") {
		t.Errorf("deps related section wrong: %q", out)
	}

	out, _, _ = run(t, app, "deps", "wc-ab2c")
	if !strings.Contains(out, "is depended on by:\n  wc-de34") {
		t.Errorf("deps reverse section wrong: %q", out)
	}
}

func TestListExcludesQuarantinedRows(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")
	// A quarantined file (conflict markers inside the frontmatter) → parse_error row.
	bad := "---\nid: wc-de34\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> x\n---\n# Broken\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-de34-broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	// Even --all never renders the quarantined row as a blank board line; it stays
	// locatable via get/search instead.
	out, _, err := run(t, app, "list", "--scope", "wc", "--all")
	if err != nil {
		t.Fatalf("list --all: %v", err)
	}
	rows := lines(out)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "wc-ab2c\t") {
		t.Fatalf("list --all should show only the healthy row, got %q", out)
	}
	for _, r := range rows {
		if strings.HasPrefix(r, "wc-de34\t") {
			t.Errorf("quarantined row must not appear on the board: %q", r)
		}
	}

	// It remains locatable for repair.
	got, _, err := run(t, app, "get", "wc-de34")
	if err != nil || strings.TrimSpace(got) == "" {
		t.Errorf("quarantined project should still resolve via get: out=%q err=%v", got, err)
	}
}

func TestSchemaErrorHoldsFromNext(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// A malformed depends entry (not a legal full id) sets schema_error and holds the
	// project out of next; the clean todo is still selected and the token rides stderr.
	addProject(t, dir, "wc-ab2c", "clean", "todo", "a0", "# Clean\n", false, "")
	addProject(t, dir, "wc-de34", "broken", "todo", "a1", "# Broken\n", false, "depends: [bogus]\n")

	out, errOut, err := run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-ab2c-clean.md") {
		t.Errorf("next should pick the clean todo, got %q", out)
	}
	if !strings.Contains(errOut, "schema_error:") {
		t.Errorf("expected schema_error on stderr for the malformed depends, got %q", errOut)
	}
}

func TestArchiveDriftTokensRideRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// A non-terminal project under archive/ and a terminal project at the dir root are
	// both layout drift; their tokens ride any read verb's stderr.
	addProject(t, dir, "wc-ab2c", "wip", "todo", "a0", "# WIP\n", true, "")
	addProject(t, dir, "wc-de34", "shipped", "done", "a1", "# Shipped\n", false, "")

	_, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(errOut, "archive_non_terminal:") {
		t.Errorf("expected archive_non_terminal for the todo under archive/, got %q", errOut)
	}
	if !strings.Contains(errOut, "archive_terminal_at_root:") {
		t.Errorf("expected archive_terminal_at_root for the done at root, got %q", errOut)
	}
}

func TestEqualOrderTokenRidesRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// Two projects sharing an order key trip equal_order on any read.
	addProject(t, dir, "wc-ab2c", "one", "todo", "a0", "# One\n", false, "")
	addProject(t, dir, "wc-de34", "two", "todo", "a0", "# Two\n", false, "")

	_, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(errOut, "equal_order:") {
		t.Errorf("expected equal_order on stderr for the shared order key, got %q", errOut)
	}
}

func TestConfigUnparseableRidesRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	// Corrupt pj.cue into a schema-invalid-but-compilable config: the name still reads
	// (so the scope resolves, no drift) but the schema fails validation, so reads stay
	// available and ride config_unparseable.
	bad := "name: \"wc\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("read under an unusable config should stay exit 0: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("reads stay available under config_unparseable; expected the board row")
	}
	if !strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("expected config_unparseable on stderr, got %q", errOut)
	}
}

func TestUnreachableScopeRidesRead(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// The scope's dir vanishes: reconcile leaves rows in place and rides
	// unreachable_scope rather than dropping the scope.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list against an unreachable scope should stay exit 0: %v", err)
	}
	if !strings.Contains(errOut, "unreachable_scope:") {
		t.Errorf("expected unreachable_scope on stderr, got %q", errOut)
	}
	// One token per dir-not-usable mode: an unreachable scope must not also read as a
	// broken config.
	if strings.Contains(errOut, "config_unparseable:") {
		t.Errorf("unreachable scope must not also ride config_unparseable, got %q", errOut)
	}
}

func TestDepsTransitiveAndTree(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// A depends chain aa22 -> bb33 -> cc44 exercises both expanded views.
	addProject(t, dir, "wc-aa22", "top", "todo", "a0", "# Top\n", false, "depends: [wc-bb33]\n")
	addProject(t, dir, "wc-bb33", "mid", "todo", "a1", "# Mid\n", false, "depends: [wc-cc44]\n")
	addProject(t, dir, "wc-cc44", "leaf", "todo", "a2", "# Leaf\n", false, "")

	// --transitive flattens the whole prerequisite closure under one section.
	out, _, err := run(t, app, "deps", "wc-aa22", "--transitive")
	if err != nil {
		t.Fatalf("deps --transitive: %v", err)
	}
	if !strings.Contains(out, "depends on (transitive):") {
		t.Errorf("expected the transitive section header, got %q", out)
	}
	if !strings.Contains(out, "wc-bb33") || !strings.Contains(out, "wc-cc44") {
		t.Errorf("transitive depends should include the whole chain, got %q", out)
	}

	// --tree pretty-prints the graph with the leaf nested two levels deep.
	out, _, err = run(t, app, "deps", "wc-aa22", "--tree")
	if err != nil {
		t.Fatalf("deps --tree: %v", err)
	}
	if !strings.Contains(out, "depends tree:") {
		t.Errorf("expected the tree header, got %q", out)
	}
	if !strings.Contains(out, "\n    wc-bb33\t") || !strings.Contains(out, "\n      wc-cc44\t") {
		t.Errorf("tree should indent bb33 then cc44 one level deeper, got %q", out)
	}
}

func TestMetaNoFrontmatterFenceIsNonZero(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	// A file with no frontmatter fence at all: reconcile quarantines it, and meta
	// reports the non-zero, empty-stdout, token-on-stderr path.
	if err := os.WriteFile(filepath.Join(dir, "wc-ff44-nofm.md"), []byte("# No frontmatter\n\njust a body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "meta", "wc-ff44")
	if err == nil {
		t.Fatal("meta on wholly-unparseable frontmatter must be non-zero")
	}
	if out != "" {
		t.Errorf("meta with no readable frontmatter must print no stdout, got %q", out)
	}
	if !strings.Contains(errOut, "parse_error:") || !strings.Contains(errOut, "no extractable frontmatter block") {
		t.Errorf("expected the no-frontmatter parse_error diagnostic, got %q", errOut)
	}
}

func TestSearchMalformedQueryCleanMessage(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	// An unbalanced quote is a query typo: a clean, teaching message, not a leaked
	// SQLite/FTS5 internal string.
	out, _, err := run(t, app, "search", `foo"`, "--scope", "wc")
	if err == nil {
		t.Fatal("malformed query must be non-zero")
	}
	if out != "" {
		t.Errorf("malformed query must print no hits, got %q", out)
	}
	if !strings.Contains(err.Error(), "invalid search query") || !strings.Contains(err.Error(), "FTS5") {
		t.Errorf("expected a clean FTS5-hint message, got %v", err)
	}
	if strings.Contains(err.Error(), "SQL logic error") || strings.Contains(err.Error(), "sqlite") {
		t.Errorf("must not leak the raw driver error: %v", err)
	}
}

func TestQueryReadOnly(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	out, _, err := run(t, app, "query", "SELECT id, status FROM projects ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !strings.Contains(out, "wc-ab2c\ttodo") {
		t.Errorf("query result wrong: %q", out)
	}
	// A write is rejected.
	_, _, err = run(t, app, "query", "DELETE FROM projects")
	if err == nil {
		t.Error("query must reject a write")
	}
	// --schema prints the shape.
	out, _, err = run(t, app, "query", "--schema")
	if err != nil || !strings.Contains(out, "NOT A STABLE API") {
		t.Errorf("query --schema = %q err=%v", out, err)
	}
}

func TestCrossScopeDependsGate(t *testing.T) {
	app := newApp(t)
	up := initScope(t, app, "up")
	wc := initScope(t, app, "wc")

	// wc-bb22 depends cross-scope on up-aa22 (still todo → non-terminal).
	addProject(t, up, "up-aa22", "core", "todo", "a0", "# Core\n", false, "")
	addProject(t, wc, "wc-bb22", "feat", "todo", "a0", "# Feature\n", false, "depends: [up-aa22]\n")
	// wc-cc33 depends on an unregistered scope → informational hold.
	addProject(t, wc, "wc-cc33", "ext", "todo", "a1", "# Ext\n", false, "depends: [zzz-zz99]\n")

	// The cross-scope gate holds wc-bb22 and wc-cc33; next is empty-because-blocked.
	out, errOut, err := run(t, app, "next", "--scope", "wc")
	if err == nil || out != "" {
		t.Fatalf("cross-scope-blocked next should be non-zero with no path: out=%q err=%v", out, err)
	}
	if !strings.Contains(errOut, "depends_unresolvable:") {
		t.Errorf("expected depends_unresolvable for the unregistered-scope dep, got %q", errOut)
	}

	// list shows the unmet cross-scope dep in waiting-on.
	out, _, _ = run(t, app, "list", "--scope", "wc")
	if !strings.Contains(out, "wc-bb22\ttodo\tFeature\t\tup-aa22") {
		t.Errorf("waiting-on should carry the cross-scope dep: %q", out)
	}

	// Finish up-aa22 (terminal → archived): the cross-scope gate now reads it done,
	// so wc-bb22 becomes runnable.
	if err := os.Remove(filepath.Join(up, "up-aa22-core.md")); err != nil {
		t.Fatal(err)
	}
	addProject(t, up, "up-aa22", "core", "done", "a0", "# Core\n", true, "")
	out, _, err = run(t, app, "next", "--scope", "wc")
	if err != nil {
		t.Fatalf("next after cross-scope unblock: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "wc-bb22-feat.md") {
		t.Errorf("wc-bb22 should be runnable once up-aa22 is done, got %q", out)
	}
}

func TestEditOpensEditorAndIsSilent(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	// A no-op editor (true) stands in for $EDITOR: edit resolves the path, runs the
	// editor, and prints nothing on success (it is not a path-hand-off verb).
	t.Setenv("EDITOR", "true")
	out, _, err := run(t, app, "edit", "wc-ab2c")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if out != "" {
		t.Errorf("edit prints nothing on success, got stdout=%q", out)
	}

	// With $EDITOR unset, edit refuses with guidance and no stdout.
	t.Setenv("EDITOR", "")
	out, _, err = run(t, app, "edit", "wc-ab2c")
	if err == nil {
		t.Fatal("edit with no $EDITOR must be non-zero")
	}
	if out != "" {
		t.Errorf("edit failure must print no stdout, got %q", out)
	}
	if !strings.Contains(err.Error(), "$EDITOR") {
		t.Errorf("expected an $EDITOR-not-set message, got %v", err)
	}
}

func TestQueryRejectsSmuggledWrites(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "network", "todo", "a0", "# Network\n", false, "")

	// A write hidden behind a CTE passes the leading-keyword classifier (WITH) but is
	// caught by the runtime query_only guard.
	if _, _, err := run(t, app, "query", "WITH x AS (SELECT 1) DELETE FROM projects"); err == nil {
		t.Error("query must reject a CTE-smuggled write")
	}
	// A write appended after a SELECT is caught by the statement splitter.
	if _, _, err := run(t, app, "query", "SELECT 1; DROP TABLE projects"); err == nil {
		t.Error("query must reject a write appended after a SELECT")
	}
	// The projects table must survive both refusals.
	out, _, err := run(t, app, "query", "SELECT count(*) FROM projects")
	if err != nil || !strings.Contains(out, "1") {
		t.Errorf("projects table should be intact after rejected writes: out=%q err=%v", out, err)
	}
}

func TestLensAppliesAndEchoes(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "fe", "todo", "a0", "# Frontend\n", false, "tags: [frontend]\n")
	addProject(t, dir, "wc-de34", "be", "todo", "a1", "# Backend\n", false, "tags: [backend]\n")
	addProject(t, dir, "wc-gh56", "un", "todo", "a2", "# Untagged\n", false, "")

	// Set a lens to frontend.
	if _, _, err := run(t, app, "lens", "frontend", "--scope", "wc"); err != nil {
		t.Fatalf("lens set: %v", err)
	}
	out, errOut, err := run(t, app, "list", "--scope", "wc")
	if err != nil {
		t.Fatalf("list under lens: %v", err)
	}
	rows := lines(out)
	// frontend (tag match) + untagged (never hidden); backend filtered out.
	if len(rows) != 2 {
		t.Fatalf("lens should show frontend + untagged, got %q", out)
	}
	for _, r := range rows {
		if strings.HasPrefix(r, "wc-de34") {
			t.Errorf("backend should be filtered by the lens: %q", r)
		}
	}
	if !strings.Contains(errOut, "lens:") {
		t.Errorf("active lens should echo on stderr, got %q", errOut)
	}

	// --no-lens restores everything.
	out, _, _ = run(t, app, "list", "--scope", "wc", "--no-lens")
	if len(lines(out)) != 3 {
		t.Errorf("--no-lens should bypass, got %q", out)
	}

	// Clear the lens.
	if _, _, err := run(t, app, "lens", "--clear", "--scope", "wc"); err != nil {
		t.Fatalf("lens clear: %v", err)
	}
	out, _, _ = run(t, app, "lens", "--scope", "wc")
	if strings.TrimSpace(out) != "" {
		t.Errorf("cleared lens should show empty, got %q", out)
	}
}
