package cli

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/order"
	"github.com/start-cli/pj/internal/slug"
	"github.com/start-cli/pj/internal/status"
)

func newCreateCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "create <title> [status] [--scope S]",
		Short: "Scaffold a new project (frontmatter + H1) and print its path",
		Long: "Mint an id, write a scaffold — built-in frontmatter with an appended order\n" +
			"key and a single # <title> H1 whose slug is frozen from the title — and print\n" +
			"the cleaned absolute path for the agent to fill the body. The default status is\n" +
			"draft; an optional second positional sets any known status (a terminal status\n" +
			"writes under archive/). create reserves the id and never self-commits in any\n" +
			"mode; git durability is the next pj sync (auto-commit) or host commit.",
		Args: usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(c *cobra.Command, args []string) error {
			st := ""
			if len(args) == 2 {
				st = args[1]
			}
			return runCreate(app, c, args[0], st, scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to create in (defaults to ambient)")
	return cmd
}

func runCreate(app *App, c *cobra.Command, titleArg, statusArg, scopeFlag string) error {
	title := strings.TrimSpace(titleArg)
	if title == "" {
		return usageErrorf("create needs a non-empty title")
	}

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
	dir := resolved.Entry.Dir

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

	newStatus := status.Draft
	if statusArg != "" {
		newStatus = statusArg
	}
	if !status.IsKnown(newStatus, custom) {
		return usageErrorf("%q is not a known status for scope %q", newStatus, scope)
	}

	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := gitRootFor(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	rows, err := e.db.ScopeProjects(scope)
	if err != nil {
		return err
	}

	shortID, err := mintUnusedID(rows)
	if err != nil {
		return err
	}
	fullID := scope + "-" + shortID

	orderKey, err := order.KeyBetween(maxValidOrder(rows), "")
	if err != nil {
		return fmt.Errorf("compute append order for %s: %w", fullID, err)
	}

	model := &frontmatter.Model{
		ID:      fullID,
		Status:  newStatus,
		Order:   orderKey,
		Created: time.Now().Format(time.RFC3339),
	}
	interior, err := frontmatter.Serialize(model)
	if err != nil {
		return err
	}
	file := frontmatter.Compose(interior, []byte("# "+title+"\n"))

	terminal := status.IsTerminal(newStatus, custom)
	base := fullID + "-" + slug.Slugify(title) + ".md"
	target, err := terminalLocation(dir, base, terminal)
	if err != nil {
		return err
	}

	if err := atomicWrite(target, file); err != nil {
		return err
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(target, "")); err != nil {
		return err
	}

	e.createDurability(ctx, c, dir, autoCommit, terminal, fullID, root, hasRoot)

	out, err := absPath(target)
	if err != nil {
		return err
	}
	stdoutln(c, out)
	return nil
}

// mintUnusedID draws a fresh short-id and redraws until it is unused among the ids
// present in the scope, so two concurrent creates under the scope flock cannot
// settle on the same id. It compares against every indexed row's short-id, including
// parse_error rows (whose id is taken from the filename), so a quarantined file's id
// is never re-minted.
func mintUnusedID(rows []*index.Project) (string, error) {
	taken := make(map[string]struct{}, len(rows))
	for _, p := range rows {
		if p.ShortID != "" {
			taken[p.ShortID] = struct{}{}
		}
	}
	for {
		s, err := id.Mint(rand.Reader)
		if err != nil {
			return "", fmt.Errorf("mint id: %w", err)
		}
		if _, used := taken[s]; !used {
			return s, nil
		}
	}
}
