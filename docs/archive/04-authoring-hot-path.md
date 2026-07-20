# P4 Authoring hot path

## Goal

Make local authoring work: `pj create`, `pj status`, `pj reorder`, and `pj next --claim`
write project files and index rows, self-commit on auto-commit scopes when a git-root
exists, and honour every autoCommit mode (pj-driven, repo-driven, plain-files). This is the
single-machine authoring product — create, promote, claim, complete — with git durability
local to the machine; the push boundary and merge come later.

## Scope

In scope:
- U19 git plumbing and self-commit: the git subprocess wrapper, git-root derivation from a
  scope `dir`, the XDG `git-roots/<key>/` operational-state directory and its locks, the
  complete-state self-commit (add specific paths, fixed message, no push), the `sync_disabled:`
  and `uncommitted:` write-path signals, the git-root commit lock, and the mid-rebase
  command classification.
- U20 write verbs: `create`, `status` (with the archive-boundary move), `reorder`, and
  `next --claim` (flock CAS), plus the on-demand creation of `archive/`.

Out of scope (named siblings own these):
- `pj sync` and all pushing, fetching, and rebasing — P6. This project self-commits locally
  only; no command here pushes. `sync_disabled:` is emitted on the write path when a
  git-root/upstream is absent, but the sync verb that also reports it is P6.
- The frontmatter merge package and merge/rebase behaviour — P6. Mid-rebase detection and
  the refuse rules are implemented here (writes must fail fast on a mid-rebase git-root), but
  the rebase itself is driven by P6.
- File-mutating integrity repair and `pj doctor` — P5. `create`/`status` move files across
  the terminal boundary as part of a complete-state write, but drift repair, id-collision
  repair, and re-space are P5.
- `next` selection and eligibility, id resolution, the index write-through upsert, and the
  read verbs — all from P3. `next --claim` reuses P3's `next` selection; do not reimplement
  it.

## Current State

P1, P2, and P3 are complete.

P1: the pure packages (id predicates and mint, `slugify`, `order`/`keyBetween`, frontmatter
model, status/terminal predicate, title extraction, auto-name).

P2: the running CLI (exit codes, colour/TTY, signal handling exiting `128+signum`, absolute
path hand-off); the XDG CUE engine and registry; ambient resolution and name-drift
fail-closed; the scope verbs; and `ScopeSchema` (name/autoCommit/knownTags/statuses/fields).
P2 already re-derives a git-root and validates autoCommit consistency across scopes sharing
a git-root, and shells `git rev-parse`; the full git subprocess wrapper is this project.

P3: the SQLite index with the write-through upsert API, reconcile (mtime/size, `parse_error`
quarantine, body-aware markers, unreachable-scope, the `pj.cue` eval cache), warn-only
integrity detection, the `edges` table and query surface, the read/locate verbs, the
`depends` gate, `next` selection/eligibility, and the lens. Everything so far is git-free on
the read path; this project introduces git only on the write and self-commit paths. The repo
is otherwise as built by P1–P3.

`design.md` is the source of truth. Durability and sync split along the commit/push seam:
complete-state verbs self-commit their own change synchronously; nothing here pushes.

## References

- `design.md` — read these sections:
  - Sync model → Writes commit their own change — the complete-state self-commit rule, the
    `autoCommit: true` / git-not-ready behaviour, the `create`-never-self-commits exception,
    the scope flock span, the git-root commit lock, the git-root operational-state DECISION
    (`${XDG_STATE_HOME}/pj/git-roots/<key>/` with `sync.lock` and `last-push-error`; `<key>`
    = SHA-256 of the cleaned absolute git-root path), and the mid-rebase command-classes
    table.
  - Sync model → Auto-commit — the per-mode behaviour matrix (pj-driven / repo-driven /
    plain-files), help-text honesty, the repo-driven dirty-health `uncommitted:` detect-only
    rule, and the git-root autoCommit coupling. (The push half is P6.)
  - Sync model → Git dependency — shell out to external git; git-missing behaves like
    missing git-root for self-commit.
  - CLI surface — the stdout contracts and behaviour for `create`, `status`, `reorder`, and
    `next` (including `--claim`).
  - Status and dependencies — `pj next --claim` closed v1 semantics and the claim procedure;
    the `create` defaults and locked scaffold contents; that only built-in `todo` is
    next-eligible.
  - Done and archive — who moves the file (status verb on the terminal boundary; create at
    the right location), the on-demand `archive/` creation, and layout being a projection of
    terminal-ness (drift repair is P5).
  - Metadata — the `order` rank domain (create appends with `keyBetween(last, null)` over
    the scope-wide max valid key; `reorder` writes `keyBetween(left, right)` single-file),
    and the always-quoted `order` rule.
  - Configuration → unparseable/invalid `pj.cue` makes the scope read-only — the shared
    write-verb refuse (`config_unparseable:`, non-zero, no write; reads and sibling scopes
    unaffected). P2 shipped the `ScopeSchema` evaluation that yields this read-only state but
    had no mutating call site; the four write verbs here are that first call site. Distinct
    from the per-project `parse_error` quarantine.
