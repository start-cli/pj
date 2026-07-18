// Package token defines the closed set of machine-readable stderr tokens pj
// emits at line start. The tokens are a wire contract shared by the commands
// that produce a condition and the doctor report that later re-surfaces it
// (doctor owns the full table in P5); agents match them as literal ASCII
// prefixes, so the strings here are frozen and must never be reworded or
// coloured. This package holds only the P2 subset.
package token

import "strings"

const (
	// NameDrift marks a scope whose registry key disagrees with the on-disk
	// pj.cue name for its dir. The scope is unusable until deliberate
	// re-registration (forget + import).
	NameDrift = "name_drift:"

	// ConfigUnparseable marks a scope whose pj.cue cannot be trusted — absent,
	// uncompilable, or compiles but fails schema validation. The scope's writes
	// refuse; its reads stay available.
	ConfigUnparseable = "config_unparseable:"

	// AutoCommitMismatch marks a registration that would place divergent
	// autoCommit values under one derived git-root.
	AutoCommitMismatch = "auto_commit_mismatch:"

	// UnreachableScope marks a registered scope whose dir is gone from disk. It
	// stays registered until forget or a successful remount.
	UnreachableScope = "unreachable_scope:"
)

// Line prefixes msg with tok and a single space, forming a stderr diagnostic
// line whose leading token an agent can match by prefix. The token is emitted
// as literal ASCII at column 0; callers never wrap or colour it.
func Line(tok, msg string) string {
	return tok + " " + msg
}

// all is the closed P2 token set, used only for prefix classification.
var all = []string{NameDrift, ConfigUnparseable, AutoCommitMismatch, UnreachableScope}

// HasKnownPrefix reports whether s begins with one of the closed tokens. The
// error printer uses it to keep a token line plain: never coloured, never given
// a decorative label, so agents can match it as a literal prefix.
func HasKnownPrefix(s string) bool {
	for _, t := range all {
		if strings.HasPrefix(s, t) {
			return true
		}
	}
	return false
}
