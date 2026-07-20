package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/token"
)

func newGetCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "get <id> [--scope S]",
		Short: "Resolve a short or full id to its project file path",
		Long: "Print the cleaned absolute path of a project's file. A short id resolves in\n" +
			"the ambient scope (or --scope / PJ_SCOPE); a full <scope>-<short-id> resolves\n" +
			"in any registered scope. get locates for repair: a project in parse_error\n" +
			"quarantine still prints its path and exits 0, riding parse_error on stderr.\n" +
			"A duplicate id is refused. Pure read; never runs git.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runGet(app, c, args[0], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runGet(app *App, c *cobra.Command, idArg, scope string) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	r, err := e.resolveProject(c, idArg, scope)
	if err != nil {
		return err
	}
	if len(r.rows) > 1 {
		return duplicateRefusal(r.rows)
	}
	p := r.rows[0]
	if err := ensureFileExists(p); err != nil {
		return err
	}
	if p.ParseError {
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: %s", p.ID, p.ParseMsg)))
	}
	stdoutln(c, p.Path)
	return nil
}

// ensureFileExists guards the get/meta/edit hand-off against a stale row whose file
// has vanished: the row still resolves, but there is no path to open, so it is a
// non-zero failure pointing at reindex/doctor rather than a bogus path.
func ensureFileExists(p *index.Project) error {
	if _, err := os.Stat(p.Path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("project %s resolves to %s but the file is missing — run pj doctor --reindex", p.ID, p.Path)
		}
		return fmt.Errorf("stat %s: %w", p.Path, err)
	}
	return nil
}
