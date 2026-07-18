package xdg

import (
	"path/filepath"
	"testing"
)

func TestConfigDirFromXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	got, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/custom/config", "pj"); got != want {
		t.Errorf("ConfigDir=%q want %q", got, want)
	}
}

func TestConfigDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/tester")
	got, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/home/tester", ".config", "pj"); got != want {
		t.Errorf("ConfigDir=%q want %q", got, want)
	}
}

func TestAcquireConfigLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireConfigLock(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// A second acquire after release must succeed.
	lock2, err := AcquireConfigLock(dir)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := lock2.Release(); err != nil {
		t.Fatalf("re-release: %v", err)
	}
	// Release is idempotent-safe on a nil lock.
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil release: %v", err)
	}
}
