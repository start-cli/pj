// Package frontmatter models a project file's leading YAML frontmatter. It
// offers two independent paths, deliberately kept separate: a raw fence-slice
// API (Split) that returns the interior bytes verbatim for pj meta, and a
// decode/encode path (Parse and Serialize) for writers that need the typed
// model. It performs no I/O.
//
// The built-in key set is closed and immutable: id, status, order, depends,
// related, tags, created, links, summary, plus the transient merge-only key
// status_conflict. Undeclared keys (a scope's pj.cue custom fields) are
// preserved verbatim in declaration order rather than dropped. The serializer
// always emits order as a quoted string (order: "a0") so the mixed digit/letter
// key cannot be YAML-coerced to a number.
package frontmatter

import (
	"bytes"
	"fmt"

	"github.com/goccy/go-yaml"
)

// Built-in frontmatter key names (closed set).
const (
	KeyID             = "id"
	KeyStatus         = "status"
	KeyOrder          = "order"
	KeyDepends        = "depends"
	KeyRelated        = "related"
	KeyTags           = "tags"
	KeyCreated        = "created"
	KeyLinks          = "links"
	KeySummary        = "summary"
	KeyStatusConflict = "status_conflict"
)

// builtinKeys is the set of recognised built-in keys, used to route decoded
// entries away from the Custom bucket.
var builtinKeys = map[string]struct{}{
	KeyID: {}, KeyStatus: {}, KeyOrder: {}, KeyDepends: {}, KeyRelated: {},
	KeyTags: {}, KeyCreated: {}, KeyLinks: {}, KeySummary: {}, KeyStatusConflict: {},
}

// IsBuiltinKey reports whether name is a built-in frontmatter key. It is the
// single source of truth for the built-in key set: scope config validation
// consults it so a custom field cannot shadow a built-in, and a key added here
// extends that check automatically.
func IsBuiltinKey(name string) bool {
	_, ok := builtinKeys[name]
	return ok
}

// Field is an undeclared (custom) frontmatter key preserved from the source in
// declaration order, with its value as decoded by the YAML layer.
type Field struct {
	Key   string
	Value any
}

// Model is the decoded frontmatter: the closed built-in keys as typed fields
// plus any undeclared keys retained in Custom. A nil slice or empty string
// means the key was absent; the serializer omits absent optional keys.
type Model struct {
	ID             string
	Status         string
	Order          string
	Depends        []string
	Related        []string
	Tags           []string
	Created        string
	Links          []string
	Summary        string
	StatusConflict []string
	Custom         []Field
}

// Split separates a file's leading ---...--- frontmatter fence from the body
// and returns the fence interior verbatim (no re-encode), the body after the
// closing fence, and whether a fence was present. When the data does not begin
// with a fence or has no closing fence, present is false and body is the whole
// input. This is the raw path pj meta prints from; it never parses YAML.
//
// A fence line must be exactly "---" (a trailing carriage return aside). This is
// intentional: a "--- " line with trailing whitespace is a CommonMark thematic
// break, not a fence, and tolerating it would misread such a break in the body
// as a closing fence. A malformed leading fence therefore yields present=false;
// doctor surfaces a project file that then lacks parseable frontmatter.
func Split(data []byte) (interior, body []byte, present bool) {
	first, rest, ok := firstLine(data)
	if !ok || !isFence(first) {
		return nil, data, false
	}
	start := len(data) - len(rest)
	pos := start
	remaining := rest
	for len(remaining) > 0 {
		line, after, _ := firstLine(remaining)
		if isFence(line) {
			return data[start:pos], after, true
		}
		pos += len(remaining) - len(after)
		remaining = after
	}
	return nil, data, false
}

// firstLine splits off the first line of data. line excludes the newline; rest
// is everything after it; hadNewline reports whether a '\n' terminated the line.
func firstLine(data []byte) (line, rest []byte, hadNewline bool) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[:i], data[i+1:], true
	}
	return data, nil, false
}

// isFence reports whether a line (ignoring a trailing carriage return) is the
// frontmatter fence marker "---".
func isFence(line []byte) bool {
	return bytes.Equal(bytes.TrimSuffix(line, []byte("\r")), []byte("---"))
}