- `AGENTS.md` — pure Go no cgo; external git binary; no cgo SQLite driver.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`
  (see Constraints). Adopt `ExitError` + the minimal exit map, `syscall.Flock`, and
  table-driven tests. The guide's `create` extras (`--dry-run`/`--force`/`--fields`/three-way
  match) are explicitly rejected by the design.

## Requirements

1. U19 — a git subprocess wrapper over the external `git` binary that runs the operations
   this project needs (`rev-parse --show-toplevel`, `add <paths>`, `commit -m`, status
   porcelain scoped to a dir) using the user's git, credentials, and SSH config. When `git`
   is absent from `PATH`, treat it like a missing git-root for self-commit (writes still
   land; self-commit skipped with `sync_disabled:`). No git library; a subprocess is not cgo.
2. U19 — git-root derivation on demand from a scope's `dir` (`git rev-parse --show-toplevel`),
   never stored in the registry. Several scopes whose `dir` derive the same repo share that
   git-root as their commit unit.
3. U19 — the XDG git-root operational-state directory
   `${XDG_STATE_HOME:-~/.local/state}/pj/git-roots/<key>/`, `<key>` = lowercase hex SHA-256
   of the cleaned absolute git-root path. It holds `sync.lock` (the flock target for the
   commit span; the push span is P6's) and `last-push-error` (P6 writes it; this project
   reads it for write-command warnings). Created on first need, never committed, on local
   disk (same guard as `index.db`). pj never writes under `<git-root>/.git/`; it may read
   Git-owned paths (mid-rebase markers, rev-parse, status).
4. U19 — complete-state self-commit: after a complete-state verb writes the file(s) and
   write-throughs the index row, when `autoCommit: true` and a git-root is derivable, stage
   only the specific touched path(s) with `git add` (path-scoped, never the whole-tree `git
   add -A` the design forbids, so unrelated dirt stays untouched) and `git commit` with the
   fixed message for that verb (e.g. `pj: <id> -> in-progress`, `pj: <id> reorder`, `pj: <id>
   -> done`), synchronously, no push. Select the pathspecs so both a tracked rename and a
   never-committed move commit cleanly: always stage the post-write path, and stage the
   old/removed path only when git can match it (it was tracked, or is still present).
   Passing an untracked, now-absent old path to `git add` is a hard `pathspec did not match
   any files` error (`-A` does not suppress it), so an old path that was never committed — a
   `create`d project not yet self-committed, then moved across the terminal boundary — must
   be omitted from the pathspec set rather than passed and left to error. Staging only the
   matchable paths records the deletion when the old path was tracked and skips it cleanly
   when it was not. An upstream is not required for a local commit. The commit span additionally takes the git-root `sync.lock` so two scopes in one
   repo serialize their commits. If no git-root exists, skip the commit without failing the
   write and emit `sync_disabled:` on stderr. If a git-root exists and git is present but the
   `git add`/`commit` itself fails, the file write and index write-through stand (never
   rolled back) and the command surfaces the git failure on stderr with a non-zero exit —
   distinct from the clean `sync_disabled:` skip when there is simply no git-root. `create`
   never self-commits in any mode (Requirement 8).
5. U19 — repo-driven dirty health (`autoCommit: false` inside git): after a complete-state
   write and after `pj create`, run a cheap `git status --porcelain -- <dir>` scoped to the
   scope dir; if any dirty path under the dir matches the auto-commit allowlist shape
   (project `<id>-<slug>.md` at dir root or `archive/`, `pj.cue`, `.gitignore`), emit
   `uncommitted:` with a short count on stderr. Never stage, commit, or push. Skip the signal
   if git is missing (no hard fail). Pure reads stay git-free and never carry `uncommitted:`.
6. U19 — mid-rebase command classification (auto-commit + git-root only): when the derived
   git-root is mid-rebase (stat of `.git/rebase-merge`|`rebase-apply`), refuse the
   complete-state / scaffold / integrity-rewrite classes at startup with a fail-fast error
   naming the scope and the file that paused the rebase, and allow the path-open (`edit`),
   read/diagnose, and sync-resume classes. The refuse is repo-granular among auto-commit
   siblings sharing that git-root. Non-auto-commit scopes never self-commit and keep writing
   files even when the surrounding host repo is mid-rebase. This project implements the
   refuse for its write verbs; `pj sync` resume is P6.
7. U20 — `pj create <title> [status] [--scope S]`: under the scope flock, mint an id
   (draw→check-local-ids→redraw against ids present in the scope, so two concurrent creates
   cannot draw the same id), write the locked scaffold — built-in frontmatter with `id`,
   `status` (default `draft`), `order` (append via `keyBetween(last, null)` over the
   scope-wide max valid key across all statuses and `archive/`), `created` (RFC3339 now),
   empty/omitted list keys and summary — and a body of exactly one H1 `# <title>` (slug
   frozen from that title via `slugify`). Write-through the index row and print the cleaned
   absolute path. The optional second positional is any known status for the target scope
   (unknown → exit 2); a terminal status writes the scaffold under `archive/` and emits a
   terse stderr durability note (not a closed token) that the scaffold is not git-durable
   until the sync/host boundary. Empty title after trim → usage exit 2. `create` never
   self-commits, in any mode. No `--status` flag, no create-time order flags.
