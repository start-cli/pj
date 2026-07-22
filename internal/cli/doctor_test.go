package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/token"
)

// projectFiles returns the .md basenames directly under dir and its archive child.
func projectFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, root := range []string{dir, filepath.Join(dir, "archive")} {
		out = append(out, projectFilesIn(t, root)...)
	}
	return out
}

// projectFilesIn returns the .md basenames directly under one directory. A missing
// directory is empty, not an error — a scope need not have an archive/ child.
func projectFilesIn(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

func fileExists(dir, base string) bool {
	_, err := os.Stat(filepath.Join(dir, base))
	return err == nil
}

func TestDoctorBareReportsAndMutatesNothing(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	before := projectFiles(t, dir)
	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("bare doctor: %v", err)
	}
	if !strings.Contains(out, "duplicate_id:") {
		t.Errorf("bare doctor should report duplicate_id, got %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("token report must never carry ANSI: %q", out)
	}
	after := projectFiles(t, dir)
	if len(before) != len(after) || !fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("bare doctor must mutate nothing: before=%v after=%v", before, after)
	}
}

func TestDoctorRepairDuplicateID(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")
	addProject(t, dir, "wc-de34", "ref", "todo", "a2", "# Ref\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	// Equal created (both 2026-01-01) → basename tie-break → beta (greater) renamed to
	// the deterministic first-free extension ab2ca.
	if !fileExists(dir, "wc-ab2c-alpha.md") {
		t.Errorf("kept side must retain its id/filename")
	}
	if fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("loser file must be renamed away")
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("loser must take the deterministic extension ab2ca, files=%v", projectFiles(t, dir))
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Errorf("repair should report the rename, got %q", out)
	}
	// Every inbound edge to the collided id is surfaced as edge_verify.
	if !strings.Contains(out, "edge_verify:") || !strings.Contains(out, "wc-de34") {
		t.Errorf("repair should emit edge_verify for the referrer, got %q", out)
	}
	// The referrer's depends entry is never rewritten.
	ref, _ := os.ReadFile(filepath.Join(dir, "wc-de34-ref.md"))
	if !strings.Contains(string(ref), "wc-ab2c") {
		t.Errorf("depends edge must be left untouched, got %q", ref)
	}
}

// The indexer accepts any tail after a valid short-id (so no project goes invisible) but
// the allowlist requires a valid slug (so no malformed name is committed). Doctor is what
// reports the resulting inconsistency; without the shape check the file is a live project
// that only rides non_allowlist, which reads as "remove this real work".
func TestDoctorFlagsMalformedSlugTail(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "good", "todo", "a0", "# Good\n", false, "")
	bad := "---\nid: wc-de34\nstatus: todo\norder: \"a1\"\ncreated: 2026-01-01T00:00:00Z\n---\n# Bad\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-de34-Bad__Slug!.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "filename/id mismatch: wc-de34-Bad__Slug!.md is not a project file shape") {
		t.Errorf("malformed slug tail must ride the structural check, got %q", out)
	}
	// The well-formed sibling is untouched by the new condition.
	if strings.Contains(out, "wc-ab2c-good.md") {
		t.Errorf("a valid project filename must not be flagged, got %q", out)
	}
}

// A custom strings field carries the same set merge as the built-in lists, so duplicates
// in it ride the same schema_warn.
func TestDoctorFlagsDuplicateInCustomStringsField(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	cfg := "name: \"wc\"\nautoCommit: false\nfields: {areas: {type: \"strings\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "areas: [api, api]\n")
	addProject(t, dir, "wc-de34", "y", "todo", "a1", "# Y\n", false, "areas: [api, ui]\n")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, `schema_warn: wc-ab2c has a duplicate areas entry "api"`) {
		t.Errorf("duplicate in a custom strings field must ride schema_warn, got %q", out)
	}
	if strings.Contains(out, "wc-de34 has a duplicate") {
		t.Errorf("a distinct-valued strings field must not be flagged, got %q", out)
	}
}

