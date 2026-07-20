package gitstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyStableAndHex(t *testing.T) {
	k1 := Key("/repo/one")
	k2 := Key("/repo/one")
	if k1 != k2 {
		t.Error("Key must be stable for the same path")
	}
	if len(k1) != 64 {
		t.Errorf("Key must be 64 hex chars, got %d", len(k1))
	}
	if Key("/repo/one") == Key("/repo/two") {
		t.Error("distinct paths must key differently")
	}
	// Cleaning is applied: an uncleaned path keys the same as its clean form.
	if Key("/repo/one/") != Key("/repo/one") {
		t.Error("Key must clean the path before hashing")
	}
}

func TestDirUnderStateHome(t *testing.T) {
	got := Dir("/state/pj", "/repo/one")
	want := filepath.Join("/state/pj", "git-roots", Key("/repo/one"))
	if got != want {
		t.Errorf("Dir = %q want %q", got, want)
	}
}

func TestCommitLockCreatesDirAndSerialises(t *testing.T) {
	state := t.TempDir()
	repo := "/repo/one"
	lock, err := AcquireCommitLock(state, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(state, repo), "sync.lock")); err != nil {
		t.Errorf("sync.lock should be created: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	// A second acquire after release succeeds.
	lock2, err := AcquireCommitLock(state, repo)
	if err != nil {
		t.Fatal(err)
	}
	_ = lock2.Release()
}

func TestReadLastPushError(t *testing.T) {
	state := t.TempDir()
	repo := "/repo/one"
	if _, ok := ReadLastPushError(state, repo); ok {
		t.Error("no marker should mean ok=false")
	}
	dir := Dir(state, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last-push-error"), []byte("  push rejected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	detail, ok := ReadLastPushError(state, repo)
	if !ok || detail != "push rejected" {
		t.Errorf("marker = %q ok=%v want trimmed detail", detail, ok)
	}
	// An empty marker reads as absent.
	if err := os.WriteFile(filepath.Join(dir, "last-push-error"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadLastPushError(state, repo); ok {
		t.Error("an empty marker should read as ok=false")
	}
}
