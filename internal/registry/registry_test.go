package registry

import (
	"os"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestLoadEmpty(t *testing.T) {
	s := NewStore(cuecontext.New(), t.TempDir())
	reg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.Scopes) != 0 || len(reg.Lens) != 0 {
		t.Errorf("expected empty registry, got %+v", reg)
	}
}

func TestWriteAndReload(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(cuecontext.New(), dir)

	scopes := map[string]Entry{
		"wc": {Dir: "/home/g/webctl/.agents/pj", Root: "/home/g/webctl"},
		"ta": {Dir: "/org/mono/teamA/.agents/pj", Root: "/org/mono/teamA"},
	}
	if err := s.WriteRegistry(scopes); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	if err := s.WriteLens(map[string][]string{"wc": {"frontend", "style"}}); err != nil {
		t.Fatalf("WriteLens: %v", err)
	}

	reg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Scopes["wc"].Root != "/home/g/webctl" {
		t.Errorf("wc root = %q", reg.Scopes["wc"].Root)
	}
	if reg.Scopes["ta"].Dir != "/org/mono/teamA/.agents/pj" {
		t.Errorf("ta dir = %q", reg.Scopes["ta"].Dir)
	}
	if got := reg.Lens["wc"]; len(got) != 2 || got[0] != "frontend" {
		t.Errorf("lens = %v", got)
	}
}

func TestWriteEmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(cuecontext.New(), dir)
	if err := s.WriteRegistry(map[string]Entry{}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, registryFile))
	if err != nil {
		t.Fatal(err)
	}
	// Must still be valid CUE that reloads to an empty set.
	reg, err := s.Load()
	if err != nil {
		t.Fatalf("reload empty: %v", err)
	}
	if len(reg.Scopes) != 0 {
		t.Errorf("expected empty scopes, file was:\n%s", data)
	}
}

func TestLoadMalformedIsHardError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, registryFile), []byte("scopes: {{{ broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(cuecontext.New(), dir)
	if _, err := s.Load(); err == nil {
		t.Fatal("expected a hard error naming the file")
	}
}
