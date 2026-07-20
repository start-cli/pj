package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/start-cli/pj/internal/token"
)

func newApp(t *testing.T) *App {
	t.Helper()
	return &App{Ctx: cuecontext.New(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}
}

// run executes the command tree with captured output and returns stdout, stderr,
// and the handler error.
func run(t *testing.T, app *App, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd(app)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestUsageErrorsExitTwo(t *testing.T) {
	app := newApp(t)
	dir := filepath.Join(t.TempDir(), "s")
	cases := []struct {
		name string
		args []string
	}{
		{"bad name alphabet", []string{"scope", "init", dir, "--name", "BAD"}},
		{"neither name flag", []string{"scope", "init", dir}},
		{"both name flags", []string{"scope", "init", dir, "--name", "x", "--auto-name"}},
		{"unknown flag", []string{"scope", "list", "--bogus"}},
		{"too few args", []string{"scope", "forget"}},
		{"too many args", []string{"scope", "forget", "a", "b"}},
		{"rebind missing name", []string{"scope", "rebind", dir}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := run(t, app, c.args...)
			if got := ExitCodeFromError(err); got != exitUsage {
				t.Fatalf("exit code = %d want %d (err=%v)", got, exitUsage, err)
			}
		})
	}
}

func TestGenericFailuresExitOne(t *testing.T) {
	app := newApp(t)
	base := t.TempDir()
	first := filepath.Join(base, "one")
	if _, _, err := run(t, app, "scope", "init", first, "--name", "dup"); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Name collision: well-formed but conflicting → generic non-zero, not usage.
	_, _, err := run(t, app, "scope", "init", filepath.Join(base, "two"), "--name", "dup")
	if got := ExitCodeFromError(err); got != exitFailure {
		t.Fatalf("name collision exit = %d want %d", got, exitFailure)
	}

	// Rebind an unknown (but well-formed) scope name → generic non-zero, not usage.
	_, _, err = run(t, app, "scope", "rebind", first, "--name", "ghost")
	if got := ExitCodeFromError(err); got != exitFailure {
		t.Fatalf("unknown-scope rebind exit = %d want %d (err=%v)", got, exitFailure, err)
	}
}

func TestScopeListEndToEnd(t *testing.T) {
	app := newApp(t)
	dir := filepath.Join(t.TempDir(), "home")
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "home"); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, errOut, err := run(t, app, "scope", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if len(fields) != 4 || fields[0] != "home" || fields[3] != "plain-files" {
		t.Fatalf("unexpected TSV row %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("stdout must never carry ANSI: %q", out)
	}
	if errOut != "" {
		t.Errorf("a healthy scope should ride no diagnostics, got stderr %q", errOut)
	}

	// Bare `pj scope` runs list; `pj scopes` aliases it.
	bare, _, _ := run(t, app, "scope")
	if bare != out {
		t.Errorf("bare scope != scope list: %q vs %q", bare, out)
	}
	alias, _, _ := run(t, app, "scopes")
	if alias != out {
		t.Errorf("scopes alias != scope list: %q vs %q", alias, out)
	}
}

func TestScopeUnknownSubcommandExitsTwo(t *testing.T) {
	app := newApp(t)
	// A mistyped subcommand must be a usage error, not a silent list.
	out, _, err := run(t, app, "scope", "froget", "wc")
	if got := ExitCodeFromError(err); got != exitUsage {
		t.Fatalf("unknown subcommand exit = %d want %d (err=%v)", got, exitUsage, err)
	}
	if out != "" {
		t.Errorf("unknown subcommand must not print a listing, got stdout %q", out)
	}

	// Bare `pj scope`, the `pj scopes` alias, and a real subcommand still work.
	if _, _, err := run(t, app, "scope"); err != nil {
		t.Errorf("bare scope should list, got %v", err)
	}
	if _, _, err := run(t, app, "scopes"); err != nil {
		t.Errorf("scopes alias should list, got %v", err)
	}
	dir := filepath.Join(t.TempDir(), "real")
	if _, _, err := run(t, app, "scope", "init", dir, "--name", "real"); err != nil {
		t.Errorf("real subcommand should dispatch, got %v", err)
	}
}

func TestScopeListEmptyExitsZeroEmptyStdout(t *testing.T) {
	app := newApp(t)
	out, _, err := run(t, app, "scope", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if out != "" {
		t.Errorf("empty registry should print empty stdout, got %q", out)
	}
}

func TestWantColor(t *testing.T) {
	t.Run("tty on, NO_COLOR unset", func(t *testing.T) {
		withoutNoColor(t)
		if !wantColor(true) {
			t.Error("colour should be on for a TTY without NO_COLOR")
		}
		if wantColor(false) {
			t.Error("colour should be off for a non-TTY")
		}
	})
	t.Run("NO_COLOR present disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if wantColor(true) {
			t.Error("NO_COLOR must disable colour even on a TTY")
		}
	})
	t.Run("NO_COLOR empty still disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		if wantColor(true) {
			t.Error("NO_COLOR presence alone (empty value) must disable colour")
		}
	})
}

