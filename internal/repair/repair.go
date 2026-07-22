// Package repair holds pj's deterministic, bit-identical integrity-repair
// procedures — the id-collision loser pick and short-id extension, the equal-order
// and over-long-order re-space, and the archive-layout move. They are factored here,
// off the CLI, because both pj doctor --repair (P5) and the pj sync integrity step
// (P6) drive the same procedures, and the same-id add/add case in P6's merge handler
// reuses the same loser pick. Determinism is load-bearing: no crypto/rand, no dirent
// order, no mtime or pointer identity enters a decision, so two machines repairing
// the same quiescent collision produce identical renames and rewrites.
//
// Each procedure returns rewrite.Op values for the rewrite durability engine to
// apply; this package reads files (for the SHA-256 tie-break and to preserve a
// project's body and custom frontmatter across an id/order rewrite) but never writes
// them — durability and commit ordering belong to the caller.
package repair

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/order"
	"github.com/start-cli/pj/internal/rewrite"
)

// OrderLongThreshold is the soft length above which an order key is pathologically
// long and eligible for the --re-space-order band re-space (design: Metadata).
const OrderLongThreshold = 64

// Row is the minimal projection of an indexed project a repair procedure needs. The
// CLI adapts index rows into these so the procedures stay decoupled from the index
// schema and reusable by P6's sync integrity step.
type Row struct {
	Path       string
	FullID     string
	ShortID    string
	OrderKey   string
	ParseError bool
}

// Rename records one collision-loser rename for the operation's report and its
// commit message. The kept side is never renamed and retains the original id.
type Rename struct {
	OldID   string
	NewID   string
	OldPath string
	NewPath string
}

// member is one file in a same-id collision, carrying the fields the deterministic
// loser pick consults: the created instant (degraded when absent/empty/non-RFC3339),
// the basename, and the raw bytes for the SHA-256 tie-break.
type member struct {
	path     string
	basename string
	created  string
	shortID  string
	raw      []byte
	model    *frontmatter.Model
	body     []byte
}

// DuplicateID builds the ops that resolve one duplicate-id collision: it keeps the
// deterministically chosen member and renames every other by the short-id extension,
// rewriting the loser's frontmatter id and filename while leaving its depends/related
// edges untouched (the kept side retains the id, so nothing dangles). occupied is the
// set of short-ids already present in the scope; it is extended with each minted id
// so multiple losers — here or in a later collision in the same scope — never clash.
// members must all share the collided full id and must be parseable (the caller skips
// a collision that involves a parse_error member, whose frontmatter cannot be safely
// rewritten). Each member's Path is absolute, so no dir is needed.
//
// occupied maps every short-id present in the scope to the file holding it. It is
// caller-owned and extended with each mint so multiple losers never clash, and its paths
// let re-entry recognise a loser a crashed prior run already extended.
func DuplicateID(scope string, rows []Row, occupied map[string]string) ([]rewrite.Op, []Rename, error) {
	if len(rows) < 2 {
		return nil, nil, fmt.Errorf("duplicate-id repair needs at least two members, got %d", len(rows))
	}
	members := make([]member, 0, len(rows))
	for _, r := range rows {
		m, err := readMember(r)
		if err != nil {
			return nil, nil, err
		}
		members = append(members, m)
	}
	// Deterministic total order: the kept member sorts first, the losers follow.
	sort.SliceStable(members, func(i, j int) bool { return keepBefore(members[i], members[j]) })

	taken := make(map[string]struct{}, len(occupied))
	for short := range occupied {
		taken[short] = struct{}{}
	}

	var ops []rewrite.Op
	var renames []Rename
	for _, loser := range members[1:] {
		op, rn, resumed, err := resumeExtension(loser, scope, occupied)
		if err != nil {
			return nil, nil, err
		}
		if resumed {
			ops = append(ops, op)
			renames = append(renames, rn)
			continue
		}
		newShort, err := id.Extend(loser.shortID, taken)
		if err != nil {
			return nil, nil, fmt.Errorf("collision repair for %s: %w (files %s)", loser.model.ID, err, membersPaths(members))
		}
		taken[newShort] = struct{}{}
		newID := scope + "-" + newShort
		newPath := filepath.Join(filepath.Dir(loser.path), Basename(loser.basename, newID))

		m := *loser.model
		m.ID = newID
		content, err := serialize(&m, loser.body)
		if err != nil {
			return nil, nil, err
		}
		occupied[newShort] = newPath
		ops = append(ops, rewrite.Op{OldPath: loser.path, NewPath: newPath, Content: content})
		renames = append(renames, Rename{OldID: loser.model.ID, NewID: newID, OldPath: loser.path, NewPath: newPath})
	}
	return ops, renames, nil
}

