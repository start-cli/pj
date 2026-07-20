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

	// ParseError marks a project whose frontmatter could not be parsed (broken
	// fence, malformed YAML, or conflict markers inside the fence). The row is
	// quarantined but stays locatable for repair; reads ride a count.
	ParseError = "parse_error:"

	// DuplicateID marks two or more project files in one scope claiming the same
	// full id. Detection only here — repair is pj doctor --repair (P5).
	DuplicateID = "duplicate_id:"

	// EqualOrder marks two or more projects in one scope sharing an order key.
	// Detection only here — repair is pj doctor --repair (P5).
	EqualOrder = "equal_order:"

	// ArchiveNonTerminal marks a non-terminal project stored under archive/.
	// Layout drift; detection only here — repair is P5/P6.
	ArchiveNonTerminal = "archive_non_terminal:"

	// ArchiveTerminalAtRoot marks a terminal project still at the dir root.
	// Layout drift; detection only here — repair is P5/P6.
	ArchiveTerminalAtRoot = "archive_terminal_at_root:"

	// DependsDangling marks a same-scope depends target with no matching project
	// row — a hard dangle (the scope is present, so the id is genuinely wrong).
	DependsDangling = "depends_dangling:"

	// DependsUnresolvable marks a cross-scope depends target that cannot be
	// resolved here (scope not registered, or registered with no matching row).
	// Informational hold, not a hard error.
	DependsUnresolvable = "depends_unresolvable:"

	// SchemaError marks a hard frontmatter schema violation surfaced on the read
	// path — here, a depends/related entry that is not a legal full project id.
	SchemaError = "schema_error:"

	// SchemaWarn marks a soft frontmatter schema issue — here, a tag not present
	// in a scope's declared knownTags (a likely typo); free-form tags stay legal.
	SchemaWarn = "schema_warn:"

	// SyncDisabled marks an auto-commit scope whose complete-state write could not
	// self-commit because no git-root is derivable (or git is absent). The file and
	// index writes still landed; there is no git durability until a repo exists.
	SyncDisabled = "sync_disabled:"

	// Uncommitted marks a repo-driven scope (autoCommit false, inside git) whose
	// allowlisted scope files are dirty after a write. Detect-only: pj never stages,
	// commits, or pushes — the host repo owns the commit.
	Uncommitted = "uncommitted:"
)

// Line prefixes msg with tok and a single space, forming a stderr diagnostic
// line whose leading token an agent can match by prefix. The token is emitted
// as literal ASCII at column 0; callers never wrap or colour it.
func Line(tok, msg string) string {
	return tok + " " + msg
}

// all is the closed token set known so far, used only for prefix classification
// so the error printer keeps a leading token plain (never coloured or labelled).
var all = []string{
	NameDrift, ConfigUnparseable, AutoCommitMismatch, UnreachableScope,
	ParseError, DuplicateID, EqualOrder, ArchiveNonTerminal, ArchiveTerminalAtRoot,
	DependsDangling, DependsUnresolvable, SchemaError, SchemaWarn,
	SyncDisabled, Uncommitted,
}

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