func TestFieldTypeError(t *testing.T) {
	strs := scopeconfig.Field{Type: scopeconfig.FieldStrings}
	enum := scopeconfig.Field{Type: scopeconfig.FieldStrings, Values: []string{"api", "ui"}}
	cases := []struct {
		name  string
		field scopeconfig.Field
		value any
		want  string
	}{
		{"strings all strings", strs, []any{"a", "b"}, ""},
		{"strings empty", strs, []any{}, ""},
		{"strings scalar", strs, "api", "should be a list of strings"},
		{"strings int element", strs, []any{"api", 7}, "has a non-string entry (7)"},
		{"strings bool element", strs, []any{true}, "has a non-string entry (true)"},
		{"strings nested list", strs, []any{[]any{"a"}}, "has a non-string entry ([a])"},
		// The element check runs first, so a mixed list can no longer slip past into the
		// enum loop — the hole that let areas: [7] pass under a declared enum.
		{"enum int element", enum, []any{7}, "has a non-string entry (7)"},
		{"enum outside values", enum, []any{"api", "db"}, `has value "db" outside its declared values`},
		{"enum within values", enum, []any{"api", "ui"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fieldTypeError(tc.field, tc.value); got != tc.want {
				t.Errorf("fieldTypeError = %q, want %q", got, tc.want)
			}
		})
	}
}

// A non-string element in a strings field is a hard schema_error, not silence.
func TestDoctorFlagsNonStringInStringsField(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	cfg := "name: \"wc\"\nautoCommit: false\nfields: {areas: {type: \"strings\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "areas: [api, 7]\n")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, `schema_error: wc-ab2c field "areas" has a non-string entry (7)`) {
		t.Errorf("non-string element must ride schema_error, got %q", out)
	}
}

// A pure self-depends is owned by depends_self; depends_cycle covers cycles among two or
// more distinct projects. A project in a real cycle that also self-depends rides both,
// because the two defects are then genuinely separate.
func TestDoctorSelfDependsIsNotAlsoACycle(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "depends: [wc-ab2c]\n")
	addProject(t, dir, "wc-de34", "y", "todo", "a1", "# Y\n", false, "depends: [wc-fg56]\n")
	addProject(t, dir, "wc-fg56", "z", "todo", "a2", "# Z\n", false, "depends: [wc-de34, wc-fg56]\n")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "depends_self: wc-ab2c") {
		t.Errorf("self-depends must ride depends_self, got %q", out)
	}
	if strings.Contains(out, "depends_cycle: wc-ab2c") {
		t.Errorf("a pure self-depends must not also ride depends_cycle, got %q", out)
	}
	// The genuine two-node cycle is untouched.
	for _, id := range []string{"wc-de34", "wc-fg56"} {
		if !strings.Contains(out, "depends_cycle: "+id) {
			t.Errorf("real cycle member %s must still ride depends_cycle, got %q", id, out)
		}
	}
	if !strings.Contains(out, "depends_self: wc-fg56") {
		t.Errorf("a cycle member that also self-depends still rides depends_self, got %q", out)
	}
}

// Combined --reindex --repair ends with an index that matches the repaired file tree.
//
// End-state assertions only. The pre-repair-rebuild and post-repair-rebuild orders are
// observationally identical while every repair reports each path it touches, so no test
// distinguishes them; the ordering is a contract, held by the comment in runDoctor.
// TestDoctorReindexRepairSeesUnindexedCollision covers the part that can actually break.
func TestDoctorReindexWithRepairRebuildsAfterRepairs(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")
	// Seed the index, then delete a file behind pj's back: the end state must not carry
	// the removed row.
	if _, _, err := run(t, app, "list"); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	addProject(t, dir, "wc-gh78", "ghost", "todo", "a2", "# Ghost\n", false, "")
	if _, _, err := run(t, app, "list"); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "wc-gh78-ghost.md")); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "doctor", "--reindex", "--repair")
	if err != nil {
		t.Fatalf("doctor --reindex --repair: %v", err)
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Errorf("the repair must still run, got %q", out)
	}

	// The post-repair index reflects the repaired tree exactly: renamed loser present,
	// old id gone, deleted file gone.
	list, _, err := run(t, app, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(list, "wc-ab2ca") {
		t.Errorf("post-repair index must carry the extended id, got %q", list)
	}
	if strings.Contains(list, "wc-gh78") {
		t.Errorf("full rebuild must drop the row for a deleted file, got %q", list)
	}
	if got := strings.Count(list, "wc-ab2c\t"); got != 1 {
		t.Errorf("kept side must appear exactly once, got %d in %q", got, list)
	}
}

