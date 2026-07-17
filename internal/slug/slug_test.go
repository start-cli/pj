package slug

import (
	"strings"
	"testing"
)

func TestSlugifyFixtures(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{name: "network output example", title: "Network Output Redesign", want: "network-output-redesign"},
		{name: "lowercase and collapse", title: "  Hello   World  ", want: "hello-world"},
		{name: "punctuation as separators", title: "foo, bar! baz?", want: "foo-bar-baz"},
		{name: "emoji as separators", title: "ship it 🚀 now", want: "ship-it-now"},
		{name: "underscores and dots", title: "a_b.c", want: "a-b-c"},
		{name: "keeps digits", title: "Sprint 42 goals", want: "sprint-42-goals"},
		{name: "only separators falls back", title: "!!! ??? ...", want: "x"},
		{name: "empty falls back", title: "", want: "x"},
		{name: "whitespace only falls back", title: "   \t\n", want: "x"},
		{name: "cjk only falls back", title: "日本語", want: "x"},
		{name: "nfkc folds compatibility forms", title: "ﬁle", want: "file"},
		{name: "nfkc fullwidth digits", title: "ｗ３", want: "w3"},
		{name: "leading separators trimmed", title: "---start", want: "start"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Slugify(tt.title)
			if got != tt.want {
				t.Fatalf("Slugify(%q) = %q, want %q", tt.title, got, tt.want)
			}
			if !Valid(got) {
				t.Fatalf("Slugify(%q) = %q which fails Valid", tt.title, got)
			}
		})
	}
}

func TestSlugifyTruncation(t *testing.T) {
	// A long multi-word title cuts at a '-' within the cap, staying valid.
	long := strings.Repeat("alpha bravo ", 20)
	got := Slugify(long)
	if len(got) > SlugMax {
		t.Fatalf("Slugify long = %q (len %d), want <= %d", got, len(got), SlugMax)
	}
	if !Valid(got) {
		t.Fatalf("Slugify long = %q which fails Valid", got)
	}
	if strings.HasSuffix(got, "-") || strings.HasPrefix(got, "-") {
		t.Fatalf("Slugify long = %q has a boundary hyphen", got)
	}
}

func TestSlugifyHardCutSingleToken(t *testing.T) {
	// One long token has no '-' to cut at; it hard-cuts to exactly SlugMax.
	title := strings.Repeat("a", 100)
	got := Slugify(title)
	if len(got) != SlugMax {
		t.Fatalf("Slugify(single long token) len = %d, want %d", len(got), SlugMax)
	}
	if !Valid(got) {
		t.Fatalf("Slugify(single long token) = %q which fails Valid", got)
	}
}

func TestSlugifyDeterministic(t *testing.T) {
	title := "Network Output Redesign"
	first := Slugify(title)
	second := Slugify(title)
	if first != second {
		t.Fatalf("Slugify is not deterministic: %q vs %q", first, second)
	}
}

func TestValid(t *testing.T) {
	valid := []string{"a", "x", "network-output-redesign", "sprint-42", "a1-b2-c3", strings.Repeat("a", SlugMax)}
	for _, s := range valid {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                             // empty
		"-lead",                        // leading hyphen
		"trail-",                       // trailing hyphen
		"double--hyphen",               // repeated hyphen
		"Upper",                        // uppercase
		"has space",                    // space
		"emoji🚀",                       // non-ascii
		strings.Repeat("a", SlugMax+1), // over the cap
	}
	for _, s := range invalid {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}
