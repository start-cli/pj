// Package rewrite is the shared multi-file in-scope rewrite durability engine
// (design: multi-file rewrite durability under Scope lifecycle). Both the doctor
// integrity repairs and pj scope rename drive it so durability never forks between
// them.
//
// The contract is best-effort under a caller-held scope flock plus doctor heal — no
// rewrite journal, no multi-phase commit. Each file is written to its new path
// (atomically, via a same-directory temp then rename, so a kill mid-file never
// leaves a truncated sole copy) before the old path is removed, and re-entry after a
// crash is idempotent: an op already at its target path is skipped so a re-run
// finishes the remaining work without double-writing. It performs file I/O only; the
// caller owns the flock, orders the plan so partial progress stays detectable, and
// (for auto-commit) commits only after every write has succeeded.
package rewrite

import (
	"bytes"
	"fmt"
	"os"

	"github.com/start-cli/pj/internal/atomicfile"
)

// fileMode is the ordinary, non-executable mode project files are written with.
const fileMode = 0o644

// Op is one file's rewrite: write Content at NewPath, then remove OldPath when it
// differs (the rename/move case). An empty OldPath, or OldPath equal to NewPath, is
// an in-place rewrite with no removal. Content is the full file bytes (frontmatter
// fence plus body), already serialized by the caller.
type Op struct {
	OldPath string
	NewPath string
	Content []byte
}

// Apply executes ops in order under the durability contract and returns every path
// it touched — new paths and removed old paths alike — so the caller can index-sync
// and commit exactly those. A move op that a prior run already completed (old path
// gone, new path present) is treated as done and skipped, making a re-run after a
// crash idempotent.
//
// A move never overwrites a different file: a destination that already holds other
// content is a hard error, not a write. Two projects can legitimately compute the
// same destination basename (a duplicate id carrying the same frozen slug), and a
// move onto one of them would erase it with no copy left anywhere, since the source
// is removed straight after. Refusing here keeps that impossible for every caller
// rather than resting on each one computing a free destination.
func Apply(ops []Op) ([]string, error) {
	var touched []string
	for _, op := range ops {
		moved := op.OldPath != "" && op.OldPath != op.NewPath
		if moved && alreadyDone(op) {
			touched = append(touched, op.NewPath, op.OldPath)
			continue
		}
		if moved {
			if err := checkFreeDestination(op); err != nil {
				return touched, err
			}
		}
		if err := atomicfile.Write(op.NewPath, op.Content, fileMode); err != nil {
			return touched, err
		}
		touched = append(touched, op.NewPath)
		if moved {
			if err := os.Remove(op.OldPath); err != nil && !os.IsNotExist(err) {
				return touched, fmt.Errorf("remove old path %s: %w", op.OldPath, err)
			}
			touched = append(touched, op.OldPath)
		}
	}
	return touched, nil
}

// checkFreeDestination reports an error when a move's destination already holds
// content other than what the op writes. An absent destination is free; one already
// holding exactly these bytes is the interrupted-move or resumed-extension window,
// where the write is a no-op and the removal is what finishes the move.
func checkFreeDestination(op Op) error {
	existing, err := os.ReadFile(op.NewPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read destination %s: %w", op.NewPath, err)
	}
	if bytes.Equal(existing, op.Content) {
		return nil
	}
	return fmt.Errorf("refusing to move %s onto %s: the destination already holds a different file — resolve the two by hand", op.OldPath, op.NewPath)
}

// alreadyDone reports whether a move op completed on a prior run: the old path is
// gone and the new path is present. It is the idempotent-re-entry test — a partially
// applied plan re-runs cleanly, skipping the files already at their target.
func alreadyDone(op Op) bool {
	if _, err := os.Stat(op.OldPath); !os.IsNotExist(err) {
		return false
	}
	_, err := os.Stat(op.NewPath)
	return err == nil
}
