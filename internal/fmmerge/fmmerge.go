// Package fmmerge is pj's pure 3-way frontmatter merge (design: Merge conflict
// handling, layer 3). It merges the frontmatter of a single conflicted project file
// from its three raw git stage blobs and a scope schema, and returns either clean
// merged frontmatter, a status-dispute payload, a same-id add/add rename directive, a
// delete/edit handoff signal, or a fail-closed *MergeError.
//
// It performs no I/O: no git subprocess, no filesystem, no index, no flock. Every
// input the merge needs — both sides' git author dates, the scope name, and the set of
// short-ids already occupied in the scope — is passed in by the driver, so the package
// is deterministic for fixed inputs and testable on canned stage blobs. The rebase
// driver (internal/rebasedriver) owns all git I/O and body handling; this package
// splits each stage blob into frontmatter and body internally, merges only the
// frontmatter, and never touches the body.
//
// Stage presence is explicit and load-bearing. A stage that is absent (no :1 on an
// add/add; no entry for the deleting side of a delete/modify) and a stage that is
// present but empty are different inputs the merge branches on — git records a deletion
// by omitting the stage entry, not by writing a zero-byte blob — so callers must set
// Stage.Present rather than conflating the two with a nil-vs-empty []byte.
package fmmerge

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/repair"
	"github.com/start-cli/pj/internal/scopeconfig"
)

// Stage is one git merge stage's blob plus whether that stage exists at all. Present
// false is an absent stage; Present true with empty Data is a present-but-empty
// (malformed) stage. Data is the whole stage blob (frontmatter fence plus body); the
// merge splits the frontmatter out itself.
type Stage struct {
	Present bool
	Data    []byte
}

// Side names one of the two non-base stages. The pure package attaches no rebase
// meaning to the labels — the driver decides which stage blob is Ours (git stage :2)
// and which is Theirs (stage :3) and passes the matching author date — so "ours" here
// is simply the first non-base side, never git's during-rebase "ours".
type Side int

const (
	// SideOurs is the stage the driver passed as ours (git stage :2).
	SideOurs Side = iota
	// SideTheirs is the stage the driver passed as theirs (git stage :3).
	SideTheirs
)

func (s Side) String() string {
	if s == SideTheirs {
		return "theirs"
	}
	return "ours"
}

// MergeMeta carries the non-blob inputs the pure merge needs, all supplied by the
// driver so the core stays I/O-free. It holds no paths: a stage blob is content and
// cannot name a file, so path composition is entirely driver work.
type MergeMeta struct {
	// OursDate and TheirsDate are the git author dates of the two sides, used for
	// both-sides scalar last-writer-wins. They are matched positionally to Ours/Theirs.
	OursDate   time.Time
	TheirsDate time.Time
	// Scope is the scope name, used only to mint the loser's new full id in an add/add.
	Scope string
	// Occupied is the set of short-ids already taken in the scope, used only to extend
	// the loser's short-id in an add/add (id.Extend). It is the driver's per-conflict
	// derivation, not an index projection.
	Occupied map[string]struct{}
}

// Outcome is the merge result class the driver branches on.
type Outcome int

const (
	// OutcomeMerged is a clean field-merged frontmatter (Result.Model).
	OutcomeMerged Outcome = iota
	// OutcomeStatusConflict is a terminal-involved status dispute: Result.Model carries
	// the merge-base status plus status_conflict, and Result.StatusConflict is the pair.
	OutcomeStatusConflict
	// OutcomeRename is a same-id add/add: Result.Rename says which side loses and the
	// loser's new short-id. The driver composes both file paths and rewrites the id.
	OutcomeRename
	// OutcomeDeleteEdit is a delete/edit handoff: Result.DeleteEdit says which side
	// deleted and the surviving side's post-edit status.
	OutcomeDeleteEdit
)

// Result is the merge outcome. Exactly the fields named by Outcome are populated.
type Result struct {
	Outcome Outcome
	// Model is the clean merged frontmatter for OutcomeMerged and OutcomeStatusConflict.
	Model *frontmatter.Model
	// StatusConflict is the disputed pair for OutcomeStatusConflict (also written into
	// Model.StatusConflict), for the driver to report.
	StatusConflict []string
	// Rename is the add/add rename directive for OutcomeRename.
	Rename *RenameDirective
	// DeleteEdit is the delete/edit handoff for OutcomeDeleteEdit.
	DeleteEdit *DeleteEditSignal
	// Warnings carries doctor-class notes the merge kept quiet about (an immutable both
	// sides rewrote identically), for the driver to surface.
	Warnings []string
}