8. U20 — `pj status <id> <status> [--scope S]`: a complete-state write. Under the scope
   flock for the whole reconcile→read→write span, rewrite the frontmatter `status`; when the
   new status crosses the terminal boundary (non-terminal ↔ terminal), rename the file
   between dir root and `archive/` in the same mutation, creating
   `archive/` on demand. Write-through the row and print the post-move cleaned absolute path.
   Self-commit when available, staging the new path and the removal of the old path via
   Requirement 4's conditional pathspec selection, so an old path that was never committed is
   omitted rather than passed to `git add` and left to error.
   Refuse (non-zero, no write, `parse_error:`) when the project is in `parse_error`
   quarantine. Membership is validated against the target scope's known statuses (built-in
   or CUE custom); an unknown status is a usage error (exit 2) with no write.
9. U20 — `pj reorder <id> (--before <id> | --after <id> | --first | --last) [--scope S]`: a
   complete-state write. Under the scope flock for the whole reconcile→read→write span, read
   the target neighbours from the index and write
   `keyBetween(left, right)` into the reordered project's frontmatter only (integer step
   and/or fraction growth; never renumber a band; the destination flag is required).
   `--first`/`--last` use the scope-wide min/max valid `order` (all statuses, root and
   `archive/`); `--before`/`--after` name an in-scope neighbour that must exist and carry a
   valid `order`. Neighbour-id exit codes follow the same contract as any id operand: a
   malformed neighbour id (or a missing flag value) is a usage error (exit 2, no write); a
   well-formed neighbour id that resolves to no project row is unknown-but-well-formed and
   exits generic non-zero (1, no write), the same code the reordered subject id returns for
   an unknown well-formed id. A neighbour pair that leaves no legal between (equal or invalid
   keys) is a hard failure with no write (band re-space to clear it is P5, not this verb). Write-through, print the
   post-write path, self-commit when auto-commit. Refuse on `parse_error` quarantine. Not
   cross-scope relocation and not an archive move.
10. U20 — `pj next --claim [--scope S] [--no-lens]`: a complete-state write reusing P3's
    `next` selection and eligibility. Under the scope flock for the whole span: reconcile as
    for unclaimed `next` (ambient + transitive depended-on scopes for gates), walk
    next-eligible candidates in the same order, and for each candidate re-validate it is
    still eligible from trusted state; skip a candidate that lost the race, is in a
    `duplicate_id:` set, or is in `parse_error` quarantine. On the first still-valid
    candidate, write `status: in-progress` only (no archive move), write-through, self-commit
    when auto-commit + git-root, and print the absolute path (exit 0). If no candidate
    remains, non-zero exit with the empty-queue / blocked-deps diagnostic and no file write.
    No push-on-claim, no leases, no assignee fields, no auto-steal of stale `in-progress`.
