package cli

import (
	"fmt"
	"os"

	"github.com/start-cli/pj/internal/atomicfile"
	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/order"
	"github.com/start-cli/pj/internal/scopeconfig"
)

// projectFileMode is the mode pj writes project markdown with — ordinary,
// non-executable, honouring the atomic-write path's explicit-mode behaviour.
const projectFileMode = 0o644

// atomicWrite writes a project file atomically at the shared project file mode.
func atomicWrite(path string, data []byte) error {
	return atomicfile.Write(path, data, projectFileMode)
}

// single builds the one-scope reconcile target map the write verbs pass to
// reconcileResult for their pre-write read.
func single(scope, dir string) map[string]string {
	return map[string]string{scope: dir}
}

// writtenPaths is the touched-path set a complete-state write hands to SyncPaths: the
// post-write path always, plus a distinct removed old path (the terminal-boundary
// move) so its now-absent row is deleted in the same write-through.
func writtenPaths(newPath, oldPath string) []string {
	if oldPath == "" || oldPath == newPath {
		return []string{newPath}
	}
	return []string{newPath, oldPath}
}

// schemaAutoCommit reports a reconciled scope's autoCommit setting. A nil schema
// (an unusable config) reads as false, but the write verbs refuse an unusable config
// before consulting it, so this only ever runs on a healthy schema.
func schemaAutoCommit(s *scopeconfig.Schema) bool {
	return s != nil && s.AutoCommit
}

// maxValidOrder returns the greatest valid order key across rows — the scope-wide
// append bound for create and reorder --last. It spans every status and both the dir
// root and archive/, and skips parse_error rows and invalid keys. An empty string
// means no valid key exists (an empty board), which the caller feeds to KeyBetween
// as an open bound.
func maxValidOrder(rows []*index.Project) string {
	best := ""
	for _, p := range rows {
		if p.ParseError || !order.Valid(p.OrderKey) {
			continue
		}
		if best == "" || p.OrderKey > best {
			best = p.OrderKey
		}
	}
	return best
}

// readProjectFile reads and parses a healthy project file's frontmatter for a
// mutator, returning the model and the body after the fence. A parse_error-
// quarantined row is refused upstream, so a parse failure here is an unexpected
// mid-write race surfaced as a hard error rather than a silent write.
func readProjectFile(path string) (*frontmatter.Model, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(data)
	if !present {
		return nil, nil, fmt.Errorf("%s has no frontmatter fence", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, body, nil
}

// writeProjectFile serializes a mutated frontmatter model back over its body and
// writes the whole file atomically. It is the single write primitive the status,
// reorder, and claim verbs share for an in-place frontmatter rewrite.
func writeProjectFile(path string, m *frontmatter.Model, body []byte) error {
	interior, err := frontmatter.Serialize(m)
	if err != nil {
		return err
	}
	return atomicWrite(path, frontmatter.Compose(interior, body))
}
