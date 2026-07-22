package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/start-cli/pj/internal/gitstate"
)

func TestDoctorRepairSelfCommitsAutoCommit(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) == 0 || log[0] != "pj: repair duplicate id wc-ab2c -> wc-ab2ca" {
		t.Fatalf("repair should self-commit with the fixed message, got %v", log)
	}
}

func TestDoctorRepairMultiLoserCommitMessageCoversBatch(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	addProject(t, dir, "wc-ab2c", "gamma", "todo", "a2", "# G\n", false, "")

	if _, _, err := run(t, app, "doctor", "--repair"); err != nil {
		t.Fatalf("doctor --repair: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) == 0 || log[0] != "pj: repair duplicate id wc-ab2c -> wc-ab2ca, wc-ab2cb" {
		t.Fatalf("multi-loser repair message must name the collided id and every rename, got %v", log)
	}
}

func TestDoctorRepairPlannedRidesSyncDisabled(t *testing.T) {
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir := filepath.Join(t.TempDir(), "wc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "wc", "--auto-commit"); err != nil {
		t.Fatalf("init planned auto-commit: %v", err)
	}
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")

	_, errOut, err := run(t, app, "doctor", "--repair")
	if err != nil {
		t.Fatalf("planned repair should land files: %v", err)
	}
	if !strings.Contains(errOut, "sync_disabled:") {
		t.Errorf("planned auto-commit repair should ride sync_disabled, got %q", errOut)
	}
	if !fileExists(dir, "wc-ab2ca-beta.md") {
		t.Errorf("planned repair must still write the files")
	}
}

// The per-git-root preflight is repo-granular: a broken sibling makes the whole git-root
// unsyncable, so doctor must name it even when only the healthy scope is being diagnosed.
func TestDoctorFlagsUnparseableSiblingSharingGitRoot(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	sibling := filepath.Join(repo, "sib")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	// A second scope in one repo needs its own code-root — nested is fine, identical is
	// refused — which is exactly the shared-git-root topology this preflight exists for.
	if _, _, err := run(t, app, "scope", "init", sibling, "--name", "sib", "--code-root", sibling, "--auto-commit"); err != nil {
		t.Fatalf("init sibling: %v", err)
	}
	// Schema-invalid but compilable: the name still reads, so this is an unusable config
	// rather than a drifted registration.
	bad := "name: \"sib\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(sibling, "pj.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "config_unparseable:") || !strings.Contains(out, "sib") {
		t.Errorf("doctor should flag the unparseable sibling sharing the git-root, got %q", out)
	}
}

// A scope reached both as the diagnosed scope and as its own git-root sibling rides the
// class once.
func TestDoctorConfigUnparseableReportedOncePerScope(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, _ := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	bad := "name: \"wc\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if n := strings.Count(out+errOut, "config_unparseable: wc"); n != 1 {
		t.Errorf("config_unparseable must ride once per scope, got %d in %q", n, out+errOut)
	}
}

// An unparseable pj.cue makes autoCommit unknown, not false: the scope must not be
// judged as repo-driven, and must still get every class that does not depend on it.
func TestDoctorUnparseableConfigSkipsAutoCommitClasses(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, _ := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")
	bad := "name: \"wc\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-allowlisted file proves the non-autoCommit classes still run.
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "config_unparseable:") {
		t.Errorf("unusable config must ride config_unparseable, got %q", out)
	}
	if strings.Contains(out, "uncommitted:") {
		t.Errorf("unknown autoCommit must not be judged repo-driven, got %q", out)
	}
	if strings.Contains(out, "sync_disabled:") {
		t.Errorf("unknown autoCommit must not be judged auto-commit either, got %q", out)
	}
	if !strings.Contains(out, "non_allowlist:") {
		t.Errorf("classes independent of autoCommit must still run, got %q", out)
	}
}

