// Package atomicfile writes a file atomically: it writes a temporary file in the
// destination's own directory and renames it over the target, so a reader never
// observes a half-written file and an interrupted write cannot truncate an
// existing one. Keeping the temp file in the same directory keeps the rename on
// one filesystem, where POSIX rename is atomic.
//
// It is the single write primitive the CUE registry, the scope pj.cue authoring
// path, and the scope .gitignore share, so the durability guarantee lives in one
// tested place rather than being re-implemented per call site.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write atomically writes data to path with the given permission bits, creating
// the parent directory if needed. It writes a same-directory temp file, sets its
// mode explicitly (independent of umask), and renames it over path; on any
// failure the temp file is removed and path is left untouched.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
