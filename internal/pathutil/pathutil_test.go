package pathutil

import "testing"

func TestUnderOrEqual(t *testing.T) {
	cases := []struct {
		child, ancestor string
		want            bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/b/c", false},
		{"/a/bc", "/a/b", false}, // separator boundary: not nested
		{"/a/b", "/a/bc", false},
		{"/a/b/c", "/a", true},
		{"/x", "/a", false},
	}
	for _, c := range cases {
		if got := UnderOrEqual(c.child, c.ancestor); got != c.want {
			t.Errorf("UnderOrEqual(%q,%q)=%v want %v", c.child, c.ancestor, got, c.want)
		}
	}
}

func TestOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b", "/a/c", false},
		{"/a/bc", "/a/b", false},
		{"/a", "/b", false},
	}
	for _, c := range cases {
		if got := Overlap(c.a, c.b); got != c.want {
			t.Errorf("Overlap(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
