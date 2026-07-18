// Command pj is the agent project management CLI. This entry point is
// deliberately minimal: it runs the command tree, maps a signal or error to a
// process exit code, and exits. All command logic lives in internal/cli.
package main

import (
	"os"

	"github.com/start-cli/pj/internal/cli"
)

func main() {
	err := cli.Execute()

	// A SIGINT/SIGTERM interrupt exits with the POSIX 128+signum code (130/143),
	// ahead of the error path: a user interrupt is intent, not a failure to map.
	if code := cli.SignalExitCode(); code != 0 {
		os.Exit(code)
	}
	if err != nil {
		cli.PrintError(err)
		os.Exit(cli.ExitCodeFromError(err))
	}
}
