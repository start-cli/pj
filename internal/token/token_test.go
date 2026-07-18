package token

import (
	"strings"
	"testing"
)

func TestLine(t *testing.T) {
	got := Line(NameDrift, "the message")
	if got != "name_drift: the message" {
		t.Errorf("Line = %q", got)
	}
	if !strings.HasPrefix(got, NameDrift) {
		t.Errorf("token must lead the line: %q", got)
	}
}

func TestHasKnownPrefix(t *testing.T) {
	known := []string{
		NameDrift + " x",
		ConfigUnparseable + " y",
		AutoCommitMismatch + " z",
		UnreachableScope + " w",
	}
	for _, s := range known {
		if !HasKnownPrefix(s) {
			t.Errorf("HasKnownPrefix(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "plain error", "some_other: token", "name_drif"} {
		if HasKnownPrefix(s) {
			t.Errorf("HasKnownPrefix(%q) = true, want false", s)
		}
	}
}
