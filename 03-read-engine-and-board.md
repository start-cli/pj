# P3 Read engine and board

## Goal

Turn `pj` into a fully usable read-only tool: a machine-wide SQLite index that reconciles
from the registered scopes, the query surface (FTS5 search, the `edges` table, transitive
walks, read-only SQL), the read/locate verbs (`list`, `next` without `--claim`, `get`,
`meta`, `deps`, `search`, `query`, `edit`), and the lens/tags view. Integrity conditions
are detected and warned about on stderr; the read path never rewrites files.

## Scope

In scope:
- U13 SQLite index: the schema, WAL mode, `BUSY_TIMEOUT_MS`, `schema_version`, open/rebuild,
  and the local-disk guard.
- U14 reconcile: the two-layer mtime/size + full-rebuild model, write-through, the
  `parse_error` quarantine, body-aware conflict-marker handling, unreachable-scope
  isolation, and the `pj.cue` evaluation cache keyed by the import closure's
  `(path, mtime, size)`.
- U15 integrity detection, warn-only: `duplicate_id:`, `equal_order:`, and archive layout
  drift (`archive_non_terminal:` / `archive_terminal_at_root:`) as cheap post-reconcile
  index aggregates that ride stderr and never mutate files.
- U16 query surface: the shared `edges` table (cross-scope and dangling rows), FTS5 bm25
  search, `WITH RECURSIVE` traversal, and the read-only SQL guard for `pj query`.
- U17 read/locate verbs: `get`, `meta`, `list`, `next` (read, no `--claim`), `deps`,
  `search`, `query`, `edit`; and the `depends`-gating computation the board reads on
  (`next` eligibility, the `waiting-on` field, the empty-because-blocked diagnostic,
  held-not-surfaced, and the read-side `schema_error:` hold on malformed edges).
- U18 lens and tags: the `pj lens` verb (writes `lens.cue`), lens application in `list` and
  `next`, and the `knownTags` typo warning.

Out of scope (named siblings own these):
- All file mutation and repair. Detection warns here; the file-mutating repair for
  `duplicate_id:` / `equal_order:` / archive layout is P5 (`pj doctor --repair`) and P6
  (sync integrity). The read path must never rename or rewrite a project file.
- `pj doctor` itself and the full integrity report catalogue (self-depends, cycles,
  depends-on-cancelled, name-drift report, residue, stale-in-progress, etc.) — P5. P3
  computes the `depends` gate and surfaces the conditions reads produce (`schema_error:`,
  `depends_unresolvable:`), but the diagnose verb and its complete class coverage are P5.
- Complete-state write verbs (`create`, `status`, `reorder`, `next --claim`) and any
  self-commit — P4. `pj next --claim` reuses this project's `next` selection and
  eligibility; do not reimplement selection there.
- Git: reconcile and every verb here is git-free. `edit` opens `$EDITOR` and never commits.

## Current State

P1 and P2 are complete.

P1 provides the pure packages: id predicates, `slugify`, the `order` grammar and
`keyBetween`, the frontmatter model (with the raw fence-slice API used by `meta`), the
status set and terminal predicate (takes custom categories as input), title extraction, and
auto-name derivation.

P2 provides: the running Cobra CLI with the exit-code map, colour/TTY rules, signal
handling, and the absolute-path hand-off helper; the XDG CUE engine and the registry model
(`registry.cue`/`lens.cue`, machine-global flock, atomic writes); ambient resolution
(`--scope` > `PJ_SCOPE` > longest-prefix code-root, the no-scope error, name-drift
fail-closed); the scope verbs `init`/`import`/`rebind`/`forget`/`list`; and the `pj.cue`
schema evaluation exposing `ScopeSchema` (name/autoCommit/knownTags/statuses/fields, the
validation matrix, and the unparseable → scope-read-only behaviour). In P2 the `pj.cue`
evaluation is correct but uncached, and `pj scope forget` dropped registry and lens entries
only — this project adds the index and so realizes the index-row drop for forgotten scopes
via reconcile/rebuild. The repo is otherwise greenfield: no SQLite yet, no project read
verbs, no git integration.

