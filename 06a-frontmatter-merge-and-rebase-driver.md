# P6a Frontmatter merge and rebase driver

## Goal

Land the two pieces `pj sync` will stand on, proven before the command exists: U21, a pure
3-way frontmatter merge over raw git stage blobs, fixture-verified against an adversarial
suite; and the rebase driver that loads stages, calls U21, 3-way merges bodies, composes
clean files, and writes and stages the result. Plus the git plumbing both need, which P4
did not build. This project ships no user-facing verb — the same shape as P1, which shipped
no CLI.

## Scope

In scope:
- U21 the frontmatter merge package: a pure 3-way merge over raw stage blobs and a scope
  schema — list/scalar/immutable rules, the status dispute path, the same-id add/add rename
  directive, the delete/edit handoff, and the adversarial fixture suite. No git, filesystem,
  index, or flock inside it.
- The rebase driver: enumerate and load a conflicted path's stages, derive each side's
  per-file author date, call U21 on the whole stage blobs (U21 splits frontmatter
  internally), split each stage's body out and 3-way text merge the bodies, compose, write,
  and stage or deliberately leave unstaged. Including the same-id add/add two-file write
  through P5's rewrite contract.
- The git plumbing every step above stands on, which P4 did not build. P4's wrapper covers
  the commit side only (availability, add, commit, staged-changes, tracked, upstream probe,
  mid-rebase probe, unmerged files, dirty paths). This project adds the read/integrate/push
  operations to `internal/git`: fetch; rebase, `--continue`, and abort; push; conflict-stage
  enumeration (`git ls-files -u`) and stage reads (`git show :N:<path>`) over the stages it
  reports; a 3-way text merge over blobs (`git merge-file` or equivalent); `ls-files`; the
  per-path author date (`git log -1 --format=%aI <rev> -- <path>`); resolution of a paused
  rebase's two side revs (`HEAD` for stage `:2`, `REBASE_HEAD` for stage `:3`) that P6b pairs
  to stages and hands the driver for the author-date reads; and the unpushed count
  (`git rev-list --count @{u}..HEAD`). It also adds the dirty-path read that carries the
  porcelain status code and the `last-push-error` write and clear in `internal/gitstate`,
  which today only reads that marker. All of it lands in those two packages — no private git
  layer inside any later command.
  The plumbing P6a does not itself consume (fetch, push, unpushed count, the status-code
  dirty read, the `last-push-error` write) still lands here so `internal/git` and
  `internal/gitstate` are complete in one pass and P6b builds only command logic on top.

Out of scope (named siblings own these):
- `pj sync` itself — P6b. The command surface, the preflight, the snapshot, the
  fetch-and-integrate loop that calls this driver per rebase stop, the sync-time integrity
  step, push and report, the mid-rebase resume contract, and the lock span are all P6b's.
  This project's driver is called *by* that loop and knows nothing about it.
- The integrity repair procedures themselves (id-collision, equal-order, archive-layout) and
  the multi-file rewrite durability contract — P5. The same-id add/add loser pick this
  project's merge package emits reuses P5's deterministic pick (requirement 3), and the
  driver's two-file add/add write goes through P5's rewrite contract.
- Complete-state self-commit, git-root derivation, the scope and git-root lock primitives,
  mid-rebase classification, and the write verbs — P4. This project extends P4's git wrapper
  per Scope but changes none of its behaviour. The lock-acquisition split P6b needs is
  P6b's, not this project's: nothing here takes a lock.
- The agent skill contract text — P7.

Note on labels: earlier projects reference "P6" for the sync and merge boundary as a whole.
That work is delivered as P6a (this project) and P6b. A P6 reference to the merge package,
the rebase driver, or `internal/git`/`internal/gitstate` plumbing means P6a; a reference to
`pj sync`, its integrity step, or its push means P6b.

## Current State

P1–P5 are complete.

P1: the pure packages, including the frontmatter model, the id predicates, and the terminal
predicate; `slugify`; `order`/`keyBetween`.

P2: the CLI framework; `ScopeSchema` (name/autoCommit/knownTags/statuses/fields) which the
merge package takes as its typing input, and its evaluation cache keyed by the import
closure's `(path, mtime, size)`; the per-git-root autoCommit-consistency evaluation; and the
unparseable-`pj.cue` → scope-read-only rule.

