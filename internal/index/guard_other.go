//go:build !linux && !darwin

package index

// localDiskWarning is a no-op on platforms pj does not target (v1 is macOS/Linux
// only; the CLI already refuses to run elsewhere). It keeps the package building
// under other GOOS for tooling without asserting a filesystem check it cannot make.
func localDiskWarning(string) string { return "" }
