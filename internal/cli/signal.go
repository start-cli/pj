package cli

import (
	"context"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

// caughtSignal records which signal, if any, cancelled the run so main can map
// it to the POSIX 128+signum exit code. It is orthogonal to the error map: a
// user interrupt is intent, not a failure the agent should retry.
var caughtSignal atomic.Int32

// signalContext returns a context cancelled on SIGINT/SIGTERM and a stop function
// that tears down the handler. The caught signal is recorded for SignalExitCode.
// This matters most later for verbs that block on the git subprocess.
func signalContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig, ok := <-ch:
			if !ok {
				return
			}
			if s, ok := sig.(syscall.Signal); ok {
				caughtSignal.Store(int32(s))
			}
			cancel()
		case <-ctx.Done():
			// stop() cancelled the run; exit rather than park on ch forever.
		}
	}()
	return ctx, func() {
		signal.Stop(ch)
		cancel()
	}
}

// SignalExitCode returns 128+signum when the run was interrupted by SIGINT (130)
// or SIGTERM (143), else 0. main consults it before the error path so a
// cancelled run exits with the POSIX code rather than a generic failure.
func SignalExitCode() int {
	if s := caughtSignal.Load(); s != 0 {
		return 128 + int(s)
	}
	return 0
}
