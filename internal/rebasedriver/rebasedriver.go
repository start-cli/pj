// Package rebasedriver resolves one conflicted project .md at a paused rebase. It is a
// unit called per conflicted path by pj sync's fetch-and-integrate loop (P6b): it does
// not run rebases, does not decide whether to continue one, and does not know how many
// stops a rebase has. It enumerates and loads the path's git stages, re-evaluates the
// scope schema from on-disk pj.cue, derives each side's per-file author date, calls the
// pure frontmatter merge (internal/fmmerge) on the whole stage blobs, 3-way text-merges
// the bodies separately, composes a clean file, and either stages the path or
// deliberately leaves it unstaged for the caller to surface.
//
// Two return channels are kept strictly apart. A data condition a human resolves
// in-file — a body conflict, a status dispute, a delete/edit, or a fail-closed merge —
// comes back as an Outcome with the path left unstaged and the details the class must
// report. An operational fault — git gone, a corrupt object, a failed stage read — is an
// ordinary error return, so P6b aborts the integrate on one and parks a single file on
// the other. This package takes no lock: it runs inside a lock span P6b owns.
package rebasedriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/start-cli/pj/internal/atomicfile"
	"github.com/start-cli/pj/internal/fmmerge"
	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/git"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/repair"
	"github.com/start-cli/pj/internal/rewrite"
	"github.com/start-cli/pj/internal/scopeconfig"
)

// fileMode is the ordinary, non-executable mode composed project files are written with.
const fileMode = 0o644

// SchemaLoader returns a scope's evaluated schema from its pj.cue as it stands on disk
// at call time. The driver calls it per conflicted file so the field merge is always
// typed from the current on-disk schema — never one captured before the fetch, which by
// construction cannot know a fields/statuses declaration the incoming commit just added.
// P6b supplies the reconcile-cache-backed loader (cheap when the import closure is
// unchanged); it must never return a schema snapshot captured earlier in the run.
type SchemaLoader func(scopeDir string) (*scopeconfig.Schema, error)

// Driver resolves conflicted project files across one rebase. Its per-rebase state — the
// short-ids it has minted so far for add/add renames — lives on the receiver, not a
// per-call local, so a later conflicted file's occupied-id set sees the ids earlier
// files minted and no extension collides with a project already present.
type Driver struct {
	gitRoot string
	load    SchemaLoader
	minted  map[string]struct{}
}

// New builds a Driver for one rebase in gitRoot, typing merges through load.
func New(gitRoot string, load SchemaLoader) *Driver {
	return &Driver{gitRoot: gitRoot, load: load, minted: map[string]struct{}{}}
}

// Conflict names one conflicted project .md and the two side revs P6b resolved for it.
// OursRev is the stage :2 side (HEAD, the upstream tip); TheirsRev is the stage :3 side
// (REBASE_HEAD, the commit being replayed). The mapping is inverted from the everyday
// during-rebase reading, so the driver pairs each rev with its stage number, never with
// "ours".
type Conflict struct {
	Path      string // repo-relative path to the conflicted project .md
	ScopeDir  string // absolute scope dir holding pj.cue
	OursRev   string // stage :2 side rev
	TheirsRev string // stage :3 side rev
}

// Class is the resolution class of a conflicted file, which the caller branches on.
type Class int

const (
	// ClassClean is a fully merged, parseable file the driver staged.
	ClassClean Class = iota
	// ClassBodyConflict is clean field-merged frontmatter with git markers confined to
	// the body; left unstaged.
	ClassBodyConflict
	// ClassStatusDispute is merge-base status plus status_conflict written; left unstaged.
	ClassStatusDispute
	// ClassDeleteEdit is a delete/edit handoff; nothing written, nothing staged.
	ClassDeleteEdit
	// ClassRename is a same-id add/add resolved into two staged files.
	ClassRename
	// ClassFailClosed is a fail-closed U21 merge; left unstaged, key named.
	ClassFailClosed
)

