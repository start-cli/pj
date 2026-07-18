package scopeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/start-cli/pj/internal/status"
)

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadValid(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, `
name: "wc"
autoCommit: true
knownTags: ["frontend", "api"]
statuses: {
	shipped: {category: "done"}
	triage:  {category: "backlog"}
}
fields: {
	estimate: {type: "int"}
	area:     {type: "string", values: ["frontend", "backend"]}
	owners:   {type: "strings"}
}
`)
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "wc" || !s.AutoCommit {
		t.Errorf("name/autoCommit = %q/%v", s.Name, s.AutoCommit)
	}
	if s.Statuses["shipped"] != status.CategoryDone || s.Statuses["triage"] != status.CategoryBacklog {
		t.Errorf("statuses = %+v", s.Statuses)
	}
	if s.Fields["estimate"].Type != FieldInt {
		t.Errorf("estimate type = %q", s.Fields["estimate"].Type)
	}
	if got := s.Fields["area"].Values; len(got) != 2 || got[0] != "frontend" {
		t.Errorf("area values = %v", got)
	}
	if s.Fields["owners"].Values != nil {
		t.Errorf("owners should have no enum, got %v", s.Fields["owners"].Values)
	}

	// Schema helper methods.
	if !s.StatusKnown("shipped") || !s.StatusKnown(status.Todo) {
		t.Error("StatusKnown should accept custom and built-in")
	}
	if s.StatusKnown("bogus") {
		t.Error("StatusKnown should reject unknown")
	}
	if !s.StatusTerminal("shipped") || !s.StatusTerminal(status.Done) {
		t.Error("StatusTerminal should be true for done-category and built-in done")
	}
	if s.StatusTerminal("triage") {
		t.Error("backlog custom is not terminal")
	}
	if f, ok := s.Field("estimate"); !ok || f.Type != FieldInt {
		t.Error("Field(estimate) lookup failed")
	}
}

func TestLoadMinimal(t *testing.T) {
	ctx := cuecontext.New()
	dir := writeCfg(t, "name: \"h\"\nautoCommit: false\n")
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "h" || s.AutoCommit {
		t.Errorf("got %+v", s)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	ctx := cuecontext.New()
	cases := []struct {
		name    string
		content string
	}{
		{"uncompilable", `name: "wc" autoCommit:::`},
		{"missing name", `autoCommit: true`},
		{"bad name alphabet", `name: "WC"` + "\nautoCommit: true"},
		{"name too long", `name: "abcdefghijklm"` + "\nautoCommit: true"},
		{"missing autoCommit", `name: "wc"`},
		{"autoCommit wrong type", `name: "wc"` + "\nautoCommit: \"yes\""},
		{"redeclare builtin status", `name: "wc"` + "\nautoCommit: true\nstatuses: {done: {category: \"done\"}}"},
		{"status bad category", `name: "wc"` + "\nautoCommit: true\nstatuses: {foo: {category: \"weird\"}}"},
		{"status bad alphabet", `name: "wc"` + "\nautoCommit: true\nstatuses: {\"Foo\": {category: \"done\"}}"},
		{"field shadows builtin", `name: "wc"` + "\nautoCommit: true\nfields: {status: {type: \"string\"}}"},
		{"field bad type", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"float\"}}"},
		{"field bad name alphabet", `name: "wc"` + "\nautoCommit: true\nfields: {\"X\": {type: \"string\"}}"},
		{"values enum on int", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"int\", values: [\"a\"]}}"},
		{"values enum on bool", `name: "wc"` + "\nautoCommit: true\nfields: {x: {type: \"bool\", values: [\"a\"]}}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeCfg(t, c.content)
			_, err := Load(ctx, dir)
			if err == nil {
				t.Fatalf("expected a config error")
			}
			if _, ok := AsConfigError(err); !ok {
				t.Fatalf("expected *ConfigError, got %T: %v", err, err)
			}
		})
	}
}

func TestLoadAbsent(t *testing.T) {
	ctx := cuecontext.New()
	_, err := Load(ctx, t.TempDir())
	if _, ok := AsConfigError(err); !ok {
		t.Fatalf("absent pj.cue should be a *ConfigError, got %v", err)
	}
}

func TestReadName(t *testing.T) {
	ctx := cuecontext.New()

	// Reads name even when the fuller schema is invalid (bad field type) as long
	// as the file compiles and the name is legal.
	dir := writeCfg(t, `name: "wc"`+"\nautoCommit: true\nfields: {x: {type: \"float\"}}")
	name, err := ReadName(ctx, dir)
	if err != nil {
		t.Fatalf("ReadName on schema-invalid-but-compilable: %v", err)
	}
	if name != "wc" {
		t.Errorf("name = %q", name)
	}

	// Uncompilable → error.
	bad := writeCfg(t, `name: "wc" broken:::`)
	if _, err := ReadName(ctx, bad); err == nil {
		t.Error("expected error on uncompilable pj.cue")
	}

	// Illegal name → error.
	illegal := writeCfg(t, `name: "WC"`+"\nautoCommit: true")
	if _, err := ReadName(ctx, illegal); err == nil {
		t.Error("expected error on illegal name")
	}
}

func TestWriteMinimalRoundTrip(t *testing.T) {
	ctx := cuecontext.New()
	dir := t.TempDir()
	if err := WriteMinimal(dir, "wc", true); err != nil {
		t.Fatalf("WriteMinimal: %v", err)
	}
	s, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	if s.Name != "wc" || !s.AutoCommit {
		t.Errorf("round-trip got %+v", s)
	}
}

func TestCueReasonIsSingleLine(t *testing.T) {
	// A multi-line underlying error must collapse to one line so the
	// config_unparseable diagnostic stays one token per line.
	got := cueReason(errors.New("line one\nline two\n\tindented three"))
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("cueReason must not contain a newline, got %q", got)
	}
	if got != "line one line two indented three" {
		t.Errorf("cueReason flattening = %q", got)
	}
}