// resumeExtension recognises a loser that a crashed prior run already extended: its
// content is present under a short-id extending its own, but the stale old-id file was
// never removed. The returned op finishes that move — the same bytes at the same path,
// then the removal — so re-entry never mints a second extension and never lands one
// project under two ids. Recognition is by content modulo the id, because the extension
// rewrites the frontmatter id and so cannot be byte-identical to the old copy.
func resumeExtension(loser member, scope string, occupied map[string]string) (rewrite.Op, Rename, bool, error) {
	var candidates []string
	for short := range occupied {
		if len(short) > len(loser.shortID) && strings.HasPrefix(short, loser.shortID) {
			candidates = append(candidates, short)
		}
	}
	sort.Strings(candidates)

	for _, short := range candidates {
		newID := scope + "-" + short
		m := *loser.model
		m.ID = newID
		content, err := serialize(&m, loser.body)
		if err != nil {
			return rewrite.Op{}, Rename{}, false, err
		}
		path := occupied[short]
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return rewrite.Op{}, Rename{}, false, fmt.Errorf("read %s: %w", path, err)
		}
		if !bytes.Equal(raw, content) {
			continue
		}
		return rewrite.Op{OldPath: loser.path, NewPath: path, Content: content},
			Rename{OldID: loser.model.ID, NewID: newID, OldPath: loser.path, NewPath: path}, true, nil
	}
	return rewrite.Op{}, Rename{}, false, nil
}

// EqualOrder builds the ops that re-space equal (tied) order keys within a scope,
// rewriting only the tied files and preserving the pre-repair (order, id) relative
// order among them and against their untied neighbours. rows is the whole scope's
// project set; untied rows are the fixed anchors between which new keys are minted.
func EqualOrder(rows []Row) ([]rewrite.Op, error) {
	valid := validOrderRows(rows)
	counts := map[string]int{}
	for _, r := range valid {
		counts[r.OrderKey]++
	}
	return respace(valid, func(r Row) bool { return counts[r.OrderKey] > 1 })
}

// LongOrder builds the ops that re-space a band of pathologically long order keys
// (length > OrderLongThreshold) into shorter legal keys, preserving order. This is
// the --re-space-order procedure only; it is never part of --repair.
func LongOrder(rows []Row) ([]rewrite.Op, error) {
	valid := validOrderRows(rows)
	return respace(valid, func(r Row) bool { return len(r.OrderKey) > OrderLongThreshold })
}

// ArchiveMove builds the op that relocates one project file across the archive
// boundary to match its terminal-ness: under <dir>/archive/ when terminal, at the
// dir root otherwise. It is a pure file move — the frontmatter is unchanged, so
// comments and custom fields are preserved byte-for-byte.
func ArchiveMove(dir string, row Row, terminal bool) (rewrite.Op, error) {
	raw, err := os.ReadFile(row.Path)
	if err != nil {
		return rewrite.Op{}, fmt.Errorf("read %s: %w", row.Path, err)
	}
	base := filepath.Base(row.Path)
	newPath := filepath.Join(dir, base)
	if terminal {
		newPath = filepath.Join(dir, "archive", base)
	}
	return rewrite.Op{OldPath: row.Path, NewPath: newPath, Content: raw}, nil
}

// InterruptedMove reports whether a same-id member set is the both-present window of an
// interrupted archive-layout move rather than a genuine duplicate-id collision: two
// byte-identical copies of one project, one at the dir root and one under archive/.
// Write-new-then-remove-old is the only way that state arises, since hand-moving a
// project file across the archive boundary is forbidden, so the layout repair must
// complete the move. Extending a short-id here would fork one project into two ids and
// lose the move irrecoverably.
func InterruptedMove(dir string, rows []Row) (bool, error) {
	archiveDir := filepath.Join(dir, "archive")
	var atRoot, archived []Row
	for _, r := range rows {
		switch filepath.Dir(r.Path) {
		case dir:
			atRoot = append(atRoot, r)
		case archiveDir:
			archived = append(archived, r)
		}
	}
	for _, a := range atRoot {
		for _, b := range archived {
			same, err := sameContent(a.Path, b.Path)
			if err != nil {
				return false, err
			}
			if same {
				return true, nil
			}
		}
	}
	return false, nil
}

func sameContent(a, b string) (bool, error) {
	ra, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", a, err)
	}
	rb, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", b, err)
	}
	return bytes.Equal(ra, rb), nil
}