// order is the board's rank space: an invalid key still sorts, just not where intended,
// so it must not pass doctor clean. Reconcile does not quarantine these — they are valid
// YAML — and order_long is gated on validity, so this is the only class that catches them.
func TestDoctorFlagsInvalidOrderKeys(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	write := func(pid, name, orderLine string) {
		body := "---\nid: " + pid + "\nstatus: todo\n" + orderLine + "created: 2026-01-01T00:00:00Z\n---\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, pid+"-"+name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wc-ab2c", "valid", "order: \"a0\"\n")
	write("wc-de34", "leaddigit", "order: \"0abc\"\n") // head must be a letter
	write("wc-fg56", "trailzero", "order: \"ab0\"\n")  // fraction must not end in 0
	write("wc-hj78", "nonstring", "order: 5\n")        // unquoted scalar
	write("wc-mm22", "emptykey", "order: \"\"\n")      // explicit empty
	write("wc-nn45", "absent", "")                     // no order key at all

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	for _, want := range []string{
		`schema_error: wc-de34 has an invalid order key "0abc"`,
		`schema_error: wc-fg56 has an invalid order key "ab0"`,
		`schema_error: wc-hj78 has an invalid order key "5"`,
		`schema_error: wc-mm22 has a missing or empty order key`,
		`schema_error: wc-nn45 has a missing or empty order key`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "wc-ab2c has an invalid order") || strings.Contains(out, "wc-ab2c has a missing") {
		t.Errorf("a valid key must not be flagged, got %q", out)
	}
}

// edge_verify identifies referrers by id, so it must be read after every collision in the
// scope is repaired. Two referrers that themselves collide would otherwise produce two
// byte-identical lines, one naming an id the same run then renames away.
func TestDoctorRepairEdgeVerifyNamesPostRepairReferrers(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	// Both referrers of wc-ab2c are themselves a collision on wc-de34.
	addProject(t, dir, "wc-de34", "gamma", "todo", "a2", "# G\n", false, "depends: [wc-ab2c]\n")
	addProject(t, dir, "wc-de34", "delta", "todo", "a3", "# D\n", false, "depends: [wc-ab2c]\n")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	// One line per distinct referrer, each naming the id that referrer holds afterwards.
	for _, want := range []string{
		"edge_verify: wc-de34 depends wc-ab2c",
		"edge_verify: wc-de34a depends wc-ab2c",
	} {
		if strings.Count(out, want+" ") != 1 {
			t.Errorf("want exactly one %q, got %q", want, out)
		}
	}
	if strings.Count(out, "edge_verify:") != 2 {
		t.Errorf("expected exactly two edge_verify lines, got %q", out)
	}
	// Every reported referrer id resolves to a real project after the run.
	for _, id := range []string{"wc-de34", "wc-de34a"} {
		if _, _, err := run(t, app, "get", id); err != nil {
			t.Errorf("edge_verify named %s, which does not resolve: %v", id, err)
		}
	}
}