func TestFprintErrorTokenPurity(t *testing.T) {
	// A token line is never coloured, even when colour is allowed.
	var buf bytes.Buffer
	fprintError(&buf, errors.New(token.NameDrift+" registry key drift"), true)
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("token line must not carry ANSI: %q", buf.String())
	}
	if !strings.HasPrefix(buf.String(), token.NameDrift) {
		t.Errorf("token must lead the line: %q", buf.String())
	}

	// A non-token line gets a coloured label only when colour is allowed.
	buf.Reset()
	fprintError(&buf, errors.New("plain failure"), true)
	if !strings.Contains(buf.String(), "\x1b") {
		t.Errorf("non-token line should carry the coloured label when colour is on: %q", buf.String())
	}
	buf.Reset()
	fprintError(&buf, errors.New("plain failure"), false)
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("no ANSI when colour is off: %q", buf.String())
	}

	// A Plain non-fault diagnostic (an empty next queue) prints verbatim: no error:
	// label and no colour, even when colour is allowed.
	buf.Reset()
	fprintError(&buf, &ExitError{Code: exitFailure, Err: errors.New("nothing ready"), Plain: true}, true)
	if strings.Contains(buf.String(), "\x1b") || strings.Contains(buf.String(), "error:") {
		t.Errorf("a Plain diagnostic must print verbatim without a label: %q", buf.String())
	}
	if strings.TrimRight(buf.String(), "\n") != "nothing ready" {
		t.Errorf("Plain diagnostic message altered: %q", buf.String())
	}
}

func TestSignalExitCode(t *testing.T) {
	t.Cleanup(func() { caughtSignal.Store(0) })

	caughtSignal.Store(0)
	if got := SignalExitCode(); got != 0 {
		t.Errorf("no signal → %d want 0", got)
	}
	caughtSignal.Store(int32(syscall.SIGINT))
	if got := SignalExitCode(); got != 130 {
		t.Errorf("SIGINT → %d want 130", got)
	}
	caughtSignal.Store(int32(syscall.SIGTERM))
	if got := SignalExitCode(); got != 143 {
		t.Errorf("SIGTERM → %d want 143", got)
	}
}

func TestAbsPath(t *testing.T) {
	got, err := absPath("relative/dir")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("absPath must return an absolute path, got %q", got)
	}
	if strings.HasPrefix(got, "~") {
		t.Errorf("absPath must not yield a ~ prefix, got %q", got)
	}
}

func TestAbsPathResolvesSymlinks(t *testing.T) {
	// A path through a symlinked component canonicalises to the real location, so
	// it matches the spelling git returns for a repo root. The not-yet-existing
	// tail ("sub") is rejoined onto the resolved ancestor.
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err := absPath(filepath.Join(link, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	realResolved, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(realResolved, "sub"); got != want {
		t.Errorf("absPath through symlink = %q, want %q", got, want)
	}
}

// withoutNoColor unsets NO_COLOR for the duration of a test, restoring any prior
// value afterwards. t.Setenv cannot express "absent", which this case needs.
func withoutNoColor(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("NO_COLOR", prev)
		}
	})
}
