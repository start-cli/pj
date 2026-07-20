package reconcile

import (
	"fmt"
	"os"
	"path/filepath"
)

// SyncPaths write-throughs specific project paths after pj itself wrote them. Unlike
// a read-path reconcile it applies no mtime skip: pj knows these exact paths changed,
// so relying on the racy-index heuristic meant for external edits would be the wrong
// tool — on a coarse-mtime filesystem a same-second, same-size rewrite could be
// missed, leaving the index stale for pj's own mutation. Each present path is
// re-parsed and upserted; each absent path (the removed side of a terminal-boundary
// move) is deleted.
//
// It touches only the named paths — no dir re-scan and no warn-only integrity
// aggregates — so it is the write verbs' cheap, exact write-through and the shape the
// repair and sync write paths reuse. A caller that also wants the integrity view runs
// a full Reconcile instead.
func (r *Reconciler) SyncPaths(scope string, paths []string) error {
	for _, path := range paths {
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if err := r.db.DeleteByPath(path); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
		fullID, ok := projectID(scope, filepath.Base(path))
		if !ok {
			// A pj-written project path always matches the filename grammar; a mismatch
			// means the caller passed a non-project path, which owns no row to sync.
			continue
		}
		archived := filepath.Base(filepath.Dir(path)) == archiveDir
		p, edges, err := parseFile(path, scope, fullID, archived, fi.ModTime().UnixNano(), fi.Size())
		if err != nil {
			return err
		}
		if err := r.db.UpsertProjectWithEdges(p, edges); err != nil {
			return err
		}
	}
	return nil
}