// The mid-rebase status_conflict tail is gated on autoCommit, not repo topology: pj sync
// runs only for an auto-commit scope, so a repo-driven scope in a mid-rebase host repo
// gets the standalone residue guidance, and an unknown autoCommit gets neither tail.
func TestDoctorStatusConflictModesGateOnAutoCommit(t *testing.T) {
	requireGit(t)
	cases := []struct {
		name       string
		scope      string
		autoCommit bool
		breakCfg   bool
		want       string
		notWant    string
	}{
		{
			name:  "auto-commit mid-rebase resolves then syncs",
			scope: "wc", autoCommit: true,
			want: "resolve in file, then pj sync",
		},
		{
			name:  "repo-driven mid-rebase is standalone residue",
			scope: "rd", autoCommit: false,
			want: "stale residue: set status and clear status_conflict", notWant: "then pj sync",
		},
		{
			name:  "unknown autoCommit claims neither tail",
			scope: "uc", autoCommit: true, breakCfg: true,
			want: "— set status and clear status_conflict", notWant: "then pj sync",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newApp(t)
			t.Setenv("PJ_SCOPE", tc.scope)
			dir, repo := initGitScope(t, app, tc.scope, tc.autoCommit)
			addProject(t, dir, tc.scope+"-ab2c", "x", "todo", "a0", "# X\n", false, "status_conflict: [done, cancelled]\n")
			if tc.breakCfg {
				bad := "name: \"" + tc.scope + "\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n"
				if err := os.WriteFile(filepath.Join(dir, "pj.cue"), []byte(bad), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
				t.Fatal(err)
			}

			out, _, err := run(t, app, "doctor")
			if err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if !strings.Contains(out, "status_conflict:") {
				t.Fatalf("expected a status_conflict line, got %q", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in %q", tc.want, out)
			}
			if tc.notWant != "" && strings.Contains(out, tc.notWant) {
				t.Errorf("must not contain %q, got %q", tc.notWant, out)
			}
		})
	}
}

// The per-git-root preflight's autoCommit verdict. scope init/import refuse a mismatching
// sibling up front, so the case doctor exists to catch is divergence introduced by editing
// pj.cue after registration. A sibling whose config will not parse has no readable
// autoCommit and must be left out of the verdict rather than counted as false — the
// schemaAutoCommit(nil) trap.
func TestDoctorAutoCommitMismatchVerdict(t *testing.T) {
	requireGit(t)
	cases := []struct {
		name       string
		siblingCue string
		wantLine   bool
	}{
		{
			name:       "divergent siblings ride the mismatch",
			siblingCue: "name: \"sib\"\nautoCommit: false\n",
			wantLine:   true,
		},
		{
			name:       "agreeing siblings do not",
			siblingCue: "name: \"sib\"\nautoCommit: true\n",
			wantLine:   false,
		},
		{
			// Unreadable autoCommit is not a disagreement: there is no value to compare.
			name:       "unparseable sibling is excluded from the verdict",
			siblingCue: "name: \"sib\"\nautoCommit: true\nfields: {x: {type: \"float\"}}\n",
			wantLine:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newApp(t)
			t.Setenv("PJ_SCOPE", "wc")
			dir, repo := initGitScope(t, app, "wc", true)
			addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

			sib := filepath.Join(repo, "sib")
			if err := os.MkdirAll(sib, 0o755); err != nil {
				t.Fatal(err)
			}
			if _, _, err := run(t, app, "scope", "init", sib, "--name", "sib", "--code-root", sib, "--auto-commit"); err != nil {
				t.Fatalf("init sibling: %v", err)
			}
			if err := os.WriteFile(filepath.Join(sib, "pj.cue"), []byte(tc.siblingCue), 0o644); err != nil {
				t.Fatal(err)
			}

			out, _, err := run(t, app, "doctor")
			if err != nil {
				t.Fatalf("doctor: %v", err)
			}
			if got := strings.Contains(out, "auto_commit_mismatch:"); got != tc.wantLine {
				t.Errorf("auto_commit_mismatch present = %v, want %v, got %q", got, tc.wantLine, out)
			}
		})
	}
}

// One token per dir-not-usable mode: an unreachable dir makes pj.cue equally unreadable,
// so a sibling whose dir is gone must not ride config_unparseable. It is excluded because
// a dir that resolves to no git-root is not a sibling of this one — reachability is
// answered by the root lookup, not by consulting its config.
func TestDoctorUnreachableSiblingRidesNoConfigError(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	sib := filepath.Join(repo, "sib")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, app, "scope", "init", sib, "--name", "sib", "--code-root", sib, "--auto-commit"); err != nil {
		t.Fatalf("init sibling: %v", err)
	}
	// The scope stays registered; its dir does not.
	if err := os.RemoveAll(sib); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out+errOut, "config_unparseable:") {
		t.Errorf("an unreachable sibling must not ride config_unparseable, got %q", out+errOut)
	}
}

func TestDoctorMutatingRefusesMidRebase(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "alpha", "todo", "a0", "# A\n", false, "")
	addProject(t, dir, "wc-ab2c", "beta", "todo", "a1", "# B\n", false, "")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, app, "doctor", "--repair"); ExitCodeFromError(err) != exitFailure {
		t.Errorf("mid-rebase --repair should refuse non-zero, got %v", err)
	}
	// Bare report still runs mid-rebase.
	if _, _, err := run(t, app, "doctor"); err != nil {
		t.Errorf("bare doctor must still run mid-rebase, got %v", err)
	}
}

func TestDoctorRepoDrivenUncommitted(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "rd")
	dir, _ := initGitScope(t, app, "rd", false)
	addProject(t, dir, "rd-ab2c", "x", "todo", "a0", "# X\n", false, "")

	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "uncommitted:") {
		t.Errorf("repo-driven bare doctor should ride uncommitted, got %q", out)
	}
}

func TestDoctorReportsLastPushError(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	t.Setenv("PJ_SCOPE", "wc")
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	marker := gitstate.Dir(app.StateDir, repo)
	if err := os.MkdirAll(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "last-push-error"), []byte("auth failed"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, app, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "last_push_error:") {
		t.Errorf("doctor should report last_push_error from XDG state, got %q", out)
	}
	if !strings.Contains(out, "sync_disabled:") {
		t.Errorf("auto-commit scope without upstream should also ride sync_disabled, got %q", out)
	}
}

func TestScopeRenameSelfCommits(t *testing.T) {
	requireGit(t)
	app := newApp(t)
	dir, repo := initGitScope(t, app, "wc", true)
	addProject(t, dir, "wc-ab2c", "x", "todo", "a0", "# X\n", false, "")

	if _, _, err := run(t, app, "scope", "rename", "wc", "core"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	log := gitLog(t, repo)
	if len(log) == 0 || log[0] != "pj: rename scope wc -> core" {
		t.Fatalf("rename should self-commit with the fixed message, got %v", log)
	}
}