`design.md` is the source of truth. Authority stays in the files; the index is a derived,
rebuildable view. Detection versus repair is a hard line: reads warn, they never mutate.

## References

- `design.md` — read these sections:
  - Read interface (SQLite index) — `modernc.org/sqlite` pure Go with FTS5, the fixed
    machine-local `index.db` path in XDG state, one DB namespaced by a `scope` column,
    write-through, schema-change-is-rebuild, WAL + `BUSY_TIMEOUT_MS = 5000`, and the
    local-disk requirement.
  - Invalidation and reconcile — the two layers, the racy-index rule, `parse_error`
    quarantine, body-aware conflict markers (FM markers → quarantine; body-only → index
    from clean FM), unreachable-scope isolation, the post-reconcile integrity detection
    (warn-only), the `pj.cue` eval cache keyed by import closure, and the detection-vs-repair
    split (doctor's report surface here is diagnose-only; `--repair` is P5).
  - Query surface — `pj search` stdout contract (bm25, TSV, parse_error hits), `pj query`
    read-only guard and non-stable schema, list filters, dependency/rollup via
    `WITH RECURSIVE`, and the `edges` table shape (full-id rows, cross-scope, dangling).
  - CLI surface — the stdout contracts for `list`, `get`, `meta`, `deps`, `search`,
    `query`, `next` (read), and `edit`, and the no-scope rules for id-taking verbs.
  - Status and dependencies — `depends` gating (cross-scope, terminal satisfaction,
    held-not-surfaced, on-disk full-id edge form, malformed-edge `schema_error:` hold), the
    `deps` verb sections and flags, `next` eligibility, and the empty-because-blocked
    diagnostic. (Doctor's full edge-hygiene report is P5.)
  - Tags and lens — the lens model, safe-by-construction rules on `next`, the lens echo on
    stderr, and the `knownTags` typo warning.
  - Done and archive — resolvable terminal projects under `archive/`; `next` never returns a
    project under `archive/`; default list hides done-class and `--all` restores.
- `AGENTS.md` — pure Go no cgo; SQLite via `modernc.org/sqlite` (FTS5 compiled in); no
  `mattn/go-sqlite3`.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`
  (see Constraints for the override list). Adopt table-driven tests, `testdata/`, and
  interfaces at the consumer for the index and reconcile boundaries.

## Requirements

1. U13 — a machine-wide SQLite index via `modernc.org/sqlite` (pure Go, FTS5 compiled in) at
   `${XDG_STATE_HOME:-~/.local/state}/pj/index.db`, opened with WAL mode and
   `BUSY_TIMEOUT_MS = 5000` (a code/test constant, not a user knob) on every connection. One
   DB holds all scopes, rows namespaced by a `scope` column, so cross-scope queries are one
   `SELECT` and FTS is one corpus. Store each file's nanosecond mtime and size and the
   last-index timestamp. A `schema_version` mismatch, a missing/corrupt DB, or an integrity
   failure triggers a full rebuild (drop, repopulate) — never an `ALTER`/migration and no
   dead columns. Guard the DB (and the git-root ops dir P4 adds) onto local disk: refuse or
   hard-warn when the parent is detected as non-local, pointing at `XDG_STATE_HOME`.
2. U14 — reconcile at the start of each command, scoped to what the command reads (a single
   ambient scope for `next`/`list`; the registered scopes a cross-scope query reads), and
   git-free. Layer 1: stat the dir root and the immediate children of the one `archive/`
   subdirectory only (no recursive walk under `archive/`), reparse files whose
   `(mtime, size)` changed, apply the racy-index rule (`mtime >= last-index` is dirty),
   delete rows for files gone from disk, index new files, and re-key (not delete) a file
   moved between dir root and `archive/` by its unchanged id, flagging `archived` when under
   `archive/`. Layer 2: full rebuild when the DB is missing/failing/`schema_version`-stale.
   Neither a per-file parse failure nor an unreachable dir is a rebuild trigger. This
   reconcile is what prunes index rows for a scope that P2's `forget` unregistered.
3. U14 — write-through is available for later mutators: a `pj` mutation upserts its own row
   right after writing the file. This project has no mutators, but the index API must expose
   the upsert path so P4 writes through it. Direct agent edits are caught by reconcile via
   mtime (the read-through half).
4. U14 — `parse_error` quarantine and body-aware markers: split the frontmatter fence from
   the body first. If the FM fence is missing/broken, the YAML fails, or conflict markers
   appear inside the frontmatter block, quarantine the row (`parse_error` flag, parser
   message, id from the filename prefix, `(mtime, size)` recorded, raw body still
   FTS-indexed). If the frontmatter parses and conflict markers are confined to the body,
   index normally from the clean FM (no quarantine). A `parse_error` project stays locatable:
   reconcile keeps it discoverable for repair; the verbs handle it per their contracts below.
   A terse `N unparseable` warning rides affected reads with `parse_error:`.
5. U14 — unreachable-scope isolation: when a scope's dir cannot be stated/opened, skip that
   scope, leave its existing rows in place (a transient unmount must not drop rows), and ride
   `unreachable_scope:` on affected reads. One token for every "dir not usable" mode. Not a
   rebuild trigger and never an auto-forget.
6. U14 — the `pj.cue` evaluation cache: cache each scope's evaluated `ScopeSchema` in the
   index keyed by the `(path, mtime, size)` of every file in that config's import closure
   (not just the entry file). Re-evaluate only when a file in the closure changed; otherwise
   deserialize the cached values. The XDG tier stays evaluated in-process each command (it is
   the bootstrap and cannot be cached in the index).
7. U15 — post-reconcile integrity detection, warn-only: over the scopes just reconciled, run
   cheap index aggregates for duplicate project ids, equal `order` keys within a scope, and
   archive layout drift, and ride the stable tokens `duplicate_id:`, `equal_order:`,
   `archive_non_terminal:`, `archive_terminal_at_root:` on stderr. Never auto-repair, never
   re-stat or re-parse — a few aggregates over materialized rows. File-mutating repair of
   these classes is out of scope (P5/P6).
8. U16 — the `edges` table: `from_id, from_scope, to_id, to_scope, kind` (`kind` in
   `depends|related`), populated by reconcile from frontmatter. `from_id`/`to_id` are always
   full project ids; a short-only frontmatter entry never enters the table. Cross-scope edges
   are ordinary rows where `from_scope != to_scope`. An edge whose `to_id` matches no project
   row is kept as a dangling row. This one table backs `pj deps`, `WITH RECURSIVE` walks, and
   the planned viewer graph.
9. U16 — `pj query <sql>`: read-only SQL over the index. Accept only read-only statements
   (`SELECT` and read-only `EXPLAIN`/`PRAGMA`); reject `INSERT`/`UPDATE`/`DELETE`/`DROP`/
   `ALTER` and any multi-statement batch containing a write, with a clear error (durable
   change is files / `pj doctor --repair`, not the DB). No ambient `--scope` flag (filter in
   SQL). The schema is explicitly not a stable API; `--help` says so and `pj query --schema`
   prints the current shape.
10. U17 — `pj list [status…] [--scope S] [--tag T]… [--all] [--no-lens]`: single-scope board
    (ambient or `--scope`; `--scope` wins). Zero or more status positionals are a union
    filter; a name is known only as a built-in or a custom in the target scope's `pj.cue`
    (unknown → exit 2; when `pj.cue` is unparseable only built-ins are known). Bare `pj list`
    is the default active set. `--tag T` repeats as OR; lens applies unless `--no-lens` (lens
    AND `--tag` when both apply); `--all` includes done/backlog and surfaces terminals under
    `archive/`. Sort `(order, id)`. One TSV line per project:
    `full-id\tstatus\ttitle\tsummary\twaiting-on` — title via the shared H1 helper (empty if
    none, no slug/summary fallback), summary from frontmatter or empty, `waiting-on` the
    space-separated sorted full ids of unmet direct `depends` (empty when all terminal). No
    path column, no order column. Empty result → exit 0, empty stdout. Lens echo and
    integrity tokens ride stderr only, never inside the TSV. Pure read, git-free.
11. U17 — `pj next [--scope S] [--no-lens]` (read, no `--claim` here): first runnable project
    by `(order, id)` — built-in `todo`, `depends` all terminal, honouring the lens, file at
    dir root (never under `archive/`), and id not in a `duplicate_id:` collision set — and
    print its absolute path. Diagnose an empty-because-blocked queue (`nothing ready; N
    todo(s) waiting on unmet deps`) distinctly from a genuinely empty queue, and report when
    the lens filters the ready queue to empty while ready work exists outside it. Pure read,
    git-free. `--claim` is P4 and reuses this exact selection and eligibility.
12. U17 — the `depends` gate this board reads on: only built-in `todo` is next-eligible and
    only when every `depends` target is terminal (built-in `done`/`cancelled` or a custom
    `category: done`). `depends` may be cross-scope; extend `next`'s reconcile to the
    transitive closure of the depended-on scopes so a cross-scope gate reads on-disk/
    local-index state (this is not a network fetch — remote freshness needs `pj sync`, which
    is P6). An unresolvable target (scope not registered here, or no matching row) is
    held-not-surfaced: the dependent stays out of `next`, its full id appears in `list`'s
    `waiting-on`, and reads may ride `depends_unresolvable:`. A frontmatter edge entry that
    fails `IsFullProjectID` does not enter `edges`, counts as unmet (holding the project out
    of `next`), and rides `schema_error:` on affected reads. Every on-disk `depends`/`related`
    entry is a full project id.
13. U17 — `pj deps <id> [--scope S] [--transitive] [--tree]` (alias `pj depends`): pure read
    over the `edges` table after reconcile; id resolution matches `get`. Default output is
    the three fixed sections — depends on, is depended on by, related (both directions,
    non-gating) — each neighbour line carrying id, status, and a short label, with `(none)`
    for empty sides. `--transitive` expands depends both ways as a flat list (related stays
    direct); `--tree` pretty-prints the depends graph (related stays a flat section after).
    Walks are cycle-safe; if the subject is in a depends cycle, print one stderr warning
    pointing at `pj doctor` (no second cycle diagram). Unknown id → non-zero, message on
    stderr, no neighbourhood on stdout. No mutation.
14. U17 — `pj get <id> [--scope S]`: resolve short (exact short-id length 4–8 in the ambient
    scope) or full id (any registered scope) to the project file path and print the cleaned
    absolute path. Under `parse_error` quarantine, when the id resolves and the file exists,
    print the path and exit 0, riding `parse_error: <id>: <message>` on stderr (locate-for-
    repair succeeds even when the header is broken). Non-zero only when no path is handed off
    (unknown id, short id with no ambient scope, unreachable scope for this id, usage → exit
    2, or the file is missing while a row exists — suggest doctor/reindex). Duplicate id →
    refuse: non-zero, no path on stdout, `duplicate_id:` naming the id and both paths.
15. U17 — `pj meta <id> [--scope S]`: read-only header inspect. Stdout is a fixed preamble
    (`id`, `title` from the H1 helper, `path` = the same absolute path `get` prints), one
    blank line, then the frontmatter interior exactly as stored (via P1's raw fence-slice
    API — no re-encode; preserve key order, quoting, comments, customs, `status_conflict`),
    never the body. Exit rules: extractable FM → exit 0 (even under `parse_error`, riding the
    token); wholly unparseable FM → non-zero, empty stdout; unknown id / short-with-no-scope
    / missing file → non-zero (usage → exit 2). `status_conflict` present → exit 0 with a
    stderr line naming the two disputed statuses. No mutation form, no key filter, no body
    dump, no aliases.
16. U17 — `pj search <terms> [--scope S]`: FTS5 bm25 over titles and bodies, machine-wide by
    default, `--scope` to bound, including archived terminals and all statuses, no lens, no
    status filter. Terms are the argv remainder (trim non-empty or usage exit 2). One TSV
    line per hit, bm25 desc then full id asc:
    `full-id\tstatus\ttitle\tsummary\tabsolute-path`. A `parse_error` hit has an empty
    `status` field but a filled path so repair stays discoverable. Empty result → exit 0,
    empty stdout. Pure read, git-free.
17. U17 — `pj edit <id> [--scope S]`: resolve the id to a path and open `$EDITOR` on it.
    Empty stdout on success (not a path-hand-off verb); errors → non-zero, message on stderr,
    empty stdout. It never rewrites frontmatter and never self-commits (editor saves are
    ordinary direct edits). It may open `parse_error` paths for human repair.
18. U18 — `pj lens [tags…] | --clear [--scope S]`: set/show/clear the machine-local default
    tag view for the resolved ambient scope, written to `lens.cue` (the data model exists
    from P2; this adds the verb). Apply the lens in `list` and `next`: an untagged project is
    never hidden; `--no-lens` bypasses; the active lens is echoed on stderr (never as a TSV
    field). Warn (`schema_warn:` typo) on a tag not in a scope's declared `knownTags` while
    still allowing free-form tags.

## Constraints

- Pure Go, no cgo: SQLite via `modernc.org/sqlite` with FTS5 compiled in. Never
  `mattn/go-sqlite3` or any cgo driver. No git in this project.
- macOS/Linux only. `index.db` and its WAL/SHM must live on local disk (WAL is unsafe on
  NFS/synced filesystems and on a separated `.db`/`-wal`); enforce the local-disk guard.
- Detection versus repair is inviolable here: no verb in this project rewrites, renames, or
  moves a project file. Integrity conditions are detected and warned about; repair is P5/P6.
- The `pj query` schema is not a stable API and must be documented as such; do not let it
  become an agent automation contract.
- `design.md` overrides the Go CLI design guide. Carry these into this project:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | `--json` + JSON envelope on every command | No `--json`, no envelope. stdout is a path or closed TSV; diagnostics + closed tokens on stderr |
  | `--color=auto\|always\|never`; honour `FORCE_COLOR`/`CLICOLOR_FORCE` | No `--color` flag; honour `NO_COLOR` only; stdout (TSV/path) never ANSI; token prefixes never coloured |
  | Rich exit map + `ErrorPayload.Code` | Minimal: `0` ok; `2` usage / malformed id / unknown status; unknown-but-well-formed id = generic non-zero (1) |
  | Aliases (get→view/show, …) | Only `scopes`→`scope` and `depends`→`deps`; `show`/`add`/`move` deferred |
  | `create`/list flags: `--dry-run`/`--force`/`--fields`/`--limit`/`--next` pagination | None. `list` has status positionals + `--tag`/`--scope`/`--all`/`--no-lens` only; no pagination, no date filters, no path column |
  | Async jobs, API client, secrets, profiles | Not applicable; registry+lens replace profiles; the CUE tiers are independent, not an override chain |
  | Agent help via `<cli> help agents` | `pj skill` is the agent contract (P7); `pj help` stays ordinary Cobra help |
  | Interactive prompt mode | pj is strictly non-interactive; never prompts |

## Implementation Plan

1. Build the index (U13): schema, WAL, `BUSY_TIMEOUT_MS`, `schema_version`, open/rebuild,
   and the local-disk guard. Expose the row model, the `edges` table, the FTS corpus, and
   the write-through upsert API (used by P4 later).
2. Build reconcile (U14): the two layers, the racy-index rule, `parse_error` quarantine and
   body-aware markers, unreachable-scope isolation, archive re-key on move, and the `pj.cue`
   eval cache keyed by the import closure. Confirm forgotten scopes are pruned on reconcile.
3. Add the post-reconcile integrity detection (U15) as warn-only aggregates with the exact
   tokens.
4. Implement the query surface (U16): FTS5 bm25 search, `WITH RECURSIVE` traversal helpers,
   and the read-only `pj query` guard with `--schema`.
5. Implement the read/locate verbs (U17) — `get`, `meta`, `search`, `list`, then `deps` and
   `next` (read) with the full `depends`-gating computation (cross-scope closure,
   held-not-surfaced, `waiting-on`, empty-because-blocked, read-side `schema_error:` hold),
   then `edit`.
6. Implement the lens/tags view (U18): the `pj lens` verb, lens application in `list`/`next`
   with the safe-by-construction rules, the lens echo on stderr, and the `knownTags` warning.
7. Verify end to end against registered scopes: reconcile after a direct file edit, a
   `parse_error` file, a body-only-marker file, an unreachable scope, a cross-scope
   `depends` gate, and a duplicate-id set; and exercise each verb's stdout/exit contract.

## Implementation Guidance

- Keep the id-resolution logic shared across `get`, `meta`, and `deps` (and reused by P4's
  id-taking mutators and P4's `next --claim`) so short/full/`duplicate_id:`/unknown/malformed
  behaviour is defined once.
- `next`'s selection is the canonical eligibility walk; structure it so P4's `next --claim`
  calls the same selection under the scope flock rather than duplicating the ordering, lens,
  archive, and collision-skip rules.
- Emit every integrity token with the exact string from the design's closed token table
  (owned as doctor's contract in P5). These same conditions are re-surfaced by doctor; do
  not fork the spellings.
- Prefer the raw fence-slice path for `meta`'s success output so it never YAML-decodes for
  stdout; tests must confirm the interior is byte-identical (comments, order, customs,
  `status_conflict`).

## Acceptance Criteria

- After a direct file edit, the next read reconciles via mtime and reflects the change with
  no git subprocess run; dropping the DB and re-running rebuilds it from the files.
- A file with conflict markers inside the frontmatter is quarantined (`parse_error:`), while
  a file with markers only in the body is indexed from clean FM and appears on the board;
  `pj get` on the quarantined file prints its path and exits 0 with `parse_error:` on
  stderr, and `pj meta` prints extractable FM (exit 0) or non-zero when the FM is wholly
  unparseable.
- An unreachable scope's rows survive (rows kept, `unreachable_scope:` on affected reads,
  no rebuild, no auto-forget); a forgotten scope's rows are pruned on the next reconcile.
- Editing an imported `schema.cue` in a scope's config closure invalidates the cached
  `ScopeSchema`; an unchanged closure serves the cached value without re-evaluating CUE.
- Post-reconcile detection rides `duplicate_id:` / `equal_order:` / archive-drift tokens on
  stderr without mutating any file, and no read verb ever renames or moves a project file.
- `pj list` prints `full-id\tstatus\ttitle\tsummary\twaiting-on` with correct default-active
  filtering, status-positional union filtering (unknown status → exit 2), `--tag` OR, lens
  AND `--tag`, `--all` restoring done/backlog and archived, and empty results exiting 0 with
  empty stdout; no ANSI or free text ever appears inside the TSV.
- `pj next` returns the first `todo` with all `depends` terminal by `(order, id)`, never a
  path under `archive/`, never a `duplicate_id:` candidate; it distinguishes an
  empty-because-blocked queue from an empty one and reports a lens-emptied ready queue.
- A cross-scope `depends` gate reads the depended-on scope's local state; an unresolvable
  or malformed-edge dependent is held out of `next`, lists the full id in `waiting-on`, and
  rides `depends_unresolvable:` / `schema_error:` as applicable.
- `pj deps` prints the three sections with `(none)` for empty sides, expands correctly under
  `--transitive`/`--tree`, is cycle-safe, and warns once (pointing at doctor) on a cycle.
- `pj search` returns bm25-ranked TSV hits including archived and `parse_error` (empty
  status) rows, exits 0 on empty results, and requires non-empty terms (usage exit 2).
- `pj query` runs `SELECT`/read-only statements and rejects any write or mutating batch with
  a clear error; `pj query --schema` prints the current shape.
- `pj edit` opens `$EDITOR`, prints nothing on success, never commits, and never rewrites
  frontmatter.
- `pj lens` sets/shows/clears the per-scope lens; an untagged project is never hidden by a
  lens; `--no-lens` bypasses; the active lens is echoed on stderr, not in stdout; a tag
  outside a scope's `knownTags` warns (`schema_warn:`) while remaining allowed.
