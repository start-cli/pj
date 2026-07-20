package cli

import (
	"errors"
	"fmt"
)

// Exit codes, per the design's minimal map: 0 success; 2 usage / bad CLI input;
// any other failure is generic non-zero (1). There is no broader multi-code map
// — the machine signal is the closed stderr token set, not exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// ExitError carries the process exit code a handler wants main to use. It is the
// single source of exit codes: handlers return it, main maps it. Cobra's own
// flag- and argument-parse failures are wrapped into ExitError{exitUsage} at the
// source (SetFlagErrorFunc and the usageArgs validator) so "usage error → 2"
// holds by construction rather than by each handler re-checking.
type ExitError struct {
	Code int
	Err  error
	// Plain marks a non-fault diagnostic — a non-zero result that is a normal
	// state, not a failure (an empty next queue). main prints its message verbatim,
	// without the error: label, the same as a closed token line.
	Plain bool
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCodeFromError maps an error to a process exit code: an ExitError's own
// code, or exitFailure for any other error. A nil error is success.
func ExitCodeFromError(err error) int {
	if err == nil {
		return exitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return exitFailure
}

// usageErrorf builds an exit-2 usage error.
func usageErrorf(format string, a ...any) error {
	return &ExitError{Code: exitUsage, Err: fmt.Errorf(format, a...)}
}
