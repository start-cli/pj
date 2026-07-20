package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/title"
	"github.com/start-cli/pj/internal/token"
)

func newMetaCmd(app *App) *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "meta <id> [--scope S]",
		Short: "Print a project's header (frontmatter) without the body",
		Long: "Print a fixed preamble (id, title, path), a blank line, then the frontmatter\n" +
			"interior exactly as stored — key order, quoting, comments, customs, and\n" +
			"status_conflict preserved, never re-encoded and never the body. Extractable\n" +
			"frontmatter exits 0 (even under parse_error, riding the token); wholly\n" +
			"unparseable frontmatter is non-zero with empty stdout. Pure read; never runs git.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(c *cobra.Command, args []string) error {
			return runMeta(app, c, args[0], scope)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "ambient scope for a short id")
	return cmd
}

func runMeta(app *App, c *cobra.Command, idArg, scope string) error {
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

	data, err := os.ReadFile(p.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", p.Path, err)
	}
	interior, body, present := frontmatter.Split(data)
	if !present {
		// No extractable frontmatter block: non-zero, empty stdout, token on stderr.
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: no extractable frontmatter block", p.ID)))
		return fmt.Errorf("project %s has no readable frontmatter", p.ID)
	}

	// Extractable: preamble + raw interior, exit 0. The title comes from the body via
	// the shared helper — the same H1 list and search show.
	stdoutln(c, "id: "+p.ID)
	stdoutln(c, "title: "+title.Extract(body))
	stdoutln(c, "path: "+p.Path)
	stdoutln(c, "")
	if _, err := c.OutOrStdout().Write(interior); err != nil {
		return err
	}

	if p.ParseError {
		stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: %s", p.ID, p.ParseMsg)))
	}
	if len(p.StatusConflict) > 0 {
		stderrln(c, fmt.Sprintf("status_conflict: %s disputes %s", p.ID, joinComma(p.StatusConflict)))
	}
	return nil
}