11. U20 — archive creation: the writing operation creates `<dir>/archive/` on demand (mkdir
    if missing) the first time it must place a terminal file there (terminal `create`, a
    `status` crossing into terminal). Its absence is never an error and never flagged: a
    scope with no terminal projects legitimately has no `archive/`.
12. U20 — shared write-verb refuse preconditions for all four verbs (`create`, `status`,
    `reorder`, `next --claim`), checked before any file write:
    - Scope config unparseable: if the target scope's `pj.cue` is uncompilable or
      schema-invalid, the P2 `ScopeSchema` evaluation yields the read-only/unusable state, so
      the verb exits non-zero with no file write and rides `config_unparseable:` naming the
      scope and its dir. Reads of that scope and writes to a healthy sibling scope stay
      available; only built-in statuses are known for membership until the config is fixed.
      This is the write half of P2's unparseable-`pj.cue` → scope-read-only DECISION (P4 is
      its first mutating call site), and it is independent of the per-project `parse_error`
      quarantine in Requirements 8–10 — a write may be refused by either, each with its own
      token.
    - Id-resolution refuse: `status` and `reorder` reuse P3's id resolution unchanged, so its
      `duplicate_id:` and malformed-id refuses apply — and because these are mutators, that
      refuse means no write to either side of a collision. `next --claim` resolves no external
      id and instead applies the candidate-skip form (skip `duplicate_id:` / `parse_error`
      candidates) in Requirement 10.

## Constraints

- Pure Go, no cgo: external `git` binary shelled out; SQLite via `modernc.org/sqlite`
  (from P3). Never a cgo git library or cgo SQLite driver.
- macOS/Linux only. The scope flock (`<dir>/.pj.lock`) and the git-root `sync.lock` are
  POSIX `syscall.Flock`, machine-local — they do not coordinate cross-clone/multi-machine
  writers. Cross-clone double-claim remains possible until each side syncs; local CAS
  serializes one working tree only.
- pj never creates or manages the git repo (`git init`/`remote`/`clone`) and never writes
  under `<git-root>/.git/`. It reads Git-owned paths as needed.
- `create` is the sole complete-state-shaped verb that never self-commits, for any status
  including terminal. Do not "fix" this by committing terminal creates.
- Signal handling from P2 applies to every verb; name it here because `status`/`reorder`/
  `next --claim` block on the git subprocess during self-commit, so an interrupt during a
  commit must exit `128+signum` cleanly.
- `design.md` overrides the Go CLI design guide. Carry these into this project:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | `--json` + JSON envelope | No `--json`; stdout is a path; diagnostics + closed tokens on stderr |
  | `create` three-way match / `--dry-run` / `--force` / `--fields` | None. `create` scaffolds-and-reserves; default `draft`; optional status positional only |
  | Rich exit map + `ErrorPayload.Code` | Minimal: `0` ok; `2` usage / malformed id / unknown status; unknown-but-well-formed id = generic non-zero (1) |
  | Aliases (create→add/new, move→…) | No `add`→`create`, no `move`→`reorder` in v1 |
  | `--color` / `FORCE_COLOR` | `NO_COLOR` only; stdout path never ANSI; token prefixes never coloured |
  | Interactive prompt mode | pj is strictly non-interactive; never prompts |
  | One-op-one-commit auto-push | Self-commit is local only; the push boundary is `pj sync` (P6), never automatic |

## Implementation Plan

1. Build the git subprocess wrapper and git-root derivation (U19), and the XDG
   `git-roots/<key>/` ops-state directory with `sync.lock` and the `last-push-error` reader,
   under the local-disk guard.
2. Implement complete-state self-commit and the git-root commit lock, the `sync_disabled:`
   skip when no git-root, and the mid-rebase classification/refuse for write verbs.
3. Implement the repo-driven dirty-health `uncommitted:` detect-only signal on the write
   path.
4. Implement the write verbs (U20): `create` first (scaffold, id draw under flock, append
   order, terminal-location, never self-commit), then `status` (archive-boundary move,
   post-move path, self-commit staging both paths), then `reorder` (single-file
   `keyBetween`), then `next --claim` (reuse P3 selection under flock with CAS and the
   candidate-skip rules). Add on-demand `archive/` creation to the terminal write paths.
   Gate all four verbs behind the shared refuse preconditions (Requirement 12):
   `config_unparseable:` on an unusable `pj.cue`, and the reused P3 id-resolution refuses
   (`duplicate_id:` / malformed id) on `status` and `reorder`.
