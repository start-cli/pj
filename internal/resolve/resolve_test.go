package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/token"
)

// scopeDir writes a pj.cue with the given name and returns the dir.
func scopeDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte("name: \""+name+"\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveScopeFlagWins(t *testing.T) {
	ctx := cuecontext.New()
	wcDir := scopeDir(t, "wc")
	apiDir := scopeDir(t, "api")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc":  {Dir: wcDir, Root: "/code/wc"},
		"api": {Dir: apiDir, Root: "/code/api"},
	}}
	got, err := Resolve(ctx, reg, Options{ScopeFlag: "api", EnvScope: "wc", Cwd: "/code/wc/sub"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "api" {
		t.Errorf("expected --scope to win, got %q", got.Name)
	}
}

func TestResolveEnvOverCodeRoot(t *testing.T) {
	ctx := cuecontext.New()
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc":  {Dir: scopeDir(t, "wc"), Root: "/code/wc"},
		"api": {Dir: scopeDir(t, "api"), Root: "/code/api"},
	}}
	got, err := Resolve(ctx, reg, Options{EnvScope: "api", Cwd: "/code/wc"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "api" {
		t.Errorf("expected PJ_SCOPE to win over code-root, got %q", got.Name)
	}
}

func TestResolveLongestPrefix(t *testing.T) {
	ctx := cuecontext.New()
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"outer": {Dir: scopeDir(t, "outer"), Root: "/repo"},
		"inner": {Dir: scopeDir(t, "inner"), Root: "/repo/frontend"},
	}}
	got, err := Resolve(ctx, reg, Options{Cwd: "/repo/frontend/src"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "inner" {
		t.Errorf("longest-prefix should pick inner, got %q", got.Name)
	}

	got, err = Resolve(ctx, reg, Options{Cwd: "/repo/backend"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "outer" {
		t.Errorf("elsewhere in repo should pick outer, got %q", got.Name)
	}
}

func TestResolveNoScope(t *testing.T) {
	ctx := cuecontext.New()
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: scopeDir(t, "wc"), Root: "/code/wc"},
	}}
	_, err := Resolve(ctx, reg, Options{Cwd: "/somewhere/else"})
	if !errors.Is(err, ErrNoScope) {
		t.Fatalf("expected ErrNoScope, got %v", err)
	}
}

func TestResolveUnknownOverrideNoFallthrough(t *testing.T) {
	ctx := cuecontext.New()
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: scopeDir(t, "wc"), Root: "/code/wc"},
	}}
	// An explicit override naming an unregistered scope must fail closed — never
	// fall through to the code-root tier even though cwd is under wc.
	_, err := Resolve(ctx, reg, Options{ScopeFlag: "ghost", Cwd: "/code/wc/sub"})
	var unknown *UnknownScopeError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected *UnknownScopeError, got %v", err)
	}
	if unknown.Name != "ghost" {
		t.Errorf("unknown name = %q", unknown.Name)
	}
}

func TestResolveDriftHardError(t *testing.T) {
	ctx := cuecontext.New()
	// registry key "wc" but pj.cue name is "renamed".
	dir := scopeDir(t, "renamed")
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: dir, Root: "/code/wc"},
	}}
	_, err := Resolve(ctx, reg, Options{ScopeFlag: "wc", Cwd: "/x"})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("expected *DriftError, got %v", err)
	}
	msg := drift.Error()
	if !strings.HasPrefix(msg, token.NameDrift) {
		t.Errorf("drift message must start with the name_drift token: %q", msg)
	}
	for _, want := range []string{`"wc"`, `"renamed"`, dir, "pj scope forget wc", "pj scope import"} {
		if !strings.Contains(msg, want) {
			t.Errorf("drift message missing %q: %q", want, msg)
		}
	}
}

func TestResolveUnparseableConfigStillResolves(t *testing.T) {
	ctx := cuecontext.New()
	dir := t.TempDir()
	// Absent pj.cue: name unreadable, but reads stay available so resolve succeeds.
	reg := &registry.Registry{Scopes: map[string]registry.Entry{
		"wc": {Dir: dir, Root: "/code/wc"},
	}}
	got, err := Resolve(ctx, reg, Options{ScopeFlag: "wc", Cwd: "/x"})
	if err != nil {
		t.Fatalf("resolve should succeed when name is unreadable (write path gates config): %v", err)
	}
	if got.Name != "wc" {
		t.Errorf("got %q", got.Name)
	}
}
