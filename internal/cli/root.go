// Package cli wires pj's Cobra command tree, the exit-code contract, signal
// handling, the OS guard, and the stdout/stderr output rules. Handlers return
// errors (never call os.Exit); cmd/pj/main.go is the sole place that formats an
// error and exits. SilenceUsage/SilenceErrors keep Cobra from printing on its
// own, and every flag- or argument-parse failure is routed to exit 2 at the
// source so the ExitError map stays the single source of exit codes.
package cli

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/scopeadmin"
	"github.com/start-cli/pj/internal/xdg"
)

// App carries the process-wide dependencies a command needs: the single CUE
// context (instantiated once per process and amortised across every CUE load),
// the resolved XDG config directory, and the XDG state directory that holds the
// machine-local SQLite index.
type App struct {
	Ctx       *cue.Context
	ConfigDir string
	StateDir  string
}

// admin builds a scope administrator over the app's config directory.
func (a *App) admin() *scopeadmin.Admin {
	return scopeadmin.New(a.Ctx, a.ConfigDir)
}

// Execute builds the command tree and runs it. It guards the OS first, resolves
// the XDG config directory, instantiates the CUE context once, and installs the
// signal handler. It returns the handler error for main to format and map.
func Execute() error {
	if !supportedOS() {
		return &ExitError{Code: exitFailure, Err: fmt.Errorf("pj supports macOS and Linux only; this operating system is unsupported")}
	}

	configDir, err := xdg.ConfigDir()
	if err != nil {
		return err
	}
	stateDir, err := xdg.StateDir()
	if err != nil {
		return err
	}
	app := &App{Ctx: cuecontext.New(), ConfigDir: configDir, StateDir: stateDir}

	root := newRootCmd(app)
	ctx, stop := signalContext()
	defer stop()
	return root.ExecuteContext(ctx)
}

// newRootCmd builds a fresh command tree. A constructor (rather than package-level
// command vars) keeps parsed flag state from leaking across invocations, which
// matters for tests that execute the tree repeatedly.
func newRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "pj",
		Short: "Agent project management CLI",
		Long: "pj tracks feature work as plain markdown files, one project per file.\n\n" +
			"Supported on macOS and Linux only.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	// Route Cobra's own flag-parse failures (unknown flag, bad value) to exit 2.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitError{Code: exitUsage, Err: err}
	})
	root.AddCommand(
		newScopeCmd(app),
		newListCmd(app),
		newNextCmd(app),
		newGetCmd(app),
		newMetaCmd(app),
		newDepsCmd(app),
		newSearchCmd(app),
		newQueryCmd(app),
		newEditCmd(app),
		newLensCmd(app),
		newCreateCmd(app),
		newStatusCmd(app),
		newReorderCmd(app),
	)
	return root
}

// usageArgs wraps a positional-args validator so an argument-count failure exits
// 2 (usage) rather than falling through to the generic failure code.
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := v(c, args); err != nil {
			return &ExitError{Code: exitUsage, Err: err}
		}
		return nil
	}
}

// stdoutln writes one line to the command's stdout (captured in tests).
func stdoutln(c *cobra.Command, s string) {
	fmt.Fprintln(c.OutOrStdout(), s)
}

// stderrln writes one line to the command's stderr (captured in tests).
func stderrln(c *cobra.Command, s string) {
	fmt.Fprintln(c.ErrOrStderr(), s)
}
