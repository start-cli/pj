// Package scopeadmin orchestrates the scope-administration verbs — init, import,
// rebind, forget, and list — over the machine-local registry. It owns the
// registration checks (§17: name collision, code-root collision, dir
// disjointness, autoCommit consistency per derived git-root) and runs each
// mutating verb as one critical section under the machine-global flock: acquire,
// load the registry, validate against that just-loaded state, write, release.
// Holding the lock across load-validate-write is what makes the invariants
// concurrency-safe against a second init/import that would otherwise pass a check
// the first's write invalidates.
//
// All paths reach these functions already cleaned, absolute, and symlink-resolved
// to their canonical location — the CLI canonicalises input at the edge — so this
// package is free of ambient cwd state and its path comparisons (code-root
// containment, collision, dir disjointness) hold even on symlinked trees, where
// git's derived repo root would otherwise use a different spelling.
package scopeadmin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"github.com/start-cli/pj/internal/atomicfile"
	"github.com/start-cli/pj/internal/gitroot"
	"github.com/start-cli/pj/internal/pathutil"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/resolve"
	"github.com/start-cli/pj/internal/scope"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/token"
	"github.com/start-cli/pj/internal/xdg"
)

// Admin performs scope administration against a fixed XDG config directory.
type Admin struct {
	ctx       *cue.Context
	store     *registry.Store
	configDir string
}

// New builds an Admin over configDir using the process-wide CUE context.
func New(ctx *cue.Context, configDir string) *Admin {
	return &Admin{ctx: ctx, store: registry.NewStore(ctx, configDir), configDir: configDir}
}

// InitParams are the resolved inputs to pj scope init. Dir is absolute; CodeRoot
// is absolute when CodeRootGiven. Exactly one of Name / AutoName is set (the CLI
// enforces the usage rule and the --name alphabet before calling).
type InitParams struct {
	Dir             string
	Name            string
	AutoName        bool
	CodeRoot        string
	CodeRootGiven   bool
	AutoCommit      bool
	AutoCommitGiven bool
}

// Init creates and registers a new scope: it authors a minimal valid pj.cue and
// a .gitignore covering .pj.lock, applies the code-root default matrix, resolves
// the name, runs the registration checks, and records the entry. It never
// prompts and never runs git. It returns the registered scope dir.
func (a *Admin) Init(p InitParams) (string, error) {
	if p.AutoName == (p.Name != "") {
		return "", fmt.Errorf("exactly one of --name or --auto-name is required")
	}

	// Pre-write guard, separate from the registered-scope checks: init authors a
	// fresh scope, so an existing pj.cue means adopt-not-author.
	if _, err := os.Stat(filepath.Join(p.Dir, "pj.cue")); err == nil {
		return "", fmt.Errorf("%s already contains a pj.cue — that scope already exists on disk; adopt it with pj scope import %s, or choose a different dir", p.Dir, p.Dir)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", filepath.Join(p.Dir, "pj.cue"), err)
	}

	// Derive the git-root without creating the dir so a failed init — a collision
	// or a consensus conflict — leaves no directory behind. The dir is created
	// only after every check passes, just before its files are written.
	gitRoot, inRepo := gitroot.RepoRootForNew(p.Dir)
	codeRoot, err := resolveCodeRoot(p.Dir, p.CodeRoot, p.CodeRootGiven, gitRoot, inRepo)
	if err != nil {
		return "", err
	}

	name := p.Name
	if p.AutoName {
		name, err = scope.AutoName(filepath.Base(codeRoot))
		if err != nil {
			return "", fmt.Errorf("--auto-name: %w", err)
		}
	}

	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return "", err
	}

	if err := checkNameCollision(reg, name, p.AutoName); err != nil {
		return "", err
	}
	if err := checkCodeRootCollision(reg, codeRoot, ""); err != nil {
		return "", err
	}
	if err := checkDirDisjoint(reg, p.Dir, ""); err != nil {
		return "", err
	}
	cons, err := siblingConsensus(a, reg, gitRoot, inRepo, "")
	if err != nil {
		return "", err
	}
	autoCommit, err := resolveInitAutoCommit(cons, p.AutoCommitGiven, p.AutoCommit)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create scope dir %s: %w", p.Dir, err)
	}
	if err := scopeconfig.WriteMinimal(p.Dir, name, autoCommit); err != nil {
		return "", err
	}
	if err := ensureGitignore(p.Dir); err != nil {
		return "", err
	}

	reg.Scopes[name] = registry.Entry{Dir: p.Dir, Root: codeRoot}
	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		// The scope's files are written but registration failed: a complete,
		// valid pj.cue now sits on disk, so the recovery is to adopt it rather
		// than re-run init (which would refuse a dir that already holds a pj.cue).
		return "", fmt.Errorf("wrote the scope files at %s but failed to register it — adopt the on-disk scope with pj scope import %s: %w", p.Dir, p.Dir, err)
	}
	return p.Dir, nil
}