// respace assigns distinct legal keys to every row for which needsRewrite is true,
// sweeping the (order, id)-sorted set left to right and minting each new key strictly
// between the running previous key and the nearest untied anchor to the right. A row
// whose computed key equals its current key is left untouched (no redundant write).
func respace(valid []Row, needsRewrite func(Row) bool) ([]rewrite.Op, error) {
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].OrderKey != valid[j].OrderKey {
			return valid[i].OrderKey < valid[j].OrderKey
		}
		return valid[i].FullID < valid[j].FullID
	})
	rewriteAt := make([]bool, len(valid))
	for i, r := range valid {
		rewriteAt[i] = needsRewrite(r)
	}

	var ops []rewrite.Op
	prev := ""
	for i, r := range valid {
		if !rewriteAt[i] {
			prev = r.OrderKey
			continue
		}
		right := ""
		for j := i + 1; j < len(valid); j++ {
			if !rewriteAt[j] {
				right = valid[j].OrderKey
				break
			}
		}
		newKey, err := order.KeyBetween(prev, right)
		if err != nil {
			return nil, fmt.Errorf("re-space order for %s between %q and %q: %w", r.FullID, prev, right, err)
		}
		prev = newKey
		if newKey == r.OrderKey {
			continue
		}
		op, err := orderRewriteOp(r.Path, newKey)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// orderRewriteOp reads a project file, rewrites only its order key, and returns an
// in-place rewrite op preserving the body.
func orderRewriteOp(path, newKey string) (rewrite.Op, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return rewrite.Op{}, fmt.Errorf("read %s: %w", path, err)
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return rewrite.Op{}, fmt.Errorf("%s has no frontmatter fence", path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return rewrite.Op{}, fmt.Errorf("parse %s: %w", path, err)
	}
	m.Order = newKey
	content, err := serialize(m, body)
	if err != nil {
		return rewrite.Op{}, err
	}
	return rewrite.Op{OldPath: path, NewPath: path, Content: content}, nil
}

// readMember reads one collision member's file and parses its frontmatter for the
// loser pick and the id rewrite. A parse failure is an error — the caller must have
// filtered parse_error members out before calling.
func readMember(r Row) (member, error) {
	raw, err := os.ReadFile(r.Path)
	if err != nil {
		return member{}, fmt.Errorf("read %s: %w", r.Path, err)
	}
	interior, body, present := frontmatter.Split(raw)
	if !present {
		return member{}, fmt.Errorf("%s has no frontmatter fence", r.Path)
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return member{}, fmt.Errorf("parse %s: %w", r.Path, err)
	}
	return member{
		path:     r.Path,
		basename: filepath.Base(r.Path),
		created:  m.Created,
		shortID:  r.ShortID,
		raw:      raw,
		model:    m,
		body:     body,
	}, nil
}

// keepBefore reports whether member a should be kept over b (a sorts first, b is a
// rename candidate). The order is the design's closed total pre-order: keep the older
// by created — a degraded created (absent, empty, or not a strict RFC3339 instant,
// date-only included) is not-newer-than-any, so it sorts oldest and is kept — then
// the lexicographically smaller basename, then the smaller SHA-256 of raw bytes, then
// the smaller path as a final total-order guarantee. It never lenient-parses a
// degraded created into an instant, so no timezone or DST variance leaks onto the
// bit-identical rename.
func keepBefore(a, b member) bool {
	ta, aok := parseInstant(a.created)
	tb, bok := parseInstant(b.created)
	if aok != bok {
		// The degraded side (not ok) is not-newer-than-any: it sorts first (kept).
		return !aok
	}
	if aok && !ta.Equal(tb) {
		return ta.Before(tb)
	}
	if a.basename != b.basename {
		return a.basename < b.basename
	}
	if c := bytes.Compare(sha(a.raw), sha(b.raw)); c != 0 {
		return c < 0
	}
	return a.path < b.path
}

// parseInstant strictly parses an RFC3339 timestamp. ok is false for a degraded
// value — absent, empty, or any string RFC3339 rejects (date-only included) — which
// the loser pick treats as not-newer-than-any rather than coercing to an instant.
func parseInstant(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func sha(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// Basename is the project filename for newID that preserves base's frozen slug — the
// run after the leading "<scope>-<short-id>" of the existing name. A file named
// "<id>.md" with no slug maps to "<newID>.md"; "<id>-<slug>.md" maps to
// "<newID>-<slug>.md".
//
// It never consults the old id, because the filename and the frontmatter id can
// legitimately disagree (doctor reports that as a structural class) and every rewrite
// that renames a project must still produce a well-formed name rather than one built
// out of the disagreement. Scope names and short-ids contain no hyphen, so the first
// two segments are exactly the id and the remainder is exactly the slug.
func Basename(base, newID string) string {
	stem := strings.TrimSuffix(base, ".md")
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 3 || parts[2] == "" {
		return newID + ".md"
	}
	return newID + "-" + parts[2] + ".md"
}

func serialize(m *frontmatter.Model, body []byte) ([]byte, error) {
	interior, err := frontmatter.Serialize(m)
	if err != nil {
		return nil, err
	}
	return frontmatter.Compose(interior, body), nil
}

func validOrderRows(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if !r.ParseError && order.Valid(r.OrderKey) {
			out = append(out, r)
		}
	}
	return out
}

func membersPaths(members []member) string {
	paths := make([]string, len(members))
	for i, m := range members {
		paths[i] = m.path
	}
	return strings.Join(paths, ", ")
}
