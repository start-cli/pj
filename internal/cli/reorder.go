package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/order"
)

// reorderDest is the parsed destination for a reorder: exactly one of the four
// mutually exclusive placements. before/after carry a neighbour id; first/last are
// scope-wide bounds.
type reorderDest struct {
	before string
	after  string
	first  bool
	last   bool
}

// chosen reports how many destination placements were requested; exactly one is
// legal.
func (d reorderDest) count() int {
	n := 0
	if d.before != "" {
		n++
	}
	if d.after != "" {
		n++
	}
	if d.first {
		n++
	}
	if d.last {
		n++
	}
	return n
}

func newReorderCmd(app *App) *cobra.Command {
	var (
		scope string
		dest  reorderDest
	)
	cmd := &cobra.Command{
		Use:   "reorder <id> (--before <id> | --after <id> | --first | --last) [--scope S]",
		Short: "Rewrite a project's order key to move it in the board",
		Long: "Move a project by writing a new order key strictly between its target\n" +
			"neighbours — a single-file write that never renumbers a band. --first/--last\n" +
			"place against the scope-wide minimum/maximum valid order (every status, dir root\n" +
			"and archive/); --before/--after name an in-scope neighbour that must exist and\n" +
			"carry a valid order. Exactly one destination is required. An auto-commit scope\n" +
			"self-commits the change when a git-root exists.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runReorder(app, c, args[0], dest, scope)
		},
	}
	cmd.Flags().StringVar(&dest.before, "before", "", "place immediately before this neighbour id")
	cmd.Flags().StringVar(&dest.after, "after", "", "place immediately after this neighbour id")
	cmd.Flags().BoolVar(&dest.first, "first", false, "place at the scope-wide front")
	cmd.Flags().BoolVar(&dest.last, "last", false, "place at the scope-wide back")
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runReorder(app *App, c *cobra.Command, idArg string, dest reorderDest, scopeFlag string) error {
	if dest.count() != 1 {
		return usageErrorf("reorder needs exactly one of --before, --after, --first, --last")
	}
	form, ok := parseIDArg(idArg)
	if !ok {
		return usageErrorf("%q is not a valid project id", idArg)
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return fmt.Errorf("unknown project id %q: scope %q is not registered here", idArg, scope)
	}
	dir := entry.Dir

	lock, err := acquireScopeLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	ctx := c.Context()
	res, err := e.reconcileResult(single(scope, dir))
	if err != nil {
		return err
	}
	if err := refuseUnusableScope(res, scope, dir); err != nil {
		return err
	}
	schema := res.Schema(scope)
	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := gitRootFor(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	subject, err := e.resolveWriteRow(scope, idArg, form)
	if err != nil {
		return err
	}

	rows, err := e.db.ScopeProjects(scope)
	if err != nil {
		return err
	}
	left, right, err := e.reorderBounds(scope, subject, rows, dest)
	if err != nil {
		return err
	}
	newKey, err := order.KeyBetween(left, right)
	if err != nil {
		return fmt.Errorf("no legal order between neighbours for %s (%w) — re-space with pj doctor", subject.ID, err)
	}

	m, body, err := readProjectFile(subject.Path)
	if err != nil {
		return err
	}
	m.Order = newKey
	if err := writeProjectFile(subject.Path, m, body); err != nil {
		return err
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(subject.Path, "")); err != nil {
		return err
	}

	message := fmt.Sprintf("pj: %s reorder", subject.ID)
	if err := e.completeStateDurability(ctx, c, scope, dir, autoCommit, message, subject.Path, "", root, hasRoot); err != nil {
		return err
	}

	out, err := absPath(subject.Path)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}

// reorderBounds computes the (left, right) order-key pair KeyBetween writes between,
// from the destination and the scope's other valid-order projects (subject excluded,
// so a move relative to others is well defined). --first/--last bound against the
// scope-wide min/max; --before/--after resolve the neighbour and take its adjacent
// slot in (order, id) order. An open bound is the empty string.
func (e *engine) reorderBounds(scope string, subject *index.Project, rows []*index.Project, dest reorderDest) (left, right string, err error) {
	others := make([]*index.Project, 0, len(rows))
	for _, p := range rows {
		if p.Path == subject.Path || p.ParseError || !order.Valid(p.OrderKey) {
			continue
		}
		others = append(others, p)
	}
	sortProjects(others)

	switch {
	case dest.first:
		if len(others) == 0 {
			return "", "", nil
		}
		return "", others[0].OrderKey, nil
	case dest.last:
		if len(others) == 0 {
			return "", "", nil
		}
		return others[len(others)-1].OrderKey, "", nil
	case dest.before != "":
		return e.neighbourBounds(scope, subject, others, dest.before, true)
	default:
		return e.neighbourBounds(scope, subject, others, dest.after, false)
	}
}

// neighbourBounds resolves a --before/--after neighbour id and returns the bounds
// that place the subject immediately before or after it. The neighbour must resolve
// to one in-scope project (an unknown well-formed id is generic non-zero, a
// duplicate_id collision refuses, a malformed id was already a usage error) that is
// not the subject and carries a valid order.
func (e *engine) neighbourBounds(scope string, subject *index.Project, others []*index.Project, neighbourArg string, before bool) (left, right string, err error) {
	form, ok := parseIDArg(neighbourArg)
	if !ok {
		return "", "", usageErrorf("%q is not a valid project id", neighbourArg)
	}
	neighbour, err := e.resolveSingleRow(scope, neighbourArg, form, "neighbour")
	if err != nil {
		return "", "", err
	}
	if neighbour.Path == subject.Path {
		return "", "", usageErrorf("cannot reorder %q relative to itself", subject.ID)
	}
	if neighbour.ParseError || !order.Valid(neighbour.OrderKey) {
		return "", "", fmt.Errorf("neighbour %q has no valid order", neighbourArg)
	}

	idx := -1
	for i, p := range others {
		if p.Path == neighbour.Path {
			idx = i
			break
		}
	}
	if idx < 0 {
		// The neighbour has a valid order and is not the subject, so it is in the
		// filtered set by construction; a miss would be a reconcile inconsistency.
		return "", "", fmt.Errorf("neighbour %q not found in scope order", neighbourArg)
	}
	if before {
		l := ""
		if idx > 0 {
			l = others[idx-1].OrderKey
		}
		return l, neighbour.OrderKey, nil
	}
	r := ""
	if idx < len(others)-1 {
		r = others[idx+1].OrderKey
	}
	return neighbour.OrderKey, r, nil
}
