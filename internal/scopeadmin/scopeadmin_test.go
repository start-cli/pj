package scopeadmin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/token"
)

type harness struct {
	ctx       *cue.Context
	admin     *Admin
	configDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := cuecontext.New()
	cfg := t.TempDir()
	return &harness{ctx: ctx, admin: New(ctx, cfg), configDir: cfg}
}

func (h *harness) reg(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.NewStore(h.ctx, h.configDir).Load()
	if err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	return r
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

func TestInitPlainFiles(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "standalone")
	got, err := h.admin.Init(InitParams{Dir: dir, Name: "home"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got != dir {
		t.Errorf("registered dir = %q want %q", got, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "pj.cue")); err != nil {
		t.Errorf("pj.cue not written: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), ".pj.lock") {
		t.Errorf(".gitignore missing .pj.lock: %v %q", err, gi)
	}
	if h.reg(t).Scopes["home"].Root != dir {
		t.Errorf("plain-files root should default to dir")
	}
}

func TestInitRepoDefaultsAndAutoName(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "webctl")
	gitInit(t, repo)
	dir := filepath.Join(repo, ".agents", "pj")

	got, err := h.admin.Init(InitParams{Dir: dir, AutoName: true, AutoCommit: true, AutoCommitGiven: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	_ = got
	entry := h.reg(t).Scopes["we"] // webctl -> "we"
	if entry.Dir != dir {
		t.Fatalf("auto-name did not register 'we': %+v", h.reg(t).Scopes)
	}
	// Code-root defaults to the repo root, resolved for symlinked temp roots.
	wantRoot, _ := filepath.EvalSymlinks(repo)
	gotRoot, _ := filepath.EvalSymlinks(entry.Root)
	if gotRoot != wantRoot {
		t.Errorf("code-root = %q want repo root %q", gotRoot, wantRoot)
	}
}

func TestInitExactlyOneName(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "s")
	if _, err := h.admin.Init(InitParams{Dir: dir}); err == nil {
		t.Error("expected error when neither --name nor --auto-name is set")
	}
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "a", AutoName: true}); err == nil {
		t.Error("expected error when both --name and --auto-name are set")
	}
}

func TestInitPreexistingPjCue(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte("name: \"x\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.admin.Init(InitParams{Dir: dir, Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "import") {
		t.Fatalf("expected an error pointing at import, got %v", err)
	}
}

func TestInitCodeRootOutsideRepo(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	dir := filepath.Join(repo, ".agents", "pj")
	outside := filepath.Join(t.TempDir(), "elsewhere")
	_, err := h.admin.Init(InitParams{Dir: dir, Name: "x", CodeRoot: outside, CodeRootGiven: true})
	if err == nil || !strings.Contains(err.Error(), "not inside the git repository") {
		t.Fatalf("expected code-root-outside-repo error, got %v", err)
	}
}

func TestInitFailedLeavesNoStrayDir(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	// First scope claims the name "dup".
	if _, err := h.admin.Init(InitParams{Dir: filepath.Join(base, "one"), Name: "dup"}); err != nil {
		t.Fatal(err)
	}
	// A colliding init must fail and leave its target dir uncreated — the checks
	// run before any mkdir, so a rejected init litters nothing.
	target := filepath.Join(base, "two")
	if _, err := h.admin.Init(InitParams{Dir: target, Name: "dup"}); err == nil {
		t.Fatal("expected name collision")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("failed init must leave no dir; stat(%s) err = %v", target, err)
	}
}

func TestInitFreshDirInRepoDefaultsToRepoRoot(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)
	// A dir that does not yet exist inside the repo still derives the repo root as
	// its code-root default — the derivation must not depend on creating the dir.
	dir := filepath.Join(repo, ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "rr"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	wantRoot, _ := filepath.EvalSymlinks(repo)
	gotRoot, _ := filepath.EvalSymlinks(h.reg(t).Scopes["rr"].Root)
	if gotRoot != wantRoot {
		t.Errorf("code-root = %q want repo root %q", gotRoot, wantRoot)
	}
}

func TestInitOutsideRepoExplicitCodeRoot(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	// Dir outside any git repo plus an explicit --code-root resolves the code-root
	// to the given path (the outside-git branch of the default matrix).
	dir := filepath.Join(base, "scope")
	codeRoot := filepath.Join(base, "project")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "cr", CodeRoot: codeRoot, CodeRootGiven: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := h.reg(t).Scopes["cr"].Root; got != codeRoot {
		t.Errorf("outside-repo explicit code-root = %q want %q", got, codeRoot)
	}
}

func TestInitAutoCommitInheritAndContradict(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)

	a := filepath.Join(repo, "a", ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: a, Name: "aa", CodeRoot: filepath.Join(repo, "a"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatalf("first scope: %v", err)
	}

	// Sibling omitting the flag inherits autoCommit=true.
	b := filepath.Join(repo, "b", ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: b, Name: "bb", CodeRoot: filepath.Join(repo, "b"), CodeRootGiven: true}); err != nil {
		t.Fatalf("inherit sibling: %v", err)
	}
	if !mustAutoCommit(t, h.ctx, b) {
		t.Error("sibling should have inherited autoCommit=true")
	}

	// Sibling with a contradicting explicit flag errors with the token.
	c := filepath.Join(repo, "c", ".agents", "pj")
	_, err := h.admin.Init(InitParams{Dir: c, Name: "cc", CodeRoot: filepath.Join(repo, "c"), CodeRootGiven: true, AutoCommit: false, AutoCommitGiven: true})
	if err == nil || !strings.HasPrefix(err.Error(), token.AutoCommitMismatch) {
		t.Fatalf("expected auto_commit_mismatch, got %v", err)
	}
}

