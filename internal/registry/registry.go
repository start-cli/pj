// Package registry is the machine-local XDG config tier: the durable record of
// which scopes are registered on this machine, at which paths, plus the
// per-scope lens. It reads and regenerates registry.cue and lens.cue only
// through the CUE Go modules — load/compile to read, encode + cue/format to
// write — with each owned file regenerated wholesale (the files are
// machine-owned, so there is no hand-authored formatting to preserve) and
// installed by an atomic same-directory rename.
//
// The registry is the bootstrap that locates every scope, so an XDG file that
// will not parse is a hard error naming the file — there is nothing to degrade
// to. Callers hold the machine-global flock (see package xdg) across the whole
// load/validate/write cycle; this package does not lock.
package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"

	"github.com/start-cli/pj/internal/atomicfile"
)

const (
	registryFile = "registry.cue"
	lensFile     = "lens.cue"
)

// Entry is a single scope's registration: the two independent absolute paths the
// registry stores. The git repo is not stored — it is derived on demand from
// Dir. The scope name is the map key (a cached copy; pj.cue is authoritative).
type Entry struct {
	// Dir is where the scope's .md files and pj.cue physically live.
	Dir string `json:"dir"`
	// Root is the code-root: the single tree under which the scope is ambient.
	Root string `json:"root"`
}

// Registry is the loaded XDG config tier: the scope records and the machine-local
// lens, keyed by scope name.
type Registry struct {
	Scopes map[string]Entry
	Lens   map[string][]string
}

// Store performs CUE I/O for the XDG config tier under a fixed directory.
type Store struct {
	ctx *cue.Context
	dir string
}

// NewStore builds a Store over configDir using the process-wide CUE context.
func NewStore(ctx *cue.Context, configDir string) *Store {
	return &Store{ctx: ctx, dir: configDir}
}

// Load reads registry.cue and lens.cue. A missing file yields an empty section
// (pj runs on built-in defaults when the XDG tier is absent). A file that will
// not compile is a hard error naming it.
func (s *Store) Load() (*Registry, error) {
	reg := &Registry{Scopes: map[string]Entry{}, Lens: map[string][]string{}}

	if v, ok, err := s.compileFile(registryFile); err != nil {
		return nil, err
	} else if ok {
		var rc struct {
			Scopes map[string]Entry `json:"scopes"`
		}
		if err := v.Decode(&rc); err != nil {
			return nil, fmt.Errorf("%s is malformed: %w", filepath.Join(s.dir, registryFile), err)
		}
		if rc.Scopes != nil {
			reg.Scopes = rc.Scopes
		}
	}

	if v, ok, err := s.compileFile(lensFile); err != nil {
		return nil, err
	} else if ok {
		var lc struct {
			Lens map[string][]string `json:"lens"`
		}
		if err := v.Decode(&lc); err != nil {
			return nil, fmt.Errorf("%s is malformed: %w", filepath.Join(s.dir, lensFile), err)
		}
		if lc.Lens != nil {
			reg.Lens = lc.Lens
		}
	}

	return reg, nil
}

// compileFile reads and compiles one owned file. ok is false when the file is
// absent; a compile failure is a hard error naming the file.
func (s *Store) compileFile(name string) (cue.Value, bool, error) {
	p := filepath.Join(s.dir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cue.Value{}, false, nil
		}
		return cue.Value{}, false, fmt.Errorf("read %s: %w", p, err)
	}
	v := s.ctx.CompileBytes(data, cue.Filename(p))
	if err := v.Err(); err != nil {
		return cue.Value{}, false, fmt.Errorf("%s will not parse — fix or remove it: %w", p, err)
	}
	return v, true, nil
}

// WriteRegistry regenerates registry.cue from scopes and installs it atomically.
func (s *Store) WriteRegistry(scopes map[string]Entry) error {
	if scopes == nil {
		scopes = map[string]Entry{}
	}
	return s.writeOwned(registryFile, map[string]any{"scopes": scopes})
}

// WriteLens regenerates lens.cue from lens and installs it atomically.
func (s *Store) WriteLens(lens map[string][]string) error {
	if lens == nil {
		lens = map[string][]string{}
	}
	return s.writeOwned(lensFile, map[string]any{"lens": lens})
}

// writeOwned encodes model to CUE, formats it as a top-level file (no wrapping
// braces), and installs it by writing a temp file in the same directory and
// renaming over the target. Map keys serialize in sorted order, so the output
// is deterministic across runs.
func (s *Store) writeOwned(name string, model any) error {
	v := s.ctx.Encode(model)
	if err := v.Err(); err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	node := v.Syntax(cue.Concrete(true), cue.Final())
	st, ok := node.(*ast.StructLit)
	if !ok {
		return fmt.Errorf("encode %s: unexpected CUE syntax %T", name, node)
	}
	file := &ast.File{Decls: st.Elts}
	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", name, err)
	}
	return atomicfile.Write(filepath.Join(s.dir, name), data, 0o600)
}