// Outcome is the driver's report for one conflicted file. Staged tells the caller
// whether the rebase can continue over this path without human action; the class-keyed
// fields carry what that class must report.
type Outcome struct {
	Path           string
	Class          Class
	Staged         bool
	Warnings       []string
	StatusConflict []string    // ClassStatusDispute: the disputed pair
	DeleteEdit     *DeleteEdit // ClassDeleteEdit
	Rename         *Rename     // ClassRename
	FailClosed     *FailClosed // ClassFailClosed
}

// DeleteEdit reports which side deleted the file and the surviving side's post-edit
// status, for the caller to surface. Deleted is the pure merge's side label (ours = the
// stage :2 / upstream side; theirs = the stage :3 / replayed side).
type DeleteEdit struct {
	Deleted         fmmerge.Side
	SurvivingStatus string
}

// Rename reports a repaired same-id add/add duplicate: the kept side stays at KeepPath
// with OldID, the loser is written to NewPath with NewID. P6b records the pair and runs
// the edge_verify: inbound-edge query for OldID at the right moment — the driver never
// emits it, because that is index work over rows the contested path does not hold.
type Rename struct {
	OldID    string
	NewID    string
	KeepPath string
	NewPath  string
}

// FailClosed reports a fail-closed U21 merge as a human-resolvable pause: the offending
// key (when one applies) and the reason, path left unstaged. It is distinct from the
// error return, which is reserved for operational faults.
type FailClosed struct {
	Key    string
	Reason string
}

// Resolve merges one conflicted project file and reports the outcome. The returned error
// is reserved for operational faults; every human-resolvable data condition is an
// Outcome with Staged=false.
func (d *Driver) Resolve(ctx context.Context, c Conflict) (Outcome, error) {
	stages, err := git.ConflictStages(ctx, d.gitRoot, c.Path)
	if err != nil {
		return Outcome{}, fmt.Errorf("enumerate conflict stages for %s: %w", c.Path, err)
	}
	if !stages.Any() {
		return Outcome{}, fmt.Errorf("%s has no conflict stages", c.Path)
	}

	base, err := d.readStage(ctx, stages.Base, 1, c.Path)
	if err != nil {
		return Outcome{}, err
	}
	ours, err := d.readStage(ctx, stages.Ours, 2, c.Path)
	if err != nil {
		return Outcome{}, err
	}
	theirs, err := d.readStage(ctx, stages.Theirs, 3, c.Path)
	if err != nil {
		return Outcome{}, err
	}

	schema, err := d.load(c.ScopeDir)
	if err != nil {
		return Outcome{}, fmt.Errorf("evaluate scope schema for %s: %w", c.Path, err)
	}

	var oursDate, theirsDate time.Time
	if stages.Ours {
		if oursDate, err = git.AuthorDate(ctx, d.gitRoot, c.OursRev, c.Path); err != nil {
			return Outcome{}, fmt.Errorf("author date (ours) for %s: %w", c.Path, err)
		}
	}
	if stages.Theirs {
		if theirsDate, err = git.AuthorDate(ctx, d.gitRoot, c.TheirsRev, c.Path); err != nil {
			return Outcome{}, fmt.Errorf("author date (theirs) for %s: %w", c.Path, err)
		}
	}

	occupied, err := d.occupiedShortIDs(ctx, c.ScopeDir, schema.Name)
	if err != nil {
		return Outcome{}, err
	}

	res, mErr := fmmerge.MergeFrontmatter(base, ours, theirs, schema, fmmerge.MergeMeta{
		OursDate:   oursDate,
		TheirsDate: theirsDate,
		Scope:      schema.Name,
		Occupied:   occupied,
	})
	if mErr != nil {
		var me *fmmerge.MergeError
		if errors.As(mErr, &me) {
			return Outcome{Path: c.Path, Class: ClassFailClosed, FailClosed: &FailClosed{Key: me.Key, Reason: me.Reason}}, nil
		}
		return Outcome{}, mErr
	}

	switch res.Outcome {
	case fmmerge.OutcomeMerged:
		return d.compose(ctx, c, base, ours, theirs, res, false)
	case fmmerge.OutcomeStatusConflict:
		return d.compose(ctx, c, base, ours, theirs, res, true)
	case fmmerge.OutcomeDeleteEdit:
		return Outcome{
			Path:       c.Path,
			Class:      ClassDeleteEdit,
			Warnings:   res.Warnings,
			DeleteEdit: &DeleteEdit{Deleted: res.DeleteEdit.Deleted, SurvivingStatus: res.DeleteEdit.SurvivingStatus},
		}, nil
	case fmmerge.OutcomeRename:
		return d.applyRename(ctx, c, ours, theirs, schema.Name, res)
	default:
		return Outcome{}, fmt.Errorf("unknown merge outcome %d for %s", res.Outcome, c.Path)
	}
}

