package cli

import (
	"strings"

	"github.com/start-cli/pj/internal/id"
)

// idForm distinguishes the two id argument shapes an id-taking verb accepts.
type idForm int

const (
	// idFull is a full <scope>-<short-id> (a token containing '-').
	idFull idForm = iota
	// idShort is a bare short-id resolved against the ambient scope.
	idShort
)

// parseIDArg classifies an id argument for the id-taking project verbs — get,
// meta, deps, and edit here, and P4's id-taking mutators later. It is the shared
// parse over P1's predicates: a token with '-' is a full-id attempt validated by
// IsFullProjectID; any other token is a short-form attempt validated by
// IsShortID. ok is false for malformed input — a '-'-containing token that fails
// IsFullProjectID, or a short form that fails IsShortID — which the caller maps to
// a usage error (exit 2) at its own call site. A well-formed id that resolves to no
// project is a separate concern: generic non-zero (exit 1) at lookup, never exit 2.
func parseIDArg(tok string) (idForm, bool) {
	if strings.ContainsRune(tok, '-') {
		return idFull, id.IsFullProjectID(tok)
	}
	return idShort, id.IsShortID(tok)
}
