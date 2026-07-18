package scopeadmin

import (
	"fmt"

	"github.com/start-cli/pj/internal/gitroot"
	"github.com/start-cli/pj/internal/pathutil"
	"github.com/start-cli/pj/internal/registry"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/token"
)

// checkNameCollision rejects a scope name already registered. There is no
// rename-on-import: the name is baked into every id, filename, and in-scope
// reference. The auto-name variant points at --name rather than rename.
func checkNameCollision(reg *registry.Registry, name string, derived bool) error {
	if _, ok := reg.Scopes[name]; !ok {
		return nil
	}
	if derived {
		return fmt.Errorf("derived scope name %q is already registered — pass --name to choose another", name)
	}
	return fmt.Errorf("scope name %q is already registered — names are machine-unique; rename at the source (pj scope rename) rather than re-registering", name)
}

// checkCodeRootCollision rejects a code-root identical to another scope's. Nested
// code-roots are fine (longest-prefix resolves); only identical ones are rejected.
func checkCodeRootCollision(reg *registry.Registry, root, exclude string) error {
	for name, e := range reg.Scopes {
		if name == exclude {
			continue
		}
		if e.Root == root {
			return fmt.Errorf("code-root %s is already registered to scope %q — nested code-roots are fine, identical ones are not", root, name)
		}
	}
	return nil
}

// checkDirDisjoint rejects a dir identical to, nested within, or containing any
// other registered scope's dir. Dirs must be mutually disjoint — unlike
// code-roots — because sync's snapshot treats everything under a dir as that
// scope's to commit.
func checkDirDisjoint(reg *registry.Registry, dir, exclude string) error {
	for name, e := range reg.Scopes {
		if name == exclude {
			continue
		}
		if pathutil.Overlap(dir, e.Dir) {
			return fmt.Errorf("scope dir %s overlaps scope %q's dir %s — dirs must be mutually disjoint; choose a sibling path (e.g. .agents/pj-teamB), not one nested under an existing scope", dir, name, e.Dir)
		}
	}
	return nil
}

// consensus is the autoCommit agreement among the registered scopes sharing a
// candidate dir's derived git-root.
type consensus struct {
	hasGitRoot bool
	gitRoot    string
	// found reports whether at least one sibling shares the git-root; value is
	// their agreed autoCommit when found.
	found bool
	value bool
}

// siblingConsensus evaluates the autoCommit values of every registered scope
// sharing the candidate's git-root (excluding excludeName). The candidate's
// git-root is passed in pre-derived — init derives it before creating the dir, so
// this cannot re-derive it from a dir that may not exist yet. It is the single
// reusable per-git-root evaluation the register-time checks use and that P4/P5
// sync/doctor preflights are meant to call, so the rules never fork.
//
// A sibling with an unusable pj.cue supplies no safe autoCommit to assume, so
// the check refuses with config_unparseable rather than proceeding. A sibling
// whose dir is gone cannot derive a git-root and drops out under the uniform
// no-git-root rule. Existing siblings that disagree among themselves surface as
// auto_commit_mismatch here too.
func siblingConsensus(a *Admin, reg *registry.Registry, gitRoot string, inRepo bool, excludeName string) (consensus, error) {
	c := consensus{hasGitRoot: inRepo, gitRoot: gitRoot}
	if !inRepo {
		return c, nil
	}
	for name, entry := range reg.Scopes {
		if name == excludeName {
			continue
		}
		sgr, sok := gitroot.RepoRoot(entry.Dir)
		if !sok || sgr != gitRoot {
			continue
		}
		schema, err := scopeconfig.Load(a.ctx, entry.Dir)
		if err != nil {
			if _, isCfg := scopeconfig.AsConfigError(err); isCfg {
				return c, fmt.Errorf("%s", token.Line(token.ConfigUnparseable,
					fmt.Sprintf("sibling scope at %s sharing git-root %s has an unparseable pj.cue — fix it before registering here", entry.Dir, gitRoot)))
			}
			return c, err
		}
		if !c.found {
			c.found = true
			c.value = schema.AutoCommit
		} else if c.value != schema.AutoCommit {
			return c, autoCommitMismatch(gitRoot, c.value, schema.AutoCommit)
		}
	}
	return c, nil
}

// autoCommitMismatch builds the auto_commit_mismatch error naming both values.
func autoCommitMismatch(gitRoot string, existing, offered bool) error {
	return fmt.Errorf("%s", token.Line(token.AutoCommitMismatch,
		fmt.Sprintf("scopes sharing git-root %s use autoCommit=%v but this scope offers autoCommit=%v — every scope in a repo must agree; an isolated auto-commit scope belongs in its own repo", gitRoot, existing, offered)))
}

// resolveInitAutoCommit determines the autoCommit an init writes: inherit
// siblings when the flag is omitted, use the flag when no siblings exist, and
// reject an explicit flag that contradicts siblings.
func resolveInitAutoCommit(c consensus, flagGiven, flagVal bool) (bool, error) {
	if c.found {
		if flagGiven && flagVal != c.value {
			return false, autoCommitMismatch(c.gitRoot, c.value, flagVal)
		}
		return c.value, nil
	}
	if flagGiven {
		return flagVal, nil
	}
	return false, nil
}