// readStage loads one stage blob when present, or an absent Stage when not. It reads
// only stages ConflictStages reported, so a non-zero exit here is a genuine fault
// surfaced as an error — never reinterpreted as an absent stage, which would silently
// reclassify a broken run as a deletion.
func (d *Driver) readStage(ctx context.Context, present bool, stage int, path string) (fmmerge.Stage, error) {
	if !present {
		return fmmerge.Stage{}, nil
	}
	data, err := git.ShowStage(ctx, d.gitRoot, stage, path)
	if err != nil {
		return fmmerge.Stage{}, fmt.Errorf("read stage :%d: for %s: %w", stage, path, err)
	}
	return fmmerge.Stage{Present: true, Data: data}, nil
}

// compose builds the merged file from the stage blobs — never from the conflicted
// working-tree file git left, whose whole-file text merge places hunks by line proximity
// and can span the fence, leaving unparseable frontmatter. It writes the merge's clean
// YAML frontmatter and the separately 3-way-merged body. A clean body is staged so the
// rebase can continue; a body conflict or a status dispute is left unstaged.
func (d *Driver) compose(ctx context.Context, c Conflict, base, ours, theirs fmmerge.Stage, res fmmerge.Result, dispute bool) (Outcome, error) {
	_, baseBody, _ := frontmatter.Split(base.Data)
	_, oursBody, _ := frontmatter.Split(ours.Data)
	_, theirsBody, _ := frontmatter.Split(theirs.Data)
	mergedBody, bodyConflicted, err := git.MergeBlobs(ctx, baseBody, oursBody, theirsBody)
	if err != nil {
		return Outcome{}, fmt.Errorf("body merge for %s: %w", c.Path, err)
	}

	interior, err := frontmatter.Serialize(res.Model)
	if err != nil {
		return Outcome{}, fmt.Errorf("serialize merged frontmatter for %s: %w", c.Path, err)
	}
	content := frontmatter.Compose(interior, mergedBody)
	abs := filepath.Join(d.gitRoot, c.Path)
	if err := atomicfile.Write(abs, content, fileMode); err != nil {
		return Outcome{}, fmt.Errorf("write merged %s: %w", c.Path, err)
	}

	out := Outcome{Path: c.Path, Warnings: res.Warnings}
	switch {
	case dispute:
		out.Class = ClassStatusDispute
		out.StatusConflict = res.StatusConflict
	case bodyConflicted:
		out.Class = ClassBodyConflict
	default:
		if err := git.Add(ctx, d.gitRoot, []string{c.Path}); err != nil {
			return Outcome{}, fmt.Errorf("stage merged %s: %w", c.Path, err)
		}
		out.Class = ClassClean
		out.Staged = true
	}
	return out, nil
}

