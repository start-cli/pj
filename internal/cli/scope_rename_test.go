package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeRenameEndToEnd(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	apiDir := initScope(t, app, "api")
	addProject(t, wcDir, "wc-ab2c", "target", "todo", "a0", "# Target\n", false, "")
	addProject(t, wcDir, "wc-de34", "dep", "todo", "a1", "# Dep\n", false, "depends: [wc-ab2c]\n")
	addProject(t, apiDir, "api-mm22", "x", "todo", "a0", "# X\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "scope", "rename", "wc", "core")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	// pj.cue name rewritten (the CUE formatter may align the value).
	data, _ := os.ReadFile(filepath.Join(wcDir, "pj.cue"))
	if !strings.Contains(string(data), `"core"`) || strings.Contains(string(data), `"wc"`) {
		t.Errorf("pj.cue name not rewritten to core: %q", data)
	}
	// Every filename and frontmatter id rewritten; the in-scope edge follows.
	if !fileExists(wcDir, "core-ab2c-target.md") || !fileExists(wcDir, "core-de34-dep.md") {
		t.Errorf("filenames not rewritten to the new scope: %v", projectFiles(t, wcDir))
	}
	if fileExists(wcDir, "wc-ab2c-target.md") {
		t.Errorf("old-prefixed file must be removed")
	}
	if id := fmValue(t, filepath.Join(wcDir, "core-ab2c-target.md"), "id"); id != "core-ab2c" {
		t.Errorf("frontmatter id not rewritten, got %q", id)
	}
	dep, _ := os.ReadFile(filepath.Join(wcDir, "core-de34-dep.md"))
	if !strings.Contains(string(dep), "core-ab2c") || strings.Contains(string(dep), "wc-ab2c") {
		t.Errorf("in-scope edge not re-keyed: %q", dep)
	}
	// Only the cross-scope inbound edge is reported (the in-scope wc-de34 -> wc-ab2c
	// edge was rewritten, not reported), never rewritten in the other scope.
	if !strings.Contains(out, "edge_verify:") || !strings.Contains(out, "api-mm22") {
		t.Errorf("cross-scope inbound edge should be reported, got %q", out)
	}
	if strings.Count(out, "edge_verify:") != 1 {
		t.Errorf("only the one cross-scope inbound edge should be reported, got %q", out)
	}
	apiFile, _ := os.ReadFile(filepath.Join(apiDir, "api-mm22-x.md"))
	if !strings.Contains(string(apiFile), "wc-ab2c") {
		t.Errorf("cross-scope edge must not be rewritten: %q", apiFile)
	}
	// Registry re-keyed: the old name is gone from the listing, the new appears.
	list, _, _ := run(t, app, "scope", "list")
	for _, row := range strings.Split(strings.TrimRight(list, "\n"), "\n") {
		if strings.HasPrefix(row, "wc\t") {
			t.Errorf("old scope name must be gone from the registry listing: %q", row)
		}
	}
	if !strings.Contains(list, "core\t") {
		t.Errorf("new scope name must appear in the registry listing: %q", list)
	}
	if _, _, err := run(t, app, "get", "core-ab2c"); err != nil {
		t.Errorf("ordinary verb must resolve under the new name, got %v", err)
	}
	if _, _, err := run(t, app, "get", "wc-ab2c"); err == nil {
		t.Errorf("the old name must no longer resolve")
	}
}

// A filename and a frontmatter id can legitimately disagree — bare doctor reports it as
// a structural class. The rename must re-prefix the id the file declares, not the one its
// name implies: doing the latter would move the project onto a different id and dangle
// every edge pointing at the real one.
func TestScopeRenameRePrefixesTheFrontmatterID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "auth", "todo", "a0", "# Auth\n", false, "")
	// Rename the file only, leaving the frontmatter id at wc-ab2c.
	if err := os.Rename(filepath.Join(dir, "wc-ab2c-auth.md"), filepath.Join(dir, "wc-zz9y-auth.md")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !fileExists(dir, "core-ab2c-auth.md") {
		t.Fatalf("the filename must follow the declared id, files=%v", projectFiles(t, dir))
	}
	if got := fmValue(t, filepath.Join(dir, "core-ab2c-auth.md"), "id"); got != "core-ab2c" {
		t.Errorf("declared id must be re-prefixed, got %q want core-ab2c", got)
	}
}

// An id that is not a project id of the old scope cannot be re-prefixed without guessing,
// so the whole rename refuses and names the file rather than inventing an identity.
func TestScopeRenameRefusesForeignFrontmatterID(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "other-ab2c", "auth", "todo", "a0", "# Auth\n", false, "")
	if err := os.Rename(filepath.Join(dir, "other-ab2c-auth.md"), filepath.Join(dir, "wc-ab2c-auth.md")); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, app, "scope", "rename", "wc", "core")
	if err == nil {
		t.Fatal("a foreign frontmatter id must refuse the rename")
	}
	if !strings.Contains(err.Error(), "other-ab2c") {
		t.Errorf("the refusal must name the offending id, got %v", err)
	}
	if !fileExists(dir, "wc-ab2c-auth.md") {
		t.Errorf("a refused rename must not have moved anything, files=%v", projectFiles(t, dir))
	}
}

