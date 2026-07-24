# P6b pj sync and the push boundary

## Goal

Ship `pj sync` — the sole push boundary — on top of P6a's merge package, rebase driver, and
git plumbing: snapshot the allowlist in one commit, fetch and integrate unconditionally,
drive the rebase to completion across every stop it can resolve on its own, hand off
`status_conflict`, body, and delete/edit conflicts to a human, run the sync-time integrity
step, and push. Plus the lock span the whole command runs under, and the mid-rebase resume
contract.

## Scope

In scope:
- U25 `pj sync`: the command surface (`--scope`, `--all`, ambient, the empty-eligible-set
  exit 0, the non-auto-commit refuse, `sync_disabled:`), the per-git-root preflight, the
  five steps (snapshot, fetch and integrate, sync-time integrity, push, report), the
  `--all` per-root failure isolation, the layer-4 resume contract, and the lock span.
- The fetch-and-integrate loop that calls P6a's rebase driver: the schema-before-data
  ordering (conflicted `pj.cue` before any project `.md`), the per-stop procedure, and the
  continue-or-pause decision across every stop of one rebase.
- The lock-acquisition split that makes sync's span reentrant: lifting acquisition out of
  P4's shared self-commit step and P5's per-scope repair orchestration (requirement 8).

Out of scope (named siblings own these):
- The frontmatter merge package, the rebase driver, and the `internal/git` /
  `internal/gitstate` plumbing — P6a. This project calls the driver per conflicted path and
  reads its outcome; it does not re-implement stage loading, field merging, body merging,
  file composition, or the add/add two-file write. Every git invocation goes through P6a's
  wrapper — no private git layer inside the sync command.
- The integrity repair procedures themselves (id-collision, equal-order, archive-layout) and
  the multi-file rewrite durability contract — P5. The sync integrity step calls those; it
  does not reimplement them.
- Complete-state self-commit, git-root derivation, the scope and git-root lock primitives,
  mid-rebase classification, and the write verbs — P4. `pj sync` builds on P4's ops state and
  locks, and is the mid-rebase "sync resume" class P4 defined. P4 owns the locks; acquiring
  them across the sync span — and lifting acquisition out of P4's self-commit step and P5's
  repair orchestration so the span is reentrant — is this project's job (requirement 8).
- The agent skill contract text — P7. This project must behave exactly as the skill will
  describe (end-of-turn sync only for pj-driven scopes, the conflict handoffs), but the
  authoritative skill body is P7.

Note on labels: earlier projects reference "P6" for the sync and merge boundary as a whole.
That work is delivered as P6a and P6b (this project). A P6 reference to `pj sync`, its
integrity step, or its push means P6b; a reference to the merge package, the rebase driver,
or `internal/git`/`internal/gitstate` plumbing means P6a.

## Current State

P1–P5 and P6a are complete.

P1: the pure packages, including the frontmatter model, the id predicates, and the terminal
predicate; `slugify`; `order`/`keyBetween`.

P2: the CLI framework; `ScopeSchema` and its evaluation cache keyed by the import closure's
`(path, mtime, size)`; the per-git-root autoCommit-consistency evaluation; ambient
resolution including the `name_drift:` fail-close on the ambient scope; and the
unparseable-`pj.cue` → scope-read-only rule.

P3: the SQLite index, reconcile, and the `edges` table (so inbound edges are queryable when
the integrity step surfaces `edge_verify:`).

P4: the git subprocess wrapper's commit side, git-root derivation, the XDG
`git-roots/<key>/` ops state (`sync.lock`, `last-push-error`), complete-state self-commit and
its reusable commit step, the mid-rebase classification (with the "sync resume" class
allowed), the repo-driven `uncommitted:` signal, and the write verbs. Complete-state verbs
self-commit locally; nothing yet pushes.

P5: the deterministic repair procedures (id-collision loser pick + short-id extension +
`edge_verify:` inbound-edge report; equal-`order` re-space; archive-layout move) on the shared
multi-file rewrite durability contract, `pj scope rename`, and `pj doctor` with the
authoritative closed token catalogue.