func TestInitCollisions(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	first := filepath.Join(base, "one")
	if _, err := h.admin.Init(InitParams{Dir: first, Name: "dup"}); err != nil {
		t.Fatal(err)
	}

	// Name collision.
	if _, err := h.admin.Init(InitParams{Dir: filepath.Join(base, "two"), Name: "dup"}); err == nil {
		t.Error("expected name collision")
	}
	// Code-root collision (same root as first, which is its dir).
	if _, err := h.admin.Init(InitParams{Dir: filepath.Join(base, "three"), Name: "cr", CodeRoot: first, CodeRootGiven: true}); err == nil {
		t.Error("expected code-root collision")
	}
	// Dir disjointness: nested under first's dir.
	nested := filepath.Join(first, "nested")
	if _, err := h.admin.Init(InitParams{Dir: nested, Name: "nd", CodeRoot: filepath.Join(base, "x"), CodeRootGiven: true}); err == nil {
		t.Error("expected dir disjointness rejection")
	}
}

func TestImportReadsConfigAndGuards(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte("name: \"im\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Import(ImportParams{Dir: dir}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, ok := h.reg(t).Scopes["im"]; !ok {
		t.Error("import did not register under pj.cue name")
	}

	// Unparseable pj.cue → refuse with config_unparseable.
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "pj.cue"), []byte("name: \"b\" broken:::"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.admin.Import(ImportParams{Dir: bad})
	if err == nil || !strings.HasPrefix(err.Error(), token.ConfigUnparseable) {
		t.Fatalf("expected config_unparseable on import, got %v", err)
	}
}

func TestImportAutoCommitMismatch(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)

	a := filepath.Join(repo, "a", ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: a, Name: "aa", CodeRoot: filepath.Join(repo, "a"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatal(err)
	}

	// Sibling on disk with autoCommit=false disagrees; import cannot inherit.
	b := filepath.Join(repo, "b", ".agents", "pj")
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "pj.cue"), []byte("name: \"bb\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.admin.Import(ImportParams{Dir: b, CodeRoot: filepath.Join(repo, "b"), CodeRootGiven: true})
	if err == nil || !strings.HasPrefix(err.Error(), token.AutoCommitMismatch) {
		t.Fatalf("expected auto_commit_mismatch, got %v", err)
	}
}

