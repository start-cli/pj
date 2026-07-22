package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/repair"
	"github.com/start-cli/pj/internal/rewrite"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/selfcommit"
	"github.com/start-cli/pj/internal/token"
	"github.com/start-cli/pj/internal/xdg"
)

// newScopeRenameCmd registers `pj scope rename <old> <new>`: the in-place rename that
// rewrites the pj.cue name, every project id, every filename, and every in-scope edge
// in one operation under the U22 durability contract, reports each cross-scope inbound
// edge as edge_verify, and re-keys this machine's registry and lens after the in-dir
// rewrite succeeds. An interrupted rename re-runs idempotently — the one verb exempt
// from the name_drift fail-close for exactly its <old> -> <new> transition.
func newScopeRenameCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a scope in place (pj.cue, ids, filenames, in-scope edges)",
		Long: "Rename a scope end to end: rewrite the pj.cue name, the <scope>- prefix of every\n" +
			"project id and filename, and every in-scope depends/related edge, then re-key this\n" +
			"machine's registry and lens. Cross-scope inbound edges live in other repos and are\n" +
			"reported as edge_verify, not rewritten. An interrupted rename re-runs idempotently.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(c *cobra.Command, args []string) error {
			return runScopeRename(app, c, args[0], args[1])
		},
	}
}

func runScopeRename(app *App, c *cobra.Command, oldName, newName string) error {
	if !id.IsScopeName(newName) {
		return usageErrorf("%q is not a legal scope name (^[a-z0-9]{1,12}$)", newName)
	}
	if oldName == newName {
		return usageErrorf("scope is already named %q", newName)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	entry, ok := e.reg.Scopes[oldName]
	if !ok {
		return fmt.Errorf("unknown scope %q — nothing to rename", oldName)
	}
	if _, taken := e.reg.Scopes[newName]; taken {
		return fmt.Errorf("scope name %q is already registered — names are machine-unique", newName)
	}
	dir := entry.Dir

	// Resolve directly from the registry key <old>, the name_drift exemption: a
	// pj.cue already reading <new> is this machine finishing its own interrupted
	// rename, not genuine drift. Any other name is real drift — recover via
	// forget+import, not rename.
	pjName, err := scopeconfig.ReadName(app.Ctx, dir)
	if err != nil {
		return fmt.Errorf("cannot rename %q: its pj.cue is unreadable: %w", oldName, err)
	}
	if pjName != oldName && pjName != newName {
		return fmt.Errorf("cannot rename %q to %q: its pj.cue name is %q — this is name drift, recover with pj scope forget %s && pj scope import %s", oldName, newName, pjName, oldName, dir)
	}

	// autoCommit governs self-commit; an unusable config is a write refusal.
	schema, err := scopeconfig.Load(app.Ctx, dir)
	if err != nil {
		if ce, isCfg := scopeconfig.AsConfigError(err); isCfg {
			return fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
				fmt.Sprintf("%s (%s): %s — fix pj.cue before renaming", oldName, ce.Dir, ce.Reason)))
		}
		return err
	}
	autoCommit := schema.AutoCommit
	root, hasRoot := gitRootFor(dir)
	if err := checkMidRebase(c.Context(), oldName, autoCommit, root, hasRoot); err != nil {
		return err
	}

	lock, err := acquireScopeLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	// Refresh the machine-wide index so the edges table reflects current on-disk
	// state, then read the cross-scope inbound edges (from other scopes' repos) to
	// report as edge_verify — they are surfaced, never rewritten here.
	if _, err := e.reconcileResult(e.allTargets()); err != nil {
		return err
	}
	inbound, err := e.db.EdgesToScope(oldName)
	if err != nil {
		return err
	}

	ops, err := renamePlan(dir, oldName, newName)
	if err != nil {
		return err
	}

	touched, err := rewrite.Apply(ops)
	if err != nil {
		return err
	}
	// The pj.cue name is written last (detectable-ordering rule): a crash before it
	// leaves old-prefixed leftovers a re-run finishes; a crash after it, before the
	// registry re-key, is the name_drift window this same command resolves on re-run.
	if pjName != newName {
		if err := scopeconfig.RewriteName(dir, newName); err != nil {
			return err
		}
	}
	touched = append(touched, filepath.Join(dir, "pj.cue"))

	// Auto-commit: one commit after every file write for the plan has succeeded.
	if autoCommit && hasRoot {
		if err := selfcommit.CommitPaths(c.Context(), selfcommit.BatchRequest{
			StateDir: app.StateDir, GitRoot: root,
			Message: fmt.Sprintf("pj: rename scope %s -> %s", oldName, newName), Paths: touched,
		}); err != nil {
			return err
		}
	} else if autoCommit && !hasRoot {
		stderrln(c, token.Line(token.SyncDisabled, fmt.Sprintf("%s: no git repository — renamed files written but not committed", newName)))
	}

	if err := e.rekeyRegistry(oldName, newName); err != nil {
		return err
	}
	if err := e.reindexRenamed(oldName, newName, dir); err != nil {
		return err
	}

	for _, ed := range inbound {
		if ed.FromScope == oldName {
			continue // in-scope edges were rewritten in place, not reported
		}
		stdoutln(c, token.Line(token.EdgeVerify, fmt.Sprintf("%s %s %s — target scope renamed to %s, update this reference", ed.FromID, ed.Kind, ed.ToID, newName)))
	}
	stderrln(c, fmt.Sprintf("renamed scope %s -> %s", oldName, newName))
	return nil
}