// Parse decodes the fence interior into a Model, routing the closed built-in
// keys into typed fields and preserving every other key in Custom in
// declaration order. It returns an error on malformed YAML or a built-in key
// whose YAML type is incompatible (for example a scalar where a list is
// required). Comments and exact byte layout are intentionally not preserved
// here — that is the Split path's job.
func Parse(interior []byte) (*Model, error) {
	var items yaml.MapSlice
	if err := yaml.Unmarshal(interior, &items); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	m := &Model{}
	for _, item := range items {
		key := fmt.Sprint(item.Key)
		if _, ok := builtinKeys[key]; !ok {
			m.Custom = append(m.Custom, Field{Key: key, Value: item.Value})
			continue
		}
		if err := m.assignBuiltin(key, item.Value); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Model) assignBuiltin(key string, value any) error {
	switch key {
	case KeyID:
		m.ID = asScalarString(value)
	case KeyStatus:
		m.Status = asScalarString(value)
	case KeyOrder:
		m.Order = asScalarString(value)
	case KeyCreated:
		m.Created = asScalarString(value)
	case KeySummary:
		m.Summary = asScalarString(value)
	case KeyDepends:
		return assignList(key, value, &m.Depends)
	case KeyRelated:
		return assignList(key, value, &m.Related)
	case KeyTags:
		return assignList(key, value, &m.Tags)
	case KeyLinks:
		return assignList(key, value, &m.Links)
	case KeyStatusConflict:
		return assignList(key, value, &m.StatusConflict)
	}
	return nil
}

func assignList(key string, value any, dst *[]string) error {
	list, err := asStringList(value)
	if err != nil {
		return fmt.Errorf("frontmatter key %q: %w", key, err)
	}
	*dst = list
	return nil
}

// Serialize encodes the Model back to clean frontmatter YAML (interior only, no
// fences). Keys are emitted in the canonical design order; order is always
// quoted; the built-in list keys use flow style; and absent optional keys are
// omitted. Custom fields follow the built-ins in their retained order.
func Serialize(m *Model) ([]byte, error) {
	items := yaml.MapSlice{
		{Key: KeyID, Value: m.ID},
		{Key: KeyStatus, Value: m.Status},
		{Key: KeyOrder, Value: quotedString(m.Order)},
	}
	items = appendList(items, KeyDepends, m.Depends)
	items = appendList(items, KeyRelated, m.Related)
	items = appendList(items, KeyTags, m.Tags)
	items = append(items, yaml.MapItem{Key: KeyCreated, Value: m.Created})
	items = appendList(items, KeyLinks, m.Links)
	if m.Summary != "" {
		items = append(items, yaml.MapItem{Key: KeySummary, Value: m.Summary})
	}
	items = appendList(items, KeyStatusConflict, m.StatusConflict)
	for _, f := range m.Custom {
		items = append(items, yaml.MapItem{Key: f.Key, Value: f.Value})
	}

	out, err := yaml.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("serialize frontmatter: %w", err)
	}
	return out, nil
}

func appendList(items yaml.MapSlice, key string, list []string) yaml.MapSlice {
	if len(list) == 0 {
		return items
	}
	return append(items, yaml.MapItem{Key: key, Value: flowStrings(list)})
}

// Compose assembles a full project file from a serialized frontmatter interior
// and a body, wrapping the interior in the ---...--- fence. interior is
// expected to end with a newline (Serialize output does).
func Compose(interior, body []byte) []byte {
	var b bytes.Buffer
	b.Grow(len("---\n---\n") + len(interior) + len(body))
	b.WriteString("---\n")
	b.Write(interior)
	b.WriteString("---\n")
	b.Write(body)
	return b.Bytes()
}

// quotedString forces a double-quoted scalar on marshal, keeping order a string.
type quotedString string

func (q quotedString) MarshalYAML() ([]byte, error) {
	return fmt.Appendf(nil, "%q", string(q)), nil
}

// flowStrings marshals a string list in flow style ([a, b]), delegating
// per-element escaping to the YAML encoder.
type flowStrings []string

func (f flowStrings) MarshalYAML() ([]byte, error) {
	return yaml.MarshalWithOptions([]string(f), yaml.Flow(true))
}

// asScalarString renders a decoded scalar as a string. A YAML string stays as
// is; other scalars (numbers, bools) are rendered via their default formatting
// so a hand-edited or mistyped value is still readable rather than dropped.
func asScalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// asStringList coerces a decoded YAML sequence into a []string. A nil value is
// an absent key (empty list); a non-sequence value is a typing error.
func asStringList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	seq, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a list, got %T", v)
	}
	out := make([]string, len(seq))
	for i, e := range seq {
		out[i] = asScalarString(e)
	}
	return out, nil
}
