package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyInPlaceRewrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched, err := Apply([]Op{{OldPath: p, NewPath: p, Content: []byte("new")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || touched[0] != p {
		t.Errorf("in-place touched = %v want [%s]", touched, p)
	}
	if data, _ := os.ReadFile(p); string(data) != "new" {
		t.Errorf("content = %q want new", data)
	}
}

func TestApplyMoveWritesNewRemovesOld(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	if err := os.WriteFile(old, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	touched, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("body2")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old path must be removed after a move")
	}
	if data, _ := os.ReadFile(newp); string(data) != "body2" {
		t.Errorf("new content = %q want body2", data)
	}
	if len(touched) != 2 {
		t.Errorf("move should touch new and old, got %v", touched)
	}
}

// Two projects can compute the same destination basename (a duplicate id carrying the
// same frozen slug), and a move onto one of them would erase it with no copy left, since
// the source is removed straight after. The move refuses; both files survive.
func TestApplyRefusesMoveOntoDifferentFile(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	if err := os.WriteFile(old, []byte("mover"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newp, []byte("occupant"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("mover")}})
	if err == nil {
		t.Fatal("move onto an occupied destination must refuse")
	}
	if data, _ := os.ReadFile(newp); string(data) != "occupant" {
		t.Errorf("destination must be untouched, got %q", data)
	}
	if data, _ := os.ReadFile(old); string(data) != "mover" {
		t.Errorf("source must be untouched, got %q", data)
	}
}

// The both-present window of an interrupted move — destination already holding exactly
// the bytes the op writes — is not an occupied destination: the move completes.
func TestApplyCompletesInterruptedMove(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	for _, p := range []string{old, newp} {
		if err := os.WriteFile(p, []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("same")}}); err != nil {
		t.Fatalf("completing an interrupted move: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old path must be removed once the move completes")
	}
}

// A re-run after a crash where the new path is already present and the old is gone is
// a no-op — the plan re-enters idempotently without double-writing.
func TestApplyIdempotentReentry(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	newp := filepath.Join(dir, "new.md")
	if err := os.WriteFile(newp, []byte("already"), 0o644); err != nil {
		t.Fatal(err)
	}
	// old does not exist; the op is already done.
	if _, err := Apply([]Op{{OldPath: old, NewPath: newp, Content: []byte("would-overwrite")}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(newp); string(data) != "already" {
		t.Errorf("idempotent re-entry must not overwrite, got %q", data)
	}
}
