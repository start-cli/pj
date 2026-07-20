package scopeconfig

import (
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/load"
)

// LoadWithClosure evaluates a scope's pj.cue through the CUE loader and returns
// both the validated Schema and the import closure: every .cue file whose content
// the evaluation depends on. The reconcile eval cache keys on the closure's
// (path, mtime, size), so a change to any closure file — the entry pj.cue or a
// sibling schema file it unifies with — invalidates the cached schema, while an
// unchanged closure serves the cached value without re-entering CUE.
//
// A scope whose pj.cue declares a package loads as the whole directory package (so
// sibling files unify); the minimal package-less file init writes loads as a single
// file. Either way the returned closure lists exactly the files CUE built from, so
// closure and evaluation can never disagree. The closure always includes the entry
// pj.cue path even when the file is absent, so creating it later invalidates the
// cached "absent" negative.
func LoadWithClosure(ctx *cue.Context, dir string) (*Schema, []string, error) {
	entry := filepath.Join(dir, "pj.cue")
	if _, err := os.Stat(entry); err != nil {
		if os.IsNotExist(err) {
			return nil, []string{entry}, &ConfigError{Dir: dir, Reason: "pj.cue is absent"}
		}
		return nil, []string{entry}, &ConfigError{Dir: dir, Reason: "cannot read pj.cue: " + err.Error()}
	}

	inst := loadInstance(dir)
	closure := closureFiles(inst, entry)
	if inst.Err != nil {
		return nil, closure, &ConfigError{Dir: dir, Reason: cueReason(inst.Err)}
	}
	v := ctx.BuildInstance(inst)
	schema, err := Evaluate(dir, v)
	return schema, closure, err
}

// loadInstance loads a scope's config, preferring the directory package so a
// multi-file scope unifies, and falling back to the single entry file for the
// package-less minimal config. A directory load that fails only because the files
// carry no package clause is not a real error — it is the package-less case — so it
// retries as a single file rather than reporting a spurious config error.
func loadInstance(dir string) *build.Instance {
	cfg := &load.Config{Dir: dir}
	if insts := load.Instances([]string{"."}, cfg); len(insts) > 0 && insts[0].Err == nil {
		return insts[0]
	}
	insts := load.Instances([]string{"./pj.cue"}, cfg)
	if len(insts) == 0 {
		// The loader always returns at least one instance for a named file arg; a
		// zero-length result would be a CUE-internal contract break, surfaced as a
		// synthetic errored instance so the caller reports it rather than panicking.
		return &build.Instance{Dir: dir}
	}
	return insts[0]
}

// closureFiles collects the absolute paths of every file the instance built from,
// plus its imported dependencies' files, always including the entry pj.cue. The set
// is deduplicated; order does not matter because the cache key sorts it.
func closureFiles(inst *build.Instance, entry string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(entry)
	if inst != nil {
		for _, f := range inst.BuildFiles {
			add(f.Filename)
		}
		for _, dep := range inst.Imports {
			for _, f := range dep.BuildFiles {
				add(f.Filename)
			}
		}
	}
	return out
}
