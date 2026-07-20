package cli

import (
	"path/filepath"
	"testing"
)

func TestLooksLikeProjectFile(t *testing.T) {
	cases := []struct {
		base string
		want bool
	}{
		{"wc-ab2c-network-redesign.md", true}, // full id + multi-word slug
		{"wc-ab2c-x.md", true},                // full id + single-token slug
		{"wc-ab2c.md", true},                  // full id, no slug tail
		{"wc-ab2c-.md", false},                // empty slug tail is not a valid slug
		{"wc-ab2c-Bad.md", false},             // slug must be the closed lowercase grammar
		{"wc-abcdefgh-x.md", true},            // 8-char short id (post-repair length)
		{"wc-9b2c-x.md", false},               // short id must be letter-first
		{"wc-ab2c-x.txt", false},              // not markdown
		{"pj.cue", false},                     // config file is not a project file
		{"AGENTS.md", false},                  // host doc, not a project file
		{"random.md", false},                  // no id prefix
		{"wc.md", false},                      // missing the short-id segment
	}
	for _, c := range cases {
		if got := looksLikeProjectFile(c.base); got != c.want {
			t.Errorf("looksLikeProjectFile(%q) = %v want %v", c.base, got, c.want)
		}
	}
}

func TestIsAllowlistedScopeFile(t *testing.T) {
	dir := filepath.FromSlash("/scope/wc")
	cases := []struct {
		rel  string
		want bool
	}{
		{"wc-ab2c-x.md", true},              // project at dir root
		{"pj.cue", true},                    // config at dir root
		{".gitignore", true},                // lock ignore at dir root
		{"archive/wc-ab2c-x.md", true},      // project as immediate archive child
		{"archive/pj.cue", false},           // only projects are allowlisted under archive/
		{"archive/sub/wc-ab2c-x.md", false}, // nested deeper than archive/ is residue
		{"random.txt", false},               // non-scope residue at dir root
		{"AGENTS.md", false},                // host doc is never allowlisted
		{"sub/wc-ab2c-x.md", false},         // no scanned subdirectory other than archive/
	}
	for _, c := range cases {
		path := filepath.Join(dir, filepath.FromSlash(c.rel))
		if got := isAllowlistedScopeFile(path, dir); got != c.want {
			t.Errorf("isAllowlistedScopeFile(%q) = %v want %v", c.rel, got, c.want)
		}
	}
	// The dir itself (rel ".") and a path escaping the dir are never allowlisted.
	if isAllowlistedScopeFile(dir, dir) {
		t.Error("the scope dir itself must not be allowlisted")
	}
	if isAllowlistedScopeFile(filepath.FromSlash("/other/wc-ab2c-x.md"), dir) {
		t.Error("a path outside the scope dir must not be allowlisted")
	}
}
