package reconcile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SyncPaths must reflect a write pj just made regardless of mtime: it upserts a
// present path unconditionally (no racy-index skip) and deletes an absent one.
func TestSyncPathsUpsertsAndDeletes(t *testing.T) {
	r, db := newReconciler(t)
	dir := mkScope(t, "wc")
	fp := filepath.Join(dir, "wc-ab2c-x.md")
	writeFile(t, fp, projFile("wc-ab2c", "todo", "a0", "# X\n"))

	now := time.Now().UnixNano()
	reconcileOne(t, r, "wc", dir, now)

	// A same-size status rewrite (todo -> done, both 4 chars) with the mtime pinned to
	// the original reconcile stamp: a read-path reconcile would skip this, but SyncPaths
	// must upsert it because pj knows it wrote it.
	writeFile(t, fp, projFile("wc-ab2c", "done", "a0", "# X\n"))
	if err := os.Chtimes(fp, time.Unix(0, now), time.Unix(0, now)); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncPaths("wc", []string{fp}); err != nil {
		t.Fatalf("SyncPaths upsert: %v", err)
	}
	rows, _ := db.ScopeProjects("wc")
	if len(rows) != 1 || rows[0].Status != "done" {
		t.Fatalf("SyncPaths must upsert the new state regardless of mtime, got %+v", rows)
	}

	// A removed path (the vanished side of a terminal-boundary move) is deleted.
	if err := os.Remove(fp); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncPaths("wc", []string{fp}); err != nil {
		t.Fatalf("SyncPaths delete: %v", err)
	}
	rows, _ = db.ScopeProjects("wc")
	if len(rows) != 0 {
		t.Fatalf("SyncPaths must delete a removed path's row, got %+v", rows)
	}
}