// ImportParams are the resolved inputs to pj scope import. Dir is absolute;
// CodeRoot is absolute when CodeRootGiven. Name and autoCommit come from the
// on-disk pj.cue, so import takes neither.
type ImportParams struct {
	Dir           string
	CodeRoot      string
	CodeRootGiven bool
}

// Import registers an existing on-disk scope, files in place. Name and autoCommit
// come from its pj.cue; an unusable pj.cue is a hard fail (config_unparseable)
// rather than admitting a scope whose schema is born read-only. It returns the
// registered scope dir.
func (a *Admin) Import(p ImportParams) (string, error) {
	schema, err := scopeconfig.Load(a.ctx, p.Dir)
	if err != nil {
		if ce, ok := scopeconfig.AsConfigError(err); ok {
			return "", fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
				fmt.Sprintf("cannot import %s: %s", p.Dir, ce.Reason)))
		}
		return "", err
	}

	gitRoot, inRepo := gitroot.RepoRoot(p.Dir)
	codeRoot, err := resolveCodeRoot(p.Dir, p.CodeRoot, p.CodeRootGiven, gitRoot, inRepo)
	if err != nil {
		return "", err
	}

	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return "", err
	}

	if err := checkNameCollision(reg, schema.Name, false); err != nil {
		return "", err
	}
	if err := checkCodeRootCollision(reg, codeRoot, ""); err != nil {
		return "", err
	}
	if err := checkDirDisjoint(reg, p.Dir, ""); err != nil {
		return "", err
	}
	cons, err := siblingConsensus(a, reg, gitRoot, inRepo, "")
	if err != nil {
		return "", err
	}
	if cons.found && cons.value != schema.AutoCommit {
		return "", autoCommitMismatch(cons.gitRoot, cons.value, schema.AutoCommit)
	}

	reg.Scopes[schema.Name] = registry.Entry{Dir: p.Dir, Root: codeRoot}
	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		return "", err
	}
	return p.Dir, nil
}

// RebindParams are the resolved inputs to pj scope rebind. Dir is absolute and
// always updates the entry's dir; Name selects the entry; CodeRoot updates root
// only when CodeRootGiven.
type RebindParams struct {
	Dir           string
	Name          string
	CodeRoot      string
	CodeRootGiven bool
}

// Rebind rewrites the machine-local registry paths for an already-registered
// scope. It updates dir always and root only when --code-root is given (a dir-
// only move leaves root untouched — it does not re-run the init defaults). It
// validates the post-rebind paths, preserves the lens (same registry key), and
// is idempotent. It is not name repair. changed reports whether anything moved.
func (a *Admin) Rebind(p RebindParams) (dir string, changed bool, err error) {
	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return "", false, err
	}

	entry, ok := reg.Scopes[p.Name]
	if !ok {
		return "", false, fmt.Errorf("unknown scope %q — nothing to rebind; register it first with pj scope init or import", p.Name)
	}

	newRoot := entry.Root
	if p.CodeRootGiven {
		newRoot = p.CodeRoot
	}

	// The new dir must open and hold a pj.cue whose name equals --name and the
	// registry key, or we would half-bind a wrong tree. Rebind is not name repair,
	// so a mismatch is refused rather than absorbed.
	pjName, err := scopeconfig.ReadName(a.ctx, p.Dir)
	if err != nil {
		return "", false, fmt.Errorf("cannot rebind %q to %s: %w", p.Name, p.Dir, err)
	}
	if pjName != p.Name {
		return "", false, fmt.Errorf("refusing to rebind %q to %s: its pj.cue name is %q, not %q — rebind moves paths, it does not repair a name", p.Name, p.Dir, pjName, p.Name)
	}

	if p.CodeRootGiven {
		if err := checkCodeRootCollision(reg, newRoot, p.Name); err != nil {
			return "", false, err
		}
	}
	if err := checkDirDisjoint(reg, p.Dir, p.Name); err != nil {
		return "", false, err
	}

	if entry.Dir == p.Dir && entry.Root == newRoot {
		return p.Dir, false, nil
	}

	reg.Scopes[p.Name] = registry.Entry{Dir: p.Dir, Root: newRoot}
	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		return "", false, err
	}
	return p.Dir, true, nil
}