// RenameDirective is the same-id add/add repair answer: which side loses and the
// loser's deterministically minted new short-id. It carries no path — the driver holds
// the one path both sides collided on and composes the keep path and the new path from
// it plus the loser's frozen slug.
type RenameDirective struct {
	Loser      Side
	CollidedID string
	NewShortID string
}

// DeleteEditSignal is the delete/edit handoff: the side that deleted the file and the
// surviving side's post-edit status. The driver attaches the path when it reports.
type DeleteEditSignal struct {
	Deleted         Side
	SurvivingStatus string
}

// MergeError is a fail-closed data condition the human resolves in-file: an unparseable
// or present-but-empty stage, a both-sides immutable disagreement, a both-sides status
// change with an unknown post-edit name, two differing inherited status_conflict pairs,
// or a call without a readable schema. It is the only error the pure package returns,
// so the driver treats every *MergeError as a fail-closed handoff and reserves its own
// error channel for operational faults. Key names the offending frontmatter key when
// one applies.
type MergeError struct {
	Key    string
	Reason string
}

func (e *MergeError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("frontmatter merge failed on key %q: %s", e.Key, e.Reason)
	}
	return "frontmatter merge failed: " + e.Reason
}

// MergeFrontmatter 3-way merges the frontmatter of one conflicted project file from its
// base/ours/theirs stage blobs, the scope's post-integration schema, and the merge
// metadata. A nil schema is refused (schema-before-data). See the package doc for the
// stage-presence contract; the outcome classes are documented on Outcome.
func MergeFrontmatter(base, ours, theirs Stage, schema *scopeconfig.Schema, meta MergeMeta) (Result, error) {
	if schema == nil {
		return Result{}, &MergeError{Reason: "no readable schema; refusing to field-merge (schema-before-data)"}
	}
	baseM, err := loadStage(base)
	if err != nil {
		return Result{}, err
	}
	oursM, err := loadStage(ours)
	if err != nil {
		return Result{}, err
	}
	theirsM, err := loadStage(theirs)
	if err != nil {
		return Result{}, err
	}

	switch {
	case !base.Present && ours.Present && theirs.Present:
		return addAdd(oursM, theirsM, ours.Data, theirs.Data, meta)
	case base.Present && ours.Present && theirs.Present:
		return threeWay(baseM, oursM, theirsM, ours.Data, theirs.Data, schema, meta)
	case base.Present && ours.Present && !theirs.Present:
		return deleteEdit(SideTheirs, oursM), nil
	case base.Present && !ours.Present && theirs.Present:
		return deleteEdit(SideOurs, theirsM), nil
	default:
		return Result{}, &MergeError{Reason: "malformed conflict: stage shape is neither add/add, a shared-base 3-way, nor delete/edit"}
	}
}

// loadStage splits and parses a stage's frontmatter, distinguishing absent (nil model,
// no error) from present-but-empty and unparseable (both malformed-input errors). A
// present zero-byte blob is malformed, not a deletion: git omits the stage entry for a
// deletion, so zero bytes at a live stage is a truncated or hand-mangled file.
func loadStage(s Stage) (*frontmatter.Model, error) {
	if !s.Present {
		return nil, nil
	}
	if len(s.Data) == 0 {
		return nil, &MergeError{Reason: "present-but-empty stage blob (a deletion is an absent stage, not a zero-byte one)"}
	}
	interior, _, present := frontmatter.Split(s.Data)
	if !present {
		return nil, &MergeError{Reason: "stage has no frontmatter fence"}
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, &MergeError{Reason: "stage frontmatter is unparseable: " + err.Error()}
	}
	return m, nil
}

