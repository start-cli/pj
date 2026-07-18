package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesFileAndDir(t *testing.T) {
	// The parent directory does not exist yet; Write must create it.
	path := filepath.Join(t.TempDir(), "sub", "file.txt")
	if err := Write(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("perm = %o want 0644", fi.Mode().Perm())
	}
}

func TestWriteOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := Write(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("overwrite content = %q want %q", got, "second")
	}
	// No temp files linger after a successful write.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the target file, got %d entries", len(entries))
	}
}

func TestWriteExplicitPermIgnoresUmask(t *testing.T) {
	// CreateTemp yields 0600; the explicit Chmod must widen it to the requested
	// mode regardless of the process umask.
	path := filepath.Join(t.TempDir(), "wide")
	if err := Write(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("perm = %o want 0644", fi.Mode().Perm())
	}
}
