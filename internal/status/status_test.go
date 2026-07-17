package status

import "testing"

func customCategories() map[string]Category {
	return map[string]Category{
		"shipped": CategoryDone,
		"wontfix": CategoryDone,
		"triaged": CategoryActive,
		"icebox":  CategoryBacklog,
	}
}

func TestBuiltins(t *testing.T) {
	got := Builtins()
	want := []string{Draft, Backlog, Todo, Review, InProgress, Blocked, Done, Cancelled}
	if len(got) != len(want) {
		t.Fatalf("Builtins() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Builtins()[%d] = %q, want %q", i, got[i], want[i])
		}
		if !IsBuiltin(got[i]) {
			t.Errorf("IsBuiltin(%q) = false", got[i])
		}
	}
	if IsBuiltin("shipped") {
		t.Error("IsBuiltin(shipped) = true, want false")
	}
}

func TestIsTerminal(t *testing.T) {
	custom := customCategories()
	terminal := []string{Done, Cancelled, "shipped", "wontfix"}
	for _, s := range terminal {
		if !IsTerminal(s, custom) {
			t.Errorf("IsTerminal(%q) = false, want true", s)
		}
	}
	nonTerminal := []string{Draft, Backlog, Todo, Review, InProgress, Blocked, "triaged", "icebox", "unknown"}
	for _, s := range nonTerminal {
		if IsTerminal(s, custom) {
			t.Errorf("IsTerminal(%q) = true, want false", s)
		}
	}
	// A custom done-category status is terminal only with its mapping supplied.
	if IsTerminal("shipped", nil) {
		t.Error("IsTerminal(shipped, nil) = true, want false without the mapping")
	}
}

func TestIsNextEligible(t *testing.T) {
	if !IsNextEligible(Todo) {
		t.Error("IsNextEligible(todo) = false, want true")
	}
	for _, s := range []string{Draft, Backlog, Review, InProgress, Blocked, Done, Cancelled, "triaged"} {
		if IsNextEligible(s) {
			t.Errorf("IsNextEligible(%q) = true, want false (only todo is eligible)", s)
		}
	}
}

func TestInDefaultList(t *testing.T) {
	custom := customCategories()
	shown := []string{Draft, Todo, Review, InProgress, Blocked, "triaged"}
	for _, s := range shown {
		if !InDefaultList(s, custom) {
			t.Errorf("InDefaultList(%q) = false, want true", s)
		}
	}
	hidden := []string{Backlog, Done, Cancelled, "shipped", "icebox", "unknown"}
	for _, s := range hidden {
		if InDefaultList(s, custom) {
			t.Errorf("InDefaultList(%q) = true, want false", s)
		}
	}
}

func TestCategoryOf(t *testing.T) {
	custom := customCategories()
	cases := map[string]Category{
		Draft: CategoryActive, Backlog: CategoryBacklog, Todo: CategoryActive,
		Done: CategoryDone, Cancelled: CategoryDone, "shipped": CategoryDone, "icebox": CategoryBacklog,
	}
	for name, want := range cases {
		got, ok := CategoryOf(name, custom)
		if !ok || got != want {
			t.Errorf("CategoryOf(%q) = (%q, %v), want (%q, true)", name, got, ok, want)
		}
	}
	if _, ok := CategoryOf("unknown", custom); ok {
		t.Error("CategoryOf(unknown) ok = true, want false")
	}
}

func TestIsKnownAndValidCategory(t *testing.T) {
	custom := customCategories()
	if !IsKnown(Todo, custom) || !IsKnown("shipped", custom) {
		t.Error("IsKnown failed for a built-in or declared custom")
	}
	if IsKnown("bogus", custom) {
		t.Error("IsKnown(bogus) = true, want false")
	}
	for _, c := range []Category{CategoryActive, CategoryBacklog, CategoryDone} {
		if !ValidCategory(c) {
			t.Errorf("ValidCategory(%q) = false", c)
		}
	}
	if ValidCategory("wip") {
		t.Error("ValidCategory(wip) = true, want false")
	}
}
