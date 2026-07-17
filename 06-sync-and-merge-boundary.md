# P6 Sync and merge boundary

## Goal

Make multi-machine auto-commit work: a pure frontmatter merge package and `pj sync` — the
sole push boundary — that snapshots the allowlist, fetches and rebases inline, field-merges
conflicted frontmatter, hands off `status_conflict` and body conflicts to a human, runs the
sync-time integrity step, and pushes. The merge package lands and is fixture-verified before
any live rebase wiring.

## Scope

In scope:
- U21 the frontmatter merge package: a pure 3-way merge over raw stage blobs and a scope
  schema — list/scalar/immutable rules, the status dispute path, the same-id add/add rename
  directive, and the adversarial fixture suite. No git, filesystem, index, or flock inside
  it.
- U25 `pj sync` and the merge wiring: the snapshot allowlist and single-commit snapshot,
  fetch/rebase/push, the rebase driver that loads stages and calls U21, the schema-before-data
  ordering, the `status_conflict` / body-conflict layer-4 handoff, the sync-time integrity
  step (reusing P5's repairs), the empty-eligible-set exit 0, and the `sync_disabled:` /
  non-auto-commit refuse paths.

Out of scope (named siblings own these):
- The integrity repair procedures themselves (id-collision, equal-order, archive-layout) and
  the multi-file rewrite durability contract — P5. The sync integrity step calls those; it
  does not reimplement them. The same-id add/add loser pick the merge package emits reuses
  P5's deterministic pick.
- Complete-state self-commit, git-root derivation, the git-root `sync.lock`, mid-rebase
  classification, and the write verbs — P4. `pj sync` uses P4's git wrapper and ops state and
  is the mid-rebase "sync resume" class P4 defined.
- The agent skill contract text — P7. This project must behave exactly as the skill will
  describe (end-of-turn sync only for pj-driven scopes, the conflict handoff), but the
  authoritative skill body is P7.

## Current State

P1–P5 are complete.

P1: the pure packages, including the frontmatter model, the id predicates, and the terminal
predicate; `slugify`; `order`/`keyBetween`.

P2: the CLI framework; `ScopeSchema` (name/autoCommit/knownTags/statuses/fields) which the
merge package takes as its typing input; the per-git-root autoCommit-consistency evaluation;
and the unparseable-`pj.cue` → scope-read-only rule.

P3: the SQLite index, reconcile, and the `edges` table (so inbound edges are queryable when
the integrity step surfaces `edge_verify:`).

P4: the git subprocess wrapper, git-root derivation, the XDG `git-roots/<key>/` ops state
(`sync.lock`, `last-push-error`), complete-state self-commit and its reusable commit step,
the mid-rebase classification (with the "sync resume" class allowed), the repo-driven
`uncommitted:` signal, and the write verbs. Complete-state verbs already self-commit locally;
nothing yet pushes.

P5: the deterministic repair procedures (id-collision loser pick + short-id extension +
`edge_verify:` inbound-edge report; equal-`order` re-space; archive-layout move) on the shared
multi-file rewrite durability contract, `pj scope rename`, and `pj doctor` with the
authoritative closed token catalogue.

The repo is otherwise as built by P1–P5. `pj sync` does not exist yet; no command pushes.
`design.md` is the source of truth. `pj sync` is the sole push boundary and applies only to
`autoCommit: true` scopes.

## References

- `design.md` — read these sections:
  - Merge conflict handling — the four layers; the schema-before-data ordering (resolve
    conflicted `pj.cue` before any project `.md` field-merge; an unparseable/unresolved
    sibling `pj.cue` fails closed for the whole git-root); the same-id add/add guard (rename
    repair, never field-merge); the list/immutable/scalar/status-dispute field rules; the
    layer-4 body-conflict and `status_conflict` handoff (frontmatter always resolved to clean
    YAML, markers only in the body, `--continue` refused while `status_conflict` is present,
    body markers not scanned before `--continue`); the pure merge package shape
    (`MergeFrontmatter(base, ours, theirs, schema, meta)`), its inputs/outputs, and the
    required adversarial fixture table.
  - Sync model → pj sync: the sole push boundary — the five steps (snapshot with the closed
    allowlist and single commit; fetch and integrate unconditionally; sync-time integrity
    repair; push if ahead with the fetch→push race loop; report), the ambient-vs-`--all`
    per-git-root dedup, the empty-eligible-set exit 0, the non-auto-commit refuse, and the
    `sync_disabled:` repo/upstream precondition. Also the autoCommit-consistency and
    unparseable-sibling sync preflight.
  - Metadata — the `status_conflict` transient key shape (a lexicographic-ascending
    two-element sequence of known status names) and its resolution rule.
  - Configuration (CUE) — schema-before-data; the unparseable-sibling refuse coupling at the
    git-root boundary.
- `AGENTS.md` — pure Go no cgo; external git binary; SQLite via `modernc.org/sqlite`.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`
  (see Constraints). Adopt table-driven tests on canned stage blobs for the merge package.

## Requirements

1. U21 — the frontmatter merge package as a pure function of the shape
   `MergeFrontmatter(base, ours, theirs []byte, schema ScopeSchema, meta MergeMeta)
   (Result, error)`. Inputs are the raw stage blobs (`:1` base may be empty for an add/add),
   the scope's post-integration `ScopeSchema` (built-ins + `fields`/`statuses`), and merge
   metadata the pure core needs (git author dates for both-sides scalar LWW; the add/add
   loser pick needs only the stages). Outputs are one of: clean merged frontmatter YAML; a
   `status_conflict` dispute payload; a same-id add/add rename directive (keep path / new
   path / new id); or an error. Body is out of scope — the driver attaches body and markers
   separately. No git subprocess, filesystem, index, or flock inside the package;
   deterministic for fixed inputs (including the SHA-256-of-stage-bytes residual where the
   id-repair path needs it).
2. U21 — the field rules, merged 3-way against the base stage:
   - List fields (`tags`, `depends`, `related`, `links`, and every custom `strings` field):
     3-way set merge — base plus either side's additions, minus either side's removals; an
     add/remove clash keeps.
   - Immutables (`id`, `created`): a separate class, never scalar LWW. Keep the base value
     when either side matches base or only one side differs; if both sides differ from base
     and from each other, fail the merge for that file naming the key (never pick by
     timestamp, never invent).
   - Scalars (`status`, `order`, `summary`, and custom `string`/`int`/`bool` fields): when
     exactly one side changed, take that value uncontested; when both sides changed and it is
     not a status dispute, last-writer-wins by git author date, with an equal-author-date
     residual of the greater SHA-256 of the raw stage bytes (never machine-local bias).
   - Status dispute: when both sides changed `status` from base to two different values and at
     least one value is terminal (built-in `done`/`cancelled` or a custom `category: done`),
     do not auto-merge — write `status` to the merge-base value and write the disputed pair
     into `status_conflict: [a, b]` (the two post-edit names, lexicographic ascending). Pure
     non-terminal both-sides pairs use the scalar LWW path.
   - Undeclared keys: scalar-ish LWW when both sides touch, else one-side-changed wins; not
     dropped.
   - Frontmatter is always resolved to clean YAML — never conflict markers in it.
3. U21 — the same-id add/add guard, checked before field-merge: when there is no base stage
   and both sides carry the same `id`, emit a rename directive (never field-merge) using P5's
   deterministic loser pick and short-id extension, so the two colliding projects are kept as
   two files. Refuse to field-merge without a readable schema (schema-before-data → error).
4. U21 — the adversarial fixture suite (minimum, extend freely), each on canned `:1/:2/:3`
   blobs: list add vs remove-same-tag; concurrent depends prune vs add; scalar one-side
   (`status: done` vs body-only) → take `done`; scalar both-sides non-terminal → LWW by
   author date; equal author dates → SHA-256 residual (deterministic, never "ours"); status
   dispute both terminal (`done` vs `cancelled`) → base + `status_conflict: [cancelled,
   done]`; status dispute terminal vs non-terminal (`done` vs `in-progress`) → same dispute
   path, not LWW; custom done-category dispute; custom `strings` vs custom scalar → typed
   rules; undeclared key both sides → scalar LWW, not dropped; same-id add/add → rename
   directive; call without readable schema → error; `created`/`id` immutables → keep base,
   both-sides-disagree fail closed; empty/malformed stage YAML → error/quarantine signal;
   equal both-sides scalar change → clean take, not false dispute.
5. U25 — `pj sync [--scope S] [--all]` targeting the ambient scope's git-root (or every
   auto-commit git-root, deduplicated, under `--all` or no ambient scope), applying only to
   `autoCommit: true` scopes. A non-auto-commit ambient scope refuses with a mode-named error;
   `--all` skips non-auto-commit scopes rather than erroring. When the eligible set is empty
   (no registered auto-commit scopes/git-roots), exit 0 with a terse "nothing to sync" note.
   An auto-commit scope with no git repo/upstream reports `sync_disabled:` with a
   professional message.
6. U25 — sync preflight per derived git-root: re-derive the git-root of every scope sharing
   it and refuse the whole git-root if any sibling's `pj.cue` is unparseable (autoCommit
   unreadable, same fail-closed class as a mismatch) or their declared autoCommit values
   disagree (`auto_commit_mismatch:`), rather than pushing under a violated or unverifiable
   invariant.
7. U25 — step 1 snapshot: `git status --porcelain -- <dir>...` scoped to the registered
   auto-commit dirs sharing this git-root (never the whole tree, never a co-located
   non-auto-commit scope's dir), stage every dirty path matching the closed allowlist, and
   make one commit for the whole snapshot on that git-root. The allowlist is: a project
   `<id>-<slug>.md` at dir root or as an immediate child of `archive/` (matching the full-id
   + frozen-slug shape; parseability not required to commit); `pj.cue` at the dir root; and
   `.gitignore` at the dir root. A deleted allowlisted path is staged and committed as
   `pj: remove <id>`. Everything else under the dir is non-allowlist residue: leave it
   uncommitted, never delete, and warn with `non_allowlist:` naming each path. Any `AGENTS.md`
   and `.pj.lock` are explicit non-members. Residue is a hygiene warning, not a hard stop.
8. U25 — step 2 fetch and integrate, unconditionally: always fetch; if the remote advanced,
   rebase local commits onto it, running the merge over conflicted files in schema-before-data
   order — every conflicted `pj.cue` first (ordinary git text merge; pause for a human on a
   real `pj.cue` conflict; refuse to field-merge any project `.md` in a scope whose `pj.cue`
   is still conflicted), then conflicted project `.md` files via the rebase driver. The driver
   loads the three stages (`git show :1/:2/:3`), calls U21, writes clean frontmatter, and for
   a body conflict confines git markers to the body and leaves the file unstaged so the rebase
   stays paused. For a `status_conflict` dispute it writes base `status` + `status_conflict:
   [a, b]` and leaves the file unstaged. For a same-id add/add directive it applies P5's
   rename repair, stages both files, and reports the repaired duplicate. An unresolvable
   conflict leaves a paused rebase, reported, never discarded.
9. U25 — step 3 sync-time integrity repair over the merged tree per scope touched: run P5's
   automatic repairs (id-collision rename with `edge_verify:` inbound report; equal-`order`
   re-space; archive-layout move both ways), each writing only its touched files and
   committing under a fixed message. This is the auto-commit twin of `pj doctor --repair`.
10. U25 — step 4 push synchronously (blocking) if ahead, with the fetch→push race handled by
    looping back to step 2 once more; a sync with nothing to push skips the push. Step 5
    report: unpushed count (`git rev-list --count @{u}..HEAD`), conflicts, repairs, and any
    non-allowlist residue. On a push failure, record `last-push-error` under the git-root ops
    state (P4's path) for doctor and write-command warnings; clear it on the next successful
    push.
11. U25 — the layer-4 resume contract: `pj sync` resumes a paused rebase after human
    resolution (`git rebase --continue`), refusing to continue while any `status_conflict` is
    present on a conflicted file, and it does not scan the body for marker-like lines before
    `--continue` (it stages and pushes whatever body the human left). Only the structured
    `status_conflict` key gates `--continue`.

## Constraints

- Pure Go, no cgo: external `git` binary; SQLite via `modernc.org/sqlite`. Never a cgo
  driver.
- macOS/Linux only. The merge package is a pure function — no git, filesystem, index, or
  flock inside it; all I/O lives in the rebase driver and sync steps.
- The merge package lands and passes its adversarial fixture suite before any live `pj sync`
  rebase wiring. Do not discover merge bugs only under live git.
- `pj sync` never field-merges `pj.cue` (a wrong autoCommit/schema guess is the failure the
  unparseable-config rule refuses). Never field-merge a same-id add/add pair. Never write
  conflict markers into frontmatter. Never `git init` or invent a remote.
- Frontmatter merge is arbitrated by git commit/author timestamps, not any frontmatter
  timestamp; there is no `updated:` field and the merge base is git's stage-1, never an
  in-frontmatter snapshot.
- `design.md` overrides the Go CLI design guide. Carry these into this project:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | `--json` + JSON envelope | No `--json`; sync reports text on stderr/stdout; closed tokens for machine signals |
  | Rich exit map + `ErrorPayload.Code` | Minimal: `0` ok (including empty eligible set); `2` usage; otherwise generic non-zero |
  | Auto-push per op / background push | Push only in `pj sync`, synchronous and blocking; no background/detached push |
  | `sync --force-unknown` / commit everything | No force-unknown; only allowlisted paths ride the push; residue warned, never force-committed |
  | Interactive merge/conflict prompts | Non-interactive; conflicts pause the rebase and are reported; the human resolves in-file, then re-runs `pj sync` |
  | `--color` / `FORCE_COLOR` | `NO_COLOR` only; token prefixes never coloured |

## Implementation Plan

1. Land the frontmatter merge package (U21) with its full adversarial fixture suite passing,
   including the same-id add/add rename directive (reusing P5's deterministic pick) and the
   schema-before-data error path. This is the first and gating step — no `pj sync` wiring
   before it is green.
2. Build the rebase driver: load stages, call the merge package, write clean frontmatter,
   attach body/markers separately, and apply a rename directive via P5's repair.
3. Implement `pj sync` (U25) step by step: preflight (autoCommit consistency + unparseable
   sibling refuse per git-root); step 1 snapshot with the closed allowlist and single commit;
   step 2 fetch/integrate in schema-before-data order using the driver; step 3 sync-time
   integrity repair via P5; step 4 push with the race loop; step 5 report and
   `last-push-error` handling. Wire the ambient-vs-`--all` per-git-root dedup, the empty-set
   exit 0, and the non-auto-commit / `sync_disabled:` refuse paths.
4. Implement the layer-4 resume contract (`--continue` refused while `status_conflict`
   present; body markers not scanned).
5. Verify multi-machine flows end to end against two clones: a clean fast-forward; a
   one-sided completion that lands uncontested; a both-sides terminal dispute producing
   `status_conflict`; a body conflict pausing and resuming; a same-id add/add producing two
   renamed files; an offline duplicate-id/equal-order set repaired at sync integrity; a
   non-allowlist residue warning; and an empty eligible set exiting 0.

## Implementation Guidance

- Keep the merge package's `MergeMeta` minimal and explicit (author dates in; the add/add
  path reads only the stages) so the pure core stays deterministic and testable without git.
- Route the same-id add/add loser pick and residual tie-breaks through P5's shared pure
  comparison rather than duplicating the total order in the merge package.
- The sync integrity step is P5's repair procedures unchanged; call them, do not fork them.
  Emit `edge_verify:` from the repairing operation's output exactly as P5 does.
- End-of-turn sync applies only to pj-driven scopes; ensure the non-auto-commit refuse and
  the empty-set exit 0 match what P7's skill will tell agents, so the skill text stays a
  faithful description of real behaviour.

## Acceptance Criteria

- The merge package passes the full adversarial fixture suite before any `pj sync` wiring:
  list add/remove keep; depends prune vs add; scalar one-side take-`done`; both-sides
  non-terminal LWW; equal-author-date SHA-256 residual (deterministic, never "ours");
  `done` vs `cancelled` → base + `status_conflict: [cancelled, done]`; `done` vs
  `in-progress` → same dispute path; custom done-category dispute; typed custom `strings` vs
  scalar; undeclared key LWW not dropped; same-id add/add rename directive; missing-schema
  error; `id`/`created` immutable keep-base and both-sides-disagree fail-closed;
  malformed-stage error; equal both-sides scalar change clean take.
- `pj sync` on an auto-commit scope snapshots only allowlisted dirty paths in one commit,
  warns `non_allowlist:` on residue (including any `AGENTS.md`) without committing it, commits
  a hand-deleted project file as `pj: remove <id>`, fetches and rebases, and pushes if ahead;
  a read-only machine still pulls and skips the push.
- A one-sided `pj status done` on one clone lands uncontested on the other after sync; a
  both-sides terminal change produces `status_conflict: [a, b]` in the file with base
  `status`, and `pj sync` refuses `--continue` until the key is resolved in-file (body
  markers are not scanned).
- A body-only conflict leaves a paused, reported rebase with clean field-merged frontmatter
  and markers only in the body; the next `pj sync` after human resolution continues and
  pushes.
- A same-id add/add conflict is resolved by renaming one side (P5 pick), keeping both files,
  emitting `edge_verify:` for inbound edges, and continuing the rebase — never a field-merge.
- The sync integrity step repairs an offline duplicate-id / equal-order / archive-drift set
  over the merged tree with fixed commit messages and no separate `pj doctor` run.
- A non-auto-commit ambient `pj sync` refuses with the mode-named error; `--all` with no
  auto-commit scopes exits 0 with a "nothing to sync" note; an auto-commit scope lacking
  repo/upstream reports `sync_disabled:`; a git-root with an unparseable sibling `pj.cue` or
  divergent autoCommit is refused whole (`auto_commit_mismatch:` for divergence).
