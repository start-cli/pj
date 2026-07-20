package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/resolve"
)

// engine is the read-path context a project verb runs against: the loaded
// registry, the open index, and a reconciler over both. It is opened per command,
// used to reconcile the scopes the command reads and then query them, and closed
// when the command returns.
type engine struct {
	app *App
	reg *registry.Registry
	db  *index.DB
	rec *reconcile.Reconciler
}

// openEngine loads the registry, opens the machine-local index (rebuilding it on a
// schema-version mismatch or corruption), and rides the local-disk guard warning on
// stderr when the state dir looks non-local. The caller defers close.
func (a *App) openEngine(c *cobra.Command) (*engine, error) {
	reg, err := registry.NewStore(a.Ctx, a.ConfigDir).Load()
	if err != nil {
		return nil, err
	}
	db, err := index.Open(a.StateDir)
	if err != nil {
		return nil, err
	}
	if db.LocalDiskWarning != "" {
		stderrln(c, db.LocalDiskWarning)
	}
	return &engine{app: a, reg: reg, db: db, rec: reconcile.New(db, a.Ctx)}, nil
}

// nowNS is the reconcile timestamp, read once at the command edge (the one place
// wall-clock time enters the read path) so reconcile stays a pure function of its
// inputs.
func nowNS() int64 { return time.Now().UnixNano() }

func (e *engine) close() {
	if e != nil && e.db != nil {
		_ = e.db.Close()
	}
}

// registeredSet is the set of every scope name the registry knows — the authority
// reconcile prunes forgotten index rows against.
func (e *engine) registeredSet() map[string]bool {
	out := make(map[string]bool, len(e.reg.Scopes))
	for name := range e.reg.Scopes {
		out[name] = true
	}
	return out
}

// allTargets is every registered scope as a name -> dir map, for machine-wide reads
// (search without --scope, query).
func (e *engine) allTargets() map[string]string {
	out := make(map[string]string, len(e.reg.Scopes))
	for name, entry := range e.reg.Scopes {
		out[name] = entry.Dir
	}
	return out
}

// reconcile reconciles the given target scopes (name -> dir), prints the resulting
// stderr token lines, and returns the result for the verb to query and extend. now
// is read here at the edge so the racy-index rule stays deterministic.
func (e *engine) reconcile(c *cobra.Command, targets map[string]string) (*reconcile.Result, error) {
	res, err := e.reconcileResult(targets)
	if err != nil {
		return nil, err
	}
	e.printWarnings(c, res.Warnings)
	return res, nil
}

// reconcileResult reconciles the targets and returns the result without printing its
// warnings, for a caller that must inspect or filter them before they ride stderr —
// a verb that refuses with its own token for a condition reconcile also warns about
// (a duplicate id) suppresses the generic echo rather than emitting it twice.
func (e *engine) reconcileResult(targets map[string]string) (*reconcile.Result, error) {
	return e.rec.Reconcile(targets, e.registeredSet(), nowNS())
}

// printWarnings rides a reconcile result's token lines on stderr, in order.
func (e *engine) printWarnings(c *cobra.Command, warnings []string) {
	for _, w := range warnings {
		stderrln(c, w)
	}
}

// ambientOptions gathers the ambient inputs at the CLI edge: the --scope flag, the
// PJ_SCOPE env, and the canonicalised cwd. Resolution is then a pure function of the
// registry plus these.
func ambientOptions(scopeFlag string) (resolve.Options, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return resolve.Options{}, fmt.Errorf("resolve working directory: %w", err)
	}
	canonical, err := absPath(cwd)
	if err != nil {
		return resolve.Options{}, err
	}
	return resolve.Options{
		ScopeFlag: scopeFlag,
		EnvScope:  os.Getenv("PJ_SCOPE"),
		Cwd:       canonical,
	}, nil
}

// resolveAmbient resolves the ambient scope for a scope-requiring verb, mapping a
// malformed override or a missing scope to the right exit class. A no-scope error
// stays generic non-zero (guidance), while resolution's own typed errors pass
// through unchanged (drift is fail-closed).
func (e *engine) resolveAmbient(scopeFlag string) (*resolve.Resolved, error) {
	opts, err := ambientOptions(scopeFlag)
	if err != nil {
		return nil, err
	}
	return resolve.Resolve(e.app.Ctx, e.reg, opts)
}
