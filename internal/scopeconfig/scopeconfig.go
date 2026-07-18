// Package scopeconfig evaluates a scope's pj.cue into a validated Schema. It is
// the scope-tier config engine: it reads <dir>/pj.cue only through the CUE Go
// modules and exposes the single "config usable?" signal the rest of pj branches
// on. A non-nil *ConfigError from Load means the scope is read-only/unusable —
// its writes refuse and it rides config_unparseable — while its reads stay
// available. Consumers never need to tell a compile failure apart from a schema
// violation; both are the same unusable state.
//
// The Schema is modelled for later projects to consume without re-reading
// pj.cue: P3 index materialization and list membership ask which statuses are
// known/terminal, P6 merge typing asks a custom field's type, and the write
// paths read AutoCommit. The evaluation is correct but uncached here — there is
// no index yet to cache it in.
package scopeconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cuelang.org/go/cue"
	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/status"
)

// Field types (closed set for v1).
const (
	FieldString  = "string"
	FieldInt     = "int"
	FieldBool    = "bool"
	FieldStrings = "strings"
)

var (
	// statusNameRe is the custom-status name alphabet: lowercase, hyphen-joined.
	statusNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	// fieldNameRe is the custom-field name alphabet: snake-friendly YAML keys.
	fieldNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// Field is a declared custom frontmatter field. Values is non-nil only when the
// declaration carries an enum (legal only for string/strings types).
type Field struct {
	Type   string
	Values []string
}

// Schema is a scope's fully-evaluated, validated pj.cue. Its presence means the
// config is usable.
type Schema struct {
	Name       string
	AutoCommit bool
	KnownTags  []string
	// Statuses maps each declared custom status name to its category. Built-in
	// statuses are not included here — they live in the status package.
	Statuses map[string]status.Category
	// Fields maps each declared custom field name to its type and optional enum.
	Fields map[string]Field
}

// ConfigError marks a pj.cue that cannot be trusted: absent, uncompilable, or
// compiled-but-schema-invalid. All three are the same unusable state downstream
// and ride the config_unparseable token. Dir is the scope directory; Reason is
// a human-readable explanation without the token prefix.
type ConfigError struct {
	Dir    string
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("scope config unparseable at %s: %s", filepath.Join(e.Dir, "pj.cue"), e.Reason)
}

// AsConfigError reports whether err is (or wraps) a *ConfigError.
func AsConfigError(err error) (*ConfigError, bool) {
	var ce *ConfigError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

type rawConfig struct {
	Name       string               `json:"name"`
	AutoCommit *bool                `json:"autoCommit"`
	KnownTags  []string             `json:"knownTags"`
	Statuses   map[string]rawStatus `json:"statuses"`
	Fields     map[string]rawField  `json:"fields"`
}

type rawStatus struct {
	Category string `json:"category"`
}

type rawField struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// Load reads and validates <dir>/pj.cue. It returns a *ConfigError for every
// unusable state (absent, uncompilable, or schema-invalid) so a single check —
// is the returned error a *ConfigError — tells a consumer the scope is
// read-only. compile is the process-wide CUE context.
func Load(ctx *cue.Context, dir string) (*Schema, error) {
	p := filepath.Join(dir, "pj.cue")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ConfigError{Dir: dir, Reason: "pj.cue is absent"}
		}
		return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("cannot read pj.cue: %v", err)}
	}

	v := ctx.CompileBytes(data, cue.Filename(p))
	if err := v.Err(); err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(err)}
	}

	var raw rawConfig
	if err := v.Decode(&raw); err != nil {
		return nil, &ConfigError{Dir: dir, Reason: cueReason(err)}
	}

	return validate(dir, v, &raw)
}

// ReadName extracts only the authoritative name from <dir>/pj.cue. It is the
// drift precondition: name-drift compares this against the registry key, and it
// must succeed even when the fuller schema is invalid, provided the file
// compiles and carries a legal name. It returns an error when the file is
// absent, uncompilable, or its name is missing or not a legal scope name.
func ReadName(ctx *cue.Context, dir string) (string, error) {
	p := filepath.Join(dir, "pj.cue")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &ConfigError{Dir: dir, Reason: "pj.cue is absent"}
		}
		return "", &ConfigError{Dir: dir, Reason: fmt.Sprintf("cannot read pj.cue: %v", err)}
	}
	v := ctx.CompileBytes(data, cue.Filename(p))
	if err := v.Err(); err != nil {
		return "", &ConfigError{Dir: dir, Reason: cueReason(err)}
	}
	name, err := v.LookupPath(cue.MakePath(cue.Str("name"))).String()
	if err != nil {
		return "", &ConfigError{Dir: dir, Reason: "name is missing or not a string"}
	}
	if !id.IsScopeName(name) {
		return "", &ConfigError{Dir: dir, Reason: fmt.Sprintf("name %q is not a legal scope name", name)}
	}
	return name, nil
}

