package scopeconfig

import (
	"fmt"
	"path/filepath"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"

	"github.com/start-cli/pj/internal/atomicfile"
)

// WriteMinimal authors a fresh, minimal valid pj.cue (name + autoCommit) at
// <dir>/pj.cue. The document is built as a CUE AST — never string-templated —
// and installed atomically via a same-directory temp file and rename. Fields are
// emitted in declaration order (name, then autoCommit) so the file reads the way
// the design shows it. It is init's authoring path; import never writes pj.cue.
func WriteMinimal(dir, name string, autoCommit bool) error {
	file := &ast.File{Decls: []ast.Decl{
		&ast.Field{Label: ast.NewIdent("name"), Value: ast.NewString(name)},
		&ast.Field{Label: ast.NewIdent("autoCommit"), Value: ast.NewBool(autoCommit)},
	}}
	data, err := format.Node(file)
	if err != nil {
		return fmt.Errorf("format pj.cue: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, "pj.cue"), data, 0o600)
}
