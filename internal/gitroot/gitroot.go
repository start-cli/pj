// Package gitroot derives a directory's enclosing git repository root by
// shelling out to the external git binary. It is the single call site for the
// code-root/git-root derivation the scope registration checks depend on.
//
// Per the design's Git dependency rule, every failure mode is uniform: a dir
// that is not inside a repository, a dir that does not exist, and a git binary
// absent from PATH all mean "no git-root". Callers never distinguish them.
package gitroot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoRoot returns the cleaned absolute path of the git repository containing
// dir, and ok=true, when dir is inside a working tree. It returns ok=false —
// never an error — when dir is outside any repository, does not exist, or git
// is not on PATH. This uniform "no git-root" outcome is deliberate: the design
// treats a missing git binary exactly like a missing repository.
func RepoRoot(dir string) (root string, ok bool) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root = strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return filepath.Clean(root), true
}

// RepoRootForNew derives the git repository root that a not-yet-created dir would
// belong to, by resolving the nearest existing ancestor and deriving its repo
// root. The eventual dir is a descendant of that ancestor, so it shares the same
// repository. For an already-existing dir it is identical to RepoRoot, and it
// shares RepoRoot's uniform (root, ok) contract. It lets init resolve the
// code-root default before creating the dir, so a failed init leaves nothing
// behind.
func RepoRootForNew(dir string) (root string, ok bool) {
	for {
		if _, err := os.Stat(dir); err == nil {
			return RepoRoot(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