func TestSiblingConfigUnparseableRefusesRegistration(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	gitInit(t, repo)

	// A registered sibling with a broken pj.cue supplies no trustworthy autoCommit.
	a := filepath.Join(repo, "a", ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: a, Name: "aa", CodeRoot: filepath.Join(repo, "a"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "pj.cue"), []byte("name: \"aa\" broken:::"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := filepath.Join(repo, "b", ".agents", "pj")
	_, err := h.admin.Init(InitParams{Dir: b, Name: "bb", CodeRoot: filepath.Join(repo, "b"), CodeRootGiven: true, AutoCommit: true, AutoCommitGiven: true})
	if err == nil || !strings.HasPrefix(err.Error(), token.ConfigUnparseable) {
		t.Fatalf("expected config_unparseable naming the broken sibling, got %v", err)
	}
}

func TestRebind(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()
	orig := filepath.Join(base, "orig")
	if _, err := h.admin.Init(InitParams{Dir: orig, Name: "rb"}); err != nil {
		t.Fatal(err)
	}
	// Seed a lens to prove it survives rebind.
	store := registry.NewStore(h.ctx, h.configDir)
	if err := store.WriteLens(map[string][]string{"rb": {"tagx"}}); err != nil {
		t.Fatal(err)
	}

	// Move the dir; keep the same pj.cue (name rb). Root unchanged (no --code-root).
	moved := filepath.Join(base, "moved")
	if err := os.Rename(orig, moved); err != nil {
		t.Fatal(err)
	}
	dir, changed, err := h.admin.Rebind(RebindParams{Dir: moved, Name: "rb"})
	if err != nil || !changed || dir != moved {
		t.Fatalf("rebind: dir=%q changed=%v err=%v", dir, changed, err)
	}
	reg := h.reg(t)
	if reg.Scopes["rb"].Dir != moved {
		t.Errorf("dir not updated: %+v", reg.Scopes["rb"])
	}
	if reg.Scopes["rb"].Root != orig {
		t.Errorf("root should be unchanged on a dir-only move, got %q", reg.Scopes["rb"].Root)
	}
	if got := reg.Lens["rb"]; len(got) != 1 || got[0] != "tagx" {
		t.Errorf("lens not preserved: %v", got)
	}

	// Idempotent.
	if _, changed, err := h.admin.Rebind(RebindParams{Dir: moved, Name: "rb"}); err != nil || changed {
		t.Errorf("expected idempotent no-op, changed=%v err=%v", changed, err)
	}

	// Unknown name.
	if _, _, err := h.admin.Rebind(RebindParams{Dir: moved, Name: "ghost"}); err == nil {
		t.Error("expected unknown-name error")
	}

	// Wrong tree: pj.cue name != --name.
	wrong := t.TempDir()
	if err := os.WriteFile(filepath.Join(wrong, "pj.cue"), []byte("name: \"other\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.admin.Rebind(RebindParams{Dir: wrong, Name: "rb"}); err == nil {
		t.Error("expected wrong-tree refusal")
	}
}

func TestForget(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "f")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "fg"}); err != nil {
		t.Fatal(err)
	}
	store := registry.NewStore(h.ctx, h.configDir)
	if err := store.WriteLens(map[string][]string{"fg": {"t"}}); err != nil {
		t.Fatal(err)
	}

	if err := h.admin.Forget("fg"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	reg := h.reg(t)
	if _, ok := reg.Scopes["fg"]; ok {
		t.Error("scope still registered after forget")
	}
	if _, ok := reg.Lens["fg"]; ok {
		t.Error("lens still present after forget")
	}
	// Files untouched.
	if _, err := os.Stat(filepath.Join(dir, "pj.cue")); err != nil {
		t.Error("forget must not touch scope files")
	}
	// Unknown scope.
	if err := h.admin.Forget("ghost"); err == nil {
		t.Error("expected unknown-scope error")
	}
}

func TestListModesAndDiagnostics(t *testing.T) {
	h := newHarness(t)
	base := t.TempDir()

	// plain-files
	plain := filepath.Join(base, "plain")
	if _, err := h.admin.Init(InitParams{Dir: plain, Name: "pl"}); err != nil {
		t.Fatal(err)
	}
	// pj-driven inside a repo
	repo := filepath.Join(base, "repo")
	gitInit(t, repo)
	pjd := filepath.Join(repo, ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: pjd, Name: "pd", AutoCommit: true, AutoCommitGiven: true}); err != nil {
		t.Fatal(err)
	}
	// repo-driven inside a repo
	repo2 := filepath.Join(base, "repo2")
	gitInit(t, repo2)
	rpd := filepath.Join(repo2, ".agents", "pj")
	if _, err := h.admin.Init(InitParams{Dir: rpd, Name: "rd"}); err != nil {
		t.Fatal(err)
	}

	listing, err := h.admin.List()
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, r := range listing.Rows {
		modes[r.Name] = r.Mode
	}
	if modes["pl"] != ModePlainFiles || modes["pd"] != ModePjDriven || modes["rd"] != ModeRepoDriven {
		t.Errorf("modes = %v", modes)
	}
	// Rows sorted by name ascending.
	var names []string
	for _, r := range listing.Rows {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "pd,pl,rd" {
		t.Errorf("rows not sorted ascending: %v", names)
	}

	// Drift: rewrite pj.cue name; still lists, rides name_drift.
	if err := os.WriteFile(filepath.Join(plain, "pj.cue"), []byte("name: \"plnew\"\nautoCommit: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unparseable: break pd's config → unknown + config_unparseable.
	if err := os.WriteFile(filepath.Join(pjd, "pj.cue"), []byte("name: \"pd\" broken:::"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unreachable: remove rd's dir.
	if err := os.RemoveAll(rpd); err != nil {
		t.Fatal(err)
	}

	listing, err = h.admin.List()
	if err != nil {
		t.Fatal(err)
	}
	diag := strings.Join(listing.Diagnostics, "\n")
	if !strings.Contains(diag, token.NameDrift) {
		t.Errorf("expected name_drift diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, token.ConfigUnparseable) {
		t.Errorf("expected config_unparseable diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, token.UnreachableScope) {
		t.Errorf("expected unreachable_scope diagnostic, got:\n%s", diag)
	}
	modes = map[string]string{}
	for _, r := range listing.Rows {
		modes[r.Name] = r.Mode
	}
	if modes["pd"] != ModeUnknown || modes["rd"] != ModeUnknown {
		t.Errorf("broken/gone scopes should be unknown: %v", modes)
	}
	if modes["pl"] != ModePlainFiles {
		t.Errorf("drifted scope keeps a real mode from its readable pj.cue: %v", modes["pl"])
	}
}

func TestListDriftAndUnparseableCoEmit(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "co")
	if _, err := h.admin.Init(InitParams{Dir: dir, Name: "co"}); err != nil {
		t.Fatal(err)
	}
	// A config that compiles under a legal name but fails schema validation
	// (autoCommit missing) and whose name drifts from the registry key must list
	// as unknown and co-emit name_drift and config_unparseable — the drift name is
	// recovered from ReadName because Load fails.
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte("name: \"conew\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	listing, err := h.admin.List()
	if err != nil {
		t.Fatal(err)
	}

	var mode string
	for _, r := range listing.Rows {
		if r.Name == "co" {
			mode = r.Mode
		}
	}
	if mode != ModeUnknown {
		t.Errorf("co-emit scope mode = %q want %q", mode, ModeUnknown)
	}

	diag := strings.Join(listing.Diagnostics, "\n")
	if !strings.Contains(diag, token.NameDrift) {
		t.Errorf("expected name_drift diagnostic, got:\n%s", diag)
	}
	if !strings.Contains(diag, token.ConfigUnparseable) {
		t.Errorf("expected config_unparseable diagnostic, got:\n%s", diag)
	}
	// The recovered name proves the ReadName fallback ran when Load failed.
	if !strings.Contains(diag, "conew") {
		t.Errorf("drift line should name the recovered pj.cue name %q, got:\n%s", "conew", diag)
	}
}

func TestListEmpty(t *testing.T) {
	h := newHarness(t)
	listing, err := h.admin.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Rows) != 0 || len(listing.Diagnostics) != 0 {
		t.Errorf("empty registry should list nothing, got %+v", listing)
	}
}

func mustAutoCommit(t *testing.T, ctx *cue.Context, dir string) bool {
	t.Helper()
	s, err := scopeconfig.Load(ctx, dir)
	if err != nil {
		t.Fatalf("load pj.cue at %s: %v", dir, err)
	}
	return s.AutoCommit
}
