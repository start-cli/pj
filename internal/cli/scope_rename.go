package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newScopeRenameCmd registers `pj scope rename` in the command tree so the help
// surface is complete, but it hard-refuses: the in-scope rewrite (pj.cue, every
// project id, filename, and in-scope edge, in one operation) lands in a later
// project. It is not implemented as forget+import and must not be faked here.
func newScopeRenameCmd(_ *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a scope in place (not yet implemented)",
		Long: "Rename a scope in place — rewriting pj.cue, every project id, filename, and\n" +
			"in-scope edge in one operation. This lands in a later project; it cannot be\n" +
			"approximated by forget + import, so it hard-refuses for now.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(_ *cobra.Command, args []string) error {
			return fmt.Errorf("pj scope rename %s %s is not implemented yet — it lands in a later project (the multi-file in-scope rewrite is out of scope here); to move paths use pj scope rebind, and to change a name safely rename at the source before other machines register", args[0], args[1])
		},
	}
}