P6a: the frontmatter merge package (`MergeFrontmatter`, fixture-verified, pure); the shared
exported collision loser comparison both P5's repair and the merge package call; the rebase
driver, which for one conflicted project `.md` at a paused rebase enumerates and loads the
stages that exist, re-evaluates the scope schema from disk, derives per-file author dates
with the correct stage-to-side mapping, field-merges frontmatter, 3-way merges the body,
composes and writes the file, stages it or deliberately leaves it unstaged, and returns its
outcome and handoff class to the caller; and the completed `internal/git` /
`internal/gitstate` surface — fetch, rebase/`--continue`/abort, push, conflict-stage
enumeration and stage reads, blob text merge, `ls-files`, per-path author date, unpushed
count, status-code-carrying dirty paths, and the `last-push-error` write and clear.

Two consequences of P6a to build on rather than repeat. The driver already classifies its
own outcome, so this project's loop reads a result rather than re-deriving it from git. And
the driver takes no locks and starts no rebases: the span and the rebase are this project's.

`pj sync` does not exist yet; no command pushes. `design.md` is the source of truth. `pj sync`
is the sole push boundary and applies only to `autoCommit: true` scopes.

## References

- `design.md` — read these sections:
  - Sync model → pj sync: the sole push boundary — the five steps (snapshot with the closed
    allowlist and single commit; fetch and integrate unconditionally; sync-time integrity
    repair; push if ahead with the fetch→push race loop; report), the ambient-vs-`--all`
    per-git-root dedup, the `--all` per-git-root failure isolation DECISION, the
    empty-eligible-set exit 0, the non-auto-commit refuse, and the `sync_disabled:`
    repo/upstream precondition. Also the autoCommit-consistency and unparseable-sibling sync
    preflight, the XDG git-root ops state, and the mid-rebase command classes.
  - Merge conflict handling — the schema-before-data ordering (resolve conflicted `pj.cue`
    before any project `.md` field-merge; an unparseable/unresolved sibling `pj.cue` fails
    closed for the whole git-root); the layer-4 handoffs (`status_conflict` gating
    `--continue`, body markers not scanned before `--continue`, the delete/edit handoff).
  - Metadata — the `status_conflict` transient key shape and its resolution rule.
  - Configuration (CUE) — the unparseable-sibling refuse coupling at the git-root boundary.
  - Registry — name-drift fail-closed, including the rule that a `pj sync` touching a
    drifted scope's git-root refuses that root (requirement 2).
  - Invalidation and reconcile — the `unreachable_scope:` isolation rule (requirement 2) and
    reconcile semantics the integrity step depends on.
- `docs/archive/06a-frontmatter-merge-and-rebase-driver.md` — the driver's contract and return
  shape, and the git plumbing this project calls.
