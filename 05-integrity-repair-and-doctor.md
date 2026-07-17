# P5 Integrity repair and doctor

## Goal

Give `pj` its integrity surface: `pj doctor` diagnoses every integrity class and, with
`--repair` / `--re-space-order`, fixes id collisions, equal `order` keys, archive layout
drift, and over-long `order` keys for every autoCommit mode; and `pj scope rename` renames a
scope end to end. Both `--repair` and `rename` share one multi-file in-scope rewrite
durability contract and the deterministic, bit-identical repair procedures the design locks.

## Scope

In scope:
- U22 multi-file rewrite durability and repair procedures: the shared in-scope rewrite
  durability contract; the id-collision repair (deterministic loser pick, deterministic
  short-id extension, edges never rewritten, `edge_verify:` inbound-edge reporting);
  equal-`order` re-space (tied files only); archive-layout move (both directions); over-long
  `order` band re-space; and crash-idempotent re-entry.
- U23 `pj scope rename <old> <new>`: the in-scope rewrite of the `pj.cue` name, every
  project id, every filename, and every in-scope edge, plus the registry/lens re-key and the
  cross-scope inbound `edge_verify:` report — built on the same durability contract as the
  repairs.
- U24 `pj doctor`: diagnose-by-default over every integrity class; the `--repair`,
  `--re-space-order`, `--reindex`, and `--all` flags; the closed doctor scope-selection
  rules; self-commit of repairs on auto-commit scopes; and the mid-rebase refuse for the
  mutating flags. This project owns the authoritative closed token catalogue.

Out of scope (named siblings own these):
- `pj sync` and its rebase/push and sync-time integrity step — P6. P6 reuses this project's
  repair procedures at sync integrity; the procedures and their determinism live here.
- The frontmatter merge package — P6. The same-id add/add rename repair the merge handler
  invokes is this project's id-collision repair; the merge package that calls it is P6.
- Read-path detection — P3 already detects and warns (`duplicate_id:`, `equal_order:`,
  archive drift) on ordinary commands without mutating. This project adds the diagnose verb
  (full class coverage) and the file-mutating repair. The detection-vs-repair line stays
  hard: bare `pj doctor` never mutates project files or `pj.cue`.

## Current State

P1–P4 are complete.

P1: the pure packages, including the collision-repair short-id extension algorithm (given a
prefix and an occupied set, produce the next unique short-id; cap 8) and the `order` grammar
and `keyBetween`.

P2: the CLI framework; the XDG CUE engine and registry (with the machine-global flock and
atomic re-key writes); ambient resolution and name-drift fail-closed; the scope verbs
`init`/`import`/`rebind`/`forget`/`list` (a `rename` placeholder is registered in the command
tree but not yet implemented); and `ScopeSchema` with its validation matrix.

P3: the SQLite index, reconcile (with the `parse_error`/body-marker/unreachable rules and
the `pj.cue` eval cache), warn-only integrity detection, the `edges` table and the query
surface (so inbound edges to any id are queryable at repair time), and the read/locate verbs
including the `depends` gate.

P4: the git subprocess wrapper, git-root derivation, the XDG `git-roots/<key>/` ops state
(`sync.lock`, `last-push-error`), complete-state self-commit (a reusable commit step keyed by
touched paths + fixed message + git-root), the mid-rebase classification/refuse for write
verbs, the repo-driven `uncommitted:` signal, and the write verbs `create`/`status`/
`reorder`/`next --claim`.

The repo is otherwise as built by P1–P4. `design.md` is the source of truth. Detection warns
on the read path (P3); this project owns the file-mutating repair and the diagnose verb.

## References

