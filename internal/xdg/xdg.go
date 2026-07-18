// Package xdg resolves pj's machine-local XDG config location and provides the
// machine-global flock that serialises every write to the XDG config tier.
//
// The config directory is resolved through the XDG environment variable
// directly (${XDG_CONFIG_HOME:-~/.config}/pj) rather than os.UserConfigDir,
// which returns the wrong location on macOS. All I/O here is deliberately thin:
// callers own the CUE read/modify/write cycle and hold the lock across it.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockName is the machine-global flock file under the config directory. It is
// gitignored by pj scope init at the scope tier; here it lives beside the
// registry it guards.
const lockName = ".pj.lock"

// ConfigDir returns the pj XDG config directory: $XDG_CONFIG_HOME/pj when
// XDG_CONFIG_HOME is set and non-empty, else ~/.config/pj. The path is not
// created; callers that write create it under the lock.
func ConfigDir() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "pj"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for XDG config: %w", err)
	}
	return filepath.Join(home, ".config", "pj"), nil
}

// Lock is a held machine-global flock over the XDG config tier. It is released
// exactly once via Release.
type Lock struct {
	f *os.File
}

// AcquireConfigLock takes the exclusive machine-global flock at
// <configDir>/.pj.lock, creating the config directory if needed. The lock spans
// the whole read-modify-write cycle of a registry mutation — the caller loads,
// validates against that just-loaded state, regenerates, and renames before
// Release — so two concurrent registrations cannot both pass a check the other's
// write then invalidates. It blocks until the lock is available.
func AcquireConfigLock(configDir string) (*Lock, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("create XDG config directory %s: %w", configDir, err)
	}
	p := filepath.Join(configDir, lockName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open config lock %s: %w", p, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire config lock %s: %w", p, err)
	}
	return &Lock{f: f}, nil
}

// Release drops the flock and closes the underlying descriptor.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