5. Verify each mode end to end: pj-driven (self-commit lands a local commit; `sync_disabled:`
   when no git-root/upstream), repo-driven (`uncommitted:` after a write, no commit),
   plain-files (writes land, no git). Exercise the terminal-boundary move, the append/reorder
   order behaviour, and a concurrent double `next --claim` on one tree.

## Implementation Guidance

- Reuse P2's per-git-root autoCommit-consistency evaluation and P3's `next` selection and id
  resolution rather than reimplementing them; this project composes existing behaviour with
  the git and flock layers.
- Structure self-commit as a single reusable step keyed by (touched paths, fixed message,
  git-root) so P5's `--repair` self-commit and P6's sync snapshot commit share the same code
  path and message discipline rather than forking commit logic three ways.
- Keep the flock span honest: all four write verbs hold the scope flock across their whole
  reconcile→read→write span, not just the two whose name implies a lock — `create`'s
  draw→check→write, `status`'s and `reorder`'s reconcile→read-neighbours→write, and
  `next --claim`'s select→re-validate→write all run under it. The reconcile that feeds
  `create`'s scope-wide `order` max and `reorder`'s neighbour lookup must run inside the
  flock so a concurrent writer cannot invalidate the snapshot before the write lands. The
  git-root `sync.lock` wraps only the commit sub-span.
- Emit `sync_disabled:` and `uncommitted:` with the exact token strings from the design's
  closed table (owned as doctor's contract in P5); do not invent local variants.

## Acceptance Criteria

- On a pj-driven scope with a git-root, `pj status <id> done` rewrites frontmatter, moves the
  file into `archive/` (created on demand), prints the post-move absolute path, and produces
  one local commit that stages the new and removed paths with the fixed message — and no
  push occurs.
- On a pj-driven scope with no git-root, the same write lands on disk and in the index and
  rides `sync_disabled:` on stderr without failing.
- On a repo-driven scope, a complete-state write and a `pj create` leave the file uncommitted
  and ride `uncommitted:`; pj never stages or commits; pure reads never carry the token.
- `pj create "Title"` scaffolds `id`, `status: draft`, an append `order`, `created`, and a
  single `# Title` H1 with a frozen slug, prints the path, and never self-commits; a terminal
  `pj create "Done thing" done` writes under `archive/` and emits the terse durability note;
  an empty title exits 2.
- `pj create` appends after the scope-wide max valid `order` (including archived and all
  statuses); `pj reorder <id> --first`/`--last` place against the scope-wide min/max and
  write only the reordered file.
- `pj next --claim` claims the same project P3's `pj next` would select, writes only
  `in-progress`, self-commits when available, prints the path, skips `duplicate_id:` and
  `parse_error` candidates, and on an empty/blocked queue exits non-zero with the diagnostic
  and no write; two concurrent claims on one tree serialize on the flock and never hand off
  the same id twice.
- `status`, `reorder`, and `next --claim` refuse with `parse_error:` (non-zero, no write)
  on a quarantined project, and refuse fail-fast on a mid-rebase auto-commit git-root naming
  the blocking scope/file; `pj edit` and reads stay allowed mid-rebase.
- Every write verb (`create`, `status`, `reorder`, `next --claim`) on a scope whose `pj.cue`
  is unparseable or schema-invalid refuses with a non-zero exit, no file write, and
  `config_unparseable:` naming the scope; reads of that scope and writes to a healthy sibling
  scope are unaffected. This refuse is independent of `parse_error`.
- `pj status <id> <unknown-status>` and `pj create` with an unknown status exit 2 with no
  write; `pj status`/`pj reorder` on an id in a `duplicate_id:` set refuse (non-zero, no
  path, no write) and on a malformed id exit 2; `pj reorder --before`/`--after` with a
  malformed neighbour id or a missing flag value exits 2 with no write, and with a
  well-formed neighbour id that names no project exits generic non-zero (1) with no write.
- With `git` absent from `PATH`, complete-state writes still land and self-commit is skipped
  with `sync_disabled:`; nothing hard-fails on the missing binary.