// renamePlan builds the rewrite ops for a scope rename: one op per project file whose
// prefix is still <old>, rewriting its frontmatter id, its in-scope depends/related
// edges, and its filename to <new>. A file already at the <new> prefix is a completed
// tail from an interrupted rename and is skipped. An unparseable project file refuses
// the whole rename (its frontmatter id cannot be safely rewritten).
func renamePlan(dir, oldName, newName string) ([]rewrite.Op, error) {
	files, err := listScopeProjectFiles(dir, oldName, newName)
	if err != nil {
		return nil, err
	}
	var ops []rewrite.Op
	for _, f := range files {
		base := filepath.Base(f)
		if hasScopePrefix(base, newName) {
			continue // already migrated
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		interior, body, present := frontmatter.Split(data)
		if !present {
			return nil, fmt.Errorf("cannot rename: %s has no frontmatter fence — fix it first", f)
		}
		m, err := frontmatter.Parse(interior)
		if err != nil {
			return nil, fmt.Errorf("cannot rename: %s has unparseable frontmatter — fix it first: %w", f, err)
		}
		// The frontmatter id is the authority on identity, not the filename: the two can
		// disagree (doctor reports it as a structural class), and re-prefixing the
		// filename's id instead would move the project onto a different id and dangle
		// every edge pointing at the real one. An id outside the old scope cannot be
		// re-prefixed without guessing, so it refuses rather than inventing one.
		if !id.IsFullProjectID(m.ID) || scopeOfFullID(m.ID) != oldName {
			return nil, fmt.Errorf("cannot rename: %s declares id %q, which is not a project id in scope %q — fix its frontmatter id (pj doctor reports this) then re-run", f, m.ID, oldName)
		}
		newID := newName + strings.TrimPrefix(m.ID, oldName)
		m.ID = newID
		m.Depends = rekeyEdges(m.Depends, oldName, newName)
		m.Related = rekeyEdges(m.Related, oldName, newName)

		newPath := filepath.Join(filepath.Dir(f), repair.Basename(base, newID))
		interiorOut, err := frontmatter.Serialize(m)
		if err != nil {
			return nil, err
		}
		ops = append(ops, rewrite.Op{OldPath: f, NewPath: newPath, Content: frontmatter.Compose(interiorOut, body)})
	}
	return ops, nil
}

// rekeyRegistry moves the scope's registry and lens entries from oldName to newName
// under the machine-global config lock — the last step, after the in-dir rewrite
// completes. A crash in the gap between the pj.cue-name write and this re-key leaves
// name_drift the same command resolves on re-run.
//
// Machine-uniqueness of newName is decided here, not by the caller's pre-lock read: that
// read is a snapshot taken before any lock, so a concurrent registration between it and
// this write would otherwise be silently overwritten, taking its dir binding and lens
// with it. The registry loaded under the lock is the only state a write may be judged
// against.
func (e *engine) rekeyRegistry(oldName, newName string) error {
	lock, err := xdg.AcquireConfigLock(e.app.ConfigDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := registry.NewStore(e.app.Ctx, e.app.ConfigDir)
	reg, err := store.Load()
	if err != nil {
		return err
	}
	entry, ok := reg.Scopes[oldName]
	if !ok {
		// Already re-keyed by a prior run (idempotent tail); nothing to do. Checked
		// before the uniqueness refusal below, so a re-run of a completed rename — where
		// newName is legitimately taken, by this very scope — stays idempotent.
		return nil
	}
	if taken, exists := reg.Scopes[newName]; exists {
		// The in-dir rewrite is already committed, so this cannot roll back — only the
		// re-key aborts. The resulting split state (pj.cue reads newName, registry still
		// reads oldName) is the name_drift window the design already defines, so say so
		// rather than leave the operator hunting for a rollback that did not happen.
		return fmt.Errorf("scope name %q was registered to %s while this rename was running — names are machine-unique, so the registry was left unchanged; %s is already renamed on disk and now reads %q, recover with pj scope forget %s && pj scope import %s",
			newName, taken.Dir, entry.Dir, newName, oldName, entry.Dir)
	}
	delete(reg.Scopes, oldName)
	reg.Scopes[newName] = entry
	if err := store.WriteRegistry(reg.Scopes); err != nil {
		return err
	}
	if lens, ok := reg.Lens[oldName]; ok {
		delete(reg.Lens, oldName)
		reg.Lens[newName] = lens
		if err := store.WriteLens(reg.Lens); err != nil {
			return err
		}
	}
	return nil
}

// reindexRenamed drops the old scope's index rows and reconciles the new scope so the
// index reflects the rename before the next command. It reloads the registry (now
// re-keyed) so the prune keeps every other scope.
func (e *engine) reindexRenamed(oldName, newName, dir string) error {
	if err := e.db.DeleteScope(oldName); err != nil {
		return err
	}
	reg, err := registry.NewStore(e.app.Ctx, e.app.ConfigDir).Load()
	if err != nil {
		return err
	}
	registered := make(map[string]bool, len(reg.Scopes))
	for name := range reg.Scopes {
		registered[name] = true
	}
	_, err = e.rec.Reconcile(map[string]string{newName: dir}, registered, nowNS())
	return err
}

// listScopeProjectFiles lists the project files of a scope during a rename — the dir
// root and immediate archive/ children whose basename carries the old or new scope
// prefix (the new prefix appears only for files a prior interrupted run already
// migrated).
func listScopeProjectFiles(dir, oldName, newName string) ([]string, error) {
	var out []string
	collect := func(root string) error {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			base := ent.Name()
			if hasScopePrefix(base, oldName) || hasScopePrefix(base, newName) {
				out = append(out, filepath.Join(root, base))
			}
		}
		return nil
	}
	if err := collect(dir); err != nil {
		return nil, err
	}
	if err := collect(filepath.Join(dir, "archive")); err != nil {
		return nil, err
	}
	return out, nil
}

// hasScopePrefix reports whether a filename is a project file of scope: <scope>-<short
// id>[-<slug>].md with a legal short-id.
func hasScopePrefix(base, scope string) bool {
	stem, ok := strings.CutSuffix(base, ".md")
	if !ok {
		return false
	}
	prefix := scope + "-"
	if !strings.HasPrefix(stem, prefix) {
		return false
	}
	short := shortAfter(stem, scope)
	return id.IsShortID(short)
}

// shortAfter returns the short-id segment of "<scope>-<short>[-<slug>]" — the run
// after the scope prefix up to the next hyphen.
func shortAfter(stem, scope string) string {
	rest := strings.TrimPrefix(stem, scope+"-")
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// rekeyEdges rewrites the scope prefix of every in-scope full-id entry from oldName to
// newName, leaving cross-scope and non-id entries untouched.
func rekeyEdges(list []string, oldName, newName string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, len(list))
	for i, e := range list {
		if id.IsFullProjectID(e) && scopeOfFullID(e) == oldName {
			out[i] = newName + strings.TrimPrefix(e, oldName)
		} else {
			out[i] = e
		}
	}
	return out
}
