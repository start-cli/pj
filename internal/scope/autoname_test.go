package scope

import (
	"errors"
	"testing"
)

func TestAutoNameDerives(t *testing.T) {
	tests := []struct {
		name     string
		basename string
		want     string
	}{
		{name: "hyphen initials", basename: "web-control", want: "wc"},
		{name: "opaque first two", basename: "webctl", want: "we"},
		{name: "underscore initials", basename: "api_gateway", want: "ag"},
		{name: "dot separators", basename: "foo.bar.baz", want: "fbb"},
		{name: "space separators", basename: "my project", want: "mp"},
		{name: "camelCase boundaries", basename: "webControlPanel", want: "wcp"},
		{name: "acronym boundary", basename: "HTMLParser", want: "hp"},
		{name: "full path reduces to basename", basename: "/home/grant/web-control", want: "wc"},
		{name: "trailing slash", basename: "/srv/api-gateway/", want: "ag"},
		{name: "digits kept in opaque token", basename: "h2server", want: "h2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AutoName(tt.basename)
			if err != nil {
				t.Fatalf("AutoName(%q) unexpected err: %v", tt.basename, err)
			}
			if got != tt.want {
				t.Fatalf("AutoName(%q) = %q, want %q", tt.basename, got, tt.want)
			}
		})
	}
}

func TestAutoNameHardErrors(t *testing.T) {
	fail := []string{
		"ill",  // seed "il" -> both dropped -> empty
		"101",  // seed "10" -> '1'/'0' not in alphabet -> empty
		"",     // empty basename -> empty seed
		"illo", // opaque -> "il" -> dropped
		"oil",  // opaque -> "oi" -> dropped
		"...",  // only separators -> no tokens
	}
	for _, s := range fail {
		if got, err := AutoName(s); !errors.Is(err, ErrCannotDerive) {
			t.Errorf("AutoName(%q) = (%q, %v), want ErrCannotDerive", s, got, err)
		}
	}
}

func TestAutoNameLeadingDigitDropped(t *testing.T) {
	// Initials "3m" (3d-model): the leading digit is stripped so a letter leads.
	got, err := AutoName("3d-model")
	if err != nil {
		t.Fatalf("AutoName(3d-model) err: %v", err)
	}
	if got != "m" {
		t.Fatalf("AutoName(3d-model) = %q, want m", got)
	}
}

func TestAutoNameCap(t *testing.T) {
	// Fifteen single-letter tokens -> 15-char seed, capped to 12.
	got, err := AutoName("a-b-c-d-e-f-g-h-j-k-m-n-p-q-r")
	if err != nil {
		t.Fatalf("AutoName cap err: %v", err)
	}
	if len(got) != 12 {
		t.Fatalf("AutoName cap len = %d (%q), want 12", len(got), got)
	}
}

func TestAutoNameWithinAlphabet(t *testing.T) {
	got, err := AutoName("web-control")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(got); i++ {
		if !inAlphabet(got[i]) {
			t.Fatalf("AutoName produced %q with char %q outside the auto-name alphabet", got, got[i])
		}
	}
}