// Repairs run before the --reindex rebuild, so they depend on the pre-repair reconcile to
// have populated the index. Rebuild drops every table and only repopulates afterwards, so
// skipping that reconcile whenever --reindex is set would leave DuplicateIDs querying an
// empty index and silently repair nothing.
func TestDoctorReindexRepairSeesUnindexedCollision(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	// No prior read seeds the index: --reindex --repair is the first command to touch it,
	// so the collision exists only on disk when the repair pass needs to find it.
	out, _, err := run(t, app, "doctor", "--reindex", "--repair")
	if err != nil {
		t.Fatalf("doctor --reindex --repair: %v", err)
	}
	if !strings.Contains(out, "repaired duplicate id: wc-ab2c -> wc-ab2ca") {
		t.Fatalf("an on-disk collision absent from the index must still be repaired, got %q", out)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("the loser must be renamed on disk, files=%v", projectFiles(t, dir))
	}
}

func TestDoctorRepairEqualOrder(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-aaaa", "a", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-bbbb", "b", "todo", "a1", "# B\n", false, "")
	addProject(t, dir, "wc-cccc", "c", "todo", "a1", "# C\n", false, "")
	addProject(t, dir, "wc-dddd", "d", "todo", "a2", "# D\n", false, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	ka := fmValue(t, filepath.Join(dir, "wc-aaaa-a.md"), "order")
	kb := fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order")
	kc := fmValue(t, filepath.Join(dir, "wc-cccc-c.md"), "order")
	kd := fmValue(t, filepath.Join(dir, "wc-dddd-d.md"), "order")
	if ka != "a0" || kd != "a2" {
		t.Errorf("untied anchors must not move: a=%q d=%q", ka, kd)
	}
	if ka >= kb || kb >= kc || kc >= kd || kb == kc {
		t.Errorf("tied keys must become distinct and ordered: %q %q %q %q", ka, kb, kc, kd)
	}
}

func TestDoctorRepairArchiveLayoutBothWays(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	// terminal at root → should move under archive/.
	addProject(t, dir, "wc-aaaa", "done1", "done", "a0", "# Done\n", false, "")
	// non-terminal under archive/ → should move to root.
	addProject(t, dir, "wc-bbbb", "todo1", "todo", "a1", "# Todo\n", true, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	if !fileExists(dir, filepath.Join("archive", "wc-aaaa-done1.md")) {
		t.Errorf("terminal project must move under archive/, files=%v", projectFiles(t, dir))
	}
	if !fileExists(dir, "wc-bbbb-todo1.md") {
		t.Errorf("non-terminal project must move to dir root, files=%v", projectFiles(t, dir))
	}
}

// A duplicate id whose members share a frozen slug shares a basename, so a layout move
// across the archive boundary would land one member on the other and remove the source —
// erasing a whole project. The layout repair defers those ids until the collision repair
// has given them distinct names, then lands the move in the same run.
func TestDoctorRepairCollisionAcrossArchiveBoundaryKeepsBothProjects(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	// Same id, same slug, different content: one terminal at root, one non-terminal
	// under archive/ — both misplaced, so both are layout-repair candidates.
	addProject(t, dir, "wc-ab2c", "alpha", "done", "a0", "# Root copy\n", false, "")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a1", "# Archive copy\n", true, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	bodies := map[string]bool{}
	for _, root := range []string{dir, filepath.Join(dir, "archive")} {
		for _, base := range projectFilesIn(t, root) {
			data, err := os.ReadFile(filepath.Join(root, base))
			if err != nil {
				t.Fatal(err)
			}
			bodies[strings.TrimSpace(string(data[strings.LastIndex(string(data), "---\n")+4:]))] = true
		}
	}
	if !bodies["# Root copy"] || !bodies["# Archive copy"] {
		t.Fatalf("repair must keep both projects, found bodies %v (files %v)", bodies, projectFiles(t, dir))
	}
	// The loser took the deterministic extension, and each project now sits on the side
	// of the archive boundary its status calls for.
	if !fileExists(dir, filepath.Join("archive", "wc-ab2c-alpha.md")) {
		t.Errorf("the done project must end under archive/, files=%v", projectFiles(t, dir))
	}
	if !fileExists(dir, "wc-ab2ca-alpha.md") {
		t.Errorf("the todo loser must be renamed and left at dir root, files=%v", projectFiles(t, dir))
	}
}

// --re-space-order shortens an over-long band and is never triggered by --repair.
func TestDoctorReSpaceOrder(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	longKey := "a1" + strings.Repeat("V", 80)
	addProject(t, dir, "wc-aaaa", "a", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-bbbb", "b", "todo", longKey, "# B\n", false, "")
	addProject(t, dir, "wc-cccc", "c", "todo", "a2", "# C\n", false, "")

	// The report locates the offending file by path, like every other per-project line —
	// with no ambient scope the report spans every scope, so the path is what says which
	// dir to open.
	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("bare doctor: %v", err)
	}
	if !strings.Contains(out, "order_long: wc-bbbb") || !strings.Contains(out, filepath.Join(dir, "wc-bbbb-b.md")) {
		t.Errorf("order_long line should name the project and its path, got %q", out)
	}
	// --repair must NOT touch the long key.
	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	if fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order") != longKey {
		t.Errorf("--repair must not re-space an over-long key")
	}
	// --re-space-order shortens it.
	if _, _, err := run(t, app, "doctor", "--re-space-order"); err != nil {
		t.Fatalf("doctor --re-space-order: %v", err)
	}
	got := fmValue(t, filepath.Join(dir, "wc-bbbb-b.md"), "order")
	if len(got) > 64 {
		t.Errorf("--re-space-order must shorten the key, got %d chars", len(got))
	}
	if got <= "a0" || got >= "a2" {
		t.Errorf("re-space must preserve order, got %q", got)
	}
}

