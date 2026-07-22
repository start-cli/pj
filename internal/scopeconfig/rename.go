package scopeconfig

import (
	"fmt"
	"path/filepath"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"

	"github.com/start-cli/pj/internal/atomicfile"
	"github.com/start-cli/pj/internal/id"
)

// RewriteName rewrites only the top-level name field of <dir>/pj.cue to newName,
// preserving every other field, comment, and formatting — it parses the file into a
// CUE AST, replaces the name field's value node, and re-formats. It is pj scope
// rename's config-write step: the design forbids string-templating .cue files, so the
// name change goes through the CUE AST like every other config write. newName must be
// a legal scope name; the file must compile and already carry a top-level name field.
func RewriteName(dir, newName string) error {
	if !id.IsScopeName(newName) {
		return fmt.Errorf("%q is not a legal scope name", newName)
	}
	p := filepath.Join(dir, "pj.cue")
	file, err := parser.ParseFile(p, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", p, err)
	}

	found := false
	for _, decl := range file.Decls {
		field, ok := decl.(*ast.Field)
		if !ok {
			continue
		}
		if labelName(field.Label) != "name" {
			continue
		}
		newValue := ast.NewString(newName)
		ast.SetComments(newValue, ast.Comments(field.Value))
		field.Value = newValue
		found = true
		break
	}
	if !found {
		return fmt.Errorf("%s has no top-level name field to rewrite", p)
	}

	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format %s: %w", p, err)
	}
	return atomicfile.Write(p, data, 0o600)
}

// labelName returns the string a field label denotes, handling both a bare
// identifier (name: …) and a quoted string label ("name": …); any other label
// shape yields "" and is skipped.
func labelName(label ast.Label) string {
	switch l := label.(type) {
	case *ast.Ident:
		return l.Name
	case *ast.BasicLit:
		if s, err := literal.Unquote(l.Value); err == nil {
			return s
		}
	}
	return ""
}