P3: the SQLite index, reconcile, and the `edges` table (so inbound edges are queryable when
a repair surfaces `edge_verify:`).

P4: the git subprocess wrapper, git-root derivation, the XDG `git-roots/<key>/` ops state
(`sync.lock`, `last-push-error`), complete-state self-commit and its reusable commit step,
the mid-rebase classification (with the "sync resume" class allowed), the repo-driven
`uncommitted:` signal, and the write verbs. Complete-state verbs already self-commit locally;
nothing yet pushes. The wrapper is commit-side only — `Available`, `Add`, `Commit`,
`Tracked`, `HasStagedChanges`, `HasUpstream`, `MidRebase`, `UnmergedFiles`, `DirtyPaths` —
`DirtyPaths` returns paths without their porcelain status code, and `internal/gitstate`
exposes `ReadLastPushError` with no writer. Every remote and history-reading operation is
this project's to build (see Scope).

P5: the deterministic repair procedures (id-collision loser pick + short-id extension +
`edge_verify:` inbound-edge report; equal-`order` re-space; archive-layout move) on the shared
multi-file rewrite durability contract, `pj scope rename`, and `pj doctor` with the
authoritative closed token catalogue. P5's collision loser comparison is currently
unexported and reachable only through a member constructor that reads files from disk
(requirement 3 lifts it).

The repo is otherwise as built by P1–P5. No merge package, no rebase driver, no command
pushes. `design.md` is the source of truth.

## References

- `design.md` — read these sections:
  - Merge conflict handling — the four layers; the same-id add/add guard (rename repair,
    never field-merge); the list/immutable/scalar/status-dispute field rules; the
    `status_conflict`-in-the-stages and delete/edit DECISIONs; the layer-4 body-conflict and
    `status_conflict` handoff (frontmatter always resolved to clean YAML, markers only in the
    body); the pure merge package shape
    (`MergeFrontmatter(base, ours, theirs, schema, meta)`), its inputs/outputs including the
    absent-vs-present-but-empty stage rule, and the required adversarial fixture table.
  - Metadata — the `status_conflict` transient key shape (a lexicographic-ascending
    two-element sequence of known status names) and its resolution rule; `created` as the
    primary loser-pick key.
  - Core model → Project ids — the id-collision repair procedure, the closed loser pre-order,
    the deterministic short-id extension, and the same-id same-title add/add sub-case.
  - Configuration (CUE) — the evaluation cache keyed by the import closure's
    `(path, mtime, size)`, which is what makes the driver's per-file re-evaluation cheap.
  - Invalidation and reconcile — the `parse_error` quarantine and body-aware marker parse
    rule the driver's compose step exists to stay clear of.
