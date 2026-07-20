package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/status"
)

func newNextCmd(app *App) *cobra.Command {
	var (
		scope  string
		noLens bool
	)
	cmd := &cobra.Command{
		Use:   "next [--scope S] [--no-lens]",
		Short: "Print the path of the first runnable project",
		Long: "Select the first runnable project by (order, id): built-in todo, every\n" +
			"depends terminal, honouring the lens, file at the dir root (never archive/),\n" +
			"and not a duplicate-id collision. Print its absolute path. An empty queue is\n" +
			"diagnosed distinctly: blocked-by-deps vs genuinely empty vs lens-emptied.\n" +
			"Pure read; never runs git. (--claim, the start-work write, is P4.)",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return runNext(app, c, scope, noLens)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient inventory scope")
	cmd.Flags().BoolVar(&noLens, "no-lens", false, "ignore the active lens")
	return cmd
}

func runNext(app *App, c *cobra.Command, scopeFlag string, noLens bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	scope := resolved.Name

	res, targets, err := e.reconcileClosure(c, scope, resolved.Entry.Dir)
	if err != nil {
		return err
	}
	gate, err := e.buildGate(res, targets)
	if err != nil {
		return err
	}

	rows, err := e.db.ScopeProjects(scope)
	if err != nil {
		return err
	}
	candidates := nextCandidates(rows)
	sortProjects(candidates)

	lens := e.reg.Lens[scope]
	applyLens := !noLens && len(lens) > 0

	tokens := newTokenSet()
	var chosen *index.Project
	blocked, readyOutsideLens := 0, 0
	for _, p := range candidates {
		ds := gate.evalDepends(p)
		tokens.add(ds.Tokens)
		if !gate.nextEligible(p, ds) {
			// A queue-blocking hold (unmet deps or a malformed edge) drives the
			// empty-because-blocked diagnostic; a duplicate-id skip does not.
			if ds.Held() {
				blocked++
			}
			continue
		}
		if applyLens && !passesLens(p, lens) {
			readyOutsideLens++
			continue
		}
		if chosen == nil {
			chosen = p
		}
	}

	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	for _, line := range tokens.lines() {
		stderrln(c, line)
	}

	if chosen != nil {
		stdoutln(c, chosen.Path)
		return nil
	}
	return emptyQueueError(applyLens, lens, blocked, readyOutsideLens)
}

// nextCandidates keeps only the projects that could be next before the depends gate
// and lens: built-in todo, at the dir root, and not quarantined.
func nextCandidates(rows []*index.Project) []*index.Project {
	var out []*index.Project
	for _, p := range rows {
		if status.IsNextEligible(p.Status) && !p.Archived && !p.ParseError {
			out = append(out, p)
		}
	}
	return out
}

// emptyQueueError builds the distinct empty-queue diagnostic: a lens-emptied ready
// queue, a queue blocked on unmet deps, or a genuinely empty one. An empty queue is
// a normal non-zero state, not a fault, so it rides as a Plain diagnostic (no error:
// label on a TTY).
func emptyQueueError(applyLens bool, lens []string, blocked, readyOutsideLens int) error {
	switch {
	case applyLens && readyOutsideLens > 0:
		return plainDiagnostic("nothing ready under lens %s; %d ready outside it", lensBracket(lens), readyOutsideLens)
	case blocked > 0:
		return plainDiagnostic("nothing ready; %d todo(s) waiting on unmet deps", blocked)
	default:
		return plainDiagnostic("nothing ready")
	}
}

// plainDiagnostic builds a non-zero, non-fault diagnostic: exit 1, message printed
// verbatim without the error: label.
func plainDiagnostic(format string, a ...any) error {
	return &ExitError{Code: exitFailure, Err: fmt.Errorf(format, a...), Plain: true}
}

// lensBracket renders a tag list as [a, b] — the single rendering both the
// empty-queue diagnostic and the stderr lens echo share, so the two can never
// drift and both match the design's worked example.
func lensBracket(lens []string) string {
	return "[" + strings.Join(lens, ", ") + "]"
}

// reconcileClosure reconciles the ambient scope plus the transitive closure of the
// scopes it depends on, so a cross-scope depends gate reads fresh local state. It
// reconciles rows batch by batch — discovering deeper scopes from the edges each
// batch materializes — accumulating the per-scope schemas and config/unreachable
// warnings, then runs the post-reconcile aggregates (parse_error count, integrity
// tokens) once over the whole reconciled set. Every scope is scanned exactly once,
// and the aggregates stay single-pass (one parse_error count for the closure, not
// one per batch). It returns the unified result and the reconciled scope names.
func (e *engine) reconcileClosure(c *cobra.Command, ambient, dir string) (*reconcile.Result, []string, error) {
	targets := map[string]string{ambient: dir}
	done := map[string]bool{}
	merged := reconcile.NewResult()
	var reconciled []string
	for {
		pending := map[string]string{}
		for name, d := range targets {
			if !done[name] {
				pending[name] = d
			}
		}
		if len(pending) == 0 {
			break
		}
		res, batch, err := e.rec.ReconcileRows(pending, e.registeredSet(), nowNS())
		if err != nil {
			return nil, nil, err
		}
		merged.Merge(res)
		reconciled = append(reconciled, batch...)
		for name := range pending {
			done[name] = true
		}
		edges, err := e.db.AllEdges()
		if err != nil {
			return nil, nil, err
		}
		for _, ed := range edges {
			if ed.Kind != index.EdgeDepends || !done[ed.FromScope] {
				continue
			}
			if entry, ok := e.reg.Scopes[ed.ToScope]; ok && !done[ed.ToScope] {
				targets[ed.ToScope] = entry.Dir
			}
		}
	}

	if err := e.rec.AppendAggregates(reconciled, merged); err != nil {
		return nil, nil, err
	}
	e.printWarnings(c, merged.Warnings)

	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	return merged, names, nil
}
