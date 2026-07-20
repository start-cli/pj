package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/status"
	"github.com/start-cli/pj/internal/token"
)

func newStatusCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "status <id> <status> [--scope S]",
		Short: "Set a project's status (promote / claim / done / …)",
		Long: "Rewrite a project's status. When the new status crosses the terminal boundary\n" +
			"(non-terminal ↔ terminal) the file is renamed between the dir root and archive/\n" +
			"in the same write, and the post-move absolute path is printed. Statuses are\n" +
			"labels: any known status (built-in or CUE custom) is accepted; an unknown one is\n" +
			"a usage error. An auto-commit scope self-commits the change when a git-root\n" +
			"exists. A quarantined or duplicate-id project is refused with no write.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(c *cobra.Command, args []string) error {
			return runStatus(app, c, args[0], args[1], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runStatus(app *App, c *cobra.Command, idArg, newStatus, scopeFlag string) error {
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
	custom := schemaCustom(schema)
	if !status.IsKnown(newStatus, custom) {
		return usageErrorf("%q is not a known status for scope %q", newStatus, scope)
	}
	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := gitRootFor(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	p, err := e.resolveWriteRow(scope, idArg, form)
	if err != nil {
		return err
	}

	m, body, err := readProjectFile(p.Path)
	if err != nil {
		return err
	}
	wasTerminal := status.IsTerminal(m.Status, custom)
	nowTerminal := status.IsTerminal(newStatus, custom)
	m.Status = newStatus

	newPath, oldPath := p.Path, ""
	if wasTerminal != nowTerminal {
		newPath, err = terminalLocation(dir, filepath.Base(p.Path), nowTerminal)
		if err != nil {
			return err
		}
		oldPath = p.Path
	}

	// Write the new content in place, then move it with an atomic rename. Doing it in
	// this order means a crash never leaves two files sharing the id: before the rename
	// only the old path exists (with the new content — repairable layout drift, not a
	// duplicate); after it, only the new path does.
	if err := writeProjectFile(p.Path, m, body); err != nil {
		return err
	}
	if oldPath != "" && oldPath != newPath {
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("move %s to %s: %w", oldPath, newPath, err)
		}
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(newPath, oldPath)); err != nil {
		return err
	}

	message := fmt.Sprintf("pj: %s -> %s", p.ID, newStatus)
	if err := e.completeStateDurability(ctx, c, scope, dir, autoCommit, message, newPath, oldPath, root, hasRoot); err != nil {
		return err
	}

	out, err := absPath(newPath)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}

// resolveSingleRow resolves a well-formed id argument to exactly one project row in
// scope, applying the id-count half of the id-taking-verb refuse contract: zero rows
// is unknown-but-well-formed (generic non-zero, worded with noun, e.g. "project" or
// "neighbour"), more than one is a duplicate_id collision refused for either side. It
// layers no row-level policy — callers add their own parse_error/order checks on the
// returned row. The malformed-id usage error is handled by the caller before this.
func (e *engine) resolveSingleRow(scope, idArg string, form idForm, noun string) (*index.Project, error) {
	var rows []*index.Project
	var err error
	if form == idFull {
		rows, err = e.db.ProjectsByID(scope, idArg)
	} else {
		rows, err = e.db.ProjectsByShortID(scope, idArg)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("unknown %s id %q", noun, idArg)
	}
	if len(rows) > 1 {
		return nil, duplicateRefusal(rows)
	}
	return rows[0], nil
}

// resolveWriteRow resolves an id argument to the single project row a mutator will
// write, applying the id-taking-verb refuse contract: an unknown-but-well-formed id
// is generic non-zero, a duplicate_id collision is refused with no write to either
// side, and a parse_error-quarantined project is refused (its frontmatter cannot be
// safely rewritten). The malformed-id usage error is handled by the caller before
// reconcile.
func (e *engine) resolveWriteRow(scope, idArg string, form idForm) (*index.Project, error) {
	p, err := e.resolveSingleRow(scope, idArg, form, "project")
	if err != nil {
		return nil, err
	}
	if p.ParseError {
		return nil, fmt.Errorf("%s", token.Line(token.ParseError,
			fmt.Sprintf("%s: %s — cannot rewrite quarantined frontmatter", p.ID, p.ParseMsg)))
	}
	return p, nil
}

// terminalLocation returns the path a project file belongs at for its terminal-ness:
// under archive/ (created on demand) when terminal, at the dir root otherwise. The
// basename is unchanged — a status write only relocates the file, never renames it.
func terminalLocation(dir, base string, terminal bool) (string, error) {
	if !terminal {
		return filepath.Join(dir, base), nil
	}
	archiveDir := filepath.Join(dir, "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}
	return filepath.Join(archiveDir, base), nil
}
