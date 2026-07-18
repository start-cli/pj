//go:build !linux && !darwin

package cli

// supportedOS reports whether pj runs on this OS. Everything outside macOS and
// Linux is unsupported: pj fails with a clear startup error rather than
// half-running under semantics (flock, paths) it does not target.
func supportedOS() bool { return false }
