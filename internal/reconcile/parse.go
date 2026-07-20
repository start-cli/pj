package reconcile

import (
	"bytes"
	"os"
	"strings"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/title"
)

// conflictMarkers are the git merge-conflict marker prefixes. Inside the
// frontmatter block they force a parse_error quarantine (untrusted metadata);
// confined to the body they do not (the FM still parses and the project indexes).
var conflictMarkers = [][]byte{[]byte("<<<<<<<"), []byte("======="), []byte(">>>>>>>")}

// parseFile turns one project file into a materialized row and its edges. It never
// fails on bad content: a broken frontmatter fence, malformed YAML, or a conflict
// marker inside the fence yields a parse_error quarantine row (id from the filename,
// body still FTS-indexed) rather than an error. fullID and archived are decided by
// the caller from the filename/location; mtime and size come from the stat already
// taken. A read error on the file itself is returned as an error (a real I/O fault,
// not bad data).
func parseFile(path, scope, fullID string, archived bool, mtimeNS, size int64) (*index.Project, []index.Edge, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	interior, body, present := frontmatter.Split(data)
	base := &index.Project{
		Path: path, Scope: scope, ID: fullID, ShortID: shortOf(fullID),
		Archived: archived, MtimeNS: mtimeNS, Size: size, Body: body,
	}

	if !present || containsConflictMarker(interior) {
		return quarantine(base, data, "frontmatter fence missing, broken, or carries conflict markers"), nil, nil
	}
	model, err := frontmatter.Parse(interior)
	if err != nil {
		return quarantine(base, data, err.Error()), nil, nil
	}

	return indexFromModel(base, model)
}

// quarantine finishes a parse_error row: no trusted frontmatter fields, but the raw
// file (frontmatter + body) is fed to FTS so pj search can still surface it for
// repair, and the id from the filename keeps it locatable.
func quarantine(base *index.Project, raw []byte, msg string) *index.Project {
	base.ParseError = true
	base.ParseMsg = msg
	base.Status = ""
	base.Body = raw
	return base
}

// indexFromModel fills a healthy row from parsed frontmatter and materializes its
// edges. A depends/related entry that is not a legal full project id does not enter
// edges and sets SchemaError so the hard schema_error condition rides affected reads.
func indexFromModel(base *index.Project, m *frontmatter.Model) (*index.Project, []index.Edge, error) {
	base.Status = m.Status
	base.OrderKey = m.Order
	base.Summary = m.Summary
	base.Created = m.Created
	base.Tags = m.Tags
	base.StatusConflict = m.StatusConflict
	base.Title = title.Extract(base.Body)
	if len(m.Custom) > 0 {
		base.Custom = map[string]any{}
		for _, f := range m.Custom {
			base.Custom[f.Key] = f.Value
		}
	}
	// The frontmatter id is authoritative for a healthy row; fall back to the
	// filename-derived id only if frontmatter omits it (a schema fault doctor flags).
	// A frontmatter id claiming a different scope than this file's directory is not
	// adopted: the row's scope is fixed by location, so a foreign-scope id would leave
	// scope and id-prefix disagreeing and no get/meta lookup could resolve the row.
	// Keeping the filename-derived id preserves reachability; doctor (P5) reports the
	// frontmatter/filename mismatch.
	if id.IsFullProjectID(m.ID) && scopeOf(m.ID) == base.Scope {
		base.ID = m.ID
		base.ShortID = shortOf(m.ID)
	}

	edges, schemaErr := edgesFrom(base, m)
	base.SchemaError = schemaErr
	return base, edges, nil
}

// edgesFrom builds the depends/related edges for a row, skipping (and flagging) any
// entry that is not a legal full project id. schemaErr is true when any entry in
// either list was malformed.
func edgesFrom(base *index.Project, m *frontmatter.Model) ([]index.Edge, bool) {
	var edges []index.Edge
	schemaErr := false
	add := func(list []string, kind string) {
		for _, target := range list {
			if !id.IsFullProjectID(target) {
				schemaErr = true
				continue
			}
			edges = append(edges, index.Edge{
				FromPath: base.Path, FromID: base.ID, FromScope: base.Scope,
				ToID: target, ToScope: scopeOf(target), Kind: kind,
			})
		}
	}
	add(m.Depends, index.EdgeDepends)
	add(m.Related, index.EdgeRelated)
	return edges, schemaErr
}

func containsConflictMarker(interior []byte) bool {
	for len(interior) > 0 {
		var line []byte
		if i := bytes.IndexByte(interior, '\n'); i >= 0 {
			line, interior = interior[:i], interior[i+1:]
		} else {
			line, interior = interior, nil
		}
		for _, marker := range conflictMarkers {
			if bytes.HasPrefix(line, marker) {
				return true
			}
		}
	}
	return false
}

// shortOf returns the short-id portion of a full id, or "" when the id is not a
// legal full id.
func shortOf(fullID string) string {
	if !id.IsFullProjectID(fullID) {
		return ""
	}
	return fullID[strings.IndexByte(fullID, '-')+1:]
}

// scopeOf returns the scope portion of a full id (already validated by the caller).
func scopeOf(fullID string) string {
	if i := strings.IndexByte(fullID, '-'); i >= 0 {
		return fullID[:i]
	}
	return ""
}
