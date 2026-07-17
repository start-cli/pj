// Package status defines the eight built-in project statuses, the closed
// category set that view and gating rules key on, and the terminal predicate
// shared by depends satisfaction, default-list exclusion, and merge dispute.
//
// It performs no I/O. Custom statuses are declared in a scope's pj.cue; P2
// parses those into the Category type defined here and passes the resulting
// name->category mapping to the predicates below. This package never loads CUE.
package status

// Category is the closed set of behaviours a status can have. Every custom
// status declares exactly one; the built-in statuses map to one internally.
type Category string

const (
	// CategoryActive statuses are shown in the default list but are neither
	// next-eligible nor terminal.
	CategoryActive Category = "active"
	// CategoryBacklog statuses are hidden from the default list (needs --all)
	// and are not terminal.
	CategoryBacklog Category = "backlog"
	// CategoryDone statuses are hidden from the default list and are terminal
	// (they satisfy a depends gate).
	CategoryDone Category = "done"
)

// The eight built-in status names. They are immutable and may not be redeclared
// as custom statuses.
const (
	Draft      = "draft"
	Backlog    = "backlog"
	Todo       = "todo"
	Review     = "review"
	InProgress = "in-progress"
	Blocked    = "blocked"
	Done       = "done"
	Cancelled  = "cancelled"
)

// builtin captures the closed per-status behaviour matrix. Terminal is derived
// from Category == CategoryDone. Only Todo is ever next-eligible.
type builtin struct {
	category      Category
	inDefaultList bool
	nextEligible  bool
}

var builtins = map[string]builtin{
	Draft:      {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Backlog:    {category: CategoryBacklog, inDefaultList: false, nextEligible: false},
	Todo:       {category: CategoryActive, inDefaultList: true, nextEligible: true},
	Review:     {category: CategoryActive, inDefaultList: true, nextEligible: false},
	InProgress: {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Blocked:    {category: CategoryActive, inDefaultList: true, nextEligible: false},
	Done:       {category: CategoryDone, inDefaultList: false, nextEligible: false},
	Cancelled:  {category: CategoryDone, inDefaultList: false, nextEligible: false},
}

// builtinOrder is the canonical status ordering, matching the design table.
var builtinOrder = []string{Draft, Backlog, Todo, Review, InProgress, Blocked, Done, Cancelled}

// Builtins returns the eight built-in status names in canonical order.
func Builtins() []string {
	out := make([]string, len(builtinOrder))
	copy(out, builtinOrder)
	return out
}

// IsBuiltin reports whether name is one of the eight built-in statuses.
func IsBuiltin(name string) bool {
	_, ok := builtins[name]
	return ok
}

// ValidCategory reports whether c is one of the three closed categories.
func ValidCategory(c Category) bool {
	return c == CategoryActive || c == CategoryBacklog || c == CategoryDone
}

// CategoryOf returns the category of a status. Built-ins resolve from the
// internal matrix; other names resolve from custom, the scope's declared
// name->category mapping. ok is false for an unknown status.
func CategoryOf(name string, custom map[string]Category) (Category, bool) {
	if b, ok := builtins[name]; ok {
		return b.category, true
	}
	if c, ok := custom[name]; ok {
		return c, true
	}
	return "", false
}

// IsKnown reports whether name is a built-in or a declared custom status.
func IsKnown(name string, custom map[string]Category) bool {
	_, ok := CategoryOf(name, custom)
	return ok
}

// IsTerminal reports whether a status satisfies a depends gate and counts as
// terminal for merge dispute: built-in done or cancelled, or any custom status
// whose category is done. An unknown status is not terminal.
func IsTerminal(name string, custom map[string]Category) bool {
	c, ok := CategoryOf(name, custom)
	return ok && c == CategoryDone
}

// IsNextEligible reports whether a status can appear in pj next. Only built-in
// todo qualifies; custom statuses never do, regardless of category. The caller
// still applies the depends-all-terminal gate on top of this.
func IsNextEligible(name string) bool {
	b, ok := builtins[name]
	return ok && b.nextEligible
}

// InDefaultList reports whether a status appears in the default pj list (no
// --all). Built-ins follow the design table; customs follow their category
// (active shown, backlog and done hidden). An unknown status is not shown.
func InDefaultList(name string, custom map[string]Category) bool {
	if b, ok := builtins[name]; ok {
		return b.inDefaultList
	}
	if c, ok := custom[name]; ok {
		return c == CategoryActive
	}
	return false
}