func TestScopeRenameValidation(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")
	initScope(t, app, "api")

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"bad new name", []string{"scope", "rename", "wc", "BAD"}, exitUsage},
		{"same name", []string{"scope", "rename", "wc", "wc"}, exitUsage},
		{"unknown old", []string{"scope", "rename", "ghost", "core"}, exitFailure},
		{"taken new", []string{"scope", "rename", "wc", "api"}, exitFailure},
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

// A rename interrupted after the pj.cue name write but before the registry re-key
// leaves registry-old / pj.cue-new — byte-identical to name_drift. Re-running the same
// rename is the exempt path that finishes the idempotent tail.
func TestScopeRenameIdempotentReentry(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	addProject(t, wcDir, "wc-ab2c", "target", "todo", "a0", "# Target\n", false, "")

	// Simulate the crash window: files and pj.cue already migrated to core, registry
	// still keys wc.
	if err := os.Rename(filepath.Join(wcDir, "wc-ab2c-target.md"), filepath.Join(wcDir, "core-ab2c-target.md")); err != nil {
		t.Fatal(err)
	}
	migrated := "---\nid: core-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Target\n"
	if err := os.WriteFile(filepath.Join(wcDir, "core-ab2c-target.md"), []byte(migrated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wcDir, "pj.cue"), []byte("name: \"core\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ordinary verbs on the drifted scope fail closed until the re-run completes.
	if _, _, err := run(t, app, "list", "--scope", "wc"); err == nil {
		t.Errorf("drifted scope should fail closed for ordinary verbs")
	}
	// The re-run is the exempt path that finishes the tail.
	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("idempotent re-run should complete: %v", err)
	}
	if _, _, err := run(t, app, "get", "core-ab2c"); err != nil {
		t.Errorf("scope should resolve under the new name after re-run, got %v", err)
	}
}

// Machine-uniqueness of <new> is decided against the registry loaded under the config
// lock, not the caller's pre-lock snapshot: a registration landing in that window must be
// refused, never overwritten.
func TestScopeRenameRefusesNameTakenUnderLock(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	addProject(t, wcDir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	// The engine's registry snapshot is taken here, while "core" is still free.
	e, err := app.openEngine(newRootCmd(app))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()

	// "core" is registered against a different dir before the re-key runs.
	victim := filepath.Join(t.TempDir(), "core")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", victim, "--name", "core"); err != nil {
		t.Fatalf("concurrent init: %v", err)
	}

	err = e.rekeyRegistry("wc", "core")
	if err == nil {
		t.Fatal("re-key must refuse a name registered under the lock, not clobber it")
	}
	// The refusal names the split state, since the in-dir rewrite cannot be rolled back.
	for _, want := range []string{"machine-unique", "already renamed on disk", "forget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got %v", want, err)
		}
	}

	// The concurrent registration survives intact, still bound to its own dir.
	list, _, _ := run(t, app, "scope", "list")
	if !strings.Contains(list, victim) {
		t.Errorf("the concurrently registered scope must keep its dir binding, got %q", list)
	}
	if strings.Contains(list, "core\t"+wcDir) {
		t.Errorf("the renamed scope must not have taken over the core key, got %q", list)
	}
}

// A completed rename re-run is still idempotent: <new> is legitimately taken, by this very
// scope, and the absent-<old> tail must be reached before the uniqueness refusal.
func TestScopeRenameRekeyStaysIdempotentAfterCompletion(t *testing.T) {
	app := newApp(t)
	initScope(t, app, "wc")

	e, err := app.openEngine(newRootCmd(app))
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()

	if err := e.rekeyRegistry("wc", "core"); err != nil {
		t.Fatalf("first re-key: %v", err)
	}
	if err := e.rekeyRegistry("wc", "core"); err != nil {
		t.Errorf("re-key of a completed rename must stay a no-op, got %v", err)
	}
}

func TestScopeRenameRejectsGenuineDrift(t *testing.T) {
	app := newApp(t)
	wcDir := initScope(t, app, "wc")
	// pj.cue name is neither the old nor the requested new name: genuine drift.
	if err := os.WriteFile(filepath.Join(wcDir, "pj.cue"), []byte("name: \"other\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, app, "scope", "rename", "wc", "core")
	if err == nil {
		t.Fatalf("rename must refuse genuine drift")
	}
	if !strings.Contains(err.Error(), "drift") && !strings.Contains(err.Error(), "forget") {
		t.Errorf("refusal should point at forget+import recovery, got %v", err)
	}
}
