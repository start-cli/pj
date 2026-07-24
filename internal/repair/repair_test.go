package repair

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/start-cli/pj/internal/order"
	"github.com/start-cli/pj/internal/rewrite"
)

// writeProj writes a project file and returns its path. An empty created omits the
// key (a degraded created).
func writeProj(t *testing.T, dir, fullID, slug, created, orderKey, body string, archived bool) string {
	t.Helper()
	target := dir
	if archived {
		target = filepath.Join(dir, "archive")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nid: " + fullID + "\nstatus: todo\norder: \"" + orderKey + "\"\n"
	if created != "" {
		fm += "created: " + created + "\n"
	}
	fm += "---\n" + body
	path := filepath.Join(target, fullID+"-"+slug+".md")
	if err := os.WriteFile(path, []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func row(path, fullID, orderKey string) Row {
	short := fullID[strings.IndexByte(fullID, '-')+1:]
	return Row{Path: path, FullID: fullID, ShortID: short, OrderKey: orderKey}
}

// The loser pick renames the newer by created; two runs (any input order) agree.
func TestDuplicateIDPicksNewerByCreated(t *testing.T) {
	dir := t.TempDir()
	older := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	newer := writeProj(t, dir, "wc-ab2c", "beta", "2026-06-01T00:00:00Z", "a1", "# B\n", false)

	for _, in := range [][]Row{
		{row(older, "wc-ab2c", "a0"), row(newer, "wc-ab2c", "a1")},
		{row(newer, "wc-ab2c", "a1"), row(older, "wc-ab2c", "a0")}, // reversed input
	} {
		occ := map[string]string{"ab2c": older}
		ops, renames, err := DuplicateID("wc", in, occ)
		if err != nil {
			t.Fatal(err)
		}
		if len(renames) != 1 {
			t.Fatalf("one loser expected, got %d", len(renames))
		}
		if renames[0].OldPath != newer {
			t.Errorf("newer (by created) must be renamed, got %s", renames[0].OldPath)
		}
		if !strings.HasPrefix(renames[0].NewID, "wc-ab2c") || renames[0].NewID == "wc-ab2c" {
			t.Errorf("loser id must be an extension of ab2c, got %s", renames[0].NewID)
		}
		if len(ops) != 1 || ops[0].OldPath != newer {
			t.Errorf("op should rewrite the loser file: %v", ops)
		}
	}
}

// A degraded (absent) created is not-newer-than-any: it is kept, the valid-created
// side is renamed.
func TestDuplicateIDDegradedCreatedKept(t *testing.T) {
	dir := t.TempDir()
	degraded := writeProj(t, dir, "wc-ab2c", "alpha", "", "a0", "# A\n", false)
	valid := writeProj(t, dir, "wc-ab2c", "beta", "2020-01-01T00:00:00Z", "a1", "# B\n", false)

	occ := map[string]string{"ab2c": degraded}
	_, renames, err := DuplicateID("wc", []Row{row(degraded, "wc-ab2c", "a0"), row(valid, "wc-ab2c", "a1")}, occ)
	if err != nil {
		t.Fatal(err)
	}
	if renames[0].OldPath != valid {
		t.Errorf("degraded created must be kept; the valid side renamed, got %s renamed", renames[0].OldPath)
	}
}

// Equal created falls to the basename tie-break: the greater basename is renamed.
func TestDuplicateIDBasenameTieBreak(t *testing.T) {
	dir := t.TempDir()
	alpha := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	beta := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-01T00:00:00Z", "a1", "# B\n", false)

	occ := map[string]string{"ab2c": alpha}
	_, renames, err := DuplicateID("wc", []Row{row(alpha, "wc-ab2c", "a0"), row(beta, "wc-ab2c", "a1")}, occ)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renames[0].OldPath) != "wc-ab2c-beta.md" {
		t.Errorf("greater basename must be renamed, got %s", renames[0].OldPath)
	}
}

// Equal created and basename (same name at root and archive) falls to the SHA-256
// tie-break: the greater digest is renamed, deterministically.
func TestDuplicateIDSHATieBreak(t *testing.T) {
	dir := t.TempDir()
	rootFile := writeProj(t, dir, "wc-ab2c", "same", "2026-01-01T00:00:00Z", "a0", "# ROOT BODY\n", false)
	archFile := writeProj(t, dir, "wc-ab2c", "same", "2026-01-01T00:00:00Z", "a0", "# ARCHIVE BODY\n", true)

	rootBytes, _ := os.ReadFile(rootFile)
	archBytes, _ := os.ReadFile(archFile)
	rootSum := sha256.Sum256(rootBytes)
	archSum := sha256.Sum256(archBytes)
	wantRenamed := rootFile
	if string(archSum[:]) > string(rootSum[:]) {
		wantRenamed = archFile
	}

	occ := map[string]string{"ab2c": rootFile}
	_, renames, err := DuplicateID("wc", []Row{row(rootFile, "wc-ab2c", "a0"), row(archFile, "wc-ab2c", "a0")}, occ)
	if err != nil {
		t.Fatal(err)
	}
	if renames[0].OldPath != wantRenamed {
		t.Errorf("greater SHA-256 must be renamed, got %s want %s", renames[0].OldPath, wantRenamed)
	}
}

// A collision whose members are already at the max short-id length hard-fails naming
// both paths rather than inventing a non-prefix id.
func TestDuplicateIDCapExhaustionHardFails(t *testing.T) {
	dir := t.TempDir()
	p1 := writeProj(t, dir, "wc-abcdefgh", "one", "2026-01-01T00:00:00Z", "a0", "# One\n", false)
	p2 := writeProj(t, dir, "wc-abcdefgh", "two", "2026-02-01T00:00:00Z", "a1", "# Two\n", false)

	occ := map[string]string{"abcdefgh": p1}
	_, _, err := DuplicateID("wc", []Row{row(p1, "wc-abcdefgh", "a0"), row(p2, "wc-abcdefgh", "a1")}, occ)
	if err == nil {
		t.Fatal("cap-8 exhaustion must hard-fail")
	}
	if !strings.Contains(err.Error(), p1) && !strings.Contains(err.Error(), p2) {
		t.Errorf("exhaustion error should name the collided paths, got %v", err)
	}
}

// KeepBefore is the shared collision pick both DuplicateID and the frontmatter merge
// package call. It is asserted directly, over in-memory members, so the add/add route
// (which never touches disk) is covered by the same total order the disk repair uses.
func TestKeepBeforeTotalOrder(t *testing.T) {
	older := LoserMember{Created: "2026-01-01T00:00:00Z", Basename: "b", Raw: []byte("z"), Path: "/z"}
	newer := LoserMember{Created: "2026-06-01T00:00:00Z", Basename: "a", Raw: []byte("a"), Path: "/a"}
	if !KeepBefore(older, newer) || KeepBefore(newer, older) {
		t.Error("older by created must be kept over newer regardless of the other terms")
	}

	degraded := LoserMember{Created: "", Basename: "z", Raw: []byte("z"), Path: "/z"}
	valid := LoserMember{Created: "2020-01-01T00:00:00Z", Basename: "a", Raw: []byte("a"), Path: "/a"}
	if !KeepBefore(degraded, valid) || KeepBefore(valid, degraded) {
		t.Error("a degraded created is not-newer-than-any and must be kept")
	}

	// Equal created and basename (the add/add placeholder case): smaller hash is kept.
	sameCreated := "2026-01-01T00:00:00Z"
	a := LoserMember{Created: sameCreated, Basename: "", Raw: []byte("AAA"), Path: ""}
	b := LoserMember{Created: sameCreated, Basename: "", Raw: []byte("BBB"), Path: ""}
	wantAKept := bytes.Compare(sha(a.Raw), sha(b.Raw)) < 0
	if KeepBefore(a, b) != wantAKept || KeepBefore(b, a) == wantAKept {
		t.Error("equal created+basename must break by smaller SHA-256 of raw bytes, symmetrically")
	}
}

// EqualOrder re-spaces only the tied files into distinct keys, preserving (order, id)
// order and leaving untied neighbours untouched.
func TestEqualOrderRespacePreservesOrder(t *testing.T) {
	dir := t.TempDir()
	a := writeProj(t, dir, "wc-aaaa", "a", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	b := writeProj(t, dir, "wc-bbbb", "b", "2026-01-01T00:00:00Z", "a1", "# B\n", false)
	c := writeProj(t, dir, "wc-cccc", "c", "2026-01-01T00:00:00Z", "a1", "# C\n", false)
	e := writeProj(t, dir, "wc-eeee", "e", "2026-01-01T00:00:00Z", "a2", "# E\n", false)

	rows := []Row{row(a, "wc-aaaa", "a0"), row(b, "wc-bbbb", "a1"), row(c, "wc-cccc", "a1"), row(e, "wc-eeee", "a2")}
	ops, err := EqualOrder(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rewrite.Apply(ops); err != nil {
		t.Fatal(err)
	}
	ka, kb, kc, ke := orderOf(t, a), orderOf(t, b), orderOf(t, c), orderOf(t, e)
	// Untied anchors are untouched.
	if ka != "a0" || ke != "a2" {
		t.Errorf("untied neighbours must not move: a=%q e=%q", ka, ke)
	}
	// The tied pair is now distinct and preserves id order (b < c) within (a0, a2).
	if ka >= kb || kb >= kc || kc >= ke {
		t.Errorf("re-space must preserve order: %q %q %q %q", ka, kb, kc, ke)
	}
	if kb == kc {
		t.Errorf("tied keys must become distinct, both %q", kb)
	}
}

// LongOrder shortens a pathologically long key while preserving order.
func TestLongOrderShortens(t *testing.T) {
	dir := t.TempDir()
	longKey := "a1" + strings.Repeat("V", 80)
	if !order.Valid(longKey) {
		t.Fatalf("test key not valid: %q", longKey)
	}
	a := writeProj(t, dir, "wc-aaaa", "a", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	b := writeProj(t, dir, "wc-bbbb", "b", "2026-01-01T00:00:00Z", longKey, "# B\n", false)
	e := writeProj(t, dir, "wc-eeee", "e", "2026-01-01T00:00:00Z", "a2", "# E\n", false)

	rows := []Row{row(a, "wc-aaaa", "a0"), row(b, "wc-bbbb", longKey), row(e, "wc-eeee", "a2")}
	ops, err := LongOrder(rows)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rewrite.Apply(ops); err != nil {
		t.Fatal(err)
	}
	kb := orderOf(t, b)
	if len(kb) > OrderLongThreshold {
		t.Errorf("long key must be shortened, got %d chars", len(kb))
	}
	if kb <= orderOf(t, a) || kb >= orderOf(t, e) {
		t.Errorf("re-space must preserve order, got %q between %q and %q", kb, orderOf(t, a), orderOf(t, e))
	}
}

func orderOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "order:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "order:")), `"`)
		}
	}
	return ""
}

// A same-id pair that is one byte-identical copy at root and one under archive/ is the
// crash window of an in-flight layout move, never a collision.
func TestInterruptedMove(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, dir string) []Row
		want  bool
	}{
		{
			name: "identical copies across the archive boundary resume",
			build: func(t *testing.T, dir string) []Row {
				a := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				b := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", true)
				return []Row{row(a, "wc-ab2c", "a0"), row(b, "wc-ab2c", "a0")}
			},
			want: true,
		},
		{
			name: "differing bytes across the boundary are a real collision",
			build: func(t *testing.T, dir string) []Row {
				a := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				b := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# Different\n", true)
				return []Row{row(a, "wc-ab2c", "a0"), row(b, "wc-ab2c", "a0")}
			},
			want: false,
		},
		{
			name: "two copies on the same side are a real collision",
			build: func(t *testing.T, dir string) []Row {
				a := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				b := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
				return []Row{row(a, "wc-ab2c", "a0"), row(b, "wc-ab2c", "a0")}
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rows := tc.build(t, dir)
			got, err := InterruptedMove(dir, rows)
			if err != nil {
				t.Fatalf("InterruptedMove: %v", err)
			}
			if got != tc.want {
				t.Errorf("InterruptedMove = %v, want %v", got, tc.want)
			}
		})
	}
}

// Re-entry across the both-present crash window finishes the interrupted extension
// rather than minting a second one under a different id.
func TestDuplicateIDResumesInterruptedExtension(t *testing.T) {
	dir := t.TempDir()
	kept := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	loser := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-02T00:00:00Z", "a1", "# B\n", false)
	rows := []Row{row(kept, "wc-ab2c", "a0"), row(loser, "wc-ab2c", "a1")}

	occupied := map[string]string{"ab2c": kept}
	ops, renames, err := DuplicateID("wc", rows, occupied)
	if err != nil {
		t.Fatalf("DuplicateID: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("want one rename op, got %d", len(ops))
	}

	// The crash window: the new-id file is written, the old-id file is not yet removed.
	if err := os.WriteFile(ops[0].NewPath, ops[0].Content, 0o644); err != nil {
		t.Fatal(err)
	}
	extended := strings.TrimPrefix(renames[0].NewID, "wc-")

	reOps, reRenames, err := DuplicateID("wc", rows, map[string]string{"ab2c": kept, extended: ops[0].NewPath})
	if err != nil {
		t.Fatalf("re-entry DuplicateID: %v", err)
	}
	if len(reOps) != 1 {
		t.Fatalf("re-entry want one op, got %d", len(reOps))
	}
	if reRenames[0].NewID != renames[0].NewID {
		t.Errorf("re-entry must reuse the first extension %q, minted %q", renames[0].NewID, reRenames[0].NewID)
	}
	if reOps[0].NewPath != ops[0].NewPath {
		t.Errorf("re-entry must target the existing extended file %q, got %q", ops[0].NewPath, reOps[0].NewPath)
	}
	if reOps[0].OldPath != loser {
		t.Errorf("re-entry must remove the stale old-id file %q, got %q", loser, reOps[0].OldPath)
	}
	if !bytes.Equal(reOps[0].Content, ops[0].Content) {
		t.Errorf("re-entry must rewrite the same bytes")
	}
}

// A hand-edited extension is not this loser's content, so it is skipped and a fresh
// extension is minted rather than silently adopting an unrelated file.
func TestDuplicateIDIgnoresUnrelatedExtension(t *testing.T) {
	dir := t.TempDir()
	kept := writeProj(t, dir, "wc-ab2c", "alpha", "2026-01-01T00:00:00Z", "a0", "# A\n", false)
	loser := writeProj(t, dir, "wc-ab2c", "beta", "2026-01-02T00:00:00Z", "a1", "# B\n", false)
	other := writeProj(t, dir, "wc-ab2ca", "gamma", "2026-01-03T00:00:00Z", "a2", "# G\n", false)

	rows := []Row{row(kept, "wc-ab2c", "a0"), row(loser, "wc-ab2c", "a1")}
	_, renames, err := DuplicateID("wc", rows, map[string]string{"ab2c": kept, "ab2ca": other})
	if err != nil {
		t.Fatalf("DuplicateID: %v", err)
	}
	if renames[0].NewID == "wc-ab2ca" {
		t.Errorf("must not adopt an unrelated project's id")
	}
}

// A basename and a frontmatter id can disagree (doctor reports it as a structural class).
// The composed name still comes out well formed, built from the new id and the slug tail
// rather than from the disagreement.
func TestBasename(t *testing.T) {
	cases := []struct{ base, newID, want string }{
		{"wc-ab2c-beta.md", "wc-ab2ca", "wc-ab2ca-beta.md"},
		{"wc-ab2c.md", "wc-ab2ca", "wc-ab2ca.md"},
		{"wc-ab2c-multi-word-slug.md", "wc-ab2ca", "wc-ab2ca-multi-word-slug.md"},
		// The name carries a short-id the frontmatter does not claim.
		{"wc-zz9y-beta.md", "wc-ab2ca", "wc-ab2ca-beta.md"},
	}
	for _, tc := range cases {
		if got := Basename(tc.base, tc.newID); got != tc.want {
			t.Errorf("Basename(%q, %q) = %q, want %q", tc.base, tc.newID, got, tc.want)
		}
	}
}