- `AGENTS.md` — pure Go no cgo; external git binary; SQLite via `modernc.org/sqlite`.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`
  (see Constraints). Adopt table-driven tests on canned stage blobs for the merge package.

## Requirements

1. U21 — the frontmatter merge package as a pure function of the shape
   `MergeFrontmatter(base, ours, theirs []byte, schema ScopeSchema, meta MergeMeta)
   (Result, error)`. Inputs are the raw stage blobs, the scope's post-integration
   `ScopeSchema` (built-ins + `fields`/`statuses`), and merge
   metadata the pure core needs, all passed in by the driver so the package stays I/O-free:
   both sides' git author dates for scalar LWW; the scope name; and the set of short-ids
   already occupied in that scope (the driver derives it per conflicted file — requirement 6;
   it is not an index projection). The last two exist solely for the add/add path — the
   loser pick reads only the stages, but minting the loser's new id is an extension walk
   against the scope's live occupancy (`id.Extend`), which no stage blob carries. Outputs are
   one of: clean merged frontmatter YAML; a `status_conflict` dispute payload; a same-id
   add/add rename directive (which side loses, and the loser's new short-id — no paths: a
   stage blob is content, so the package cannot name a file, and path composition is driver
   work that needs no merge knowledge; the driver already holds the one path both sides
   collided on and composes the keep path and the new path from it plus the loser's frozen
   slug); a delete/edit handoff signal
   (requirement 2's last bullet); or an error. Body is out of scope for U21's *output* — but its
   *input* is still the whole stage blob per side, not a pre-split frontmatter slice: U21 splits
   internally to field-merge, and both its equal-author-date scalar residual and its same-id
   add/add loser pick hash the whole stage bytes — the same byte range P5's disk-backed repair
   hashes (whole file), so the two collision routes cannot fork. The driver merges the three
   stages' bodies and composes them with this package's output separately (requirement 6). No git
   subprocess, filesystem, index, or flock in the package; deterministic for fixed inputs
   (including the SHA-256-of-whole-stage-bytes residual where the id-repair path needs it).
   A stage that is *absent* and a stage that is *present but empty* are different inputs and
   the signature must distinguish them — a plain `[]byte` cannot, so carry presence
   explicitly (a small stage struct, or a nil-vs-empty rule stated in the doc comment and
   tested). Absence is how git reports the two structural cases this package branches on:
   an add/add has no stage 1, and a delete/modify has no stage entry at all for the deleting
   side. Neither is an empty blob. `git ls-files -u` on a delete/modify lists stages 1 and 2
   only, and `git show :3:<path>` exits fatal — "path is in the index, but not at stage 3" —
   so the driver cannot hand the package an empty slice and call it the deleted side without
   first inventing a distinction git did not make. Conflating the two collapses the
   delete/edit handoff into "one side has empty frontmatter", which field-merges to the
   surviving side and silently resurrects a deletion or discards a completion.
2. U21 — the field rules, merged 3-way against the base stage:
   - List fields (`tags`, `depends`, `related`, `links`, and every custom `strings` field):
     3-way set merge — base plus either side's additions, minus either side's removals; an
     add/remove clash keeps.
   - Immutables (`id`, `created`): a separate class, never scalar LWW. Keep the base value
     when either side matches base or only one side differs; if both sides differ from base
     and from each other, fail the merge for that file naming the key (never pick by
     timestamp, never invent). When both sides differ from base but agree with each other —
     the same illegal hand edit made on two machines — still keep base and report it as a
     doctor-class warning; agreement between two rewrites is not consent to change identity,
     and base stays the source of identity for the merge. When the base stage lacks the key
     altogether (it should not for a normal project), take the value if both sides agree and
     fail closed naming the key if they disagree.
   - Scalars (`status`, `order`, `summary`, and custom `string`/`int`/`bool` fields): when
     exactly one side changed, take that value uncontested; when both sides changed and it is
     not a status dispute, last-writer-wins by git author date, with an equal-author-date
     residual of the greater SHA-256 of the raw stage bytes (never machine-local bias).
   - Status dispute: when both sides changed `status` from base to two different values and at
     least one value is terminal (built-in `done`/`cancelled` or a custom `category: done`),
     do not auto-merge — write `status` to the merge-base value and write the disputed pair
     into `status_conflict: [a, b]` (the two post-edit names, lexicographic ascending). Pure
     non-terminal both-sides pairs use the scalar LWW path.
     Both post-edit names must be known statuses of the schema for either path to run. If
     both sides changed `status` and either post-edit value is not a known status — a typo
     or a hand edit — fail the merge for that file naming the key, the same fail-closed
     class as a both-sides immutable disagreement. The `status_conflict` shape is fixed at
     exactly two distinct known status names, so an unknown value cannot be written into the
     dispute key; and terminality of an unknown name is undecidable, so the LWW path cannot
     assert "neither side is terminal" and would risk erasing a completion. Both onward paths
     are closed, so the file goes to a human. This applies only when both sides changed: an
     unknown value arriving on the one-side-changed path is taken uncontested like any other
     scalar — the merge does not validate frontmatter, and `pj doctor` already flags an
     unknown status.
   - `status_conflict` in the stages: its own class, merge-owned, never set-merged. It is
     this package's previous output, not user data, and it is reachable in a stage — a key
     left behind by an interrupted cleanup rides an ordinary snapshot commit to the other
     machine. Under the list rule two different inherited pairs would set-merge into three
     names, a value no verb can repair and the shape (exactly two distinct known status
     names) forbids. So: if this merge yields a status dispute, write that pair and discard
     whatever the stages carried. Otherwise treat the whole sequence as one value on the
     one-side-changed shape — keep it when the sides agree or only one side carries it — and
     if the two sides carry different pairs, fail the merge for that file naming the key, the
     same fail-closed class as a both-sides immutable disagreement. Never drop an unresolved
     inherited pair while writing a `status` no human chose.
   - Undeclared keys: scalar-ish LWW when both sides touch, else one-side-changed wins; not
     dropped.
   - Frontmatter is always resolved to clean YAML — never conflict markers in it.
   - Delete/edit: a present base stage with exactly one side's stage absent is a deletion on
     that side against an edit on the other, not malformed input. It is reachable by
     construction — P6b's snapshot stages and commits a hand-deletion — so it gets
     its own outcome, distinct from the malformed-stage error: neither side is auto-resolved
     (resurrecting a deliberate deletion and discarding a completion are both silent data
     decisions), and the package emits a handoff signal naming which side deleted and the
     surviving side's post-edit `status`. The path is the driver's to attach when it reports
     (requirement 6) — a stage blob is content and cannot name a file, the same reason the
     rename directive carries no paths. Only a stage that is present but unparseable, or a
     base present with both sides absent, is the malformed-input error. A present-but-empty
     stage blob is also malformed input, not a deletion: git records a deletion by omitting
     the stage entry, so zero bytes at a live stage is a truncated or hand-mangled file.
3. U21 — the same-id add/add guard, checked before field-merge: when there is no base stage
   and both sides carry the same `id`, emit a rename directive (which side loses and the
   loser's new short-id, per requirement 1; never field-merge) so the two colliding projects
   are kept as two files. The pick and
   the extension are P5's, not a second copy: extract P5's collision loser comparison — the
   closed pre-order over created instant, basename, SHA-256 of raw bytes, path — into an
   exported pure comparison over in-memory members, and have both P5's disk-backed repair
   and this merge path call it. Neither side is materialised on disk during a rebase, so a
   comparison that reads files cannot serve this path; the two collision routes must be the
   same code, not the same intent. In an add/add both sides are one path with one basename,
   so those two terms of the pre-order are constant and only the created instant and the
   byte hash can decide; the merge package feeds the shared comparison the same placeholder
   for basename and path on both sides, which is why the comparison must take them as
   supplied values rather than deriving them. Mint the new id with `id.Extend` against the
   occupied set supplied in the merge metadata. Refuse to field-merge without a readable schema
   (schema-before-data → error).
4. U21 — the adversarial fixture suite (minimum, extend freely), each on canned `:1/:2/:3`
   blobs: list add vs remove-same-tag; concurrent depends prune vs add; scalar one-side
   (`status: done` vs body-only) → take `done`; scalar both-sides non-terminal → LWW by
   author date; equal author dates → SHA-256-of-whole-stage-blob residual, pinned by polarity:
   the expected winner is the greater-hashing stage, named in the fixture, and the case is run both ways
   round (greater as `ours`, greater as `theirs`) so determinism alone cannot satisfy it and
   an implementation that reached for P5's smaller-hash-keeps comparison fails; status
   dispute both terminal (`done` vs `cancelled`) → base + `status_conflict: [cancelled,
   done]`; status dispute terminal vs non-terminal (`done` vs `in-progress`) → same dispute
   path, not LWW; both sides changed `status` with one post-edit value not a known status →
   fail closed naming the key, never a dispute key carrying an unknown name and never LWW;
   custom done-category dispute; custom `strings` vs custom scalar → typed
   rules; undeclared key both sides → scalar LWW, not dropped; same-id add/add → rename
   directive; call without readable schema → error; `created`/`id` immutables → keep base,
   both-sides-disagree fail closed, both sides agreeing on the same non-base value → still
   keep base plus a doctor-class warning, base missing the key → agreed value taken and
   disagreement failed closed; delete/edit (base present, one side's stage absent) →
   handoff signal, not an error and not an auto-resolve, asserted with the absent side as
   each of `ours` and `theirs` in turn; present-but-empty stage blob → malformed-input
   error, not a deletion; unparseable present stage →
   error/quarantine signal;
   equal both-sides scalar change → clean take, not false dispute; inherited
   `status_conflict` on one side with no new dispute → carried through unchanged; inherited
   `status_conflict` plus a fresh dispute → the new pair only; two different inherited pairs
   with no new dispute → fail closed, never a three-name key.
5. The git plumbing named in Scope, landing in `internal/git` and `internal/gitstate` as
   ordinary tested functions with no command logic and no locking. Conflict-stage
   enumeration must report *which* stages exist for a path, not merely that the path is
   conflicted, because requirement 6 branches on absence. Rev resolution for a paused rebase's
   two sides — `HEAD` for stage `:2` and `REBASE_HEAD` for stage `:3` — also lands here as
   plumbing; the driver takes the resolved revs as inputs (requirement 6) rather than deriving
   them, and P6b calls this operation to supply them. Everything else is a thin,
   faithful wrapper over the named git invocation. The operations P6b alone consumes (fetch,
   push, unpushed count, the status-code-carrying dirty read, the `last-push-error` write and
   clear) land and are tested here even though nothing in this project calls them, so the two
   packages are finished in one pass.
6. The rebase driver, over one conflicted project `.md` at a paused rebase. It is a unit
   called per conflicted path; it does not run rebases, does not decide whether to continue
   one, and does not know how many stops a rebase has. It loads the stages, derives each
   side's author date, calls U21, writes the result, and either stages the path or
   deliberately leaves it unstaged — the caller (P6b) reads which, and decides.
   Stage loading determines which stages exist before reading any of them: read the conflict
   entry (`git ls-files -u -- <path>`, or an equivalent that enumerates stage numbers) and
   `git show :N:<path>` only the stages it lists. A missing stage is normal input — add/add
   has no `:1`, delete/modify has no entry for the deleting side — and must reach U21 as an
   absent stage (requirement 1), never as an empty blob and never as a driver error.
   Probing with `git show` and treating its non-zero exit as absence is the same thing done
   backwards: it cannot separate "this stage does not exist" from a genuine git failure
   (a corrupt object, a bad path, git gone from `PATH` mid-run), so a real fault would be
   silently reclassified as a deletion and handed to a human as a delete/edit handoff.
   Enumerate first, then read.
   The `ScopeSchema` the driver hands U21 is re-evaluated from the scope's `pj.cue` as it
   stands on disk at the moment the driver runs, per conflicted file — never a schema
   captured when the command started. That is the whole content of schema-before-data: P6b
   orders conflicted `pj.cue` resolution ahead of any project `.md`, and that ordering is
   pointless if the driver then types the field merge with a schema captured before the
   fetch, which by construction cannot know a `fields` or
   `statuses` declaration the incoming commit just added. The failure is silent and
   permanent — a field the other machine declared `strings` is scalar-LWW'd here, one side's
   list is dropped, and the result is clean YAML that stages and pushes. P2's evaluation
   cache is keyed by the import closure's `(path, mtime, size)`, so re-evaluating is cheap
   when nothing changed and correct when it did; neither this project nor P6b may add a
   longer-lived schema cache across an integrate. It needs its own driver test: a conflicted
   `.md` carrying a key that only the incoming `pj.cue` declares as `strings` is set-merged,
   not LWW'd.
   The merged file is built from the stages, never from the conflicted file git left in the
   working tree. Hand U21 the whole stage blob for each side — it splits each into frontmatter
   and body itself and field-merges the frontmatters, so its equal-author-date scalar residual
   and its same-id add/add loser pick both hash the whole stage bytes (matching `design.md` and
   P5's whole-file hash), never a frontmatter-only slice. Passing U21 a pre-split frontmatter
   would fork the pick: the shared comparison of requirement 3 would then hash a different byte
   range than P5's disk-backed repair, and because the polarity fixtures pin the winner against
   whatever bytes the package is fed, the divergence would pass every test while silently
   parting from the design's "raw stage bytes". The driver splits the same blobs itself only for
   the body: 3-way text merge the three bodies separately (`git merge-file` or an equivalent over
   the same blobs), and compose U21's clean YAML with the merged body. The
   working-tree file is a whole-file text merge whose hunks are placed by line proximity, so a
   two-sided frontmatter change — the case this merge exists for — produces a hunk that spans
   the fence, leaving no closing fence to split on and no body region to lift out; writing that
   back yields unparseable frontmatter and `parse_error` quarantine, the one outcome the design
   forbids. Splitting first keeps the two halves independent: frontmatter is always clean YAML,
   markers appear only where the prose genuinely conflicts. A body that merges cleanly gives a
   fully clean file, which is staged so the rebase can continue even though git flagged the
   path. A body conflict confines git markers to the body region and leaves the file unstaged.
   For a `status_conflict` dispute it writes base `status` + `status_conflict:
   [a, b]` and leaves the file unstaged. For a same-id add/add directive it composes both paths
   itself — the conflicted path is the keep path, and the new path is that directory plus the
   loser's new id and its frozen slug — then writes both files from the stage blobs it
   already holds: the kept side's blob at the keep path, the loser's blob with its `id`
   rewritten at the new path, through P5's multi-file rewrite durability
   contract, stages both, and reports the repaired duplicate to its caller. It never calls
   P5's disk-backed
   duplicate-id procedure: that reads index rows and the files behind them, and at this moment
   the one contested path holds a marker-laden text merge of two projects while neither side's
   clean bytes are on disk anywhere. The shared loser pick of requirement 3 is the only part of
   P5 this path uses; the directive is already the repair's answer, so the driver's job is to
   write it. The `edge_verify:` inbound-edge report for the collided id is index work and is
   not the driver's to emit: the driver returns the rename pair, and P6b records it in the
   run's report state and runs the query at the right moment.
   For a delete/edit
   handoff it stages nothing and returns the path, which side deleted it, and the surviving
   edit's `status`, for its caller to report.
   For a U21 merge that fails closed — a present-but-unparseable stage, a both-sides
   immutable disagreement, a both-sides `status` change with an unknown post-edit name, or
   two differing inherited `status_conflict` pairs — the driver stages nothing, leaves the
   path unstaged, and returns a fail-closed outcome naming the file and the key or reason U21
   reported: a fourth handoff class beside body conflict, status dispute, and delete/edit,
   and the one every merge error U21 raises lands in. It is a human-resolvable pause exactly
   like those three (`design.md`: quarantine / pause with a clear error naming the key), not
   a driver abort. It must stay distinct from an operational fault — git absent from `PATH`, a
   corrupt object, a failed stage read — which the driver surfaces as an ordinary error return
   so P6b can abort the integrate rather than parking one file for a human. Keeping the two
   apart on the way out is the mirror of the stage-enumeration rule above: a data condition
   the human resolves in-file and a broken run must never share a return channel, the same
   reason a genuine `git show` failure is never reclassified as a deletion.
   The occupied short-id set the add/add path needs is the driver's own, derived per
   conflicted file from the rebase's current state: the project files the index holds under
   that scope dir (`git ls-files`) union the project files on disk under it, short-ids taken
   from the basenames, plus a running set of every id the driver has minted so far this
   rebase. It needs no index rows. No single snapshot serves it — a set captured before the
   fetch is blind to the incoming ids that cause add/add conflicts, and a set captured once
   mid-rebase is blind to the ids the driver mints on later conflicted files in the same run;
   either produces an extension that collides with a project already present, leaving a fresh
   duplicate for the integrity step to repair, sometimes by renaming the other file. The
   "minted so far" set therefore lives across driver calls within one rebase, so the driver's
   per-rebase state is an explicit input or receiver, not a per-call local.
   The author dates are per conflicted file, not per branch: for each side, the author date
   of the last commit on that side that touched that path (`git log -1 --format=%aI <rev> --
   <path>`), with the upstream side's rev for stage `:2` and the commit being replayed for
   stage `:3`. The two revs are explicit driver inputs, each paired to its stage number and
   supplied by P6b: at a paused rebase they are `HEAD` (the upstream tip, `:2`) and
   `REBASE_HEAD` (the commit being replayed, `:3`), and resolving them is P6a plumbing —
   requirement 5's rev-resolution operation — that P6b calls before invoking the driver. The
   driver does not reach into rebase-orchestration state to derive which commit is being
   replayed; it takes the revs as data and runs `git log` per side, which also keeps its
   date-derivation and stage-to-side tests trivial to set up (any two revs in a fixture repo).
   Branch-tip dates are wrong — an unrelated commit made later on one machine
   would then decide every field of every conflicted project. The stage-to-side mapping is
   inverted from the everyday reading during a rebase (`:2` is upstream, `:3` is the local
   commit being replayed), so pair each date with the stage it came from, never with "ours".
   Both rules need driver-level tests: one where a newer unrelated commit on one branch does
   not decide another project's fields, and one asserting the stage-to-side mapping. The pure
   fixture suite takes dates as given and cannot catch either error, and both fail silently —
   clean YAML, staged, pushed, with the later edit gone.

## Constraints

- Pure Go, no cgo: external `git` binary; SQLite via `modernc.org/sqlite`. Never a cgo
  driver.
- macOS/Linux only. The merge package is a pure function — no git, filesystem, index, or
  flock inside it; all I/O lives in the driver.
- The merge package lands and passes its adversarial fixture suite before the driver is
  wired to real git. Do not discover merge bugs only under live git.
- Nothing in this project takes a lock. The driver runs inside a lock span P6b owns; adding
  acquisition here would be the same reentrancy hang P6b's requirement 8 exists to prevent.
- Never field-merge a same-id add/add pair. Never write conflict markers into frontmatter.
  The driver never field-merges `pj.cue` — config resolution is P6b's, ahead of any call
  into this driver.
- Frontmatter merge is arbitrated by git commit/author timestamps, not any frontmatter
  timestamp; there is no `updated:` field and the merge base is git's stage-1, never an
  in-frontmatter snapshot.
- `design.md` overrides the Go CLI design guide. Carry these into this project:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | `--json` + JSON envelope | No `--json`; closed tokens for machine signals |
  | Rich exit map + `ErrorPayload.Code` | Minimal: `0` ok; `2` usage; otherwise generic non-zero |
  | Interactive merge/conflict prompts | Non-interactive; an unresolvable conflict is left unstaged for the caller to surface |
  | `--color` / `FORCE_COLOR` | `NO_COLOR` only; token prefixes never coloured |

## Implementation Plan

1. Land the frontmatter merge package (U21) with its full adversarial fixture suite passing,
   including the same-id add/add rename directive (reusing P5's deterministic pick) and the
   schema-before-data error path. This is the first and gating step — no driver wiring
   before it is green. Lift P5's comparison off its file-reading member constructor as part
   of this step, and confirm P5's own repair still passes its tests by feeding the lifted
   comparison what it reads.
2. Extend `internal/git` and `internal/gitstate` with the plumbing of requirement 5 — stage
   enumeration and reads, blob text merge, fetch/rebase/continue/abort/push, `ls-files`,
   per-path author date, unpushed count, status-code-carrying dirty paths, and the
   `last-push-error` write and clear.
3. Build the rebase driver on them (requirement 6): enumerate and load the stages that exist
   (absent ≠ empty), re-evaluate the scope schema, derive each side's per-file author date,
   call the merge package on the whole stage blobs (it splits frontmatter internally), split
   each stage's body out and 3-way text merge the bodies, compose, write, and stage or leave
   unstaged. Apply a rename directive by writing both stage blobs through P5's rewrite contract.
4. Test the driver against real rebases in fixture repositories — this is the step the whole
   split exists to reach early. Cover the date derivation and the stage-to-side mapping
   (the fixture suite cannot reach them), a two-sided frontmatter change proving the
   composed file's frontmatter parses where the working-tree file git left would not, a
   delete/modify conflict reaching the driver as an absent stage rather than an error, an
   add/add producing two written and staged files, a fail-closed U21 merge returning the
   fail-closed handoff class (unstaged, key named) distinct from an operational error, and
   the occupied-id set correctly accumulating ids minted earlier in the same rebase.

## Implementation Guidance

- Keep the merge package's `MergeMeta` minimal and explicit — both sides' author dates, the
  scope name, and the occupied short-id set, nothing else, and in particular no paths — so
  the pure core stays deterministic and testable without git. The driver gathers the occupied
  set from the rebase's current index and working tree per conflicted file, never from index
  rows.
- Route the same-id add/add loser pick — and only that — through P5's shared pure
  comparison rather than duplicating the total order in the merge package. The scalar
  equal-author-date residual is a different rule and must not be routed through it: the
  collision pick keeps the side whose stage bytes hash smaller, after first comparing
  created instant and basename, while the residual takes the side whose stage bytes hash
  greater and compares nothing else. Two edits of one project share `created` and basename,
  so the shared comparison would fall straight through to the hash term and pick the
  opposite winner — clean YAML, staged, pushed, one machine's edit gone, and every
  determinism fixture still green. Implement the residual where requirement 2 states it.
  Sharing the pick means
  lifting P5's comparison off its file-reading member constructor first, so it takes bytes
  and metadata already in hand; the disk-backed repair keeps working by feeding it what it
  reads.
- Design the driver's return value for the caller P6b will write: it must be able to tell,
  without re-reading git, whether this path ended staged, and if not, which handoff class it
  is (body conflict, `status_conflict` dispute, delete/edit, or a fail-closed merge) plus the
  details that class must report. The fail-closed class carries every error U21 raises —
  unparseable stage, both-sides immutable disagreement, both-sides unknown `status`, two
  differing inherited `status_conflict` pairs — as a named, unstaged, human-resolvable pause,
  and the Go `error` return is reserved for operational faults (git gone, corrupt object,
  failed stage read) so P6b aborts on one and parks a single file on the other. A driver that
  only writes files and leaves the caller to re-derive the outcome, or that folds a
  fail-closed merge into the same `error` channel as a broken run, pushes the classification
  into the loop, where it is harder to test.

## Acceptance Criteria

- The merge package passes the full adversarial fixture suite before any driver wiring:
  list add/remove keep; depends prune vs add; scalar one-side take-`done`; both-sides
  non-terminal LWW; equal-author-date SHA-256-of-whole-stage-blob residual taking the
  greater-hashing stage, asserted with that stage on each side in turn (deterministic, never
  "ours", and never P5's smaller-hash-keeps polarity);
  `done` vs `cancelled` → base + `status_conflict: [cancelled, done]`; `done` vs
  `in-progress` → same dispute path; both-sides `status` change with an unknown post-edit
  name failing closed rather than disputing or LWW-ing; custom done-category dispute;
  typed custom `strings` vs
  scalar; undeclared key LWW not dropped; same-id add/add rename directive; missing-schema
  error; `id`/`created` immutable keep-base, both-sides-disagree fail-closed, agreed
  non-base rewrite still keep-base with a warning, and absent base key taken on agreement /
  failed closed on disagreement; delete/edit
  handoff signal from an absent stage (asserted on each side in turn), with a
  present-but-empty stage blob erroring instead; unparseable-stage error; equal both-sides
  scalar change clean take;
  inherited `status_conflict` carried, superseded by a fresh dispute, and fail-closed on two
  differing inherited pairs — never a three-name key.
- P5's id-collision repair is unchanged in behaviour and still green after its loser
  comparison is lifted into a shared exported pure function that both it and the merge
  package call.
- `internal/git` and `internal/gitstate` expose the full read/integrate/push surface of
  requirement 5, each tested directly, including stage enumeration that reports which stages
  exist for a conflicted path and a dirty-path read that carries the porcelain status code.
- Against a real paused rebase, a conflict where both sides changed frontmatter and the
  bodies merge cleanly produces a fully clean, parseable file that the driver stages — no
  fence-spanning markers, no `parse_error` quarantine — where the working-tree file git left
  would not have parsed.
- A body-only conflict is written with clean field-merged frontmatter, markers only in the
  body, and left unstaged, with the driver reporting it as a body-conflict handoff.
- A delete/modify conflict reaches the merge package as an absent stage and comes back as a
  delete/edit handoff naming the deleting side and the surviving `status`; the driver stages
  nothing and returns that to its caller. A genuine `git show` failure is an error, never
  silently reclassified as a deletion.
- A U21 fail-closed merge (unparseable stage, both-sides immutable disagreement, both-sides
  unknown `status`, or two differing inherited `status_conflict` pairs) comes back as a
  fail-closed handoff naming the file and the offending key, path left unstaged, and is
  distinct from an operational fault, which returns an ordinary error; the driver stages
  nothing in either case.
- A same-id add/add conflict is resolved by renaming one side (P5's shared pick), writing
  both files through P5's rewrite contract, staging both, and returning the rename pair for
  the caller to report — never a field-merge.
- Driver tests prove the per-file author dates and the stage-to-side mapping: a newer
  unrelated commit on one branch does not decide another project's fields, and `:2` is
  paired with upstream while `:3` is paired with the commit being replayed.
- A conflicted project `.md` carrying a key that only the incoming `pj.cue` declares as
  `strings` is set-merged, proving the driver typed the merge from the on-disk schema at
  driver time rather than one captured earlier.
- Across several conflicted files in one rebase, an id minted for an earlier add/add is in
  the occupied set the driver uses for a later one, so no extension collides with a project
  already present.
