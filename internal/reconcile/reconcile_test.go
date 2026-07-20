package reconcile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cuelang.org/go/cue/cuecontext"

	"github.com/start-cli/pj/internal/index"
)

func newReconciler(t *testing.T) (*Reconciler, *index.DB) {
	t.Helper()
	db, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, cuecontext.New()), db
}

// mkScope creates a scope dir with a minimal pj.cue and returns its path.
func mkScope(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "pj.cue"), "name: \""+name+"\"\nautoCommit: false\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projFile(id, status, order, body string) string {
	fm := "---\nid: " + id + "\nstatus: " + status + "\norder: \"" + order + "\"\ncreated: 2026-01-01T00:00:00Z\n---\n"
	return fm + body
}

func reconcileOne(t *testing.T, r *Reconciler, name, dir string, now int64) *Result {
	t.Helper()
	res, err := r.Reconcile(map[string]string{name: dir}, map[string]bool{name: true}, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func TestReconcileIndexesAndReflectsEdit(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	fp := filepath.Join(dir, "wc-ab2c-network.md")
	writeFile(t, fp, projFile("wc-ab2c", "todo", "a0", "# Network redesign\n\nbody"))

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	rows, _ := db.ScopeProjects("wc")
	if len(rows) != 1 || rows[0].Status != "todo" || rows[0].Title != "Network redesign" {
		t.Fatalf("initial row = %+v", rows)
	}

	// Edit the file (status -> done) with a later mtime, reconcile at a later now.
	writeFile(t, fp, projFile("wc-ab2c", "done", "a0", "# Network redesign\n\ndone"))
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(fp, future, future)
	reconcileOne(t, r, "wc", dir, future.Add(time.Minute).UnixNano())

	rows, _ = db.ScopeProjects("wc")
	if len(rows) != 1 || rows[0].Status != "done" {
		t.Fatalf("edited row = %+v", rows)
	}
}

func TestReconcileNamesScopeIndependently(t *testing.T) {
	// A scope named other than "wc" indexes under its own name, so a later verb
	// namespaces its rows correctly.
	r, db := newReconciler(t)
	dir := mkScope(t, "ui")
	writeFile(t, filepath.Join(dir, "ui-ab2c-x.md"), projFile("ui-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "ui", dir, time.Now().UnixNano())
	if rows, _ := db.ScopeProjects("ui"); len(rows) != 1 || rows[0].Scope != "ui" {
		t.Fatalf("ui rows = %+v", rows)
	}
}

func TestForeignScopeFrontmatterIDNotAdopted(t *testing.T) {
	// A file under scope "wc" whose frontmatter claims another scope's id must not
	// adopt that id: doing so would leave the row's scope ("wc") and id-prefix ("sb")
	// disagreeing and make the project unresolvable by get/meta. The filename-derived
	// id is kept instead so the project stays reachable.
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	fp := filepath.Join(dir, "wc-ab2c-note.md")
	writeFile(t, fp, projFile("sb-cd34", "todo", "a0", "# Note"))

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	rows, _ := db.ScopeProjects("wc")
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0].ID != "wc-ab2c" || rows[0].ShortID != "ab2c" {
		t.Fatalf("foreign-scope id should be rejected; row id = %q short = %q", rows[0].ID, rows[0].ShortID)
	}
	if rows[0].Scope != "wc" {
		t.Fatalf("row scope = %q, want wc", rows[0].Scope)
	}
}

func TestSameScopeFrontmatterIDIsAuthoritative(t *testing.T) {
	// Same-scope id/filename drift is legitimate: the frontmatter id is authoritative,
	// so a file named wc-ab2c-*.md carrying id wc-cd34 indexes as wc-cd34.
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-ab2c-note.md"), projFile("wc-cd34", "todo", "a0", "# Note"))

	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	rows, _ := db.ScopeProjects("wc")
	if len(rows) != 1 || rows[0].ID != "wc-cd34" || rows[0].ShortID != "cd34" {
		t.Fatalf("same-scope frontmatter id should win; row = %+v", rows)
	}
}

func TestParseErrorQuarantineVsBodyMarkers(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")

	// Conflict markers inside the frontmatter fence → quarantine.
	bad := filepath.Join(dir, "wc-ab2c-broken.md")
	writeFile(t, bad, "---\nid: wc-ab2c\n<<<<<<< HEAD\nstatus: todo\n=======\nstatus: done\n>>>>>>> other\n---\n# T\n")

	// Conflict markers only in the body → indexed from clean FM.
	bodyOnly := filepath.Join(dir, "wc-de34-body.md")
	writeFile(t, bodyOnly, projFile("wc-de34", "todo", "a1", "# Title\n<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> x\n"))

	res := reconcileOne(t, r, "wc", dir, time.Now().UnixNano())

	rows, _ := db.ProjectsByID("wc", "wc-ab2c")
	if len(rows) != 1 || !rows[0].ParseError || rows[0].Status != "" {
		t.Fatalf("quarantined row = %+v", rows)
	}
	rows, _ = db.ProjectsByID("wc", "wc-de34")
	if len(rows) != 1 || rows[0].ParseError || rows[0].Status != "todo" {
		t.Fatalf("body-only-marker row should index normally: %+v", rows)
	}
	if !hasToken(res.Warnings, "parse_error:") {
		t.Errorf("expected a parse_error warning, got %v", res.Warnings)
	}
}

func TestUnreachableScopeKeepsRows(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-ab2c-x.md"), projFile("wc-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())

	// Remove the dir: it becomes unreachable. Rows must survive.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	res := reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if !res.Unreachable["wc"] {
		t.Fatalf("scope should be unreachable")
	}
	if rows, _ := db.ScopeProjects("wc"); len(rows) != 1 {
		t.Fatalf("unreachable scope rows should survive, got %d", len(rows))
	}
	if !hasToken(res.Warnings, "unreachable_scope:") {
		t.Errorf("expected unreachable_scope warning, got %v", res.Warnings)
	}
	// An unreachable dir rides unreachable_scope only — never config_unparseable for
	// a pj.cue that is unreadable solely because the dir is gone.
	if hasToken(res.Warnings, "config_unparseable:") {
		t.Errorf("unreachable scope must not also ride config_unparseable, got %v", res.Warnings)
	}
}

func TestForgottenScopePruned(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	writeFile(t, filepath.Join(dir, "wc-ab2c-x.md"), projFile("wc-ab2c", "todo", "a0", "# X"))
	reconcileOne(t, r, "wc", dir, time.Now().UnixNano())
	if rows, _ := db.ScopeProjects("wc"); len(rows) != 1 {
		t.Fatalf("precondition: wc should have a row")
	}

	// Reconcile a different scope with wc no longer registered → wc pruned.
	other := mkScope(t, "ui")
	if _, err := r.Reconcile(map[string]string{"ui": other}, map[string]bool{"ui": true}, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ScopeProjects("wc"); len(rows) != 0 {
		t.Fatalf("forgotten scope rows should be pruned, got %d", len(rows))
	}
}

func TestConfigCacheHitAndInvalidation(t *testing.T) {
	r, db := newReconciler(t)
	// A packaged scope whose knownTags live in a sibling schema.cue — a real import
	// closure with more than the entry file.
	dir := filepath.Join(t.TempDir(), "wc")
	writeFile(t, filepath.Join(dir, "pj.cue"), "package wccfg\nname: \"wc\"\nautoCommit: false\nknownTags: tags\n")
	writeFile(t, filepath.Join(dir, "schema.cue"), "package wccfg\ntags: [\"frontend\"]\n")

	schema, cfgErr := r.schemaFor("wc", dir)
	if cfgErr != nil || schema == nil || len(schema.KnownTags) != 1 || schema.KnownTags[0] != "frontend" {
		t.Fatalf("cold eval = %+v err=%v", schema, cfgErr)
	}

	// Prove the cache is used: overwrite the cached schema with a sentinel while
	// leaving the closure stats untouched, and confirm schemaFor returns it without
	// re-evaluating CUE.
	entry, _, _ := db.ConfigCacheGet("wc")
	entry.SchemaJSON = `{"Name":"wc","KnownTags":["CACHED"]}`
	if err := db.ConfigCacheSet("wc", entry); err != nil {
		t.Fatal(err)
	}
	cached, _ := r.schemaFor("wc", dir)
	if cached == nil || len(cached.KnownTags) != 1 || cached.KnownTags[0] != "CACHED" {
		t.Fatalf("expected cache hit to serve the sentinel, got %+v", cached)
	}

	// Editing the imported schema.cue invalidates the closure → re-evaluate.
	writeFile(t, filepath.Join(dir, "schema.cue"), "package wccfg\ntags: [\"backend\"]\n")
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "schema.cue"), future, future)
	fresh, _ := r.schemaFor("wc", dir)
	if fresh == nil || len(fresh.KnownTags) != 1 || fresh.KnownTags[0] != "backend" {
		t.Fatalf("closure change should re-evaluate, got %+v", fresh)
	}
}

func hasToken(warnings []string, prefix string) bool {
	for _, w := range warnings {
		if len(w) >= len(prefix) && w[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