- `design.md` — read these sections:
  - Project ids — the id-collision repair procedure in full: the closed deterministic loser
    pick (`created` newer, then lexicographically greater basename, then greater SHA-256 of
    raw stage/file bytes; never machine-local bias), the deterministic short-id extension
    (no `crypto/rand`; cap 8; hard-fail on exhaustion), the never-rewrite-edges rule and the
    `edge_verify:` inbound-edge report (operation-time only, not persisted), and the
    plain-files repair-concurrency contract (one repair at a time).
  - Metadata — the equal-`order` re-space (tied files only; preserve pre-repair `(order,
    id)` relative order) and the over-long `order` band re-space (`--re-space-order` only;
    soft threshold length > 64).
  - Done and archive — archive layout drift and the both-direction repair move.
  - Scope lifecycle — `pj scope rename` semantics and the multi-file in-scope rewrite
    durability contract (scope flock for the whole plan; write-new-then-remove-old / atomic
    rename; detectable partial progress ordering; commit-after-all-writes for auto-commit;
    idempotent re-entry; no rewrite journal); and the post-share name-drift recovery.
  - Registry — name-drift fail-closed (doctor reports `name_drift:`; recovery is
    forget+import, not auto-rekey).
  - Invalidation and reconcile — doctor scope selection (report ambient-or-all; mutate
    ambient/`PJ_SCOPE`/`--all`; no `--scope`; mutating with no ambient and no `--all` → usage
    exit 2), the `--repair`/`--re-space-order`/`--reindex` behaviour, self-commit of repairs,
    and the mid-rebase refuse for mutating flags.
  - Status and dependencies — the edge/list hygiene doctor classes (self-depends hard,
    duplicate list entries and self-related soft, `links` vs project ids), depends dangling
    (same-scope hard) vs unresolvable (cross-scope informational), depends cycles,
    depends-on-cancelled, and stale-in-progress (72h, built-in only, mtime-based).
  - Agent skill contract → Doctor and integrity warnings — the authoritative closed token
    table. This project owns it as doctor's contract; every other emitting project uses these
    exact strings.
  - CLI surface — the `pj doctor` bullet (full diagnose class list and flag behaviour).