- `AGENTS.md` — pure Go no cgo; external git binary; SQLite via `modernc.org/sqlite`.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`
  (see Constraints).

## Requirements

1. U25 — `pj sync [--scope S] [--all]` targeting the ambient scope's git-root (or every
   auto-commit git-root, deduplicated, under `--all` or no ambient scope), applying only to
   `autoCommit: true` scopes. `--scope S` is P2's ordinary ambient override, not a second
   selector: it names the scope whose git-root is targeted, and the run is still the
   single-root ambient case. `--scope` together with `--all` is a usage error (exit 2) —
   `design.md` is silent on the pair, and two contradictory target selectors are the
   exit-2 class rather than a silent precedence rule; flag this if the design later fixes
   it otherwise. A non-auto-commit ambient scope refuses with a mode-named error;
   `--all` skips non-auto-commit scopes rather than erroring. When the eligible set is empty
   (no registered auto-commit scopes/git-roots), exit 0 with a terse "nothing to sync" note.
   An auto-commit scope with no git repo/upstream reports `sync_disabled:` with a
   professional message, and so does an auto-commit scope on a machine with no `git` on
   `PATH` — the same token and the same fail-closed exit, never a raw exec or git error
   surfaced to the caller. P4's availability probe already answers that question; this
   command must route it to `sync_disabled:` rather than letting the first `git` call fail.
   Under `--all` the git-roots are independent units of work: visit every eligible one,
   isolate each root's outcome, and continue past a root that could not complete rather than
   returning at the first error. A refused preflight (requirement 2), a `sync_disabled:`
   root, a rebase paused for a human (requirements 4 and 7), and a failed push
   (requirement 6) each report their own line against the root they belong to, and the run
   moves on — one stale repo must not strand every other repo's push, and aborting would
   make which roots got synced depend on iteration order. The closing report names which
   roots synced and which need attention. Exit non-zero when any root ended needing a human
   or a retry; exit 0 when every visited root either synced or had nothing to do. This is
   `--all` only: an ambient sync has exactly one root, so that root's failure is the
   command's failure, and the ambient non-auto-commit refuse stays non-zero as stated above.
2. U25 — sync preflight per derived git-root: re-derive the git-root of every scope sharing
   it and refuse the whole git-root if any sibling's `pj.cue` is unparseable
   (`config_unparseable:` — autoCommit unreadable, same fail-closed class as a mismatch),
   if their declared autoCommit values disagree (`auto_commit_mismatch:`), or if any
   sibling is name-drifted (`name_drift:` — registry key ≠ `pj.cue` name), rather than
   pushing under a violated or unverifiable invariant.
   The name-drift arm is the whole-root refuse, not merely a refuse of operations scoped to
   the drifted entry. `design.md` offers both ("refuses that root … or at minimum refuses
   operations scoped to that entry"); take the stronger one, because sync is repo-granular
   in exactly the way the narrower reading assumes it is not — the snapshot sweeps every
   auto-commit dir sharing the root and pushes them under one commit, so a drifted sibling's
   files ride this push while the name that composes its allowlist commit messages and its
   integrity attribution is unverifiable. That is the same reason an unparseable sibling
   refuses the whole root. P2's `name_drift:` fail-close only covers the drifted scope when
   it is the ambient one; this preflight is what covers it as a sibling. The error names the
   drifted scope, its dir, and the `pj scope forget` + `pj scope import` recovery, exactly as
   P2's refusal does.
   A registered scope whose dir is unreachable (unmounted, deleted, permission or I/O error)
   is skipped by the preflight, not refused on and not fail-closed: report
   `unreachable_scope:` against the run and carry on. `design.md` does not cover sync against
   an unreachable dir — flagged rather than assumed — and its two governing rules pull
   opposite ways, so the resolution is stated here. Reconcile isolates an unreachable scope
   rather than escalating, but an unreadable autoCommit is elsewhere the same fail-closed
   class as a disagreeing one. The tie-break is that the preflight's premise cannot even be
   evaluated: deriving a git-root requires the dir, so an unreachable scope can never be
   shown to *share* this git-root, and fail-closing on scopes that merely might share it
   would let one unmounted drive block every sync on the machine. Skipping is also safe on
   the invariant's own terms — autoCommit consistency exists to stop the snapshot sweeping a
   non-auto-commit scope's files into an auto-commit push, and a dir pj cannot read
   contributes no files to sweep.
   The split is by derivability, not by reachability alone: if the git-root *is* derivable
   and matches this root, the scope participates, and a `pj.cue` that then fails to read is
   the ordinary `config_unparseable:` whole-root refuse above — a permission-denied `pj.cue`
   inside a reachable git-root is that case, not this one. Only a dir that cannot be stat-ed
   at all takes the skip. When the unreachable scope is the sync *target* rather than a
   sibling, it is a root that could not complete: `--all` reports it against that entry and
   continues (requirement 1), an ambient sync fails.
3. U25 — step 1 snapshot, reached only when the git-root is not mid-rebase (requirement 7
   runs first): `git status --porcelain -- <dir>...` scoped to the registered
   auto-commit dirs sharing this git-root (never the whole tree, never a co-located
   non-auto-commit scope's dir), stage every dirty path matching the closed allowlist, and
   make one commit for the whole snapshot on that git-root. The allowlist is: a project
   `<id>-<slug>.md` at dir root or as an immediate child of `archive/` (matching the full-id
   + frozen-slug shape; parseability not required to commit); `pj.cue` at the dir root; and
   `.gitignore` at the dir root. A deleted allowlisted path is dirty like any other and is
   staged as a deletion, so the committed tree converges with disk; sync mirrors what the
   human left, it never authors or polices deletions. The message is one summary for the
   whole snapshot — `pj: sync <n> path(s)` — except when the snapshot is exactly one path,
   which takes that path's class-specific form (`pj: add <id> <slug>` for an untracked
   project, `pj: edit <id>` for a modified one, `pj: remove <id>` for a deleted one with the
   id parsed from the basename, `pj: config <scope>`, `pj: gitignore <scope>`), matching what
   the write verbs already produce for a single file. Never one commit per path or per class
   to make a per-file message true — the single snapshot commit exists to keep the other
   machine's rebase from replaying a pile of tiny commits. Choosing the class needs the
   porcelain status code per path, which P6a's status-code-carrying dirty read supplies
   (P4's `DirtyPaths` drops it). Everything else under the dir is non-allowlist residue:
   leave it
   uncommitted, never delete, and warn with `non_allowlist:` in the design's form — the count
   and the dir, then the paths (`non_allowlist: N path(s) under <dir> not committed — move or
   remove; see pj doctor`). Any `AGENTS.md`
   and `.pj.lock` are explicit non-members. Residue is a hygiene warning, not a hard stop.
4. U25 — step 2 fetch and integrate, unconditionally: always fetch; if the remote advanced,
   rebase local commits onto it, resolving conflicted files in schema-before-data order —
   every conflicted `pj.cue` first (ordinary git text merge; pause for a human on a
   real `pj.cue` conflict; refuse to field-merge any project `.md` in a scope whose `pj.cue`
   is still conflicted or unreadable after that step, failing closed with the
   `config_unparseable:` class), then conflicted project `.md` files, each through P6a's
   rebase driver. The driver writes and stages, or writes and deliberately leaves unstaged,
   and returns its outcome; this loop reads that outcome and decides what happens to the
   rebase. It does not re-derive the classification from git and does not reach inside the
   driver's steps.
   That procedure is per rebase stop, not per invocation, and one `pj sync` drives the
   rebase to completion across every stop it can resolve on its own. A rebase replays local
   commits one at a time and halts at each one that conflicts, so a single integrate can
   present several independent rounds of conflicted files, and a later round's stages do not
   exist until the earlier one is resolved. So: run the per-stop procedure (conflicted
   `pj.cue` first, then conflicted project `.md`); if every conflicted path ended up staged,
   `git rebase --continue` and repeat from the top; if the rebase completes, fall through to
   step 3; if a stop leaves any path unstaged — a body conflict, a `status_conflict`
   dispute, a delete/edit handoff, or a real `pj.cue` conflict — stop there, report it, and
   exit with the rebase paused for the human. A paused rebase is reported only for that last
   case: a stop pj resolved itself is never surfaced as work for a human, and a sync that
   advanced the rebase by one commit and quit would leave the remote stale while telling an
   agent obeying the end-of-turn contract that its work is blocked. The loop needs its own
   test: a rebase of several local commits conflicting at two separate stops completes in one
   invocation and pushes.
   Report each handoff from what the driver returned: for a `status_conflict` dispute, the
   path and the two disputed statuses; for a delete/edit handoff, the path, which side
   deleted it, and the surviving edit's `status`, so the human restores or removes the file
   and re-runs `pj sync`; for a body conflict, the path. An unresolvable conflict leaves a
   paused rebase, reported, never discarded.
   For a same-id add/add the driver has already written and staged both files and returned
   the rename pair. The `edge_verify:` inbound-edge report for the collided id is this
   project's to emit, because it is index work: record the rename pair in the run's report
   state and run the query in step 3 after the reconcile when the rebase completed, or off
   the rows currently available before exiting when the rebase pauses first. It must be
   emitted in this invocation either way — the token is operation-time only and never
   persisted, and the next sync finds no duplicate to re-report because this one repaired it.
5. U25 — step 3 sync-time integrity repair over the merged tree per scope touched: run P5's
   automatic repairs (id-collision rename with `edge_verify:` inbound report; equal-`order`
   re-space; archive-layout move both ways), each writing only its touched files and
   committing under a fixed message. This is the auto-commit twin of `pj doctor --repair`.
   Reconcile each touched scope after the rebase completes and before any repair runs. P5's
   procedures are driven from index rows, not from the tree, and whatever index state this
   machine already holds describes the pre-rebase tree — the step the merged rows are needed
   for is exactly
   the step that changed which projects exist, what ids they carry, and where they sit
   relative to `archive/`. Repairing from stale rows would miss the duplicate arriving from
   the other machine, which is the main condition this step exists for. This is the only
   reconcile `pj sync` runs: no other step reads index rows — the snapshot works off
   `git status`, the driver off the stages and the working tree — so the command does not
   reconcile at startup. This reconcile serves the repairs only; the driver's occupied
   short-id set is its own (P6a), not from here.
6. U25 — step 4 push synchronously (blocking) if ahead, with the fetch→push race handled by
   looping back to step 2 once more; a sync with nothing to push skips the push. Step 5
   report: unpushed count (`git rev-list --count @{u}..HEAD`), conflicts, repairs, and any
   non-allowlist residue. On a push failure, record `last-push-error` under the git-root ops
   state (P4's path, P6a's writer) for doctor and write-command warnings; clear it on the
   next successful push.
7. U25 — the layer-4 resume contract, entered before any commit. Mid-rebase is an entry
   condition of the whole command, not a case inside step 2: after the lock span and the
   preflight, and before step 1, stat the git-root for a paused rebase
   (`.git/rebase-merge`|`rebase-apply`). If one is present, skip the snapshot entirely —
   a snapshot commit is a pj-authored commit and would land on the rebase's temporary
   HEAD, the exact write the mid-rebase freeze exists to prevent, orphaning the committed
   work when the rebase finishes — and go straight to resolution: resume with
   `git rebase --continue`, refusing to continue while any `status_conflict` is present on
   a conflicted file. Do not scan the body for marker-like lines before `--continue`; stage
   and push whatever body the human left. Only the structured `status_conflict` key gates
   `--continue`. If the rebase completes, fall through into the normal flow from step 1, so
   one `pj sync` after human resolution also snapshots the leftover dirt, runs the integrity
   step, and pushes. If it stops again, it re-enters step 2's per-stop loop (requirement 4)
   rather than exiting: a later replayed commit whose conflicts pj resolves on its own is
   resolved and continued here exactly as it would be in a fresh integrate, and only a stop
   needing a human ends the command — reported, with no commit attempted.
8. U25 — the lock contract for the whole sync span. `pj sync` acquires the `.pj.lock` of
   every scope participating in this git-root first, in sorted scope-name order, then the
   git-root `sync.lock`, and holds all of them across snapshot, fetch, integrate, integrity
   repair, and push, releasing them on every exit path including a paused rebase. The order
   is not free choice: the write verbs already take the scope lock for their whole span and
   the git-root lock inside it, both blocking and exclusive, so taking the git-root lock
   first here would deadlock sync against a concurrent `pj status`. The scope lock is what
   makes the driver's merged-file writes and the integrity step's rewrites safe against the
   write paths that stay legal mid-rebase (`pj create`, `pj edit`, direct agent file tools),
   which the mid-rebase refusal does not cover.
   Holding that span forces a reentrancy contract on the steps sync reuses. P4's shared
   self-commit step acquires the git-root lock itself (`selfcommit.Commit` and
   `selfcommit.CommitPaths` both call `gitstate.AcquireCommitLock` as their first act) and
   P5's per-scope repair orchestration acquires the scope lock itself, both unconditionally
   and both built on the
   assumption that the caller holds neither. A flock is per open file description, so a
   second acquire from the same process blocks forever rather than erroring: sync calling
   either under its own span hangs on the ordinary success path — the snapshot commit of a
   single dirty file. Split acquisition out of both. The self-commit step keeps a lock-free
   core (stage the matchable paths, commit if anything staged) with its current entry
   points as thin acquire-then-call wrappers; the per-scope repair orchestration gains the
   same split, a locks-already-held core and an acquiring wrapper. The write verbs, scope
   rename, and `pj doctor --repair` keep calling the wrappers with no behaviour change;
   `pj sync` calls the cores. The core's precondition — both locks already held — is part
   of the contract and is tested. Do not substitute a caller-supplied "locks held" flag or
   release-and-re-acquire around each commit: the first makes correctness depend on a
   boolean whose wrong value is a hang, and the second reopens the fetch→push window this
   span exists to close.

## Constraints

- Pure Go, no cgo: external `git` binary; SQLite via `modernc.org/sqlite`. Never a cgo
  driver.
- macOS/Linux only. Every git invocation goes through P6a's `internal/git` — no private git
  layer inside the sync command, and no new git plumbing here.
- `pj sync` never field-merges `pj.cue` (a wrong autoCommit/schema guess is the failure the
  unparseable-config rule refuses). Never write conflict markers into frontmatter. Never
  `git init` or invent a remote.
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

1. Lift lock acquisition out of P4's self-commit step and P5's per-scope repair
   orchestration (requirement 8). This is first: every later step commits or repairs under
   sync's own span, and a wrapper that re-acquires blocks rather than errors, so it cannot be
   retrofitted after the steps are wired. Confirm the write verbs, `pj scope rename`, and
   `pj doctor --repair` are unchanged.
2. Build the command shell: target selection and per-git-root dedup, `--scope`/`--all`
   validation, the non-auto-commit refuse, the empty-set exit 0, and `sync_disabled:`
   (requirement 1); then the preflight (requirement 2); then the lock span (scope locks in
   sorted order, then the git-root lock, released on every exit path — requirement 8).
3. The mid-rebase entry check (requirement 7) ahead of any commit, then step 1 snapshot with
   the closed allowlist and single commit (requirement 3).
4. Step 2 fetch and integrate (requirement 4): the schema-before-data ordering, the per-stop
   procedure calling P6a's driver, and the continue-or-pause loop across every stop. Test the
   multi-stop case — several replayed commits conflicting at two separate stops, resolved and
   continued within one invocation.
5. Step 3 sync-time integrity repair via P5 with its reconcile (requirement 5); step 4 push
   with the race loop and step 5 report, including `last-push-error` (requirement 6).
6. Complete the resume contract on top of the entry check: `--continue` refused while
   `status_conflict` is present, body markers not scanned, the fall-through into step 1 once
   the rebase completes, and re-entry into the per-stop loop if it stops again.
7. Wire the `--all` per-root failure isolation and its exit rule (requirement 1), then verify
   multi-machine flows end to end against two clones: a clean fast-forward; a one-sided
   completion that lands uncontested; a both-sides terminal dispute producing
   `status_conflict`; a body conflict pausing and resuming; a same-id add/add producing two
   renamed files; a hand-deletion against a concurrent edit pausing for the human; an
   offline duplicate-id/equal-order set repaired at sync integrity; a non-allowlist residue
   warning; and an empty eligible set exiting 0.

## Implementation Guidance

- The sync integrity step is P5's repair procedures unchanged; call them, do not fork them.
  The only edit they take is the acquisition split of requirement 8 — the procedures and
  their commit messages behave identically, they just stop taking a lock the caller holds.
  Emit `edge_verify:` from the repairing operation's output exactly as P5 does. The rebase
  driver's add/add path is the one exception: P6a shares P5's loser pick but not its
  disk-backed procedure, and this project emits the `edge_verify:` line for that case from
  the rename pair the driver returned (requirement 4).
- Keep the integrate loop thin. Its job is ordering, the continue-or-pause decision, and
  reporting; the per-file work is P6a's. If the loop starts re-reading stages or inspecting
  frontmatter, the seam has been crossed and the driver's tests no longer cover what runs.
- End-of-turn sync applies only to pj-driven scopes; ensure the non-auto-commit refuse and
  the empty-set exit 0 match what P7's skill will tell agents, so the skill text stays a
  faithful description of real behaviour.

## Acceptance Criteria

- `pj sync` on an auto-commit scope snapshots only allowlisted dirty paths in one commit,
  warns `non_allowlist:` on residue (including any `AGENTS.md`) without committing it, fetches
  and rebases, and pushes if ahead; a read-only machine still pulls and skips the push.
- A snapshot of several dirty paths is one commit messaged `pj: sync <n> path(s)`; a snapshot
  of exactly one path takes that path's class form — a lone hand-deleted project file commits
  as `pj: remove <id>`.
- A one-sided `pj status done` on one clone lands uncontested on the other after sync; a
  both-sides terminal change produces `status_conflict: [a, b]` in the file with base
  `status`, and `pj sync` refuses `--continue` until the key is resolved in-file (body
  markers are not scanned).
- A rebase replaying several local commits that conflicts at more than one stop is carried
  to completion by a single `pj sync` — each auto-resolvable stop is merged, staged, and
  continued without being reported as a human handoff — and the run then repairs and pushes.
- A body-only conflict leaves a paused, reported rebase with clean field-merged frontmatter
  and markers only in the body; the next `pj sync` after human resolution continues and
  pushes.
- A `pj sync` entered while the git-root is mid-rebase makes no commit before resuming: with
  an unrelated dirty allowlisted file present, the resume path leaves it uncommitted until
  the rebase completes, and the same invocation then snapshots it, repairs, and pushes —
  never a snapshot commit on the temporary HEAD.
- A hand-deletion on one clone meeting a `pj status` edit on the other pauses the rebase with
  both the deleted path and the surviving status reported, resolves nothing automatically,
  and completes on the next `pj sync` after the human restores or removes the file.
- A same-id add/add conflict arriving through the rebase ends with both files kept, one
  renamed, `edge_verify:` emitted for inbound edges to the collided id in this invocation,
  and the rebase continued — never a field-merge.
- The sync integrity step repairs an offline duplicate-id / equal-order / archive-drift set
  over the merged tree with fixed commit messages and no separate `pj doctor` run, including
  a duplicate id that arrives through the rebase and was absent before the fetch.
- `pj sync` holds every participating scope's `.pj.lock` and then the git-root `sync.lock`
  across its whole span and still completes: a snapshot commit, an integrity repair, and a
  push all run under the held locks without blocking, a concurrent `pj status` on a scope in
  that repo blocks until sync finishes rather than interleaving, and neither command
  deadlocks in either start order. The write verbs and `pj doctor --repair` are unchanged by
  the acquisition split.
- `pj sync --all` over several git-roots visits every one of them: a root that is refused by
  preflight, riding `sync_disabled:`, paused for a human, or failing its push is reported
  against that root and the remaining roots still sync and push, with the run exiting
  non-zero because a root needs attention; an `--all` run where every root synced or had
  nothing to do exits 0.
- A non-auto-commit ambient `pj sync` refuses with the mode-named error; `--all` with no
  auto-commit scopes exits 0 with a "nothing to sync" note; an auto-commit scope lacking
  repo/upstream reports `sync_disabled:`, and so does one on a machine with no `git` on
  `PATH` — the token, not a raw git error; `--scope` combined with `--all` is a usage error
  (exit 2); a git-root with an unparseable sibling `pj.cue`, a name-drifted sibling, or
  divergent autoCommit is refused whole (`config_unparseable:`, `name_drift:` naming the
  scope, its dir, and the forget+import recovery, and `auto_commit_mismatch:` respectively),
  including when the drifted or unparseable scope is a sibling rather than the ambient one.
- A registered scope whose dir is unreachable does not refuse anyone else's sync: as a
  sibling it is skipped with `unreachable_scope:` and the root still syncs; as an `--all`
  target it is reported against its own entry and the remaining roots still push. An
  unmounted drive never blocks a healthy repo.
