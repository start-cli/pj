package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Stages reports which of a conflicted path's three merge stages exist in the index —
// base (:1), ours (:2), theirs (:3). It reads the conflict entry with
// `git ls-files -u` and reports presence per stage rather than merely "conflicted",
// because a missing stage is normal input the rebase driver branches on: an add/add has
// no :1, and a delete/modify has no entry for the deleting side. Enumerating stages
// before reading them is what keeps a genuine `git show` failure from being
// misclassified as a deletion.
type Stages struct {
	Base   bool
	Ours   bool
	Theirs bool
}

// Any reports whether the path has any conflict stage at all.
func (s Stages) Any() bool { return s.Base || s.Ours || s.Theirs }

// ConflictStages enumerates which merge stages exist for a conflicted path via
// `git ls-files -u -- <path>`. An empty result (no unmerged entry) is not an error — it
// is a path git does not consider conflicted.
func ConflictStages(ctx context.Context, gitRoot, path string) (Stages, error) {
	out, err := run(ctx, gitRoot, "ls-files", "-u", "--", path)
	if err != nil {
		return Stages{}, err
	}
	var s Stages
	for _, line := range nonEmptyLines(string(out)) {
		// "<mode> <sha> <stage>\t<path>"; the stage number is the field before the tab.
		meta := line
		if tab := strings.IndexByte(line, '\t'); tab >= 0 {
			meta = line[:tab]
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		switch fields[len(fields)-1] {
		case "1":
			s.Base = true
		case "2":
			s.Ours = true
		case "3":
			s.Theirs = true
		}
	}
	return s, nil
}

// ShowStage returns the raw blob of one conflict stage via `git show :<stage>:<path>`.
// Callers must enumerate stages with ConflictStages first and read only the stages it
// reports; a non-zero exit here is a genuine fault (corrupt object, bad path, git gone),
// surfaced as an error, never reinterpreted as an absent stage.
func ShowStage(ctx context.Context, gitRoot string, stage int, path string) ([]byte, error) {
	if stage < 1 || stage > 3 {
		return nil, fmt.Errorf("git show stage: stage %d out of range", stage)
	}
	return run(ctx, gitRoot, "show", fmt.Sprintf(":%d:%s", stage, path))
}

// MergeBlobs 3-way text-merges three in-memory blobs with `git merge-file`, returning
// the merged bytes and whether git left conflict markers. It is the body merge behind
// the rebase driver: the driver splits each stage's body out and merges the three
// bodies here, keeping any markers confined to the body region. Labels annotate the
// conflict markers for the human who resolves them.
func MergeBlobs(ctx context.Context, base, ours, theirs []byte) (merged []byte, conflicted bool, err error) {
	dir, err := os.MkdirTemp("", "pj-merge-*")
	if err != nil {
		return nil, false, fmt.Errorf("merge-file temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	oursPath := filepath.Join(dir, "ours")
	basePath := filepath.Join(dir, "base")
	theirsPath := filepath.Join(dir, "theirs")
	for _, f := range []struct {
		path string
		data []byte
	}{{oursPath, ours}, {basePath, base}, {theirsPath, theirs}} {
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return nil, false, fmt.Errorf("merge-file write: %w", err)
		}
	}

	// -p writes the result to stdout; the exit code is the number of conflict hunks
	// (0 = clean, >0 = conflicted), or negative on a real error.
	cmd := exec.CommandContext(ctx, "git", "merge-file", "-p",
		"-L", "ours", "-L", "base", "-L", "theirs",
		oursPath, basePath, theirsPath)
	out, runErr := cmd.Output()
	if runErr == nil {
		return out, false, nil
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() > 0 {
		return out, true, nil
	}
	return nil, false, fmt.Errorf("git merge-file: %w", runErr)
}

// ListFiles lists the index-tracked paths under dir via `git ls-files -- <dir>`, each
// repo-relative. The rebase driver reads it to derive a scope's occupied short-ids from
// project basenames.
func ListFiles(ctx context.Context, gitRoot, dir string) ([]string, error) {
	out, err := run(ctx, gitRoot, "ls-files", "--", dir)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// AuthorDate returns the author date of the last commit on rev that touched path
// (`git log -1 --format=%aI <rev> -- <path>`). It is the per-file, per-side arbiter for
// both-sides scalar last-writer-wins — a per-file date, never a branch-tip date, so an
// unrelated later commit on one side cannot decide another project's fields. When no
// commit on rev touched path the output is empty and a zero time is returned, which lets
// the merge fall to its byte-hash residual rather than erroring.
func AuthorDate(ctx context.Context, gitRoot, rev, path string) (time.Time, error) {
	out, err := run(ctx, gitRoot, "log", "-1", "--format=%aI", rev, "--", path)
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse author date %q: %w", s, err)
	}
	return t, nil
}

// RebaseSides resolves the two side revs of a paused rebase: HEAD (the upstream tip,
// paired to stage :2) and REBASE_HEAD (the commit being replayed, paired to stage :3).
// It is plumbing P6b calls before invoking the driver; the driver takes the resolved
// revs as inputs rather than reaching into rebase-orchestration state. The mapping is
// inverted from the everyday during-rebase reading of "ours"/"theirs", so callers must
// pair each rev with its stage number, never with a side label.
func RebaseSides(ctx context.Context, gitRoot string) (head, rebaseHead string, err error) {
	h, err := run(ctx, gitRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	rh, err := run(ctx, gitRoot, "rev-parse", "REBASE_HEAD")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(h)), strings.TrimSpace(string(rh)), nil
}

// Fetch updates remote-tracking refs (`git fetch`) using the user's own remotes and
// credentials. It is consumed by pj sync (P6b); it lands here so internal/git is
// complete in one pass.
func Fetch(ctx context.Context, gitRoot string) error {
	_, err := run(ctx, gitRoot, "fetch")
	return err
}

// Rebase starts `git rebase <upstream>`. paused is true when the rebase stopped on a
// conflict (mid-rebase with unmerged files) rather than failing outright, so the caller
// distinguishes "resolve and continue" from a genuine error.
func Rebase(ctx context.Context, gitRoot, upstream string) (paused bool, err error) {
	return rebaseStep(ctx, gitRoot, "rebase", upstream)
}

// RebaseContinue runs `git rebase --continue`. paused is true when the rebase stopped
// again on a later conflict. The commit editor is disabled so a replayed commit's
// message is reused non-interactively.
func RebaseContinue(ctx context.Context, gitRoot string) (paused bool, err error) {
	return rebaseStep(ctx, gitRoot, "rebase", "--continue")
}

// RebaseAbort runs `git rebase --abort`, restoring the pre-rebase state.
func RebaseAbort(ctx context.Context, gitRoot string) error {
	return runEnv(ctx, gitRoot, editorEnv(), "rebase", "--abort")
}

// rebaseStep runs a rebase-family command with the editor disabled and reclassifies a
// non-zero exit that left the repo mid-rebase as a pause rather than an error.
func rebaseStep(ctx context.Context, gitRoot string, args ...string) (bool, error) {
	err := runEnv(ctx, gitRoot, editorEnv(), args...)
	if err == nil {
		return false, nil
	}
	if MidRebase(ctx, gitRoot) && len(UnmergedFiles(ctx, gitRoot)) > 0 {
		return true, nil
	}
	return false, err
}

// Push runs `git push` to the current branch's upstream. It is pj sync's sole push
// (P6b); it lands here so the wrapper is complete.
func Push(ctx context.Context, gitRoot string) error {
	_, err := run(ctx, gitRoot, "push")
	return err
}

// UnpushedCount returns how many commits the current branch is ahead of its upstream
// (`git rev-list --count @{u}..HEAD`). Consumed by P6b's report; it lands here.
func UnpushedCount(ctx context.Context, gitRoot string) (int, error) {
	out, err := run(ctx, gitRoot, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse unpushed count %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

// DirtyEntry is one dirty path under a scope dir plus its two-character porcelain status
// code (the "XY" of `git status --porcelain`). The rebase and sync layers read the code
// to tell an added file from a modified or deleted one.
type DirtyEntry struct {
	Path string
	Code string
}

// DirtyEntries returns the dirty paths under dir with their porcelain status codes,
// scoped to dir so it never scans the whole working tree. It is the status-code-carrying
// dirty read P4's DirtyPaths could not provide; DirtyPaths is now a thin projection of
// it. Rename/copy entries contribute their destination path.
func DirtyEntries(ctx context.Context, gitRoot, dir string) ([]DirtyEntry, error) {
	out, err := run(ctx, gitRoot, "status", "--porcelain", "-z", "-uall", "--", dir)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(string(out), "\x00")
	var entries []DirtyEntry
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		path := entry[3:]
		if strings.ContainsAny(code, "RC") {
			i++ // consume and ignore the rename/copy source path in the next NUL field
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(gitRoot, path)
		}
		entries = append(entries, DirtyEntry{Path: path, Code: code})
	}
	return entries, nil
}

// editorEnv disables git's interactive editors so a rebase/continue never blocks on a
// commit-message or sequence prompt in the non-interactive sync path.
func editorEnv() []string {
	return []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}
}

// runEnv runs git with extra environment appended to the inherited environment,
// discarding stdout — its callers (rebase/abort) act on the exit status, not output.
func runEnv(ctx context.Context, gitRoot string, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), extraEnv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}