// addAdd handles a base-absent both-present conflict: the same-id add/add guard,
// checked before any field merge. Both sides carrying the same id are two distinct
// projects that collided on id and slug, not two edits of one project, so they are kept
// as two files via a rename rather than field-merged into one.
func addAdd(oursM, theirsM *frontmatter.Model, oursRaw, theirsRaw []byte, meta MergeMeta) (Result, error) {
	if oursM.ID == "" || theirsM.ID == "" || oursM.ID != theirsM.ID {
		return Result{}, &MergeError{Key: frontmatter.KeyID, Reason: "add/add conflict without a shared id — cannot field-merge two distinct projects"}
	}
	// Both stages are one path with one basename, so Basename and Path are the same
	// placeholder on both sides and only Created and the byte hash decide the loser.
	loser := SideTheirs
	if !repair.KeepBefore(
		repair.LoserMember{Created: oursM.Created, Raw: oursRaw},
		repair.LoserMember{Created: theirsM.Created, Raw: theirsRaw},
	) {
		loser = SideOurs
	}

	full := oursM.ID
	if !id.IsFullProjectID(full) {
		return Result{}, &MergeError{Key: frontmatter.KeyID, Reason: "add/add id is not a legal full project id: " + full}
	}
	short := full[strings.IndexByte(full, '-')+1:]
	newShort, err := id.Extend(short, meta.Occupied)
	if err != nil {
		return Result{}, &MergeError{Key: frontmatter.KeyID, Reason: err.Error()}
	}
	return Result{Outcome: OutcomeRename, Rename: &RenameDirective{Loser: loser, CollidedID: full, NewShortID: newShort}}, nil
}

func deleteEdit(deleted Side, survivor *frontmatter.Model) Result {
	return Result{Outcome: OutcomeDeleteEdit, DeleteEdit: &DeleteEditSignal{Deleted: deleted, SurvivingStatus: survivor.Status}}
}

// threeWay field-merges a shared-base conflict per the design's typed rules.
func threeWay(base, ours, theirs *frontmatter.Model, oursRaw, theirsRaw []byte, schema *scopeconfig.Schema, meta MergeMeta) (Result, error) {
	res := &frontmatter.Model{}
	var warnings []string

	idVal, warn, err := mergeImmutable(base.ID, ours.ID, theirs.ID, frontmatter.KeyID)
	if err != nil {
		return Result{}, err
	}
	if warn {
		warnings = append(warnings, fmt.Sprintf("both sides rewrote immutable %q identically off base; kept base %q", frontmatter.KeyID, base.ID))
	}
	res.ID = idVal

	createdVal, warn, err := mergeImmutable(base.Created, ours.Created, theirs.Created, frontmatter.KeyCreated)
	if err != nil {
		return Result{}, err
	}
	if warn {
		warnings = append(warnings, fmt.Sprintf("both sides rewrote immutable %q identically off base; kept base %q", frontmatter.KeyCreated, base.Created))
	}
	res.Created = createdVal

	res.Tags = mergeList(base.Tags, ours.Tags, theirs.Tags)
	res.Depends = mergeList(base.Depends, ours.Depends, theirs.Depends)
	res.Related = mergeList(base.Related, ours.Related, theirs.Related)
	res.Links = mergeList(base.Links, ours.Links, theirs.Links)

	res.Order = mergeScalar(base.Order, ours.Order, theirs.Order, oursRaw, theirsRaw, meta)
	res.Summary = mergeScalar(base.Summary, ours.Summary, theirs.Summary, oursRaw, theirsRaw, meta)

	st, err := mergeStatus(base, ours, theirs, oursRaw, theirsRaw, schema, meta)
	if err != nil {
		return Result{}, err
	}
	res.Status = st.status
	res.StatusConflict = st.statusConflict

	mergeCustom(res, base, ours, theirs, oursRaw, theirsRaw, schema, meta)

	out := Result{Outcome: OutcomeMerged, Model: res, Warnings: warnings}
	if st.dispute {
		out.Outcome = OutcomeStatusConflict
		out.StatusConflict = st.statusConflict
	}
	return out, nil
}

// mergeImmutable merges an identity/provenance key (id, created), which is never scalar
// LWW. Base is the source of identity: it is kept whenever either side still matches
// base or only one side differs. Both sides differing and disagreeing fails closed;
// both differing but agreeing keeps base with a doctor-class warning. When base lacks
// the key, an agreed value is taken and a disagreement fails closed.
func mergeImmutable(base, ours, theirs, key string) (val string, warn bool, err error) {
	if base != "" {
		oursDiff := ours != base
		theirsDiff := theirs != base
		if oursDiff && theirsDiff {
			if ours == theirs {
				return base, true, nil
			}
			return "", false, &MergeError{Key: key, Reason: fmt.Sprintf("both sides changed immutable off base to different values (%q vs %q)", ours, theirs)}
		}
		return base, false, nil
	}
	if ours == theirs {
		return ours, false, nil
	}
	return "", false, &MergeError{Key: key, Reason: fmt.Sprintf("base lacks the key and the two sides disagree (%q vs %q)", ours, theirs)}
}