func TestDoctorMutatingScopeSelection(t *testing.T) {
	app := newApp(t)
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	// No ambient and no --all: usage error (exit 2) naming the three ways to select.
	_ = os.Unsetenv("PJ_SCOPE")
	if _, _, err := run(t, app, "doctor", "--repair"); ExitCodeFromError(err) != exitUsage {
		t.Errorf("mutating doctor with no scope should exit 2, got %v", err)
	}
	// --all repairs without an ambient scope.
	if _, _, err := run(t, app, "doctor", "--repair", "--all"); err != nil {
		t.Errorf("doctor --repair --all should run, got %v", err)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("--all should have repaired the collision, files=%v", projectFiles(t, dir))
	}
}

func TestDoctorStructuralAndCreatedClasses(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	// A file whose frontmatter created is date-only (non-RFC3339) and legal otherwise.
	fm := "---\nid: wc-ab2c\nstatus: todo\norder: \"a0\"\ncreated: 2026-06-20\n---\n# X\n"
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-x.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "created value missing or not RFC3339 in wc-ab2c") {
		t.Errorf("doctor should flag a non-RFC3339 created, got %q", out)
	}
	// This class carries no token by design, so no report line may open with the shape of
	// one — a lowercase word and a colon is what an agent scans for.
	for _, line := range lines(out) {
		if word, _, found := strings.Cut(line, ": "); found && !strings.ContainsAny(word, " /") {
			if !token.HasKnownPrefix(word + ":") {
				t.Errorf("token-shaped prefix %q is not in the closed catalogue: %q", word+":", line)
			}
		}
	}
	// The file was not mutated.
	if fmValue(t, filepath.Join(dir, "wc-ab2c-x.md"), "created") != "2026-06-20" {
		t.Errorf("bare doctor must not rewrite created")
	}
}

func TestDoctorSchemaWarnClasses(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	// self-related (schema_warn), an id-shaped link (schema_warn), a duplicate tag.
	extra := "related: [wc-ab2c]\nlinks: [wc-de34]\ntags: [x, x]\n"
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, extra)

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Count(out, "schema_warn:") < 2 {
		t.Errorf("doctor should ride multiple schema_warn lines, got %q", out)
	}
}

