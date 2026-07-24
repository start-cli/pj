// Package gitstate manages the per-git-root operational state pj keeps in
// machine-local XDG state — never under <git-root>/.git/, which is Git's namespace.
// For each auto-commit git-root it owns a directory keyed by the git-root path:
//
//	${XDG_STATE_HOME:-~/.local/state}/pj/git-roots/<key>/
//
// where <key> is the lowercase hex SHA-256 of the cleaned absolute git-root path.
// The directory holds sync.lock (the flock serialising two scopes sharing one repo
// through their self-commit) and last-push-error (a marker P6's push writes and this
// package reads for write-command warnings). It is created on first need, never
// committed, and lives on local disk beside the index.
package gitstate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/start-cli/pj/internal/flock"
)

// lastPushErrorFile is the marker P6's push writes on failure and this package
// reads; cleared on the next successful push. P4 only reads it.
const lastPushErrorFile = "last-push-error"

// Key is the lowercase hex SHA-256 of the cleaned absolute git-root path. It is
// stable for the same path on the same machine and is the git-roots/ subdirectory
// name for that repo.
func Key(gitRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(gitRoot)))
	return hex.EncodeToString(sum[:])
}

// Dir is the operational-state directory for gitRoot under the XDG state dir. It is
// not created here; AcquireCommitLock creates it on first need.
func Dir(stateDir, gitRoot string) string {
	return filepath.Join(stateDir, "git-roots", Key(gitRoot))
}

// AcquireCommitLock takes the exclusive flock at git-roots/<key>/sync.lock,
// creating the directory if needed. It wraps only the commit sub-span of a
// complete-state write, so two scopes in one repo serialise their self-commits (and
// P6's rebase/push) on the shared git index instead of corrupting it. The caller
// releases it as soon as the commit completes.
func AcquireCommitLock(stateDir, gitRoot string) (*flock.Lock, error) {
	dir := Dir(stateDir, gitRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create git-root state dir %s: %w", dir, err)
	}
	return flock.Acquire(filepath.Join(dir, "sync.lock"))
}

// ReadLastPushError returns the last-push-error marker's detail, and ok=true, when
// a non-empty marker is present for gitRoot. A missing or empty marker returns
// ok=false. Write commands ride this as a warning that the repo has unpushed work
// from a failed push; the marker itself is written by WriteLastPushError.
func ReadLastPushError(stateDir, gitRoot string) (detail string, ok bool) {
	data, err := os.ReadFile(filepath.Join(Dir(stateDir, gitRoot), lastPushErrorFile))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", false
	}
	return s, true
}

// WriteLastPushError records a failed-push detail under gitRoot's state dir, creating
// the dir if needed. pj sync's push (P6b) writes it so the next write command warns the
// repo has unpushed work; ClearLastPushError removes it on the next successful push.
func WriteLastPushError(stateDir, gitRoot, detail string) error {
	dir := Dir(stateDir, gitRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create git-root state dir %s: %w", dir, err)
	}
	p := filepath.Join(dir, lastPushErrorFile)
	if err := os.WriteFile(p, []byte(strings.TrimSpace(detail)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// ClearLastPushError removes the last-push-error marker for gitRoot. A successful push
// clears it. An absent marker is not an error — clearing is idempotent.
func ClearLastPushError(stateDir, gitRoot string) error {
	p := filepath.Join(Dir(stateDir, gitRoot), lastPushErrorFile)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}