func validate(dir string, v cue.Value, raw *rawConfig) (*Schema, error) {
	if !id.IsScopeName(raw.Name) {
		return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("name %q is not a legal scope name (^[a-z0-9]{1,12}$)", raw.Name)}
	}
	if raw.AutoCommit == nil {
		return nil, &ConfigError{Dir: dir, Reason: "autoCommit is required"}
	}

	statuses, err := validateStatuses(dir, raw.Statuses)
	if err != nil {
		return nil, err
	}
	fields, err := validateFields(dir, v, raw.Fields)
	if err != nil {
		return nil, err
	}

	return &Schema{
		Name:       raw.Name,
		AutoCommit: *raw.AutoCommit,
		KnownTags:  raw.KnownTags,
		Statuses:   statuses,
		Fields:     fields,
	}, nil
}

func validateStatuses(dir string, raw map[string]rawStatus) (map[string]status.Category, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]status.Category, len(raw))
	for name, st := range raw {
		if !statusNameRe.MatchString(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("status name %q is outside its alphabet (^[a-z][a-z0-9-]{0,31}$)", name)}
		}
		if status.IsBuiltin(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("status %q redeclares a built-in status", name)}
		}
		cat := status.Category(st.Category)
		if !status.ValidCategory(cat) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("status %q has category %q, want active|backlog|done", name, st.Category)}
		}
		out[name] = cat
	}
	return out, nil
}

func validateFields(dir string, v cue.Value, raw map[string]rawField) (map[string]Field, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]Field, len(raw))
	for name, f := range raw {
		if !fieldNameRe.MatchString(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field name %q is outside its alphabet (^[a-z][a-z0-9_]{0,31}$)", name)}
		}
		if frontmatter.IsBuiltinKey(name) {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q shadows a built-in frontmatter key", name)}
		}
		switch f.Type {
		case FieldString, FieldInt, FieldBool, FieldStrings:
		default:
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q has type %q, want string|int|bool|strings", name, f.Type)}
		}
		// values presence is read from the CUE value: a nil-vs-empty slice
		// heuristic cannot tell an absent enum from an explicit empty one.
		hasEnum := v.LookupPath(cue.MakePath(cue.Str("fields"), cue.Str(name), cue.Str("values"))).Exists()
		if hasEnum && f.Type != FieldString && f.Type != FieldStrings {
			return nil, &ConfigError{Dir: dir, Reason: fmt.Sprintf("field %q has a values enum, legal only for string or strings (not %s)", name, f.Type)}
		}
		field := Field{Type: f.Type}
		if hasEnum {
			field.Values = f.Values
		}
		out[name] = field
	}
	return out, nil
}

// cueReason renders a CUE error as a ConfigError reason on a single line. A
// reason is emitted as one config_unparseable stderr line (token + space +
// reason), which agents match one-token-per-line; a stray newline in the CUE
// error would split that line. CUE's Error() is single-line today, but that is
// not contractual, so collapse any interior whitespace run — newline included —
// to a single space to keep the guarantee in our code.
func cueReason(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

// customCategories projects the schema's custom statuses into the map the status
// package predicates take.
func (s *Schema) customCategories() map[string]status.Category {
	return s.Statuses
}

// StatusKnown reports whether name is a built-in or a status this scope declares.
func (s *Schema) StatusKnown(name string) bool {
	return status.IsKnown(name, s.customCategories())
}

// StatusTerminal reports whether name is terminal for this scope (built-in done
// or cancelled, or a custom status whose category is done).
func (s *Schema) StatusTerminal(name string) bool {
	return status.IsTerminal(name, s.customCategories())
}

// Category returns the category of a status known to this scope.
func (s *Schema) Category(name string) (status.Category, bool) {
	return status.CategoryOf(name, s.customCategories())
}

// Field returns the declared custom field of the given name, if any.
func (s *Schema) Field(name string) (Field, bool) {
	f, ok := s.Fields[name]
	return f, ok
}