func TestDoctorReindexRebuilds(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	// --reindex never mutates project files and still reports cleanly.
	if _, _, err := run(t, app, "doctor", "--reindex"); err != nil {
		t.Fatalf("doctor --reindex: %v", err)
	}
	if !fileExists(dir, "wc-ab2c-x.md") {
		t.Errorf("--reindex must not touch project files")
	}

	// Non-mutating --reindex skips the union reconcile, so the rebuild and its
	// machine-wide reconcile are the only things populating the index. Seed a row, remove
	// its file behind pj's back, and the rebuild must derive the tree without it.
	addProject(t, dir, "wc-de34", "ghost", "todo", "a1", "# Ghost\n", false, "")
	if _, _, err := run(t, app, "list"); err != nil {
		t.Fatalf("seed index: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "wc-de34-ghost.md")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "doctor", "--reindex"); err != nil {
		t.Fatalf("doctor --reindex: %v", err)
	}
	list, _, err := run(t, app, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(list, "wc-de34") {
		t.Errorf("rebuild must drop the row for a removed file, got %q", list)
	}
	if !strings.Contains(list, "wc-ab2c") {
		t.Errorf("rebuild must keep the surviving project, got %q", list)
	}
}

// Re-entry after a crash inside the archive move's write-new-then-remove-old window
// completes the move; it must never be escalated into a duplicate-id rename.
func TestDoctorRepairResumesInterruptedArchiveMove(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	// The crash window: the new copy is written, the old copy is not yet removed.
	addProject(t, dir, "wc-ab2c", "ship", "done", "a0", "# Ship\n", false, "")
	addProject(t, dir, "wc-ab2c", "ship", "done", "a0", "# Ship\n", true, "")

	out, _, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	if strings.Contains(out, "repaired duplicate id:") {
		t.Fatalf("interrupted move must not be repaired as a collision, got %q", out)
	}
	files := projectFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("interrupted move must resolve to a single file, got %v", files)
	}
	if !fileExists(dir, filepath.Join("archive", "wc-ab2c-ship.md")) {
		t.Errorf("terminal project must end under archive/ with its id intact, got %v", files)
	}
	// A second run is a no-op.
	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("second doctor --repair: %v", err)
	}
	if got := projectFiles(t, dir); len(got) != 1 {
		t.Errorf("re-run must stay idempotent, got %v", got)
	}
}

// Re-entry after a crash inside the collision extension's write-new-then-remove-old
// window finishes that extension; it must never mint a second one.
func TestDoctorRepairResumesInterruptedExtension(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := initScope(t, app, "wc")
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# Alpha\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# Beta\n", false, "")

	stale, err := os.ReadFile(filepath.Join(dir, "wc-ab2c-beta.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	// Recreate the crash window: the extended file stands, the old-id file never went.
	if err := os.WriteFile(filepath.Join(dir, "wc-ab2c-beta.md"), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("re-entry doctor --repair: %v", err)
	}
	files := projectFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("re-entry must leave two files, got %v", files)
	}
	if fileExists(dir, "wc-ab2c-beta.md") {
		t.Errorf("stale old-id file must be removed, got %v", files)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("loser must stay under its first extension, got %v", files)
	}
	if fileExists(dir, "wc-ab2cb-beta.md") {
		t.Errorf("re-entry must not mint a second extension, got %v", files)
	}
}

// Under --all one unreachable scope must skip, not stop the run: the flock is a file in
// the scope dir, so taking it before checking reachability would abort every remaining
// scope over one unplugged drive.
func TestDoctorRepairAllSkipsUnreachableScope(t *testing.T) {
	app := newApp(t)
	gone := initScope(t, app, "gone")
	live := initScope(t, app, "wc")
	addProject(t, live, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, live, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	_ = os.Unsetenv("PJ_SCOPE")
	_, errOut, err := run(t, app, "doctor", "--repair", "--all")
	if err != nil {
		t.Fatalf("--all must survive an unreachable scope, got %v", err)
	}
	if !strings.Contains(errOut, "skipping gone: dir unreachable") {
		t.Errorf("the unreachable scope should be reported as skipped, got %q", errOut)
	}
	if !fileExists(live, "wc-ab2ca-beta.md") {
		t.Errorf("the reachable scope must still be repaired, files=%v", projectFiles(t, live))
	}
}