// mergeList 3-way set-merges a list field against base: an in-base element is kept only
// when both sides still carry it (either side's removal prunes it, honouring a
// concurrent removal), and a not-in-base element is kept when either side added it (an
// add with no matching base element to remove keeps). Output order is deterministic:
// retained base elements in base order, then additions in ours-then-theirs order.
func mergeList(base, ours, theirs []string) []string {
	baseSet := toSet(base)
	oursSet := toSet(ours)
	theirsSet := toSet(theirs)
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, e := range base {
		if oursSet[e] && theirsSet[e] {
			add(e)
		}
	}
	for _, e := range ours {
		if !baseSet[e] {
			add(e)
		}
	}
	for _, e := range theirs {
		if !baseSet[e] {
			add(e)
		}
	}
	return out
}

// mergeScalar merges a plain scalar string ("" is absent): one side changed takes that
// side uncontested; both sides changed to the same value takes it; both changed to
// different values is last-writer-wins by author date, tie-broken by the greater
// SHA-256 of the whole stage bytes.
func mergeScalar(base, ours, theirs string, oursRaw, theirsRaw []byte, meta MergeMeta) string {
	if ours == theirs {
		return ours
	}
	if ours == base {
		return theirs
	}
	if theirs == base {
		return ours
	}
	if pickOurs(meta, oursRaw, theirsRaw) {
		return ours
	}
	return theirs
}

type statusMerge struct {
	status         string
	statusConflict []string
	dispute        bool
}

// mergeStatus merges status together with the merge-owned status_conflict key. A fresh
// terminal-involved both-sides dispute writes the merge-base status plus the disputed
// pair and discards any inherited status_conflict. Otherwise status follows the scalar
// one-side/both-equal/LWW rule and the inherited status_conflict is carried by its own
// one-side-changed rule (two differing inherited pairs fail closed).
func mergeStatus(base, ours, theirs *frontmatter.Model, oursRaw, theirsRaw []byte, schema *scopeconfig.Schema, meta MergeMeta) (statusMerge, error) {
	bs, os, ts := base.Status, ours.Status, theirs.Status
	if os != bs && ts != bs && os != ts {
		// Both sides changed status to two different values.
		if !schema.StatusKnown(os) || !schema.StatusKnown(ts) {
			return statusMerge{}, &MergeError{Key: frontmatter.KeyStatus, Reason: fmt.Sprintf("both sides changed status to different values and %q or %q is not a known status", os, ts)}
		}
		if schema.StatusTerminal(os) || schema.StatusTerminal(ts) {
			return statusMerge{status: bs, statusConflict: sortedPair(os, ts), dispute: true}, nil
		}
		// Pure non-terminal pair: last-writer-wins.
		sc, err := mergeInherited(base.StatusConflict, ours.StatusConflict, theirs.StatusConflict)
		if err != nil {
			return statusMerge{}, err
		}
		return statusMerge{status: mergeScalar(bs, os, ts, oursRaw, theirsRaw, meta), statusConflict: sc}, nil
	}

	sc, err := mergeInherited(base.StatusConflict, ours.StatusConflict, theirs.StatusConflict)
	if err != nil {
		return statusMerge{}, err
	}
	return statusMerge{status: mergeScalar(bs, os, ts, oursRaw, theirsRaw, meta), statusConflict: sc}, nil
}

// mergeInherited merges an inherited status_conflict sequence as one merge-owned value
// on the one-side-changed shape: keep it when the sides agree or only one side changed
// it; fail closed when the two sides carry different pairs (a set-merge would yield a
// three-name key no verb can repair and the shape forbids).
func mergeInherited(base, ours, theirs []string) ([]string, error) {
	oursChanged := !equalSeq(ours, base)
	theirsChanged := !equalSeq(theirs, base)
	switch {
	case !oursChanged && !theirsChanged:
		return base, nil
	case oursChanged && !theirsChanged:
		return ours, nil
	case !oursChanged && theirsChanged:
		return theirs, nil
	default:
		if equalSeq(ours, theirs) {
			return ours, nil
		}
		return nil, &MergeError{Key: frontmatter.KeyStatusConflict, Reason: "the two sides carry different inherited status_conflict pairs"}
	}
}