// applyRename repairs a same-id add/add: it composes both paths — the conflicted path is
// the keep path, the new path is that directory plus the loser's new id and its frozen
// slug — writes the kept side's clean blob and the loser's id-rewritten blob through P5's
// rewrite durability contract, stages both, and reports the pair. It never calls P5's
// disk-backed duplicate-id procedure, whose index rows and clean files are absent at this
// moment; the shared loser pick already produced the answer, so the driver only writes it.
func (d *Driver) applyRename(ctx context.Context, c Conflict, ours, theirs fmmerge.Stage, scope string, res fmmerge.Result) (Outcome, error) {
	keepBlob, loserBlob := ours.Data, theirs.Data
	if res.Rename.Loser == fmmerge.SideOurs {
		keepBlob, loserBlob = theirs.Data, ours.Data
	}
	newID := scope + "-" + res.Rename.NewShortID
	loserContent, err := rewriteID(loserBlob, newID)
	if err != nil {
		return Outcome{}, fmt.Errorf("rewrite loser id for %s: %w", c.Path, err)
	}

	abs := filepath.Join(d.gitRoot, c.Path)
	newPath := filepath.Join(filepath.Dir(abs), repair.Basename(filepath.Base(abs), newID))
	newRel, err := filepath.Rel(d.gitRoot, newPath)
	if err != nil {
		return Outcome{}, fmt.Errorf("relativise new path for %s: %w", c.Path, err)
	}

	if _, err := rewrite.Apply([]rewrite.Op{
		{OldPath: abs, NewPath: abs, Content: keepBlob},
		{NewPath: newPath, Content: loserContent},
	}); err != nil {
		return Outcome{}, fmt.Errorf("write add/add files for %s: %w", c.Path, err)
	}
	if err := git.Add(ctx, d.gitRoot, []string{c.Path, newRel}); err != nil {
		return Outcome{}, fmt.Errorf("stage add/add files for %s: %w", c.Path, err)
	}

	d.minted[res.Rename.NewShortID] = struct{}{}
	return Outcome{
		Path:   c.Path,
		Class:  ClassRename,
		Staged: true,
		Rename: &Rename{OldID: res.Rename.CollidedID, NewID: newID, KeepPath: c.Path, NewPath: newRel},
	}, nil
}

// occupiedShortIDs derives the short-ids taken in a scope for the add/add extension: the
// index-tracked project files under the scope dir (git ls-files) union the project files
// on disk under it (dir root and archive/), plus every id the driver has minted so far
// this rebase. No single snapshot serves it — a pre-fetch set is blind to incoming ids,
// and a once-mid-rebase set is blind to ids minted on later conflicted files — so it is
// re-derived per file and folds in the running minted set.
func (d *Driver) occupiedShortIDs(ctx context.Context, scopeDir, scope string) (map[string]struct{}, error) {
	occ := map[string]struct{}{}
	tracked, err := git.ListFiles(ctx, d.gitRoot, scopeDir)
	if err != nil {
		return nil, fmt.Errorf("list tracked files under %s: %w", scopeDir, err)
	}
	for _, f := range tracked {
		addShortID(occ, filepath.Base(f), scope)
	}
	for _, sub := range []string{scopeDir, filepath.Join(scopeDir, "archive")} {
		entries, err := os.ReadDir(sub)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan %s: %w", sub, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				addShortID(occ, e.Name(), scope)
			}
		}
	}
	for s := range d.minted {
		occ[s] = struct{}{}
	}
	return occ, nil
}

// addShortID records a project basename's short-id in occ when the basename is a project
// file for scope. The short-id is the second "-"-separated segment; scope names and
// short-ids carry no hyphen, so the split is unambiguous and any remainder is the slug.
func addShortID(occ map[string]struct{}, basename, scope string) {
	stem := strings.TrimSuffix(basename, ".md")
	if stem == basename {
		return // not a .md file
	}
	parts := strings.SplitN(stem, "-", 3)
	if len(parts) < 2 || parts[0] != scope || !id.IsShortID(parts[1]) {
		return
	}
	occ[parts[1]] = struct{}{}
}

// rewriteID re-serialises a stage blob with its frontmatter id replaced, preserving the
// body. It matches P5's loser rewrite so the two collision routes produce the same file.
func rewriteID(blob []byte, newID string) ([]byte, error) {
	interior, body, present := frontmatter.Split(blob)
	if !present {
		return nil, fmt.Errorf("loser blob has no frontmatter fence")
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return nil, fmt.Errorf("parse loser blob: %w", err)
	}
	m.ID = newID
	ni, err := frontmatter.Serialize(m)
	if err != nil {
		return nil, err
	}
	return frontmatter.Compose(ni, body), nil
}