- `AGENTS.md` — pure Go no cgo; external git binary; SQLite via `modernc.org/sqlite`.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`
  (see Constraints).

## Requirements

1. U22 — the multi-file in-scope rewrite durability contract, implemented once and used by
   every multi-file integrity rewrite and by `pj scope rename`: hold the scope flock for the
   whole plan; per file prefer write-new-path-then-remove-old (or atomic rename via a temp in
   the same directory) so a kill mid-file never leaves a truncated sole copy, rewriting
   frontmatter with (or before) the path change so id and basename stay paired; order the
   plan so partial progress is detectable; for auto-commit do not `git commit` until every
   intended write for the plan has succeeded; and make re-entry idempotent where possible
   (skip files already at the target id/path, never double-extend an already-repaired
   short-id, continue remaining work). No rewrite journal, no multi-phase commit protocol,
   no automatic background healer — recovery is a re-run of the same command or `--repair`.
2. U22 — id-collision repair: choose the loser by the closed deterministic total order that
   never consults inbound edges — rename the newer by `created` (RFC3339); on equal
   timestamps, the lexicographically greater basename; on equal basenames (same-title
   add/add), the greater SHA-256 of the raw file/stage bytes. Rename the loser by the
   deterministic short-id extension (P1's algorithm over the occupied set; cap 8; hard-fail
   the repair naming both paths on exhaustion), and set its filename to
   `<new-full-id>-<same-frozen-slug>.md`. Never rewrite a `depends`/`related` entry (the kept
   side retains the original id, so nothing dangles, and reference intent is side-ambiguous).
   Report every inbound edge to the collided id — `depends` and `related`, in-scope and
   cross-scope, read from the machine-wide `edges` table at repair time — as `edge_verify:`
   lines in the operation's own output. This report is operation-time only and not persisted
   (a later bare doctor cannot re-derive it).
3. U22 — equal-`order` re-space: rewrite only the tied files, assigning distinct legal keys
   under the `order` grammar that preserve the pre-repair `(order, id)` relative order among
   the tied set and relative to untied neighbours. Do not touch untied files. This is the
   `--repair` class, separate from the over-long band re-space.
4. U22 — archive layout repair: move terminal projects into `<dir>/archive/` and non-terminal
   projects out to dir root, both directions, until layout matches status, creating
   `archive/` on demand. Idempotent, fixed commit message.
5. U22 — over-long `order` band re-space: rewrite the reported band of pathologically long
   `order` keys (soft threshold length > 64, or keys the implementation selects in the
   reported band) into shorter legal keys preserving order. This is `--re-space-order` only —
   never implied by `--repair` and never implicit on any other path.
6. U23 — `pj scope rename <old> <new>`: validate `<new>` (`^[a-z0-9]{1,12}$`,
   machine-unique), then under the scope flock rewrite in one operation the `pj.cue` `name`,
   the `<scope>-` prefix of every project id in frontmatter, every filename (mirroring the
   id), and every in-scope `depends`/`related` edge — using the U22 durability contract (for
   auto-commit, one commit after all file writes succeed). Report each cross-scope inbound
   edge (from the machine-wide `edges` table at rename time) as an `edge_verify:` line
   (target scope renamed — update this reference); those live in other scopes' repos and are
   not rewritten here. Re-key this machine's registry and lens entries only after the in-dir
   rewrite completes successfully. `rename` is not name-drift repair (post-share drift still
   needs forget+import).
7. U24 — `pj doctor [--reindex] [--repair] [--re-space-order] [--all]`: diagnose-by-default.
   Bare `pj doctor` (and `--reindex`) never mutates project files or `pj.cue`. Report every
   integrity class with the stable token where one is defined (Requirement 9). Report
   coverage: the ambient scope only when ambient resolves (`PJ_SCOPE` or cwd code-root —
   there is no `--scope` on doctor), else every registered scope.
8. U24 — the closed doctor scope-selection rules for the mutating flags: `--all` acts on
   every registered scope; else an ambient scope (`PJ_SCOPE` or cwd) acts on that scope only;
   else a usage error (exit 2) whose message names all three ways to select. `--all` wins
   over ambient when both are present. `--repair` runs the id-collision, equal-`order`, and
   archive-layout repairs (not the over-long band re-space); `--re-space-order` runs only the
   over-long band re-space. On an auto-commit scope with a git-root each repair batch
   self-commits its touched files with a fixed message (reusing P4's commit step; no push);
   without a git-root, files are written and `sync_disabled:` may ride; non-auto-commit
   writes files only. Both mutating flags refuse on a mid-rebase auto-commit git-root (same
   class as complete-state verbs); bare report still runs. `--reindex` rebuilds the index
   from files (never touches project files) and may combine with any of the above.
9. U24 — the authoritative closed token catalogue and full diagnose class coverage. Doctor
   reports, with these exact stable token prefixes, at least: `duplicate_id:`, `equal_order:`,
   `order_long:`, `parse_error:`, `unreachable_scope:`, `non_allowlist:`, `config_unparseable:`,
   `status_conflict:`, `depends_cycle:`, `depends_dangling:` (same-scope hard),
   `depends_self:` (hard), `depends_unresolvable:` (cross-scope informational),
   `depends_on_cancelled:`, `edge_verify:` (only in the repairing operation's output),
   `related_unresolvable:`, `auto_commit_mismatch:`, `archive_non_terminal:`,
   `archive_terminal_at_root:`, `sync_disabled:`, `last_push_error:`, `stale_in_progress:`
   (built-in `in-progress`, file mtime older than 72h), `name_drift:`, `uncommitted:`,
   `schema_error:` (unknown status, bad field type/`values`, an edge entry not a legal full
   id), and `schema_warn:` (undeclared key, `knownTags` typo, self-related, duplicate list
   entries, id-shaped `links`; there is no `related_self:` token). Token characters are never
   ANSI-coloured or interrupted. Adding a token is a conscious design change; do not invent
   ad-hoc prefixes. Every other project that emits a token uses the string from this table.

## Constraints

- Pure Go, no cgo: SQLite via `modernc.org/sqlite`; external `git` binary. Never a cgo
  driver.
- macOS/Linux only. The scope flock is POSIX and machine-local; it does not coordinate
  plain-files peers on Syncthing/Dropbox/NFS. The v1 contract is one repair at a time against
  a given on-disk scope tree — concurrent dual `--repair` is unsupported (not a second
  deterministic mode). On a partial/crashed rewrite, bare `pj doctor` then re-run `--repair`
  (idempotent re-entry).
- Determinism is load-bearing: the loser pick and the short-id extension must be
  bit-identical across machines (no `crypto/rand` on the repair path, no dirent order,
  "ours", mtime, or pointer identity). Two machines repairing the same quiescent collision
  set must produce identical renames and rewrites.
- Detection versus repair is a hard line: bare `pj doctor` never mutates files or `pj.cue`.
  File mutation is only `--repair` / `--re-space-order` (and, in P6, the sync integrity
  step). There is no third tier of silent heuristics and no mutate-by-default on the diagnose
  verb.
- Out of the auto-repair budget (do not add without a new DECISION): auto-rewriting inbound
  edges after a collision or rename, auto-picking a terminal status, auto-healing
  registry/`pj.cue` name drift, renumber-the-loser / max+1 id schemes, multi-file renumber on
  hot-path reorder, or any repair on pure reads.
- `design.md` overrides the Go CLI design guide. Carry these into this project:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | `--json` + JSON envelope | No `--json`; text on stderr/stdout; machine signal is the closed token set |
  | Rich exit map + `ErrorPayload.Code` | Minimal: `0` ok; `2` usage (including mutating doctor with no ambient/`--all`); otherwise generic non-zero |
  | `doctor --scope S` / rich scope flags | No `--scope` on doctor; ambient via `PJ_SCOPE`/cwd; mutation needs ambient/`PJ_SCOPE`/`--all` |
  | `--color` / `FORCE_COLOR` | `NO_COLOR` only; token prefixes never coloured or interrupted |
  | Interactive prompt / confirm-before-repair | Non-interactive; `--repair`/`--re-space-order` are the explicit opt-in; no prompt |

## Implementation Plan

1. Build the shared multi-file in-scope rewrite durability contract (U22) as one reusable
   engine (flock-held plan, safe per-file write, detectable ordering, commit-after-all,
   idempotent re-entry) that both the repairs and `rename` drive.
2. Implement the deterministic repair procedures on that engine: id-collision (loser pick +
   short-id extension + `edge_verify:` inbound report), equal-`order` re-space, archive-layout
   move, and the over-long band re-space. Land each with table-driven tests including the
   determinism cases (equal `created` → basename tie-break → SHA-256 tie-break; cap-8
   exhaustion hard-fail; idempotent re-entry after a simulated partial rewrite).
3. Implement `pj scope rename` (U23) on the same engine, with the cross-scope inbound
   `edge_verify:` report and the registry/lens re-key after a successful in-dir rewrite.
   Replace P2's `rename` command-tree placeholder.
4. Implement `pj doctor` (U24): the diagnose-by-default report over every class with the
   exact tokens; the scope-selection rules; `--repair` / `--re-space-order` wiring to the
   procedures with self-commit and the mid-rebase refuse; and `--reindex`.
5. Verify: run `--repair` on a duplicate-id set, an equal-order set, and an archive-drift
   scope across pj-driven / repo-driven / plain-files; run `--re-space-order` on an over-long
   key; run `pj scope rename` end to end; and confirm bare doctor mutates nothing and the
   scope-selection usage error fires when a mutating flag has no ambient and no `--all`.

## Implementation Guidance

- Present the repair machinery once (U22) and have both `--repair` and `pj scope rename` use
  it; do not fork durability or the `edge_verify:` inbound-edge reporting between them.
- Reuse P1's short-id extension and P4's self-commit step directly. The loser-pick total
  order and the same-id add/add tie-break are shared with P6's merge same-id add/add case —
  factor them so the merge package (P6) calls the same pure comparison rather than
  reimplementing it.
- `edge_verify:` is operation-time only and read from the live `edges` table at repair/rename
  time; do not attempt to persist it or re-derive it in a later bare doctor run.
- Preserve relative `(order, id)` order in every re-space; a re-space that reshuffles the
  board is a bug, not a repair.

## Acceptance Criteria

- `pj doctor --repair` renames the deterministic loser of a duplicate-id pair via the
  short-id extension, keeps both files, leaves every `depends`/`related` entry untouched, and
  emits an `edge_verify:` line for each inbound edge to the collided id (in-scope and
  cross-scope); two machines repairing the same quiescent collision produce identical
  renames.
- The loser pick follows `created`, then greater basename, then greater SHA-256 of raw bytes,
  and never uses machine-local bias; a same-title add/add pair (equal basename) resolves by
  the SHA-256 tie-break; cap-8 exhaustion hard-fails naming both paths.
- `pj doctor --repair` re-spaces only tied `order` files (preserving relative `(order, id)`
  order) and moves terminal/non-terminal files across the `archive/` boundary both ways;
  `--re-space-order` shortens an over-long `order` band and is never triggered by `--repair`.
- `pj scope rename old new` rewrites `pj.cue`, every id, every filename, and every in-scope
  edge in one operation, re-keys this machine's registry and lens, and reports each
  cross-scope inbound edge as `edge_verify:`; an interrupted rename re-runs idempotently.
- On an auto-commit scope with a git-root, each repair batch self-commits its touched files
  with a fixed message and no push; without a git-root it rides `sync_disabled:`;
  non-auto-commit writes files only.
- Bare `pj doctor` reports every integrity class (each with its exact token) and mutates
  nothing; a mutating flag with no ambient scope and no `--all` is a usage error (exit 2)
  naming all three selection options; `--all` wins over ambient; both mutating flags refuse
  on a mid-rebase auto-commit git-root while bare report still runs.
- The closed token catalogue is emitted with the exact prefixes and none are ANSI-coloured
  or interrupted.
