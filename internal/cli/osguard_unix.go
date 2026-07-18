//go:build linux || darwin

package cli

// supportedOS reports whether pj runs on this OS. v1 supports macOS and Linux
// only; POSIX flock and path semantics are assumed throughout.
func supportedOS() bool { return true }