// Forget unregisters a scope: it drops the scope's registry and lens entries and
// never touches its files or repo. In P2 it drops registry and lens only —
// dropping index rows is P3, once an index exists. A merely unreachable dir stays
// registered until forget.
func (a *Admin) Forget(name string) error {
	lock, err := xdg.AcquireConfigLock(a.configDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	reg, err := a.store.Load()
	if err != nil {
		return err
	}
	if _, ok := reg.Scopes[name]; !ok {
		return fmt.Errorf("unknown scope %q — nothing to forget", name)
	}

	delete(reg.Scopes, name)
	hadLens := false
	if _, ok := reg.Lens[name]; ok {
		delete(reg.Lens, name)
		hadLens = true
	}

	if err := a.store.WriteRegistry(reg.Scopes); err != nil {
		return err
	}
	if hadLens {
		if err := a.store.WriteLens(reg.Lens); err != nil {
			return err
		}
	}
	return nil
}

// ListRow is one registered scope's parse-stable listing fields.
type ListRow struct {
	Name string
	Dir  string
	Root string
	Mode string
}

// Mode labels for pj scope list.
const (
	ModePjDriven   = "pj-driven"
	ModeRepoDriven = "repo-driven"
	ModePlainFiles = "plain-files"
	ModeUnknown    = "unknown"
)

// Listing is the result of pj scope list: the TSV rows (sorted by name) plus the
// soft diagnostics that ride stderr without wrapping or interleaving the TSV.
type Listing struct {
	Rows        []ListRow
	Diagnostics []string
}

// List enumerates every registered scope, sorted by name ascending. Name, Dir,
// and Root are pure registry reads; Mode stats each dir and derives its git-root
// so one bad scope never fails the listing. Drift, unreachable dirs, and
// unparseable configs surface as soft diagnostics rather than errors.
func (a *Admin) List() (*Listing, error) {
	reg, err := a.store.Load()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(reg.Scopes))
	for name := range reg.Scopes {
		names = append(names, name)
	}
	sort.Strings(names)

	out := &Listing{}
	for _, name := range names {
		entry := reg.Scopes[name]
		row := ListRow{Name: name, Dir: filepath.Clean(entry.Dir), Root: filepath.Clean(entry.Root)}

		if fi, err := os.Stat(entry.Dir); err != nil || !fi.IsDir() {
			row.Mode = ModeUnknown
			out.Diagnostics = append(out.Diagnostics, token.Line(token.UnreachableScope,
				fmt.Sprintf("%s: dir %s is gone", name, entry.Dir)))
			out.Rows = append(out.Rows, row)
			continue
		}

		// Derive the git-root once and reuse it for both the mode label and the
		// drift recovery suggestion, and compile pj.cue once on the healthy path.
		gitRoot, inRepo := gitroot.RepoRoot(entry.Dir)
		schema, cfgErr := scopeconfig.Load(a.ctx, entry.Dir)

		// The authoritative name is the compiled schema's on the healthy path; when
		// the config is unusable, recover a legal name from ReadName so a config that
		// compiles under a legal name but fails schema validation still drifts.
		driftName := ""
		if cfgErr == nil {
			driftName = schema.Name
		} else if pjName, nameErr := scopeconfig.ReadName(a.ctx, entry.Dir); nameErr == nil {
			driftName = pjName
		}
		if driftName != "" && driftName != name {
			out.Diagnostics = append(out.Diagnostics,
				resolve.DriftLine(name, driftName, entry.Dir,
					resolve.SuggestCodeRootWith(entry.Dir, entry.Root, gitRoot, inRepo)))
		}

		if cfgErr != nil {
			row.Mode = ModeUnknown
			if ce, ok := scopeconfig.AsConfigError(cfgErr); ok {
				out.Diagnostics = append(out.Diagnostics, token.Line(token.ConfigUnparseable,
					fmt.Sprintf("%s: %s", name, ce.Reason)))
			} else {
				out.Diagnostics = append(out.Diagnostics, token.Line(token.ConfigUnparseable,
					fmt.Sprintf("%s: %v", name, cfgErr)))
			}
			out.Rows = append(out.Rows, row)
			continue
		}

		row.Mode = deriveMode(schema.AutoCommit, inRepo)
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// deriveMode maps autoCommit and git-root presence to the closed mode label.
func deriveMode(autoCommit, inRepo bool) string {
	if autoCommit {
		return ModePjDriven
	}
	if inRepo {
		return ModeRepoDriven
	}
	return ModePlainFiles
}

// resolveCodeRoot applies the init/import code-root default matrix. An explicit
// code-root inside a git repo must resolve within that same repo; a path outside
// it is a hard error teaching the fix.
func resolveCodeRoot(dir, codeRoot string, given bool, gitRoot string, inRepo bool) (string, error) {
	if given {
		if inRepo && !pathutil.UnderOrEqual(codeRoot, gitRoot) {
			return "", fmt.Errorf("--code-root %s is not inside the git repository %s that holds the dir — a code-root is where the scope is ambient; keep it inside the repo, or omit it to default to the repo root", codeRoot, gitRoot)
		}
		return codeRoot, nil
	}
	if inRepo {
		return gitRoot, nil
	}
	return dir, nil
}

// ensureGitignore makes sure <dir>/.gitignore ignores the .pj.lock file, creating
// or appending as needed without disturbing existing entries.
func ensureGitignore(dir string) error {
	p := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", p, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == ".pj.lock" {
			return nil
		}
	}
	buf := existing
	if len(buf) > 0 && !bytes.HasSuffix(buf, []byte("\n")) {
		buf = append(buf, '\n')
	}
	buf = append(buf, []byte(".pj.lock\n")...)
	return atomicfile.Write(p, buf, 0o644)
}
