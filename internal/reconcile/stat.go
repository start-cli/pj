package reconcile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/start-cli/pj/internal/id"
)

// archiveDir is the lone tool-managed subdirectory reconcile scans, and only its
// immediate children (nested paths under it are residue, not projects).
const archiveDir = "archive"

// statEntry is the on-disk stat of one candidate project file.
type statEntry struct {
	FullID   string
	Archived bool
	MtimeNS  int64
	Size     int64
}

// statScope lists the project files of a scope: the dir root and the immediate
// children of its one archive/ subdirectory, no deeper. reachable is false when the
// scope dir cannot be listed (unmounted, deleted, permission/I/O error), which the
// caller isolates — it leaves the scope's rows in place and rides unreachable_scope,
// never a rebuild or an auto-forget. A missing archive/ is normal (a scope with no
// terminal projects) and not an error.
func statScope(scope, dir string) (files map[string]statEntry, reachable bool) {
	files = map[string]statEntry{}
	if !collectDir(scope, dir, false, files) {
		return nil, false
	}
	// archive/ is optional; its absence is correct state, not an error, so a failed
	// listing there does not mark the whole scope unreachable.
	collectDir(scope, filepath.Join(dir, archiveDir), true, files)
	return files, true
}

// collectDir adds the project files directly under root to files, flagging archived.
// ok is false only when root itself cannot be read; a per-entry stat failure skips
// that entry without failing the scope.
func collectDir(scope, root string, archived bool, files map[string]statEntry) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fullID, ok := projectID(scope, e.Name())
		if !ok {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(root, e.Name())
		files[path] = statEntry{
			FullID:   fullID,
			Archived: archived,
			MtimeNS:  fi.ModTime().UnixNano(),
			Size:     fi.Size(),
		}
	}
	return true
}

// projectID extracts the full project id from a filename of the form
// <scope>-<short-id>[-<slug>].md, or reports ok=false for any other file (residue
// like AGENTS.md, README.md, a foreign scope's file, or a malformed name). The id
// is derived from the filename so a parse_error file — whose frontmatter cannot be
// trusted — is still locatable.
func projectID(scope, name string) (string, bool) {
	rest, ok := strings.CutSuffix(name, ".md")
	if !ok {
		return "", false
	}
	prefix := scope + "-"
	if !strings.HasPrefix(rest, prefix) {
		return "", false
	}
	tail := rest[len(prefix):]
	short := tail
	if i := strings.IndexByte(tail, '-'); i >= 0 {
		short = tail[:i]
	}
	if !id.IsShortID(short) {
		return "", false
	}
	return scope + "-" + short, true
}
