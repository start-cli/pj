// Package flock is a thin POSIX advisory-lock helper shared by pj's machine-local
// serialisation points: the per-scope dir lock (<dir>/.pj.lock) that serialises a
// scope's reconcile→write span, and the per-git-root commit lock
// (git-roots/<key>/sync.lock) that serialises two scopes sharing one repo through
// their self-commit. Both are exclusive, blocking, and machine-local — they do not
// coordinate cross-clone or networked-filesystem writers.
//
// The lock is Unix-only by construction (syscall.Flock); pj supports macOS and
// Linux only, so no build tag or portable fallback is provided.
package flock

import (
	"fmt"
	"os"
	"syscall"
)

// Lock is a held exclusive flock over a single lock file, released exactly once.
type Lock struct {
	f *os.File
}

// Acquire opens (creating if absent) the lock file at path and takes the exclusive
// flock, blocking until it is available. The caller holds it across the span it
// must serialise and releases it with Release. The parent directory must already
// exist; callers that own a state directory create it before acquiring.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the flock and closes the descriptor. It is safe on a nil or
// already-released lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
