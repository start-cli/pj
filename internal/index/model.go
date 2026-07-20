package index

// Project is one materialized project row. It is the derived view of a single
// project file: everything here is reconstructable from the file, so the index
// can be dropped and rebuilt at any time. Path is the physical key.
type Project struct {
	Path     string
	Scope    string
	ID       string // full id <scope>-<short-id>; on a parse error, from the filename prefix
	ShortID  string
	Status   string
	OrderKey string
	Title    string
	Summary  string
	Created  string
	Tags     []string
	Custom   map[string]any
	// StatusConflict holds the disputed terminal statuses when a merge left a
	// status_conflict key in frontmatter; empty otherwise.
	StatusConflict []string
	// Archived is true when the file lives under the scope's archive/ subdir.
	Archived bool
	// ParseError is true when the frontmatter could not be parsed and the row is
	// a quarantine record (id from the filename, body still FTS-indexed).
	ParseError bool
	ParseMsg   string
	// SchemaError is true when a depends/related entry failed IsFullProjectID.
	// The malformed entry does not enter edges, but the flag rides affected reads.
	SchemaError bool
	// Body is the markdown after the closing fence, used only to populate FTS on
	// write; it is not stored as a project column and is not read back.
	Body    []byte
	MtimeNS int64
	Size    int64
}

// Edge is one depends/related relationship materialized from frontmatter. FromID
// and ToID are always full project ids. FromPath ties the edge to its owning file
// so a reconcile can replace exactly that file's edges.
type Edge struct {
	FromPath  string
	FromID    string
	FromScope string
	ToID      string
	ToScope   string
	Kind      string // EdgeDepends or EdgeRelated
}

// Edge kinds.
const (
	EdgeDepends = "depends"
	EdgeRelated = "related"
)