// optAny is one custom/undeclared key's value plus whether it is present on a side.
type optAny struct {
	present bool
	value   any
}

// mergeCustom merges every custom and undeclared key across the three sides. A schema
// strings field is set-merged like a built-in list; every other declared type and every
// undeclared key is scalar-ish last-writer-wins over an optional value (an undeclared
// key present on one side is retained, never dropped). Output order is deterministic:
// base-order keys first, then ours-only, then theirs-only.
func mergeCustom(res, base, ours, theirs *frontmatter.Model, oursRaw, theirsRaw []byte, schema *scopeconfig.Schema, meta MergeMeta) {
	baseC, oursC, theirsC := customMap(base), customMap(ours), customMap(theirs)
	for _, k := range orderedCustomKeys(base, ours, theirs) {
		field, declared := schema.Field(k)
		if declared && field.Type == scopeconfig.FieldStrings {
			merged := mergeList(toStrings(baseC[k]), toStrings(oursC[k]), toStrings(theirsC[k]))
			if len(merged) > 0 {
				res.Custom = append(res.Custom, frontmatter.Field{Key: k, Value: toAnyList(merged)})
			}
			continue
		}
		if val, present := mergeScalarOpt(baseC[k], oursC[k], theirsC[k], oursRaw, theirsRaw, meta); present {
			res.Custom = append(res.Custom, frontmatter.Field{Key: k, Value: val})
		}
	}
}

// mergeScalarOpt is mergeScalar over optional values: a side is characterised by its
// (present, value) state, and an absent key on one side is a legitimate state that can
// win LWW (dropping the key).
func mergeScalarOpt(base, ours, theirs optAny, oursRaw, theirsRaw []byte, meta MergeMeta) (any, bool) {
	sb, so, st := stateKey(base), stateKey(ours), stateKey(theirs)
	switch {
	case so == st:
		return ours.value, ours.present
	case so == sb:
		return theirs.value, theirs.present
	case st == sb:
		return ours.value, ours.present
	default:
		if pickOurs(meta, oursRaw, theirsRaw) {
			return ours.value, ours.present
		}
		return theirs.value, theirs.present
	}
}

// pickOurs reports whether ours wins a both-sides scalar tie: later author date wins,
// and equal author dates fall to the greater SHA-256 of the whole stage bytes — never a
// machine-local bias, and independent of which physical side the driver labelled ours.
func pickOurs(meta MergeMeta, oursRaw, theirsRaw []byte) bool {
	if meta.OursDate.After(meta.TheirsDate) {
		return true
	}
	if meta.TheirsDate.After(meta.OursDate) {
		return false
	}
	return bytes.Compare(sha(oursRaw), sha(theirsRaw)) >= 0
}

func customMap(m *frontmatter.Model) map[string]optAny {
	out := make(map[string]optAny, len(m.Custom))
	for _, f := range m.Custom {
		out[f.Key] = optAny{present: true, value: f.Value}
	}
	return out
}

func orderedCustomKeys(base, ours, theirs *frontmatter.Model) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []*frontmatter.Model{base, ours, theirs} {
		for _, f := range m.Custom {
			if !seen[f.Key] {
				seen[f.Key] = true
				keys = append(keys, f.Key)
			}
		}
	}
	return keys
}

func toStrings(o optAny) []string {
	if !o.present {
		return nil
	}
	switch v := o.value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

func toAnyList(list []string) []any {
	out := make([]any, len(list))
	for i, e := range list {
		out[i] = e
	}
	return out
}

func stateKey(o optAny) string {
	if !o.present {
		return "\x00"
	}
	return "\x01" + fmt.Sprint(o.value)
}

func sortedPair(a, b string) []string {
	if a <= b {
		return []string{a, b}
	}
	return []string{b, a}
}

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func toSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, e := range list {
		out[e] = true
	}
	return out
}

func sha(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
