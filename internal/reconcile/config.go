package reconcile

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/scopeconfig"
)

// closureFile is one file in a config import closure with the stat that decides
// whether it changed. The JSON tags stay short because a copy is stored per scope.
type closureFile struct {
	Path    string `json:"p"`
	MtimeNS int64  `json:"m"`
	Size    int64  `json:"s"`
}

// schemaFor returns a scope's evaluated config, using the index cache when the
// config import closure is unchanged and falling back to a CUE evaluation
// otherwise. A *scopeconfig.ConfigError (returned as the second value) means the
// config is unusable — the scope's reads still work but its writes are blocked and
// it rides config_unparseable. The negative is cached too, so a still-broken,
// unchanged config is not re-evaluated every command.
func (r *Reconciler) schemaFor(scope, dir string) (*scopeconfig.Schema, *scopeconfig.ConfigError) {
	if entry, ok, err := r.db.ConfigCacheGet(scope); err == nil && ok {
		if files, ok := parseClosure(entry.ClosureJSON); ok && closureUnchanged(files) {
			if entry.ConfigError != "" {
				return nil, &scopeconfig.ConfigError{Dir: dir, Reason: entry.ConfigError}
			}
			var s scopeconfig.Schema
			if json.Unmarshal([]byte(entry.SchemaJSON), &s) == nil {
				return &s, nil
			}
		}
	}
	return r.evaluateAndCache(scope, dir)
}

// SchemaCached returns a scope's evaluated schema via the cache, or nil when its
// config is unusable. It is the read-side lookup for a scope a command did not
// reconcile but must still judge (a cross-scope depends target's terminal-ness),
// so it never reconciles rows and never surfaces the config error.
func (r *Reconciler) SchemaCached(scope, dir string) *scopeconfig.Schema {
	s, _ := r.schemaFor(scope, dir)
	return s
}

// SchemaOrError returns a scope's evaluated schema, or the config error when its
// config is unusable. It is the lookup for a command that must judge a scope it did
// not reconcile and needs to report why the config is unusable — doctor's per-git-root
// sibling preflight. Like SchemaCached it never reconciles rows.
func (r *Reconciler) SchemaOrError(scope, dir string) (*scopeconfig.Schema, *scopeconfig.ConfigError) {
	return r.schemaFor(scope, dir)
}

// evaluateAndCache runs the cold path: evaluate the config through CUE, discover
// its closure, stat every closure file, and store the result (schema or the config
// error) under those stats for the next command to reuse.
func (r *Reconciler) evaluateAndCache(scope, dir string) (*scopeconfig.Schema, *scopeconfig.ConfigError) {
	schema, closurePaths, loadErr := scopeconfig.LoadWithClosure(r.ctx, dir)
	entry := index.ConfigCacheEntry{ClosureJSON: marshalClosure(statClosure(closurePaths))}

	if loadErr != nil {
		ce, ok := scopeconfig.AsConfigError(loadErr)
		if !ok {
			ce = &scopeconfig.ConfigError{Dir: dir, Reason: loadErr.Error()}
		}
		entry.ConfigError = ce.Reason
		_ = r.db.ConfigCacheSet(scope, entry)
		return nil, ce
	}

	if b, err := json.Marshal(schema); err == nil {
		entry.SchemaJSON = string(b)
	}
	_ = r.db.ConfigCacheSet(scope, entry)
	return schema, nil
}

// statClosure stats each closure path, recording (mtime, size). A path that cannot
// be stated is recorded with zeroes so its later reappearance or change is still a
// closure change (the key differs), forcing a re-evaluation rather than a stale hit.
func statClosure(paths []string) []closureFile {
	out := make([]closureFile, 0, len(paths))
	for _, p := range paths {
		cf := closureFile{Path: p}
		if fi, err := os.Stat(p); err == nil {
			cf.MtimeNS = fi.ModTime().UnixNano()
			cf.Size = fi.Size()
		}
		out = append(out, cf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// closureUnchanged reports whether every recorded closure file still has the same
// (mtime, size) on disk. Any drift — a changed sibling schema, a vanished import —
// means the cached schema may be stale and the config must be re-evaluated.
func closureUnchanged(files []closureFile) bool {
	for _, f := range files {
		fi, err := os.Stat(f.Path)
		if err != nil {
			return false
		}
		if fi.ModTime().UnixNano() != f.MtimeNS || fi.Size() != f.Size {
			return false
		}
	}
	return true
}

func marshalClosure(files []closureFile) string {
	b, err := json.Marshal(files)
	if err != nil {
		return ""
	}
	return string(b)
}

func parseClosure(s string) ([]closureFile, bool) {
	if s == "" {
		return nil, false
	}
	var files []closureFile
	if err := json.Unmarshal([]byte(s), &files); err != nil {
		return nil, false
	}
	return files, true
}
