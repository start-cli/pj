# Agent Project Management CLI (pj) — Design

This document states the landed design after a few iterations and
rework: scopes are the unit, a machine-local registry resolves them, and one
machine-wide index serves reads.

Status legend:
- DECISION: agreed, treat as settled unless revisited.
- OPEN: not yet decided.
- IDEA: considered, worth keeping, not committed.

## Problem

Feature work is tracked in numbered markdown files (e.g. `01-architecture-review.md`
through `64-console-index-and-drilldown.md`). After the work completes the files
either rot in the repo or get deleted, losing the record. Wanted:

- Clear-text markdown files, edited in place.
- Mark work done so it is known but not read unless needed.
- Order execution; express status (pending, in progress, blocked, waiting on a
  dependency).
- Discoverable by an AI agent with no ceremony.
- Usable across a distributed environment: many machines, many repos.

The numbered-filename scheme worked except that the number coupled identity with
order — renumbering on insert/reorder was the weak point. `beans` was rejected for
forcing a temp-file hand-off (double handling). `beads` was digested in depth: a
Dolt-backed issue tracker whose storage is overkill here, but whose interface
(`ready`, status categories, path-centric CLI, one-op-one-commit, onboarding dump) is worth
borrowing. See "Borrowed from beads" and "Anti-goals".

## Vocabulary

DECISION: two levels.

- scope: one unit of tracked work with a machine-unique name — a repo's project set,
  or a personal/cross-cutting set. A scope is one directory of markdown files plus a
  `pj.cue`. It is the addressing unit and the version-control unit at once
  (store and scope are the same thing; there is no separate "store" container).
- project: one unit of work = one markdown file. "Add feature X to app/website Y."
- task: a checkbox or section inside a project file. The CLI does not model tasks.

A repo is not a "project"; it hosts a scope. "project" is kept for the unit of work
(deliberated at length previously; nothing fit feature work better), on the condition
the container is always "scope" or "repo", never "project".

## Core model

DECISION: a project is one markdown file. Tasks live inside it as checkboxes/sections
the agent edits directly. The CLI never models tasks.

DECISION: the markdown files are always the source of truth. No database is the source
of truth. The CLI edits files in place; there is no import/export or temp-file round
trip (the beans anti-pattern is explicitly avoided). Everything else — the registry
and the index — is derived or machine-local convenience, and can be rebuilt or
re-declared from the files and the user's choices.

### Project ids

DECISION: ids are `<scope>-<short-id>`, e.g. `wc-ab2c`. The scope is the scope name;
the short-id is not a content hash. `pj create` always mints a random 4-character
short-id. Collision repair may lengthen it (see Uniqueness and collisions) up to
`SHORT_ID_MAX = 8`. Ids are typed by a human, so they stay short and unambiguous.

Short-id alphabet (typeability):
- Letters (23): `abcdefghjkmnpqrstuvwxyz` (drops i, l, o).
- Digits (8): `23456789` (drops 0, 1).
- Append / enumeration order for repair (31 chars, fixed):
  `abcdefghjkmnpqrstuvwxyz23456789`.
- First character is always a letter (create and every repaired id, since repair only
  appends). Create positions 2–4 are a 50/50 coin-flip between a letter and a digit;
  repair appends use the full 31-char ordered alphabet above.
- Lowercase, alphanumeric, no symbols. Legal short-id length: 4 through 8 inclusive
  (create = 4; longer only via collision repair).

Generation and stability:
- Drawn from `crypto/rand` at `pj create`, which checks the ids already present in the
  scope and redraws on any local hit. The draw -> check -> write runs under the scope
  `flock`, so two concurrent local `pj create`s serialise and cannot both reserve the
  same id. Online creation therefore never collides; only offline-concurrent creation
  across machines can. Repair of those duplicates is file-mutating and auto-commit-aware (see
  Uniqueness and collisions below) — never implicit on the read path.
- Not derived from the title or content, so editing a title never changes the id. The
  id is stable by construction; other projects reference it by that value (`depends`,
  `related`).
- Canonical in frontmatter (`id: wc-ab2c`); the filename mirrors it as `<id>-<slug>.md`
  (e.g. `wc-ab2c-network-output-redesign.md`) for human browsing. DECISION: the slug is
  derived once at `pj create` by a pure function `slugify(title)` from the create title
  argument and is frozen for the life of the file. Retitling the work (H1 or body prose)
  does not rename the file and does not change the id. There is no frontmatter `title`
  key and no `pj retitle` verb — the human-facing name lives in the markdown H1; the path
  stays the create-time name. `pj doctor` flags a structural filename/id mismatch
  (filename does not start with the frontmatter `id` plus `-`, or the id/slug shape is
  not a project file shape), not "slug no longer matches the H1". Agents must not rename
  project files to chase a new title; paths handed off by `get`/`create`/`status` remain
  valid across retitles.

  DECISION: `slugify` is create-time wire contract (synced filenames, allowlist,
  same-title add/add), same class as the `order` grammar — not best-effort display text.
  Empty create title is a usage error (exit 2) before slugify runs; after trimming
  surrounding whitespace, title must be non-empty.

  Closed grammar for a legal slug (allowlist and doctor structural checks):
  ```
  slug = token ( "-" token )*
  token = [a-z0-9]+
  ```
  Equivalent: `^[a-z0-9]+(-[a-z0-9]+)*$`. Length 1 through `SLUG_MAX = 48` inclusive
  (UTF-8 bytes of the final ASCII slug string). No leading/trailing `-`, no `--`, no
  uppercase, no characters outside `a-z0-9-`.

  `slugify(title)` (deterministic; pure; no locale, no filesystem probe, no uniqueness
  pass — two same-title creates in one scope get the same slug and different ids):
  1. Interpret title as Unicode; normalise with NFKC.
  2. Map each character: ASCII `A-Z` → `a-z`; ASCII `a-z` and `0-9` kept; every other
     character (whitespace, punctuation, symbols, non-ASCII letters after NFKC that are
     not ASCII alnum) is a separator.
  3. Split on separator runs; join non-empty tokens with a single `-`.
  4. If the result is empty (title was only separators/symbols), use the single token `x`.
  5. If the result's byte length exceeds `SLUG_MAX` (48): truncate to at most 48 bytes.
     Prefer cutting at the last `-` whose index is ≤ 48 so the result stays a valid slug
     grammar (no trailing `-`); if no such `-` exists (one long token), hard-cut to 48
     bytes (ASCII-only at this stage, so byte = char). After truncate, strip any trailing
     `-` if a hard cut left one (should not with the prefer-`-` rule). If truncation
     yields empty, use `x`.
  6. Validate against the closed grammar; the steps above must always produce a legal
     slug — treat a failed validate as an implementation bug, not a user-facing soft
     path.

  Ship as a pure package with table-driven fixtures (empty→usage error before call;
  `"Network Output Redesign"` → `network-output-redesign`; punctuation/emoji → separators;
  CJK-only → `x` or NFKC-ASCII if any; long title → ≤48 and valid grammar; identical
  titles → identical slugs). Do not unique-ify the slug when another file already uses
  it; the id prefix makes the basename unique.
- DECISION (three human-facing names): frozen filename slug, live H1, and frontmatter
  `summary` may diverge by design. They are three labels, not one identity: slug is a
  create-time path fragment; H1 is the current human title; `summary` is optional
  one-line what/why for lists. No automatic rename, no doctor soft-warn on
  slug≠slugify(H1) or H1≠summary, no requirement that they match. Agents update H1 and
  `summary` in-file when useful; they never rename the file to chase wording.

Uniqueness and collisions:
- Because scope names are machine-unique (see "Scope names"), ids are machine-unique
  too — `wc-ab2c` addresses exactly one project anywhere.
- Raw keyspace is 23 x 31^3 ~= 685k; the even letter/digit split biases toward digits,
  lowering the effective keyspace to ~306k. Birthday collision odds (~1.6% at ~100
  ids, ~0.15% at ~30) apply only to uncoordinated draws. `pj create` redraws on a local
  hit, so the only uncoordinated draws are offline-concurrent creates on two machines
  before a sync or external file-sync — a small window for a single user, so a real
  collision is near-never.
- Resolution is reference-safe within the scope, and surfaced (not silently rewritten)
  across scopes. An offline-concurrent duplicate with distinct titles produces no git
  conflict: the filename is `<id>-<slug>.md` and the slug derives from each create-time
  title, so two same-id projects with different titles land at different paths and the
  rebase merges them clean.
- Detect vs repair (all scopes): after reconcile, pj runs a cheap index query over the
  scopes just reconciled (duplicate `id` rows; equal `order` keys). Hits ride a terse
  warning on the command (stderr) — they never rewrite files on a read
  command. File-mutating repair is confined to:
  - auto-commit: the integrity step at the end of `pj sync` (automatic after integrate), and
  - every scope: `pj doctor --repair`, which runs the identical automatic repairs off-sync
    (the only repair path for non-auto-commit scopes, which have no `pj sync` seam).
    Bare `pj doctor` is report-only (see CLI surface).
  Plain-files multi-machine (Dropbox/Syncthing/NFS) is supported on that basis: no sync
  engine, but the same disk-visible duplicates are detected every command and repaired
  when the user or agent runs `pj doctor --repair`. `pj skill` tells agents to run bare
  `pj doctor` when warnings appear (and periodically for plain-files), then `--repair`
  when acting on `duplicate_id:` / `equal_order:`. External sync may also drop
  vendor conflict-copy names that do not match `<id>-<slug>.md`; those never enter the id
  namespace — reconcile leaves them unindexed (or `parse_error` if they look like projects),
  and `pj doctor` flags non-allowlist residue under the dir for human cleanup (auto-commit
  snapshot never commits them either; see "pj sync").
- Repair procedure (sync integrity and `pj doctor --repair`):
  - Choose the side to rename by inbound `depends`, checked both in-scope and — via the
    machine-wide `edges` table — cross-scope: rename the side nothing depends on,
    preserving a referenced id. Cross-scope inbound weighs at least as heavily as in-scope,
    because the repair can rewrite in-scope edges but not cross-scope ones, so a
    cross-scope-referenced id is the more valuable one to keep. If both or neither are
    referenced, rename the newer by `created:` (RFC3339 timestamp set at `pj create`; see
    Metadata). If the timestamps are equal — same second, or clock skew that lands on the
    same instant — fall through a residual total-order that does not use the id string
    (both sides share it by definition of the collision): rename the side whose basename
    (`<id>-<slug>.md`) is lexicographically greater; if basenames are equal (same-title
    add/add: one path, two git stages), rename the side whose SHA-256 of the raw stage
    bytes is lexicographically greater. Basename and content digest need no new
    frontmatter, are available for both the dual-file and add/add cases, and always
    total-order the pair, so the repair never stalls or picks non-deterministically.
    Do not use machine-local bias (dirent order, "ours", mtime, pointer identity) — two
    machines repairing the same collision must pick the same loser.
  - Rename by a deterministic short-id extension that stays unique in-scope and
    bit-identical across machines (no `crypto/rand` on the repair path — plain-files
    dual `pj doctor --repair` must mint the same new id). Procedure:
    - Let `prefix` be the loser's current short-id (length N, normally 4; may already
      be longer after a prior repair). Keep the recognisable prefix; only append.
    - Occupied set: every short-id already present in the scope (both collision sides
      and every other project), taken from frontmatter/index before any rename write.
    - Short-id alphabet for appended characters (ordered, closed): the same 31 typeable
      chars as create positions 2–4, in this fixed order:
      `abcdefghjkmnpqrstuvwxyz23456789`.
    - Cap: `SHORT_ID_MAX = 8` (create always mints length 4; repair may grow through 8).
    - For target length L = N+1, N+2, …, `SHORT_ID_MAX`: enumerate every extension string
      of length `L-N` over that alphabet in lexicographic order; candidate short-id is
      `prefix + extension`. Take the first candidate not in the occupied set.
    - Normal case: N=4, first free single-char extension → length 5. If that length is
      fully blocked (all 31 one-char extensions taken — absurd outside tests), grow L.
    - Same procedure when the loser is already length 5+ (second collision or clash with
      an existing extended id): grow again under the same rules.
    - If no free candidate exists at any L ≤ `SHORT_ID_MAX`, hard-fail the repair with a
      doctor-class error naming both paths — do not invent a non-prefix id, do not use
      max+1, do not silently skip. Exhaustion is a compound near-never of near-never;
      the fail-closed path is the budget, not a human renumber scheme.
    - New full id is `<scope>-<new-short-id>`; filename becomes
      `<new-full-id>-<same-frozen-slug>.md`.
  - In the same operation (same repo, same commit) rewrite every in-scope
    `depends`/`related` that pointed at the renamed id, so no in-scope edge
    dangles.
  - Cross-scope edges pointing at the renamed side live in another scope's repo and cannot
    be rewritten in this repo's commit, so the repair does not touch them. Note the hazard
    they carry: because the kept side retains the original id, a cross-scope reference that
    meant the kept side stays correct, but one that meant the renamed side now resolves to
    a different project — a silent mispoint, not a visible dangle. So the repair records
    every cross-scope inbound edge to the collided id and `pj doctor` flags each for human
    confirmation ("target was collision-repaired — verify this reference"), converting a
    silent wrong-edge into a surfaced check. Detection is best-effort (it sees cross-scope
    referrers the index already knows), which is proportionate to a compound near-never
    event: a newborn duplicate id that also acquired a cross-scope reference before its
    first sync.
  - Report the rename and any flagged cross-scope references. Other external references it
    cannot reach (PR text, agent memory) are a small, surfaced cost — the id is newborn at
    first sync, so its reference blast radius is minimal.
  No counter, no max+1, no renumber-the-loser.

Same-id, same-title sub-case (no clean merge): when the two colliding projects also share
a title they share a slug, so both write the identical path and the rebase produces a git
add/add conflict on one file rather than two clean files. This must not be field-merged —
the two stages are distinct pieces of work that happen to collide on id and path, and
folding their frontmatter together would silently collapse two projects into one and lose
one. The merge handler detects it (an add/add conflict, no base stage, both sides carrying
the same `id`) and resolves it automatically with the same deterministic short-id
extension repair above — keeping both files, renaming one side, staging both so the
rebase continues, and reporting the duplicate — never a field-merge (see "Merge conflict
handling", layer 3).
The id draw is from `crypto/rand`, independent of the title, so this compound stays gated
by the near-never id collision; the guard exists so that when it does occur the outcome is
two preserved projects, not one silently merged.

DECISION: auto-repair budget (closed set). Near-never multi-machine events still need an
explicit policy so the design does not grow beads-scale recovery machinery. Detect-on-
reconcile warnings stay universal and never mutate files. File mutation is only on the
seams already named (`pj sync` integrity for auto-commit; `pj doctor --repair` for every
scope). Bare `pj doctor` never mutates files. Each mutating path is either automatic
(deterministic; bit-identical across machines) or surfaced-for-human — no third tier of
silent heuristics; no mutate-by-default on the diagnose verb.

| Path | Policy | Why this tier |
|---|---|---|
| Post-reconcile duplicate-id / equal-order detect | Warn only (every command) | Read path must not rewrite files; agents learn without waiting for sync |
| Offline-concurrent id collision (dual file or `doctor --repair`) | Automatic deterministic short-id extension (unique in-scope, bit-identical, cap 8) + in-scope edge rewrite; bit-identical loser pick | Flat id namespace is load-bearing; in-scope references must not dangle; plain-files dual `--repair` is not serialised by git, so machine-local bias ("ours", mtime, dirent) or `crypto/rand` on the new id would diverge |
| Same-id same-title add/add mid-rebase | Automatic same extension repair (never field-merge) | Rebase must progress; field-merge would collapse two projects into one |
| Residual loser tie-break (basename, then SHA-256 of raw stage/file bytes) | Keep; residual only after depends / `created` | Cheap total order with no new frontmatter; required for same-basename add/add and for dual `--repair` agreement. Auto-commit integrate is usually single-writer, but the rule is shared with plain-files repair and must not fork |
| Equal-`order` re-space | Automatic at sync integrity / `doctor --repair` (tied files only) | Reads still sort via `(order, id)`, but hot-path `pj reorder` into an equal slot has no legal between-key until keys differ |
| Pathologically long `order` | Bare doctor soft report; file rewrite only via explicit `pj doctor --re-space-order` | Never implicit on `pj reorder` or bare doctor; not part of `--repair` (equal-key only) |
| Both-sides status dispute (at least one terminal) | Automatic write of `status_conflict`; human picks; sync refuses `--continue` while present | Any terminal-involved both-sides disagree is semantic (complete vs reopen, done vs cancelled) — LWW would silently erase a terminal; pure non-terminal pairs stay LWW |
| Cross-scope inbound after collision rename or scope rename | Doctor flag to verify (possible silent mispoint); never auto-rewrite other repos | Other scope is another git-root; best-effort flag is proportionate to compound near-never |
| Registry key ≠ `pj.cue` name (post-share rename drift) | Fail closed on that scope until `forget` + `import`; `name_drift:`; never half-work ambient | Correct full id would fail under pure registry lookup; degraded mode is agent-hostile; registration stays deliberate (no auto-rekey) |
| Non-allowlist residue under dir / vendor conflict-copy names | Flag only; never commit | Human cleanup; outside id namespace |

Out of budget (do not add without a new DECISION): auto-rewriting cross-scope edges,
auto-picking terminal status, auto-healing registry/pj.cue name drift, renumber-the-loser
or max+1 id schemes, multi-file renumber on hot-path reorder, or any repair on pure reads.
Bit-identical loser selection is justified by plain-files dual doctor and shared add/add
code paths — not by a fantasy of two auto-commit machines racing the same integrity step
(they usually serialise through fetch/push). Serialised sync does not license dropping
determinism on the shared repair procedure.

Ergonomics / id resolution (shared by every id-taking verb: `get`, `meta`, `deps`,
`status`, `edit`, `reorder`, `archive`, …):
- Full id `<scope>-<short-id>`: registry lookup by scope name, then exact short-id match
  in that scope. No ambient scope required. Short-id length is 4 through `SHORT_ID_MAX`
  (8) per the closed grammar (create-minted or collision-repaired).
- Short form (no scope prefix): exact match of the whole token against a project's
  short-id in the ambient scope (`--scope` / `PJ_SCOPE` / longest-prefix code-root).
  Accept any legal short-id length 4–8 — not create-only length 4 — so a repaired id
  such as `ab2c9` resolves the same way as `ab2c`. Match is exact on the short-id
  string, not a prefix search. Zero hits → unknown id (non-zero); more than one hit is
  treated as unknown/conflict (should not occur while short-ids are unique in-scope).
- A token that contains `-` is always parsed as a full id (scope is `^[a-z0-9]{1,12}$`
  before the first `-`; remainder is the short-id). Do not accept bare short-ids that
  include `-`.

Rationale for random over content-hash: a title hash would collide on same-titled
projects and change the id when the title is edited, breaking id stability; mixing in
a timestamp/salt to avoid that is just a random id with ceremony. Rationale for random
over sequential: avoids collisions under offline-concurrent creation.

### Scope names

DECISION: a scope has one identifier, its name. It is the cross-scope address
(`--scope wc`) and the id prefix (`wc-ab2c`). It is not a directory name — where the
files live is a separate, user-chosen path (see "Storage").

- Value constraint: `^[a-z0-9]{1,12}$` — lowercase alphanumeric, no hyphen (reserved as
  the `<scope>-<short-id>` separator), no symbols/spaces, max 12. Ambiguous chars are
  allowed because the human picks the name deliberately (`wc`, `webctl`, `ilili` are
  all legal). Lowercase is forced; the id crosses case-insensitive (macOS) and
  case-sensitive (Linux) machines.
- DECISION: unique across every scope registered on the machine. Everything is visible
  (see "Visibility"), and the id namespace is flat, so `pj get wc-ab2c` must resolve
  to exactly one scope. A name collision at `pj scope init`/`pj scope import` is a hard error (see
  "Scope lifecycle").
- Typical is 2-4 chars for ergonomics; a readable word up to 12 (`webctl`) is fine.
- Never silently defaulted (the beads mistake: an auto-assigned junk name to rename
  later). Always a conscious choice at init.

Names are fleet-global in effect, enforced per machine. A synced file carrying
`depends: [api-3m9k]` assumes `api` names the same scope on every machine that resolves
it, but uniqueness is checked only against the local registry — nothing stops machine
A's `api` and machine B's `api` being different scopes, which would resolve a
cross-scope gate against the wrong project, silently. Accepted as a stated assumption
for the single-user fleet pj targets: one person registers names consistently across
their machines. A genuine clash or divergence is repaired with `pj scope rename` (see
"Scope lifecycle") — rename is a tooled operation, never a manual multi-file rewrite;
prefer renaming before other machines register. After share, those machines re-register
with forget then import (lens not preserved).

Auto-derivation of a proposed name (what `--auto-name` proposes): split the code-root basename on `[-_. ]+` and camelCase boundaries; two or
more tokens -> their initials (`web-control` -> `wc`); one opaque token -> its first
two letters (`webctl` -> `we`). Sanitize to the restricted alphabet (no i/l/o/0/1,
first char a letter). A single opaque token cannot yield true initials, which is why
the override exists.

## Metadata (frontmatter)

DECISION: per-project metadata lives in the file's own YAML frontmatter — not a
separate index, not a database. The file is the single source of truth for content and
state, eliminating index-vs-files drift.

```markdown
---
id: wc-ab2c                # <scope>-<short-id>, canonical; filename mirrors it
status: in-progress        # draft|backlog|todo|review|in-progress|blocked|done|cancelled (+ CUE customs)
order: "a0"                # integer+fraction rank key (quoted string); execution order
depends: [wc-9k3m]         # project ids that block this one (same- or cross-scope)
related: [wc-7x4p, api-3m9k] # soft "see also" project ids; never gates (same- or cross-scope)
tags: [network, cdp]
created: 2026-06-20T14:30:00+10:00  # RFC3339, set once at pj create, immutable
links: [PR#142, issue#88, branch:network-redesign] # external artefacts only, never project ids
summary: One-line what/why.
# Optional keys declared in pj.cue fields: (see Configuration) — e.g.
# estimate: 3
# area: frontend
# stakeholders: [platform, design]
---

# Network output redesign

Tasks as checkboxes below...
```

Built-in frontmatter keys (closed, immutable set): `id`, `status`, `order`, `depends`,
`related`, `tags`, `created`, `links`, `summary`, and the transient merge-only key
`status_conflict` (see "Merge conflict handling"). A scope may declare additional keys
via `pj.cue` `fields` (see "Configuration"); those sit beside the built-ins in the same
YAML map. There is no nested `fields:` key in the file — declaration is in CUE, presence
is flat in frontmatter — so a human reading the markdown sees one metadata block.

DECISION: `status_conflict` is a transient built-in, not normal project metadata. It is
written only by the auto-commit frontmatter merge when both sides change `status` from
the merge base to two different values and **at least one** of those values is terminal
(see Merge conflict handling); it is never set by `pj create`, `pj status`, or ordinary
authoring. Shape: a YAML sequence of exactly two distinct known status names (the two
post-edit values — not necessarily both terminal), e.g. `status_conflict: [done, cancelled]`
or `status_conflict: [done, in-progress]`. While present, the project is mid-status
dispute involving a terminal: `status` holds the merge-base (last-agreed) value,
`pj get`/`pj meta`/`pj doctor` surface the choice, and `pj sync` refuses to continue the
rebase until the key is gone. Resolution is in-file — set `status` to either listed value,
or another known status (including a non-terminal reopen), and remove `status_conflict` —
the same class of direct edit as resolving a body conflict. The file remains the source
of truth, so an index rebuild still sees the dispute. A `status_conflict` present when
the git-root is not mid-rebase is doctor-hard residue (stale hand-edit or interrupted
cleanup); clear it. Custom `fields` must not shadow the name.

DECISION: `created` is an RFC3339 timestamp written once at `pj create` and never updated.
It is provenance for humans and the primary residual total-order key for id-collision
repair when inbound-`depends` does not decide (see "Project ids"). Local wall-clock is
fine — the single-user fleet accepts clock skew as a near-never residual, closed when two
timestamps compare equal by lexicographic basename then SHA-256 of the raw file/stage
bytes (not by the id string: both sides share it). `pj doctor` flags a missing or
non-RFC3339 `created` (date-only values included) so a hand-edited file cannot silently
weaken the repair order.

DECISION: `order` is the single sequencing key; there is no separate `priority`.
`pj next` and default `pj list` sort by `(order, id)`. Urgency is expressed by moving
a project earlier with `pj reorder`, not by a second sort axis. Banded triage, if ever
wanted, returns as a tag or a CUE custom field, not a built-in.

DECISION: `order` is an integer-plus-fraction rank key (fractional indexing), not a dense
integer and not a free-form letter string. The value is one string under a closed grammar;
board order is ordinary byte-wise string compare (memcmp / Go string `<` / SQLite
`ORDER BY`). No custom collation and no multi-segment parse at compare time — the
encoding is chosen so string order is rank order. Keys are durable source-of-truth data
(synced markdown, git history) — the on-disk format is a wire contract, not an internal
cache. A Go package implements generation (prefer a faithful port of the Rocicorp /
Figma fractional-indexing construction); it must emit this format. Swapping libraries
must not change the alphabet, integer grammar, sort order, or the meaning of existing
keys. Changing the format is a conscious, versioned migration of every `order` value,
never a quiet dependency bump.

Rationale for integer+fraction over pure `a`–`z`: a pure lowercase alphabet with free-form
non-empty keys has a least element `"a"`. `keyBetween(null, "a")` is impossible, so
`pj reorder --first` fails after a handful of uses. Prefix pairs such as `"a"` / `"aa"`
also leave an empty open interval. Integer heads (`Z`…`A` negative, `a`…`z` non-negative)
give practical unbounded open-end insert under the same byte-wise sort; the theoretical
floor (`SMALLEST_INTEGER`) is not an everyday failure mode.

Wire format (frozen for v1; treat as append-only protocol):

Alphabet (62 chars, ascending = rank order = ASCII byte order):
```
0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz
```
Only these characters are legal. Digit index 0 is `0` (min / pad digit). No spaces,
hyphens, or other symbols.

Key shape:
```
order_key = integer_part + fractional_part
integer_part   = head + digits   # fixed width from head
fractional_part = digit*         # may be empty; must not end with 0
```

Integer part — head encodes the total length of the integer part (head + its digits):

| Head | Integer length | Form | Role |
|---|---|---|---|
| `a` | 2 | `a` + 1 digit | non-negative, width 1 |
| `b` | 3 | `b` + 2 digits | non-negative, width 2 |
| … | … | … | … |
| `z` | 27 | `z` + 26 digits | non-negative, width 26 |
| `Z` | 2 | `Z` + 1 digit | negative, width 1 |
| `Y` | 3 | `Y` + 2 digits | negative, width 2 |
| … | … | … | … |
| `A` | 27 | `A` + 26 digits | negative, width 26 |

Length formula: head in `a`–`z` → `(head - 'a') + 2`; head in `A`–`Z` → `('Z' - head) + 2`;
any other head is invalid. The integer part is exactly `key[0:length]`; the remainder is
the fractional part. Because `'Z' < 'a'` in ASCII, negative heads sort before
`INTEGER_ZERO` without a custom comparator.

Special integer constants:
- `INTEGER_ZERO` = `a0` — empty board / `keyBetween(null, null)`; first `pj create` writes
  `order: "a0"`.
- `SMALLEST_INTEGER` = `A` + 26 × `0` — least legal integer part; cannot decrement further.

Fractional part: may be empty (`a0`, `Z9`, `a1` are valid). Must not end with `0` (no
trailing min digit). That forbids pad-style tails and avoids prefix-style empty intervals
between keys.

Validity (closed grammar): non-empty; every character in the base-62 alphabet; first
character `A`–`Z` or `a`–`z`; `len(key) >= integer_length(head)`; fractional part does not
end with `0`. `pj doctor` and every mutating write that sets `order` reject invalid keys
(hard). Missing, non-string, or empty `order` is also hard. Hand-edited garbage must not
enter the rank space silently.

`keyBetween(left, right)` (each bound a valid key or null):
- Precondition when both non-null: `left < right` (byte-wise). `left == right` is
  undefined — equal keys have no strict between; see equal-key repair (never invent a
  between on the hot path).
- null,null → `a0`.
- left,null → a key strictly after left (append path; `pj create` always uses this with
  left = current last key, or null,null when the scope has no projects yet). Prefer
  `incrementInteger(integer_part(left))` when that yields a key; else grow the fraction
  via `midpoint` under left's integer.
- null,right → a key strictly before right (`--first` / before-head reorder). Prefer
  bare `integer_part(right)` when right has a non-empty fraction and that integer is
  strictly less than right; else `decrementInteger(integer_part(right))`; if already at
  `SMALLEST_INTEGER`, densify under that integer with `midpoint` against right's
  fraction. Exhaustion of the theoretical floor is an error (doctor/migration), not a
  multi-file renumber on the hot path.
- left,right with left < right → same-integer `midpoint` on the fractions when integer
  parts match; otherwise try `incrementInteger(left's integer)` when it sorts before
  right, else `left's integer + midpoint(left's fraction, null)`.
- Integer inc/dec use base-62 digit arithmetic on the integer part only; overflow widens
  the positive head (`a9` → `b00`, …); underflow widens the negative head (`Z0` →
  wider `Y…`, …). Return failure only past max positive width or `SMALLEST_INTEGER`.
- Midpoint (fractional strings only): prefer short results; lengthen when neighbours are
  adjacent in the digit space; never end with `0`. Exact midpoint among legal shorts may
  vary only if the full key remains valid and strictly between the bounds — prefer one
  shared algorithm (Rocicorp port) so tests and fleets match.

DECISION: `keyBetween` (and the grammar) ship as a pure package with table-driven unit
tests before any CLI reorder wiring. Do not only test mid-board inserts. Minimum fixtures:

| Case | Expect |
|---|---|
| `keyBetween(null, null)` | `a0` (`INTEGER_ZERO`) |
| Repeated append (`left, null`) from empty board | strictly increasing keys; all valid |
| Repeated prepend (`null, right`) from `a0` / mid board | strictly decreasing keys; all valid; many steps without error |
| Same-integer adjacent densify (no single-digit fraction midpoint) | length growth; result strictly between; never ends with `0` |
| Open interval across integer boundary | valid key with left < k < right (byte-wise) |
| Equal keys (`left == right`) | no between on hot path — error / undefined, never invent |
| Invalid keys (trailing `0` fraction, bad head, empty, non-alphabet) | reject on validate / order-setting path |
| Integer overflow widen (`a9` → `b00` class) | append still succeeds after widen |
| Integer underflow widen (`Z0` → wider negative head) | prepend still succeeds after widen |
| Theoretical floor at `SMALLEST_INTEGER` densify then exhaust | densify while fraction room remains; hard error at true exhaustion — never multi-file renumber |
| Theoretical max positive integer width exhaust | hard error on further append past max width |
| Byte-wise sort == rank order | random valid key pairs: string `<` matches intended order |
| Round-trip validate | every key emitted by `keyBetween` passes the closed grammar |

Prefer one shared algorithm (Rocicorp-style port) so fixture strings stay stable across
implementations and agent tests.

Sort interoperability: two implementations are compatible if they accept the same
grammar, emit only valid keys, and preserve byte-wise order as rank order. Identical
midpoint strings are not required for sort correctness; identical generation is required
for boring fixtures and agent predictability, hence the shared-port preference.

Evolution: existing keys keep their sort meaning forever under this grammar. A future
format change requires a designed re-space/migration of all `order` values in a scope
(or a new key with a version discriminator) — not a silent alphabet or head-rule edit.

Inserting or moving computes a new key strictly between the two neighbours
(`keyBetween(left, right)`), so a reorder writes only the reordered project's file — no
neighbour is renumbered. `pj create` always appends with `keyBetween(last, null)` — no
create-time order flags (`--first` / `--before` / … live only on `pj reorder`). A new
scaffold is not yet queue-committed (`draft` by default); place it on the board with
`pj reorder` after promote when order matters.

Invariants (load-bearing for merge avoidance):
- Single-file write: `pj reorder` and `pj create` never rewrite a neighbour's `order`. There is
  no multi-file renumber on the hot path.
- Open ends and between-keys: for any two valid keys with left < right, and for null open
  bounds short of the theoretical integer floor/ceiling, `keyBetween` returns a valid key
  strictly between them (integer step and/or fraction length growth). An implementation
  that renumbers a band when "no space" remains is non-conforming — that would reintroduce
  multi-file conflicts on reorder, undoing layer 1 of "Merge conflict handling". The only
  hot-path hard failures are invalid existing keys or true exhaustion of
  `SMALLEST_INTEGER` / max positive integer width (not reached in normal use).
- Equal keys are the only ordinary "no value strictly between" case: two machines offline can
  compute the same key for the same slot. For reads the tie breaks deterministically by
  id (`(order, id)` sort). Generation still has no string strictly between two equal
  keys, so a later `pj reorder` into that slot would have nothing legal to write. Detect vs
  repair matches ids: reconcile-time index detection warns on equal keys (all scopes);
  file-mutating re-space is confined to the `pj sync` integrity step (auto-commit) and
  `pj doctor --repair` off-sync (every scope, including non-auto-commit with no sync
  seam), rewriting only the tied files. This keeps `pj reorder` a single-file write on the
  hot path and never renames or rewrites ranks from a pure read. Re-space assigns distinct
  legal keys that preserve the pre-repair `(order, id)` relative order among the tied set
  (and relative to untied neighbours), using only this grammar.
- Pathological length (optional escape): repeated inserts into the same microscopic gap
  can grow a fractional tail long. Bare `pj doctor` reports over-long `order` values (soft
  threshold: length > 64). File rewrite is only `pj doctor --re-space-order` (explicit;
  same shape as equal-key re-space: only the rewritten files; auto-commit self-commits when
  a git-root exists — see doctor CLI). Never implicit on `pj reorder`, bare doctor, or
  `--repair`.
- Why not dense integers: no value between 3 and 4, so an insert rewrites every
  displaced project — reintroducing the identity/order coupling the id scheme escaped
  and turning every offline reorder into a conflict source. Integer+fraction rank keys
  keep `pj reorder` a single-file edit for normal use.
- Always quoted (`order: "a0"`). Keys mix digits and letters; a bare YAML scalar can be
  coerced (e.g. bare numbers, or legacy letter-only forms such as `n` / `y` / `no` under
  YAML 1.1). Quoting keeps the value a string. `pj doctor` flags an unquoted/non-string
  `order`.

Derived, never in frontmatter: task counts, percent done, next runnable project, blocked
count. Materialized in the index, recomputed on reconcile, so they never go stale and
never pollute the source of truth.

## Storage

DECISION: a scope is a directory holding `pj.cue` plus the project `.md` files,
flat. That directory is the scope directory (`dir`) — where the markdown and `pj.cue`
live; not the code-root (ambient cwd match) and not the git-root (derived). No
subdirectory per scope — the directory is the scope. The one exception is an optional
`archive/` subdirectory that `pj archive` moves done projects into; reconcile scans it
too (see "Done and archive"), so archived projects stay indexed and resolvable.

```
<dir>/
  pj.cue                          # scope name, schema, auto-commit setting, knownTags
  .gitignore                      # ignores .pj.lock; written by pj scope init
  wc-ab2c-network-output-redesign.md
  wc-9k3m-cdp-session-pool.md
  archive/                        # optional; pj archive moves done projects here (still scanned)
    wc-7h2n-legacy-cleanup.md
  ...
```

- `pj.cue` (renamed from the old `config.cue`) is namespaced so it cannot
  collide with a repo's own `config.cue` or another tool's, now that the dir may
  be any directory the user points at, not a pj-dedicated one.
- The dir is intended to hold only pj scope files (source of truth + the small allowlist
  below). Source code never belongs in it. That rule is not trusted as an informal
  convention alone: auto-commit snapshot commits only an explicit allowlist (see
  "pj sync"), and non-allowlist paths under the dir are residue — never committed by
  pj, warned on `pj sync` stderr, and flagged by `pj doctor` for human cleanup. Do not
  put secrets, dumps, or unrelated trees here; even with the allowlist, residue can
  still sit on disk and confuse humans. It is typically a subdirectory of the code it
  tracks (`<repo>/.agents/pj/`), or a standalone directory for personal/cross-cutting work.
- Recommended dir (scope directory): `.agents/pj/` (beside other agent tooling) or
  `.agents/projects/`. Not enforced — the user names the path at init.
- A git repo may host several scopes, each rooted at a distinct code-root — a large
  monorepo carries one scope per team/area (`/org/mono/teamA`, `/org/mono/teamB`), and a
  personal pj repo carries several scopes as sibling subdirectories. The only per-repo
  constraint is autoCommit consistency: every scope sharing a repo agrees on its
  autoCommit (see "Auto-commit"). The unit is the scope (a dir), not the repo.
  (Superseding the earlier "one repo = one scope", which was an artefact of welding
  code-root to the repo root; the dir/code-root decoupling in "Resolution" removes it.)

## Visibility

DECISION: every registered scope is visible from anywhere on the machine. There is no
private/local class. `pj scope list`, cross-scope `pj search`, `pj list --scope`, and
`pj get <scope>-<id>` reach any registered scope. This is the payoff of the flat id
namespace and the machine-wide index: an agent in one repo answers "what is in flight
in wc?" without leaving.

Consequences accepted:
- Scope names must be machine-unique (see "Scope names").
- Registering an existing scope is a deliberate act (`pj scope import`), never automatic, so
  cloning a repo does not silently pull its scope into the machine-wide view.

## Resolution

DECISION: resolution is a registry lookup, not a filesystem walk. There is no up-scan
for a marker and no blessed default location. A scope becomes known only by `pj scope init`
or `pj scope import`, which records it in the machine-local registry.

The registry (see "Registry") records, per scope: its name, its dir (where the
`.md` and `pj.cue` live), and its code-root (the directory tree under which the
scope is ambient).

Ambient scope (bare `pj list`, `pj next`, `pj create`): the scope whose code-root is the
longest prefix of cwd. Precedence, most-specific first:
1. `--scope` flag.
2. `PJ_SCOPE` env.
3. longest-prefix code-root match against cwd (the registry).
4. none -> scope-requiring commands error with guidance; discovery commands still work.

- Longest-prefix means nested code-roots resolve deterministically: register `<repo>/`
  -> scope A and `<repo>/frontend/` -> scope B, and cwd under `frontend/` picks B,
  elsewhere in the repo picks A.
- No two scopes may register the identical code-root (bare `pj list` could not choose).
  Nested is fine; identical is rejected at init/import.
- Cross-scope addressing (`--scope wc`, `pj get wc-ab2c`) is a direct registry lookup
  by name, independent of cwd.

The lookup is cheap (an in-memory prefix match over a handful of registry entries), and
because it is a registry read rather than a stat-walk, it never depends on filesystem
markers or git.

## Registry

DECISION: the registry is machine-local durable state in the XDG config directory
(`${XDG_CONFIG_HOME:-~/.config}/pj/`). It is not synced and never lives in a
repo. Each machine registers its own scopes: the scope's files travel (git clone,
Dropbox), but "this machine knows about this scope, at these paths" is a per-machine
fact.

It is the one thing the index cannot rebuild — a scope's files record their content and
state, not the fact of registration or the code-root binding — so it lives in a real
config file, not the derived index. Drop the index and it rebuilds by walking the
registry; drop the registry and the scopes are simply unknown until re-`init`/`import`.

Shape (one CUE package, one file per concern):

```cue
// registry.cue — written by pj scope init|import|use|rename|forget
scopes: {
    wc: {                                             // single-scope repo, files under root
        dir: "/home/grant/projects/webctl/.agents/pj"
        root:  "/home/grant/projects/webctl"
    }
    ta: {                                             // one of several scopes in a monorepo
        dir: "/org/mono/teamA/.agents/pj"
        root:  "/org/mono/teamA"
    }
    home: {                                           // standalone, files == root
        dir: "/home/grant/notes/home"
        root:  "/home/grant/notes/home"
    }
}

// lens.cue — written by pj lens
lens: {wc: ["frontend", "style"]}   // machine-local default tag view, per scope
```

DECISION: the XDG config directory is machine-written, owned by pj — the user never
needs to hand-edit it, because every mutation has a verb. It is one CUE package split
into per-concern files, each owned wholesale by the verb family that writes it:
`registry.cue` (`pj scope init|import|use|rename|forget`) and `lens.cue` (`pj lens`).
There is no `editor` key: `pj edit` resolves the editor from `$EDITOR` at point of use
(already the CLI-surface behaviour), so no setting exists that would require
hand-editing; a `settings.cue` appears only when a real setting and its verb do. Reads
and writes go only through the CUE Go modules (`cuelang.org/go`): load/compile the
package, mutate via the API, regenerate the whole owned file with `cue/ast` +
`cue/format`, write to a temp file in the same directory, atomically rename. No
string-built CUE and no non-CUE codec for these paths (see Configuration). Wholesale
per-file regeneration is safe precisely because the files are machine-owned — there is
no hand-authored formatting to preserve. All XDG-config writes serialize under one
machine-global flock (`${XDG_CONFIG_HOME:-~/.config}/pj/.pj.lock`); the per-scope flock
protects scope files, not this machine-global state, so without it two concurrent
`pj scope init`s could silently drop a registration. Hand-editing still works (it is
plain CUE, read back through the same CUE load path), but an XDG file that will not
parse is a hard error naming the file — the registry is the bootstrap that locates every
scope, so unlike a scope's `pj.cue` there is nothing to degrade to.

Each scope records exactly two paths, and they are independent:
- `dir`: where the `.md` and `pj.cue` physically live; what reconcile
  stats. Must be distinct per scope.
- `root` (code-root): a single path — where the scope is ambient for bare-`pj`
  resolution. Not a list (a scope has one root); `pj scope use` re-points it. `dir` need not
  live under `root` — they are matched in different steps and never interact.

The git repo is not recorded. It is derived on demand from `dir`
(`git rev-parse --show-toplevel`), so moving or renaming the repo never staples the
registry; several scopes whose `dir` derive the same repo share that repo as their sync
unit. The scope name is cached here for fast `--scope` lookup; the authoritative name is
in each scope's `pj.cue`. `pj doctor` reconciles the two and flags drift (a scope whose
`pj.cue` name no longer matches its registry key — typically a remote `pj scope rename`
absorbed as ordinary file changes — or a registry entry whose dir is gone). Drift
is not auto-healed: registration is deliberate, so the recovery is unregister then
re-import (see `pj scope rename`), not a silent re-key.

DECISION (registry / `pj.cue` name drift — fail closed): when a registered scope's
registry key and the on-disk `pj.cue` `name` for that `dir` disagree, that scope is
**unusable** until deliberate re-registration. Do not document or implement a half-working
ambient window (short-id works, correct full id fails, wrong full id fails). That mode is
agent-hostile and invites working through a broken name binding.

While drift is present for a scope entry:
- Hard-error every command that would use that scope: ambient code-root hit, `--scope`
  / `PJ_SCOPE` naming the registry key, short-form or full-id resolution that would land
  in that dir (including full ids whose prefix is the new `pj.cue` name — the registry
  has no such key yet, and ambient must not quietly adopt the new name either).
- Error names both names and the recovery, e.g. `scope name drift: registry key "old"
  but pj.cue name is "new" at <dir> — run: pj scope forget old && pj scope import <dir>
  [--code-root …]`. Stable token: `name_drift:` (doctor and command refuse share it).
- Still allowed (so recovery and diagnosis work): bare `pj doctor` / doctor flags,
  `pj scope list`, `pj scope forget`, `pj scope import`, `pj skill`, `help`, and other
  discovery commands that do not open project files under the drifted scope. `pj sync`
  that would touch the drifted scope's git-root refuses that root for the same reason
  (name binding unverifiable for allowlist messages / integrity attribution) until fixed —
  or at minimum refuses operations scoped to that entry; sibling scopes in the same
  git-root without drift keep their ordinary rules (sync preflight still applies for
  autoCommit consistency across the root).
- Index: do not pretend the drifted entry is a healthy namespace. Reconcile may skip
  project materialization for that scope (or keep last rows but never serve them for
  `next`/`get`/`list`/`deps`) while the refuse stands; after forget+import, normal
  reconcile rebuilds under the new key. Prefer skip-or-drop-on-doctor over serving
  mixed old-key / new-id rows.
- Never auto-rekey the registry to match `pj.cue` (out of auto-repair budget).

## Scope lifecycle

DECISION: `pj scope init <dir> (--name <name> | --auto-name) [--code-root <path>]
[--auto-commit]` creates a new scope and registers it. `pj scope import <dir>
[--code-root <path>]` registers an existing on-disk scope (post-clone), files in place.
They are symmetric entrances to the registered state; init writes a fresh `pj.cue` and
a `.gitignore` covering `.pj.lock` (authoring its own dir, not managing the
repo), import reads an existing scope as it ships (name and autoCommit come from its
`pj.cue`, so import takes neither `--name` nor `--auto-commit`).

pj is non-interactive — it never prompts. Everything it needs is a flag or a deterministic
default; the only TTY-sensitive behaviour anywhere in pj is colour. So init takes the name
and auto-commit choice as flags, not prompts.

Name (init only): exactly one of `--name <name>` or `--auto-name` is required; supplying
neither is an error (the name is never silently defaulted — "always a conscious choice"
survives, and `--auto-name` is that conscious choice to accept derivation). `--name` is
validated against `^[a-z0-9]{1,12}$`. `--auto-name` derives from the code-root basename (the
algorithm in "Scope names") and sanitizes to the alphabet; because code-root may now be a
subdirectory, `--auto-name` reads well for monorepos (`/org/mono/teamA` -> `ta`). A derived
name that is already registered is a hard error naming the clash and telling you to pass
`--name` — never an auto-suffix (the beads junk-name mistake).

The dir is required (never defaulted). The code-root defaults from git or is given
explicitly, by this matrix — `--code-root` is always allowed (it is what makes several
scopes share a repo), and defaults are just conveniences:

| dir in a git repo? | `--code-root` given? | result |
|---|---|---|
| yes | no | code-root = the repo root (`git rev-parse --show-toplevel`) — single-scope default |
| yes | yes | code-root = the given path — the sub-scoping case (monorepo team, sibling in a pj repo) |
| no | no | code-root = the dir — standalone, ambient in its own tree |
| no | yes | code-root = the given path |

Errors teach the fix, e.g.:

```
--code-root /elsewhere is not inside the git repository /foo/bar that holds the
dir. A code-root is where the scope is ambient; keep it inside the repo, or omit
it to default to the repo root:
  pj scope init /foo/bar/.agents/pj --name <n> --code-root /foo/bar/teamA
```

Registration checks (both commands):
- Scope-name collision: DECISION hard fail. If the name is already registered, refuse.
  There is no rename-on-import — the name is baked into every id, filename, and
  in-scope reference, so a rename applied only locally would diverge from every other
  clone. The remedy is `pj scope rename` (below), run at the source before other machines
  register — the cheap path. Renaming after other machines already have the scope is
  rare recovery: those machines re-register with forget then import (see rename), not an
  auto-absorb. A same-store re-clone (same name, same ids at a new path) is refused too,
  with guidance to `--code-root`/re-point rather than double-register.
- Code-root collision: reject a code-root identical to an existing scope's (see
  "Resolution"). Nested code-roots are fine (longest-prefix resolves).
- Dir disjointness: reject a dir identical to, nested within, or containing
  any registered scope's dir. Two scopes cannot share one dir, and — unlike
  code-roots, where nesting resolves cleanly by longest prefix — dirs must be
  mutually disjoint, never nested. This is a load-bearing invariant, not a nicety: the
  `pj sync` snapshot (step 1) treats everything inside a scope's dir as that scope's
  to commit, and reconcile scans a dir flat (plus its one `archive/`); if scope B's
  dir nested inside scope A's, A's sync would sweep and commit B's files under A's
  repo while A's flat reconcile ignored them — cross-scope attribution and double-handling
  the git-root lock cannot see, because it guards the shared git index, not file ownership.
  The error teaches the fix (choose a sibling path, e.g. `.agents/pj-teamB`, not a path
  under an existing scope). Nested code-roots stay fine — only dirs carry the
  disjointness rule.
- autoCommit consistency per repo: every scope sharing a derived git-root has the same
  `autoCommit` value. Auto-commit is a property of the branch/remote path, not a
  subdirectory, so one repo cannot mix auto-commit (pj pushes) with non-auto-commit
  (repo-driven PR commits or plain files). A scope added to a repo that already hosts
  scopes inherits their `autoCommit` (so `--auto-commit` is optional there: omit means
  inherit, not re-default to false; an explicit flag that contradicts siblings errors);
  the first scope in a repo sets it. An auto-commit scope that must live beside a
  non-auto-commit tree belongs in its own repo (sibling or submodule), which derives a
  different git-root. The error names the existing autoCommit and points at that fix.
  Same remedy when the user wants **write/sync isolation** between auto-commit scopes:
  multi-scope-per-repo is one-push convenience, not fault isolation (see Auto-commit).
  This check is not init-only: the git-root is derived at runtime (never stored — see
  "Registry"), so a later git-topology change can bring divergent-autoCommit scopes under
  one git-root after both were registered. `pj sync` re-derives and re-validates
  autoCommit consistency across the scopes sharing its git-root as a preflight (refusing
  rather than pushing under a violated invariant), and `pj doctor` runs the same check
  off-sync; sync safety does not rely on the invariant silently persisting (see
  "pj sync", step 1).
- Malformed `pj.cue` (import only): fail the import cleanly, naming the parse
  error, rather than registering a scope whose schema will not load. This is the one
  place an untrusted scope's config is first read.

`--auto-commit` records `autoCommit: true` in `pj.cue` (init only; see "Auto-commit").
Omitted records `autoCommit: false`. It is never prompted. Doc/error labels only (not
stored): "pj-driven" / "auto-commit" when true; "repo-driven" when false and inside git;
"plain files" when false and outside git.

Init matrix:

| Situation | `--auto-commit`? | Result |
|---|---|---|
| Outside git | omit | plain files (`autoCommit: false`) |
| Outside git | set | pj-driven planned: file writes succeed; self-commit and `pj sync` disabled until repo/remote exist (`sync_disabled:` on writes/sync) |
| First scope in a git repo | omit | repo-driven (`autoCommit: false`) |
| First scope in a git repo | set | pj-driven (`autoCommit: true`) |
| Repo already has scopes | omit | inherit siblings' `autoCommit` |
| Repo already has scopes | set | must match siblings; contradict → error |

Accepted tradeoff: first scope in a git repo + omit flag = repo-driven, with no separate
"I meant that on purpose" signal. With a single positive flag it is the only coherent
rule: off is default; on is opt-in. Wrong omit on a dedicated pj repo → files change on
disk, no self-commit, no sync warnings. Mitigate in docs / `pj skill` / init help ("in a
dedicated pj repo, pass `--auto-commit`"), not a second flag.

Import reads `autoCommit` from the on-disk `pj.cue` (no flag). There is no separate
host/none gate: false outside git is plain files; false inside git is repo-driven.

Discoverability without auto-slurping: pj never probes the filesystem for an unregistered
scope. Resolution is registry-only (see "Resolution") — no up-scan, no candidate-path
check for `pj.cue`, no "unimported scope here" inference from cwd. A scope-requiring
command with no ambient scope errors with the generic no-scope guidance only (see "CLI
surface"). Post-clone registration is a deliberate `pj scope import <dir>` by the
user (or by an agent that already knows the path from `pj skill` / human instruction);
cloning never auto-registers, and v1 does not discover an on-disk `pj.cue` for you. The
planned `pj skill install` is the consented way to leave that path in-tree for a cold
agent.

DECISION: `pj scope rename <old> <new>` renames a scope in place — the tooled remedy for
a name clash. The name is baked into every id, filename, and in-scope reference, so
rename must be an operation, not an instruction. It validates `<new>`
(`^[a-z0-9]{1,12}$`, machine-unique), then under the scope flock rewrites everything
in-scope in one operation: the name in `pj.cue`, the `<scope>-` prefix of every project
id in frontmatter, every filename (the filename mirrors the id), and every in-scope
`depends`/`related` edge; for auto-commit, one commit. Cross-scope inbound edges live in
other scopes' repos and cannot be rewritten here — exactly as in the id-collision
repair, they are recorded (read from the machine-wide `edges` table) and `pj doctor`
flags each for confirmation ("target scope was renamed — update this reference"). The
authoring machine's registry and lens entries are re-keyed (both machine-written XDG
files key by scope name).

Cheap path: rename at the source before other machines register the scope, so clones
import under the final name and never see drift.

Post-share recovery (rare): another machine that already registered the old name receives
the rewrite as ordinary file changes at its next sync. Its registry still keys the old
name; `pj.cue` and all project ids now use the new name. That is **name drift**: the
scope is fail-closed unusable (see Registry) until re-registration — not a degraded
operable mode. `pj doctor` reports `name_drift:`; project verbs and ambient use of that
code-root hard-error with the same recovery. There is no auto-rekey and no silent absorb
— registration is deliberate, and a bare `pj scope import` of the same dir would hit the
dir disjointness guard while the old registration still exists. The recovery is conscious
re-registration:

```
pj scope forget <old>
pj scope import <dir> [--code-root <path>]
```

`forget` drops the old registry and lens entries and the index rows; `import` registers
under the name now in `pj.cue`. The machine-local lens is not preserved across that
boundary — re-set with `pj lens` if wanted. That cost is accepted: post-share rename is
the expensive path, not a multi-machine operation the registry tries to heal. Prefer
renaming before other machines register so this window never appears.

DECISION: `pj scope forget <name>` unregisters a scope: removes its registry and lens
entries and drops its index rows. It never touches the scope's files or repo — the files simply
become unknown to this machine until re-registered with `pj scope import`. This is the
deliberate permanent exit; a merely unreachable dir (unmounted drive) stays
registered and is reported by `pj doctor` (see "Invalidation and reconcile").

## Configuration (CUE)

DECISION (owner hard lock-in — not under review): config is CUELang end to end.
Both tiers — machine-written XDG (`registry.cue`, `lens.cue`) and scope `pj.cue` — are
CUE. No alternate on-disk format (JSON, TOML, gob, hand-rolled text) for either tier in
v1 or as a reserved escape. CUE is the product config language; latency of
`cuecontext.New()` is accepted operational cost, not a reason to split formats.

DECISION: every CUE config file is read and written only through the CUE Go modules
(`cuelang.org/go` — load/compile/unify/evaluate, and `cue/ast` + `cue/format` for
writes). Forbidden: string-templating `.cue` files, `fmt.Fprintf` of CUE syntax,
`encoding/json`/`yaml` round-trips of the same paths, or a second hand-rolled parser.
Human/agent hand-edits remain plain CUE on disk; pj always re-enters via the CUE API.
Malformed CUE is reported as CUE errors, never half-parsed by a fallback.

Two tiers, least to most specific; later overrides earlier:

1. XDG config directory `${XDG_CONFIG_HOME:-~/.config}/pj/` — machine-local and
   machine-written by pj (see "Registry"): one CUE package, per-concern files
   (`registry.cue`, `lens.cue`). Optional; pj runs on built-in defaults when absent.
   (No configurable default autoCommit — omit is false (with inheritance when siblings
   exist), `--auto-commit` is true; see Scope lifecycle, so there is nothing else to
   configure.)
2. Scope config `<dir>/pj.cue` — the scope name, auto-commit setting, optional custom
   statuses, optional custom frontmatter fields, and the optional controlled tag
   vocabulary (`knownTags`). This is the tier that validates each project's frontmatter.

Env (`PJ_SCOPE`) and flags (`--scope`) override.

Why CUE: the custom statuses and fields a scope declares become the schema `pj doctor`
(and every mutating write) validates every project's frontmatter against. CUE is a typed,
validated schema language — chosen on purpose, not as a heavier TOML.

### Scope `pj.cue` shape

DECISION: `pj.cue` is a single concrete CUE value per scope. `pj scope init` writes a
minimal valid file (name + autoCommit); everything else is optional and additive. Shape:

```cue
// <dir>/pj.cue — synced with the scope; humans/agents may edit after init
name:  "wc"    // required; ^[a-z0-9]{1,12}$; authoritative (registry caches a copy)
autoCommit: true  // required bool; true only with --auto-commit (or inherited true)

// Optional controlled tag vocabulary. Free-form tags remain allowed; doctor warns on
// values not in this list (typos), it does not reject them.
knownTags?: [...string]

// Additive custom statuses. Built-ins are immutable and must not be redeclared.
// Name: lowercase, hyphen-joined, ^[a-z][a-z0-9-]{0,31}$, not a built-in status name.
// category drives default list filters and terminal-status merge dispute / depends
// satisfaction — not membership in pj next (only built-in todo is ever next-eligible).
// See "Status and dependencies", "Merge conflict handling".
statuses?: {
	[name=string]: {
		category: "active" | "wip" | "backlog" | "done"
	}
}

// Optional custom frontmatter fields. Keys sit flat beside built-ins in project YAML.
// Name: ^[a-z][a-z0-9_]{0,31}$ (snake-friendly YAML keys); must not shadow a built-in
// frontmatter key (id|status|order|depends|related|tags|created|links|summary|
// status_conflict).
fields?: {
	[name=string]: #Field
}

#Field: {
	type: "string" | "int" | "bool" | "strings"
	// Optional closed enum. Legal only when type is string or strings; every value
	// present in frontmatter must be in this list (doctor hard-flags unknowns).
	values?: [...string]
}
```

Example (custom status + fields):

```cue
name:  "wc"
autoCommit: true
knownTags: ["frontend", "backend", "style", "api", "network", "cdp"]
statuses: {
	shipped:  {category: "done"}
	wontfix: {category: "done"}
}
fields: {
	estimate:     {type: "int"}
	area:         {type: "string", values: ["frontend", "backend", "cross"]}
	stakeholders: {type: "strings"}
}
```

Field types (closed set for v1):
- `string` — YAML string scalar. Merge: scalar rules.
- `int` — YAML integer scalar. Merge: scalar rules.
- `bool` — YAML bool scalar. Merge: scalar rules.
- `strings` — YAML sequence of strings. Merge: 3-way set merge (same as `tags`/`depends`/
  `related`/`links`). Not a free-form sequence of mixed types.

Validation (writes and `pj doctor`):
- `status` must be a built-in or a name declared under `statuses`.
- Each declared field, when present in frontmatter, must match its `type` (and `values`
  when set). Absent is always legal — fields are optional on every project; there is no
  required-field flag in v1.
- A frontmatter key that is neither a built-in nor declared under `fields` is a doctor
  warning (hand-edit / forward-compat residue), not a write blocker and not silently
  dropped from the file. The raw file still carries it so nothing is hidden.
- Redeclaring a built-in status or shadowing a built-in frontmatter key in `pj.cue` is a
  hard config error (scope read-only until fixed), same class as a malformed file.
- Custom status names that collide with built-ins, or field names outside the name
  alphabet, are hard config errors at evaluate time.

DECISION: there is no dedicated `pj field` / `pj set` verb in v1. Custom fields are
authored by direct frontmatter edit (`pj edit` or the agent's file tool), the same path
as body and tags. pj validates on the next reconcile/write; it does not intermediate
field mutation. A verb family can return later without schema change. Read-side inspect
of the header is `pj meta` (see CLI surface) — not a write API and not a substitute for
opening the body.

Custom fields live in the project file (flat frontmatter) and are materialized in the
index for `pj query` / filters. Agents read them from the file via path from `get`/`next`
/ `create`/`status`, or inspect the header with `pj meta` — there is no nested JSON
`fields` object to document. The index implementation may use a JSON column; `pj query`
schema is not a stable API either way.

Cost note (not a format escape hatch): CUE is heavier than decoding TOML —
`cuecontext.New()` plus compile/unify on a command an agent may call dozens of times a
session. That cost is accepted under the hard lock-in above. Steady-state scope config
cost is reduced by caching (below); the XDG registry remains a fixed per-command CUE load
because it is the bootstrap.

DECISION: the CUE evaluation of each scope's `pj.cue` is cached in the index,
keyed by the `(path, mtime, size)` of every file in that config's import closure — not
just the entry file, so an edit to an imported `schema.cue` or a `cue.mod` module
invalidates the cache rather than validating against a stale schema. A steady-state
command re-evaluates a scope's config only when a file in its closure changed; otherwise
it deserializes the cached values. The XDG tier is small and optional and is evaluated
in-process each command (it holds the registry, so it must be read before any scope's
files are located — caching it in the index would be a bootstrap circle). Cache hits
still originated from a prior CUE evaluation; cold paths always use the CUE modules.

DECISION (accepted cost): because the registry cannot be cached in the index — it is the
bootstrap that locates every scope — every invocation, the hot `pj next` included, loads
XDG config through CUE. `cuecontext.New()` is instantiated once per process and amortizes
across the registry read and any cache-miss scope-config evaluation in the same command.
There is no reserved JSON (or other) split of `registry.cue`/`lens.cue` — both tiers stay
CUE, read and written only via `cuelang.org/go`, for the life of the design.

DECISION: a malformed `pj.cue` makes its scope read-only until fixed — fail fast on
write, never a silent degrade. A `pj.cue` that will not compile cannot be trusted for
either the custom schema a write validates against or autoCommit, which decides how a
write commits; autoCommit lives only in `pj.cue` (the registry caches the name, not
autoCommit), so there is no safe value to fall back to. Writing under a guessed schema
or, worse, a guessed autoCommit is exactly the quiet failure the Scope-lifecycle
autoCommit rule refuses to risk — a silently-wrong false fallback would let an
auto-commit scope pile up uncommitted, unpushed work with no warning. So pj refuses every
mutating command on the affected scope with a clear error (`scope config unparseable —
fix pj.cue before writing`) rather than degrade the write.

Reads need neither the custom schema nor autoCommit, so they stay fully available:
`pj get`/`meta`/`next`/`list`/`deps`/`search` work against the scope, and because only
that one scope's writes are blocked, machine-wide commands that reconcile many scopes
(cross-scope `search`/`list`) are never bricked by one broken config. Per-scope file mutations on a
sibling scope that still parses keep working. This is the isolation property that matters
for ordinary commands — one bad edit degrades nothing machine-wide and loses no work; it
just gates that scope's writes. It is distinct from the per-project `parse_error`
quarantine in "Invalidation": a project `.md` is data, so a bad one is a flagged row the
rest of the scope reads past; `pj.cue` is the scope's write contract, so a bad one blocks
writes rather than being written past. The block is loud — `pj doctor` reports it
prominently and a terse warning rides the scope's reads. Fix the config and the next
command re-evaluates it (cache keyed by the import closure's `(path, mtime, size)`) and
restores writes.

TRADEOFF (accepted): `pj sync` is the exception to that per-scope isolation. Sync is
repo-granular and its preflight must re-verify autoCommit consistency across every scope that
shares the derived git-root (see "pj sync", step 1). An unreadable autoCommit is the same class
of failure as a disagreeing autoCommit — there is no safe value to assume — so if any scope
sharing that git-root has an unparseable `pj.cue`, `pj sync` refuses the entire git-root
until it is fixed (`scope <x> config unparseable — fix <dir>/pj.cue before sync`),
rather than omitting the broken sibling and pushing under an incomplete proof. Same shape
as the mid-rebase freeze among auto-commit siblings: availability couples at the repo
boundary only where the shared invariant is checked before network mutation. Ordinary
per-scope writes on a healthy sibling remain unblocked.

## Read interface (SQLite index)

DECISION: SQLite is the read/query interface in v1, via `modernc.org/sqlite` (pure Go,
no cgo; FTS5 is compiled in by default). It is a materialized view derived from the
files, not a second source of truth.

DECISION: one machine-wide index at a fixed, machine-local path:

```
${XDG_STATE_HOME:-~/.local/state}/pj/index.db
```

- It lives in XDG state, never inside any scope's dir, so no version-control or
  filesystem-sync mechanism (git, Dropbox, Syncthing, NFS) ever carries it. The
  "derived, never synced, rebuildable" invariant is true by construction for every
  autoCommit mode, and WAL always runs on a local disk (WAL is unsafe on a network/synced
  filesystem — separating the `.db` from its `-wal`/`-shm` corrupts it; keeping the DB
  in XDG state removes that hazard entirely).
- One DB, all scopes. Rows are namespaced by a `scope` column, so cross-scope queries
  are a single `SELECT` and full-text search is one FTS corpus (bm25 ranks are
  corpus-relative and cannot be merged correctly across separate indexes — one corpus
  is the only way to rank a machine-wide search honestly).
- Authority stays in the files: pj writes the file first, then the row (write-through);
  the file mtime is the arbiter, so the view cannot durably diverge. Damaged or deleted,
  the DB rebuilds from the files. A schema change is a rebuild, not a migration — bump
  `schema_version`, drop, repopulate; no `ALTER`, no dead columns (the beads failure
  this avoids).

DECISION (owner hard requirement): SQLite is the v1 index, not contingent on any later
component. It stands on v1's own query surface: one machine-wide FTS5 corpus so cross-scope
`pj search` ranks honestly (bm25 is corpus-relative and cannot be merged across separate
indexes), `WITH RECURSIVE` for transitive `depends`/rollup traversal, and ad-hoc
`pj query` — capabilities a per-command in-memory scan can only hand-roll piecemeal, and
never as one durable store. That the corpus is "only tens of files" today does not unwind
the choice: the store, not the row count, is the point, and the write-through/reconcile/
`edges` plumbing is woven through the write path, so landing SQLite later would be a
rearchitecture rather than an addition.

DECISION (v1 concurrency): WAL mode and a connection `busy_timeout` are on from day one —
cheap, correct defaults even for a single writer (crash-safe commits, future readers).
v1 has one intentional writer class: the CLI process (command-scoped reconcile and
write-through). Concurrent CLI invocations serialize on SQLite's single-writer lock;
`busy_timeout` makes contention wait rather than error. No v1 multi-writer protocol
beyond that (no file-watch reconcile daemon, no viewer write path, no cross-process
ownership of reconcile).

The planned pj viewer — a web-based project monitor as a second, long-lived process — is a
real future consumer the same machine-wide DB already fits, and it reinforces SQLite
without carrying the v1 choice. Viewer design is deferred: when it lands it will need its
own change-observation (file watcher or poll) and an explicit second-writer design
(how its reconcile coexists with CLI reconcile). Until then do not invent that protocol
in the CLI. Even if the viewer never ships, the v1 query surface above already earns
SQLite.

Alternatives rejected: scan-only, and a gob/json snapshot cache. Both serve simple reads
but neither provides FTS5 search, ad-hoc SQL, or `WITH RECURSIVE` dependency/rollup, and
neither is a durable store a second process can later attach to — each would have to be
rebuilt into SQLite the moment the query surface or a viewer pressed on it.

### Invalidation and reconcile

DECISION: pj reconciles the index at the start of each command, scoped to what the
command reads (`pj next` in `wc` reconciles only `wc`; a cross-scope query reconciles
all registered scopes it reads). Git-free — reconcile never runs a git subprocess.

Two layers:
1. mtime + size per file. The DB stores each file's nanosecond mtime and size; reconcile
   stats the scope's dir (and its one `archive/` subdirectory), reparses only files
   whose `(mtime, size)` changed, deletes rows for files gone from disk, indexes new files.
   A file moved into `archive/` is re-keyed by its (unchanged) id and flagged `archived`,
   not treated as a deletion, so the record survives the move. The last-index timestamp is
   stored and any file with `mtime >= that` treated as dirty (git's racy-index rule),
   closing the same-tick hole. A reparse that fails (malformed YAML, leftover git
   conflict markers, an unquoted-`order` coercion) is quarantined, not fatal: reconcile
   writes a minimal error-row — id from the filename prefix, a `parse_error` flag with
   the parser message, `(mtime, size)` recorded so a fix re-indexes it, raw body still
   FTS-indexed. The project stays addressable (`pj get` prints it flagged, `pj meta`
   prints extractable raw frontmatter when possible, `pj doctor` lists it, a terse
   `N unparseable` warning rides affected reads) rather than being silently dropped or
   triggering a scope-wide rebuild loop.
   An unreachable dir (unmounted drive, deleted-but-still-registered repo) is
   likewise isolated to its own scope, not escalated: reconcile cannot stat the directory,
   so it skips that scope, leaves its existing rows in place (a transient unmount must not
   drop rows a remount would restore), and rides a terse `N scope(s) unreachable` warning
   on affected reads. It is not a full-rebuild trigger. `pj doctor` owns the durable
   response — it reports the unreachable entry and drops its rows, and the scope reappears
   when the path is reachable again.
2. Full rebuild. DB missing, failing an integrity check, `schema_version` mismatch, or an
   error reading the DB itself -> walk the registry and repopulate every reachable scope
   from its dir (an unreachable scope is skipped, per layer 1, not allowed to error
   the rebuild). Layer 1 is the optimization; layer 2 is always safe (derived). Neither a
   per-file parse failure nor an unreachable dir is a rebuild trigger — both are
   handled in layer 1, so one bad file or one offline scope never taxes a machine-wide
   command with a full rebuild.

Write-through: a `pj` mutation upserts its own row right after writing the file
(including `pj create`, so a just-scaffolded project is queryable before its body exists).
Direct agent edits are the read-through half, caught by reconcile via mtime.

DECISION: after reconcile (still git-free, still no file writes), pj runs cheap index
queries over the scopes just reconciled for offline-concurrent integrity signals:
duplicate project ids and equal `order` keys within a scope. Cost is one or two
aggregates over already-materialized rows — not a re-stat or re-parse of the dir.
Hits surface as a terse warning on the command (stderr) using the stable tokens
`duplicate_id:` / `equal_order:` (see Agent skill contract, Doctor and integrity
warnings); they do not auto-repair. File-mutating repair stays on `pj sync` (auto-commit
integrity step) and `pj doctor --repair` (all scopes). Rationale: the read path must stay
free of multi-file rewrites (a `pj next` must not rename projects), while plain-files
multi-machine still learns about collisions without waiting for a sync that does not
exist. See "Project ids" and "Metadata" for the repair procedure itself.

DECISION: `pj doctor` is diagnose-by-default. Bare `pj doctor` (and `pj doctor --reindex`)
never mutates project files or `pj.cue`. It reports every integrity class (text on
stderr/stdout; stable tokens where defined). File-mutating integrity is opt-in:
- `pj doctor --repair` — run the same automatic repairs as the auto-commit `pj sync`
  integrity step: offline-concurrent id collision (deterministic extension + in-scope
  edge rewrite) and equal-`order` re-space (tied files only). Identical bit-identical
  procedures. Scope: ambient scope, or every registered scope when no ambient / when
  doctor is run as a discovery command over the machine (same visibility as bare doctor
  reporting). Does not run pathological long-`order` band re-space (that is
  `--re-space-order` only).
- `pj doctor --re-space-order` — explicit local-band re-space for pathologically long
  `order` keys (soft threshold length > 64, or any keys the implementation selects in
  the reported band). Never combined silently into `--repair`.
- Durability of mutations: when the affected scope is auto-commit and a git-root exists,
  each repair batch self-commits the touched files (fixed messages, e.g.
  `pj: repair duplicate id <old> -> <new>`, `pj: repair equal order`, `pj: re-space order`)
  — same class as `status`/`reorder`, no push. Without a git-root, files are written and
  `sync_disabled:` may ride if auto-commit planned. Non-auto-commit: write files only
  (host or plain-files durability).
- `--reindex` — full index rebuild from files; never touches the files. Combinable with
  bare doctor; not a repair flag.
- `--repair` and `--re-space-order` may be combined with `--reindex` (reindex after
  repair is wise); both mutating flags may be passed together when the operator wants
  both classes in one invocation.
Rationale: agents are told to run doctor on warnings; diagnose-by-default prevents
surprise multi-file renames; `--repair` is the explicit off-sync twin of sync integrity;
self-commit on auto-commit closes dirty-repair holes.

### Query surface

- `pj search <terms> [--scope S]` — full-text over titles and bodies via FTS5 (bm25;
  phrase/prefix/boolean). Machine-wide by default, `--scope` to bound.
- `pj query <sql>` — read-only SQL over the index, for ad-hoc inspection. Rejects
  writes. The schema is explicitly not a stable API: derived, rebuilt on any
  `schema_version` bump, may reshape between releases with no migration. `--help` says
  so; `pj query --schema` prints the current shape. Not for saved queries or tooling.
- Rich `pj list` filters (status union positionals, `--tag`, `--scope`, `--all`,
  `--no-lens`) compile to index queries. No date filters on list in v1 — use
  `pj query` for ad-hoc date cuts.
- Dependency and rollup queries — transitive `depends` via `WITH RECURSIVE`, counts by
  status/scope — come from the index. The first-class CLI for edge inspection is
  `pj deps` (see "Status and dependencies" and "CLI surface"); `pj query` remains the
  ad-hoc escape hatch (schema not stable — agents prefer `deps`).
- `depends` and `related` are materialized as rows in one shared `edges` table
  (`from_id, from_scope, to_id, to_scope, kind`, `kind` in `depends|related`), populated
  by reconcile from frontmatter. One table backs `pj deps` (direct and transitive walks,
  reverse lookup), `WITH RECURSIVE` for ad-hoc `pj query`, and the planned viewer's
  project graph. Cross-scope edges are just rows where `from_scope != to_scope`
  — one machine-wide index, no special casing. An edge whose `to_id` matches no project
  row (unregistered scope, or a not-yet-synced target) is kept as a dangling row so the
  viewer can render it as an external node and `pj doctor` can surface the unresolvable
  ones.

WAL mode and `busy_timeout` from day one (see v1 concurrency DECISION above). v1 writers
are CLI processes only; SQLite's single-writer lock plus `busy_timeout` covers concurrent
CLI invocations. A future viewer that both reads and writes is out of v1 scope — its
second-writer coordination is designed when that process exists, not as latent CLI
complexity today.

## Sync model

Applies only to scopes with `autoCommit: true` (pj-driven). Repo-driven scopes
(`autoCommit: false` inside git) ride the surrounding repo's own git (human/PR-managed);
plain-files scopes (`autoCommit: false` outside git) do no syncing.

DECISION: durability and sync split along the commit/push seam.
- Durable-local: a mutating command commits its own change synchronously to the scope's
  repo; a direct agent/human edit is committed at the next `pj sync`. Work is never lost
  and no command blocks on the network.
- Remote sync: happens only in `pj sync`, whose push is synchronous and reported.
  Ordinary commands never push.

### Reads never touch git

DECISION: read commands (`next`/`list`/`get`/`deps`/`search`/`query`) are git-free. A read
reconciles the index from the files and answers; it does not commit, push, or run any
git subprocess. A direct agent edit is reflected because reconcile stats the files.
Consequence: a cross-machine read can be stale until the next `pj sync` fetch —
acceptable for a single user working mostly one machine at a time.

### Writes commit their own change

DECISION: a mutating command that produces a complete state (`status`/`reorder`/`archive`)
writes the file, write-throughs its row, then — when git self-commit is available —
commits just that file (`git add <file>` + `git commit -m "pj: wc-ab2c -> done"`).
Adding the specific path (not `-A`) leaves unrelated dirty files untouched. Synchronous,
tens of milliseconds, no push.

DECISION (autoCommit true, git not ready): `autoCommit: true` means pj *owns* the commit
path when a git-root exists; it does not require git at every write. Init may set
`--auto-commit` outside git ("planned" repo). Complete-state mutations always:
1. write the file and write-through the index (exit 0 on success of that write);
2. attempt self-commit only if a git-root is derived from `dir` (`git rev-parse
   --show-toplevel` succeeds). Self-commit does **not** require an upstream — local
   commits are fine; only `pj sync` needs a remote.
3. if no git-root: skip the commit without failing the mutation; emit a terse stderr
   line with stable token `sync_disabled:` (files landed; no git durability until the
   user creates a repo with plain git). Never `git init` / never invent a remote.
4. Mid-rebase refuse and git-root flock apply only when a git-root exists; without one
   there is no rebase state to corrupt and no commit span to lock.
When a git-root exists but has no upstream, complete-state mutations **do** self-commit;
`pj sync` alone reports `sync_disabled:` until upstream exists (same token, sync path).
Once the user adds git (and later upstream), the next write that can commit does so and
the next ready `pj sync` runs the full path. Distinct from `autoCommit: false` outside
git (plain files): that mode never self-commits and never runs `pj sync` by policy;
planned auto-commit without a git-root is temporarily commit-skipped with
`sync_disabled:` on writes until a git-root exists, then becomes full local self-commit
and (with upstream) full sync.

`pj create` is the deliberate exception: it scaffolds frontmatter plus an H1 from the
title argument and returns the path for the agent to fill the rest of the body directly,
so it produces an incomplete artifact by design and does not commit. Writing the skeleton
reserves the id and gives list/search a title; the complete project is committed at the
next `pj sync` snapshot (when git is ready). Principle: self-commit when the verb yields
a complete state and git self-commit is available; defer to sync when it yields a
scaffold; never block the file write on missing git for auto-commit scopes.

Concurrent writes in a scope serialize through a scope-level `flock` on
`<dir>/.pj.lock`, held for the whole reconcile -> write span. `pj create` takes it too
(without committing) so its draw -> check-local-ids -> write-skeleton is atomic and two
concurrent adds cannot draw the same id. Because a scope's ids and files live under its
own dir, this per-scope lock is sufficient for the file write and id draw even when
several scopes share a repo.

The git commit is the part that is repo-granular, not scope-granular: `git add`/`commit`
mutate the one shared index of the derived git-root, so for auto-commit the committing span
additionally takes a git-root lock (`<git-root>/.git/pj-sync.lock`). Two scopes in the
same repo therefore serialize their commits (and `pj sync`'s rebase/push) instead of
corrupting each other's index, while non-auto-commit never commit and need only the
per-dir lock. The locks cannot coordinate every index writer (a read command reconciles without the
git-root lock; concurrent CLI processes each open the DB), so index cross-writer
coordination is SQLite's single-writer WAL lock plus `busy_timeout`, not the flock. A
viewer process is not a v1 writer.

A mutating command on an auto-commit scope refuses at startup if that scope's derived
git-root is mid-rebase (a stat of `.git/rebase-merge`|`rebase-apply`) — a self-committing
write would land on the rebase's temporary HEAD and corrupt it. The refuse is keyed to
the self-commit path, not to "any mutation in any repo": non-auto-commit never
self-commit, so their mutating commands keep writing files even when the surrounding
repo is mid-rebase (a host monorepo mid-PR-rebase must not block `pj status`). Any
conflict markers that land in a project file are handled by the existing per-file
`parse_error` quarantine, not by freezing writes. For auto-commit the refuse fails fast,
naming the scope and file that paused the rebase so the block is legible even from a
sibling scope: `teamA-ab2c is mid-sync-conflict in shared repo <git-root> — resolve
<file> then run pj sync`. Reads are git-free and unaffected for every scope.

TRADEOFF (accepted): this mid-rebase refusal is repo-granular among auto-commit scopes,
not scope-granular, and that is the one place the per-scope isolation the design
otherwise holds does not reach. A paused rebase is git state on the shared `.git`, so a
conflict left unresolved in one auto-commit scope freezes writes to every sibling
auto-commit scope sharing that git-root until the rebase resolves — the same coupling
that makes "one `pj sync` pushes the whole repo" convenient. It does not freeze
non-auto-commit scopes (they share no self-commit path with the paused rebase). It is
bounded (reads stay git-free and fully available for every scope, including the
conflicted one; the freeze ends the moment the rebase is resolved and lasts only while
a human leaves it paused) and it never risks data (the refusal is fail-fast, not a
silent degrade). But it means the multi-scope-per-repo layout the design recommends for
auto-commit (a central pj repo of siblings) couples write-availability at the repo level
during a conflict — same repo-level coupling as an unparseable sibling `pj.cue` refusing
`pj sync` for the whole git-root (see "Configuration"), and distinct from a malformed
`pj.cue` or an unreachable dir for ordinary per-scope mutations, which still isolate
to their own scope. The error naming the blocking scope and file is what keeps it from
being a mystery to whoever hits it from another scope. A scope that must never be frozen
by a sibling's conflict or broken config belongs in its own repo (a different git-root),
the same remedy the autoCommit-consistency rule already points at.

### pj sync: the sole push boundary

DECISION: `pj sync` is the "done for now / reconcile now" command and the only place pj
pushes. It targets the ambient scope's repo; `pj sync --all` syncs every auto-commit
scope, and with no ambient scope `pj sync` syncs all. Because sync is repo-granular, both
the ambient case and `--all` operate per derived git-root, deduplicated: syncing the
ambient scope syncs its whole repo (every scope sharing it), and `--all` visits each
git-root once rather than re-fetching a shared repo per scope. Ambient-only is deliberate:
it matches the end-of-turn pattern (push where you worked), keeps the hot path to one repo,
and `--all` covers the world when wanted. It is bidirectional by construction — always
fetch, then push only if ahead — because reads are git-free, so sync is the sole point a
machine learns of another's work. Steps:

Caveat, cross-scope freshness: because bare `pj sync` fetches only the ambient scope, a
cross-scope `depends` target living in another auto-commit scope is only as fresh as that
scope's last fetch on this machine. Its status can lag until that scope is synced
(`pj sync --all`, or syncing it directly). This is the same "a cross-machine read can be
stale until the next sync" limitation reads already accept — documented here so a
cross-scope gate reading a stale target is a known bound, not a surprise. Not worth
splitting sync into ambient-push/all-fetch in v1.

1. Snapshot: `git status --porcelain -- <dir>...` — scoped to the registered
   auto-commit dirs sharing this git-root, never the whole working tree, and never a
   co-located non-auto-commit scope's dir even when it sits under the same git-root —
   then commits only paths that match the scope-file allowlist (below), one commit per
   file, message derived from class and porcelain code. Direct edits, `$EDITOR` edits,
   and filled `create` skeletons are included when they are allowlisted project files.
   DECISION: snapshot allowlist (closed for v1; expand only when a new first-class
   scope file is designed in). A dirty path under a scanned dir is committed only if it
   is one of:
   - A project `.md` at the dir root or under `archive/`, whose basename matches the
     project filename shape (`<id>-<slug>.md`). The id prefix is a legal scope name, `-`,
     and a short-id of length 4 through `SHORT_ID_MAX` (8) inclusive (create always 4;
     longer only after id-collision repair — see "Project ids"), first character a
     letter from the short-id alphabet, remaining characters from that alphabet, then
     `-` and the frozen create-time slug matching the closed slug grammar
     (`^[a-z0-9]+(-[a-z0-9]+)*$`, length 1–48; see Project ids `slugify`). Message:
     `??` -> `pj: add <id> <slug>`, modified -> `pj: edit <id>`.
     Parseability of frontmatter is not required to commit (an unparseable project
     file still needs to travel; reconcile already quarantines it as `parse_error`).
   - `pj.cue` at the dir root. Message: `pj: config <scope>`. Must sync so a second
     machine validates against the same schema.
   - `.gitignore` at the dir root (written by `pj scope init` for `.pj.lock`). Message:
     `pj: gitignore <scope>`.
   - `AGENTS.md` at the dir root only (optional human/agent note living beside the
     scope; not auto-written by pj). Message: `pj: agents <scope>`.
   Explicit non-members (never committed by pj, even if dirty under the dir):
   - `.pj.lock` (also gitignored; skipped defensively).
   - Any other path: vendor conflict copies, editor swap files, secrets, dumps,
     nested trees, `archive/` non-project files, random `.md` that does not match
     `<id>-<slug>.md`, and anything else. These are non-allowlist residue.
   Residue handling: leave uncommitted and unstaged; do not delete; emit a terse
   stderr warning naming each path (`N non-allowlist path(s) under <dir> not
   committed — move or remove; see pj doctor`). `pj doctor` lists the same residue
   for human cleanup (same class as external-sync conflict-copy names). Sync still
   proceeds for allowlisted dirty files and continues to fetch/integrate/push — residue
   is a hygiene warning, not a hard stop (a hard stop would block legitimate work when
   a conflict-copy or editor junk is present). There is no `pj sync --force-unknown`
   in v1: unknown bytes never ride the auto-commit push path. Blast radius accepted
   only for the allowlist itself (project bodies and config can still hold secrets if
   the author puts them there — ordinary git discipline).
   TRADEOFF: catch-all "commit everything under the dir" was rejected. Dir disjointness
   still prevents sweeping another scope's files or unrelated repo source outside the
   dir; it does not prove every byte inside the dir is safe to publish. "pj-only" is
   therefore enforced by membership, not by trusting the directory label.
   Scoping the snapshot to the auto-commit dirs remains what makes the repo-wide push
   safe against non-pj trees: such a dir is disjoint from every other scope's dir (the
   disjointness invariant enforced at registration; see "Scope lifecycle"), so an
   allowlisted path inside it cannot be another scope's file, while anything outside
   every auto-commit dir — unrelated source in a shared repo, a co-located
   non-auto-commit scope's tree — is never touched. The disjointness invariant is what
   forbids the one case that would break this — a sibling scope's dir nested inside
   this one, whose files a recursive `git status` would otherwise sweep under the wrong
   scope. A repo holding several pj scopes snapshots the union of their auto-commit
   dirs, so "one `pj sync` pushes the whole repo" still means every auto-commit scope
   in it (allowlisted paths only), not the non-pj remainder.
   Crucially, the snapshot's candidate dirs are defined by autoCommit, not by the
   autoCommit-consistency invariant continuing to hold: the safety does not assume
   every scope under this git-root is auto-commit. That invariant (enforced at init; see
   "Scope lifecycle") keys on a git-root that is derived at runtime, so a later
   git-topology change — a `git init` at a parent, a moved dir, a new remote — can bring
   a non-auto-commit scope under an auto-commit scope's git-root after both were
   registered. Sync must therefore not sweep by git-root membership alone. As a
   preflight, `pj sync` re-derives the git-root of every scope sharing this root and
   refuses to proceed if (a) any of those scopes has an unparseable `pj.cue` —
   autoCommit unreadable, same fail-closed class as a mismatch; see "Configuration" —
   or (b) their declared autoCommit values disagree (`scope <x> (autoCommit false)
   shares this git repository with auto-commit scopes — split it into its own repo or
   re-declare autoCommit`), rather than pushing under a silently violated or
   unverifiable invariant; `pj doctor` runs the same per-git-root checks off-sync
   (unparseable sibling + autoCommit divergence) and flags both.
   The index lives in XDG state; the scope lock is covered by the `.gitignore` that
   `pj scope init` writes into the dir, and the snapshot skips `.pj.lock`
   defensively regardless — so neither ever appears here.
2. Fetch and integrate, unconditionally. Always fetch; if the remote advanced, rebase
   local commits onto it, running the frontmatter merge on any conflicted file. Conflicted
   `pj.cue` files are resolved before any project `.md` field-merge in the same integrate
   (see "Merge conflict handling") so custom-field typing uses one post-integration schema.
   This runs whether or not step 1 produced a commit, so a read-only machine still pulls
   others' work. An unresolvable body conflict leaves the store in a paused rebase,
   marked and reported, never discarded — nothing is pushed until it resolves, auto-commit
   mutating commands refuse meanwhile, and a later `pj sync` resumes the paused rebase.
3. Integrity repair over the merged tree, per scope touched: duplicate ids and tied
   `order` keys — the offline-concurrent artefacts that land at different paths, so the
   rebase merges them clean and no git conflict fires. For auto-commit, sync owns the
   automatic repair here (rename the side nothing depends on and rewrite in-scope
   `depends`/`related` atomically — in-scope reference-safe; cross-scope edges are
   recorded for doctor to verify, not rewritten; re-space tied `order` keys) rather than
   deferring to `pj doctor --repair`. Both write only the files they touch and commit
   under a fixed message. (Detection of the same conditions is cheaper and universal —
   every command's post-reconcile index check, all scopes — and never mutates files; see
   "Invalidation and reconcile". Non-auto-commit repair only via `pj doctor --repair`.)
4. Push synchronously (blocking) if ahead. Step 2 already integrated the remote, so an
   ordinary sync fast-forwards; a reject means the remote moved in the fetch->push race,
   handled by looping to step 2 once more. A sync with nothing to push (a read-only
   machine) skips the push — it already pulled in step 2.
5. Report unpushed count, conflicts, repairs, and any non-allowlist residue warnings.

Blocking on the push (~100ms-1.5s, dropped toward ~100ms by SSH `ControlMaster` reuse)
is negligible against LLM latency and is what makes sync reliable: when it returns, the
remote has the work and any conflict has surfaced in sync's output. `pj skill` tells
agents to run `pj sync` at end of turn only for pj-driven (`autoCommit: true`) scopes —
not on repo-driven or plain-files (see Agent skill contract, End of turn). Forgetting
sync on a pj-driven scope costs a delayed push, never data. No `pj save`/`pj end` verb —
`pj sync` is that boundary for auto-commit only.

This replaces any background/detached push: such machinery is inert under an agent
harness that reaps the command's process group before a child completes, and cannot
reliably report a merge conflict from a reaped child. Blocking `pj sync` puts conflict
resolution where it can be seen.

Health: `git rev-list --count @{u}..HEAD` gives the unpushed count. A last-push-error
marker records failures for `pj doctor` and write-command warnings — pure operational
git-root state, not project metadata. It lives at
`<git-root>/.git/pj/last-push-error` (pj-owned directory under `.git`, never committed,
never in the dir, never in the rebuildable index as sole copy). Cleared on the
next successful push. This is distinct from terminal-status dispute, which is recorded
in the project file via `status_conflict` (see "Merge conflict handling"), not under
`.git`. Before the unpushed count is meaningful there is the precondition pj does not
create — the repo itself and the autoCommit mode:
- Non-auto-commit ambient scope (repo-driven or plain-files): `pj sync` refuses with a
  clear error naming the mode (`sync is for auto-commit scopes only — this scope is
  repo-driven; commit project files with the host repo` / `… plain-files; there is no
  pj sync — run pj doctor if integrity warnings appear`). `pj sync --all` skips
  non-auto-commit scopes (or visits only auto-commit git-roots); it does not error solely
  because other registered scopes are non-auto-commit.
- Auto-commit: sync first checks the scope is a git repo with an upstream (a `.git` stat,
  then `git rev-parse --abbrev-ref @{u}`), and if not, reports sync disabled with stable
  token `sync_disabled:` and a professional message (`sync is disabled until this scope
  is a git repository with a remote; set one up with git, then pj sync`) rather than a
  raw git error. The same token rides complete-state write commands that skip self-commit
  for the same reason (see Writes). Reads stay git-free and do not carry it.

### Auto-commit

DECISION: each scope declares `autoCommit: bool` in `pj.cue`. It is a scope property,
identical on every machine, so it is synced, not machine-local. The useful control is
one bit: whether pj commits. Host vs plain-files is derived from "is the dir inside a
git repository?", not a third stored choice.

| `autoCommit` | In a git repo? | Behaviour |
|---|---|---|
| `true` | yes, with upstream | pj-driven: complete mutations self-commit; `pj sync` is the fetch/push boundary |
| `true` | yes, no upstream yet | pj-driven local: complete mutations self-commit; `pj sync` reports `sync_disabled:` until upstream exists |
| `true` | no git-root | pj-driven planned: file + index writes succeed; self-commit skipped with `sync_disabled:` on those writes; `pj sync` same token; pj never creates the repo |
| `false` | yes | repo-driven: surrounding repo / human / PR commits; pj never runs git |
| `false` | no | plain files: no VC (or external Dropbox/Syncthing/NFS); pj never runs git |

Help-text honesty: "auto-commit" means pj owns the commit path when a git-root exists, not
every keystroke and not "fail closed without git":
- `status` / `reorder` / `archive` → write always; self-commit when `autoCommit: true`
  and a git-root exists (upstream not required); if no git-root, `sync_disabled:` and no
  commit
- `create` → scaffold only (frontmatter + H1); complete project commits at `pj sync`
  snapshot (when git-root exists)
- direct agent / `$EDITOR` edits → committed at `pj sync` (when git-root exists)
- push only in `pj sync`, never automatic; `pj sync` reports `sync_disabled:` until
  repo+upstream

When `autoCommit: false` inside git (repo-driven): a single host repo may carry many
scopes (a monorepo, one scope per team/area). pj never runs git.

When `autoCommit: false` outside git (plain files): single machine, or cross-machine
handled externally. The index still serves reads; only sync is absent. Multi-machine
integrity is detect-on-reconcile + repair-via-`pj doctor --repair` (no automatic repair
seam — there is no `pj sync`). External sync conflict-copy filenames are doctor-flagged residue,
not merged. Prefer `autoCommit: true` when git-shaped multi-machine merge and automatic
integrity matter.

`autoCommit` is a per-repo fact: every scope sharing a derived git-root must declare the
same value (enforced at init). Because auto-commit sync operates on the git-root,
syncing any scope in a multi-scope auto-commit repo fetches/rebases/pushes that repo once
and its snapshot step commits every scope's dirty files under the one push — the "one
push syncs everything" behaviour a central pj repo wants.

DECISION (init help / skill messaging): multi-scope under one auto-commit git-root
optimises **one-push sync**, not isolation. Coupling that is intentional and must be
stated plainly in `pj scope init` help, errors, and `pj skill`:

- One unparseable sibling `pj.cue` → `pj sync` refuses the whole git-root.
- One mid-rebase / terminal dispute / body conflict → auto-commit mutating commands
  refuse for every auto-commit sibling sharing that root.
- autoCommit divergence across the derived root → sync preflight refuse.

Isolation (one scope must never freeze another) requires a **separate git-root** —
sibling repository or submodule — not merely a separate scope dir in the same repo.
Init help for `--auto-commit` and multi-scope layouts must say this; do not market
central multi-scope as free fault isolation.

DECISION: pj never creates or manages the git repo — no `git init`/`git remote`/
`git clone`. For an auto-commit scope the user creates the repo and its remote with plain
git first, then runs `pj scope init` inside it, and clones onto other machines themselves
(then `pj scope import`). pj shells out to git for commit/fetch/push but owns none of the
repo's lifecycle. When the repo or upstream is missing, sync is reported disabled (the
warning above); the file writes still land on disk.

### Git dependency

DECISION: pj shells out to the external `git` binary rather than driving git in-process.
It uses the user's git version, credential helpers, and SSH config for free — including
`ControlMaster` connection reuse — and carries no git library. git is required only for
auto-commit scopes and only on the write and `pj sync` paths; reads and reconcile are
git-free. This satisfies "pure Go, no cgo" (a subprocess is not cgo).

## Merge conflict handling

Auto-commit only (where pj drives the rebase). Repo-driven defers to git plus the human
on the PR; plain-files never merges (external filesystem sync clobbers whole-file).
Four layers, lightest first.

1. Structure to avoid: one file per project means edits to different projects never
   touch the same file. Reordering holds too, because `order` is an integer+fraction rank
   key — `pj reorder` rewrites only the reordered file, and `keyBetween` steps the integer
   part and/or grows the fraction rather than renumbering neighbours (see "Metadata").
   There is no registry inside the repo to contend on.
2. Shrink the window: `pj sync` fetches and rebases inline before pushing, so git
   auto-merges non-overlapping text and any conflict surfaces in sync's own output.
3. Semantic merge of frontmatter, by post-rebase stage parsing (not a git merge driver —
   a driver fires on every merge in the repo, including a host PR, and would require the
   pj binary there). pj lets the rebase produce standard conflicts, then field-merges.
   DECISION: schema-before-data ordering. Custom field merge typing reads each scope's
   on-disk `pj.cue` after that file is integrated, not a mix of base/ours/theirs mid-loop.
   Within one integrate (and when resuming a paused rebase), process conflicted paths in
   this order:
   1. Every conflicted `pj.cue` under an auto-commit dir sharing this git-root.
      `pj.cue` is config, not project frontmatter: resolve it with ordinary git text merge
      when auto-merge succeeds; if it conflicts, pause the rebase on that file for a human
      (no silent field-merge of CUE — a wrong autoCommit/schema guess is the failure the
      unparseable-config rule already refuses). Do not field-merge any project `.md` in a
      scope whose `pj.cue` is still conflicted or unreadable after this step — fail closed
      with the same class of error as an unparseable `pj.cue` (`scope <x> config unparseable
      — fix <dir>/pj.cue before sync can merge projects`).
   2. Conflicted project `.md` files: load the now-current on-disk `pj.cue` for that
      scope (cached evaluation still keyed by import-closure mtime/size) and use its
      `fields` / `statuses` declarations for typed list vs scalar rules and terminal
      predicates. Keys absent from that declaration stay on the undeclared scalar path.
   Steady-state and merge-time therefore share one rule: the declaration is whatever
   `pj.cue` currently says on disk. Concurrent schema+data evolution is serialized by
   resolving config first; a human stuck on a conflicted `pj.cue` must finish that before
   project merges run — the same availability coupling as sync preflight when a sibling
   config will not parse.
   For each conflicted project file, read the three stages (`git show :1/:2/:3:<f>`),
   split each into frontmatter and body, and field-merge the frontmatter.
   - Same-id add/add guard (checked first): if there is no base stage (`:1` empty — an
     add/add conflict) and both sides carry the same `id`, the two stages are distinct
     projects that collided on both id and slug (the same-title sub-case in "Project ids"),
     not two edits to one project. Do not field-merge — that would collapse two projects
     into one and lose one. Resolve it automatically with the id-collision repair from
     "Project ids": keep the side nothing depends on renamed (deterministic short-id
     extension, new path), keep the other at its path, rewrite in-scope `depends`/`related`
     and record cross-scope inbound edges for `pj doctor` to verify, then stage both files
     so the rebase continues, and report a repaired duplicate. This is not a layer-4 human
     handoff — it is the same automatic repair the sync integrity step runs, triggered here
     because the shared slug made the paths coincide instead of landing as two clean files.
   Otherwise (a shared base stage — the ordinary case of two edits to one project) field-merge
   the frontmatter:
   Every field is merged 3-way against the base stage (`:1`), scalars included, so
   "changed on one side" is never confused with "changed on both". The base stage is
   already parsed and already trusted for the list merge; the scalar rules use the same
   term rather than comparing only the two sides.
   - List fields (`tags`, `depends`, `related`, `links`, and every custom field whose
     declared `type` is `strings`): 3-way set merge against the base stage — base plus
     either side's additions, minus either side's removals; an add/remove clash keeps.
     This honours a concurrent removal (a pruned `depends` stays pruned, not resurrected).
   - Scalars (`status`, `order`, `summary`, `created`, `id`, and every custom field whose
     declared `type` is `string`/`int`/`bool`), one side changed: when exactly one of the
     two stages differs from the base, that side changed and the other did not, so take the
     changed value uncontested — no tiebreaker, no handoff. This is the common cross-machine
     case (one machine runs `pj status <id> done` while the other edits the body or an
     unrelated field): the completion or reorder lands cleanly and is never reverted by the
     other side's later commit timestamp.
   - Scalars, both sides changed and not a status dispute (below): a genuine two-sided
     edit, so last-writer-wins by git commit timestamp (author date). `order` follows
     this; a tied key resolves at read time by `(order, id)`. Both-sides `status` pairs
     where **neither** value is terminal (e.g. `todo` vs `in-progress`) use this path.
     Custom scalars use this path only — there is no dispute key for custom fields.
   - A frontmatter key that is undeclared (not a built-in and not in `pj.cue` `fields`) is
     merged as a scalar string-ish last-writer-wins when both sides touch it, otherwise
     one-side-changed wins; doctor already warns on it. Prefer declaring the field so the
     typed list/scalar rule applies.
   - Scalar `status`, both sides changed from base to two different values, and **at least
     one** of those values is terminal: do not auto-merge, do not pick a winner. Terminal
     is one definition shared with `depends` satisfaction, done-class list filters, and
     merge dispute: a value is terminal when it is built-in `done` or `cancelled`, or a
     CUE custom status whose declared `category` is `done` (e.g. `shipped`, `wontfix`).
     Dispute examples: `done` vs `cancelled`, `done` vs `shipped`, `shipped` vs `wontfix`,
     **and** `done` vs `in-progress`, `cancelled` vs `todo`, `shipped` vs `review` —
     any terminal-involved both-sides disagree, so LWW cannot silently erase a completion
     or abandonment under concurrent multi-machine edit. Pure non-terminal pairs stay on
     the LWW path above. Keep the frontmatter clean YAML — never conflict markers in it —
     so the file stays parseable and indexable: write `status` to the merge-base
     (last-agreed) value and write the disputed pair into the built-in
     `status_conflict: [a, b]` key on the same file (see Metadata; `a` and `b` are the two
     post-edit status names). Route the file into layer 4's paused-rebase handoff for a
     human to decide (complete, abandon, or reopen). The dispute lives in the project
     file — source of truth — not in out-of-band "sync-state" or as index-only memory;
     reconcile materializes it like any other frontmatter, so rebuilds cannot drop the
     choice. One-sided completion (only one side changed `status`) still takes the
     changed value uncontested via the one-side-changed rule above.
   pj always resolves the frontmatter to clean YAML (never leaves markers in it, so the
   file stays parseable and indexable); the body is layer 4's concern, resolved
   independently within the one file.
   The arbiter is the git commit timestamp, not a frontmatter timestamp — which is why the
   schema carries no `updated:` field. Such a field could only stay honest if every edit
   stamped it, but a direct agent edit never goes through pj and reconcile is git-free and
   must not write files, so it would sit stale on the common edit path and judging a merge
   by it would keep the older edit. Git records a commit timestamp for every change, direct
   or pj-authored, so the arbiter is always present with no maintained metadata. The merge
   base is git's stage-1, never an in-frontmatter `previous:` snapshot (which would go stale
   for the same reason and reintroduce the dead metadata `updated:` would have been).
   Residual: for a direct edit the timestamp is the commit time, not the keystroke time,
   so two machines editing the same project offline can invert if their snapshot order
   disagrees with their edit order — the same bounded, single-user, concurrent-offline
   window the id analysis treats as near-never.
4. Surface, never hide — two handoffs, and neither ever puts a conflict marker in the
   frontmatter. A body (prose) conflict git could not merge: pj writes the file with its
   frontmatter already field-merged and git markers confined to the body region, and leaves
   it unstaged so the rebase stays paused; the human edits the body to resolve, and the next
   `pj sync` resumes the rebase (`git rebase --continue`) and pushes. A status dispute
   layer 3 declined (both sides changed `status`, values differ, at least one terminal):
   there is no body conflict and pj writes no markers at all — the frontmatter carries
   merge-base `status` plus `status_conflict: [a, b]` (the two post-edit names), clean and
   indexable, so `pj get`/`pj meta`/`pj doctor` surface "status conflict — set status to
   one of: <a>, <b> (or another known status) and remove status_conflict in <file>". The
   path is left unstaged, so the rebase stays paused at the git level; the fail-fast that
   closes the silent-erasure hole is that `pj sync` refuses to `git rebase --continue`
   while `status_conflict` is still present on the file, rather than sailing past a file
   that only looks resolved. The human makes the call by editing `status:` to either
   disputed value or another known status (including a non-terminal reopen) and deleting
   `status_conflict` — a direct file edit, exactly as a body conflict is resolved in-file,
   and correct because a `pj status` mutation on an auto-commit scope is refused
   mid-rebase; the next `pj sync` sees the key gone, stages the file, continues the
   rebase, and pushes. Common to both: nothing is pushed, every auto-commit mutating
   command refuses while the rebase is in progress (fail fast), and the file is reported
   via `pj doctor`. Reads stay git-free, so `pj next`/`get`/`search` keep working — only
   auto-commit mutation is blocked, correct while the base is inconsistent. Because the
   frontmatter is resolved to clean YAML before the file is written, the index can read the
   project throughout — whether a body or a status decision awaits a human.

DECISION: frontmatter merge is a pure-function package, tested before live rebase wiring.
The rebase driver only loads stages, calls the package, and stages/writes results — it
does not embed field rules. Package shape (names illustrative):

```
MergeFrontmatter(base, ours, theirs []byte, schema ScopeSchema, meta MergeMeta) (Result, error)
```

- Inputs: raw stage blobs (`:1` may be empty for add/add), the scope's post-integration
  schema (built-ins + `fields`/`statuses` from on-disk `pj.cue`), and merge metadata the
  pure core needs (e.g. git author dates for both-sides scalar LWW, inbound-depends
  hints for same-id add/add loser pick when those are supplied by the driver).
- Outputs: one of clean merged frontmatter YAML; `status_conflict` dispute payload;
  same-id add/add rename directive (keep path / new path / new id); or error. Body is
  out of scope — the driver attaches body/markers separately.
- No git subprocess, no filesystem, no index, no flock inside the package. Deterministic
  for fixed inputs (including residual SHA-256 of stage bytes where the id-repair path
  needs it).

Implementation order: land the package with table-driven tests on canned `:1/:2/:3`
blobs and only then wire `pj sync` rebase. Do not discover merge bugs only under live git.

Required adversarial fixtures (minimum; extend freely):

| Case | Expect |
|---|---|
| List: base+ours add, theirs remove same tag | set-merge: add/remove clash keeps (or documented rule) |
| List: concurrent depends prune vs add | pruned id stays pruned; new id kept |
| Scalar one-side: ours `status: done`, theirs body-only / unrelated field | take `done` uncontested |
| Scalar both-sides non-terminal: `todo` vs `in-progress` | LWW by commit timestamp meta |
| Status dispute both terminal: `done` vs `cancelled` | base status + `status_conflict: [done, cancelled]`; no markers in FM |
| Status dispute terminal vs non-terminal: `done` vs `in-progress` | same dispute path — not LWW (must not erase completion) |
| Status dispute with custom done-category | e.g. `shipped` vs `done` or `shipped` vs `review` same path |
| Custom `strings` field vs custom scalar | typed rules from schema, not both treated as tags |
| Undeclared key both sides | scalar-ish LWW; not dropped |
| Same-id add/add (`:1` empty, same id both sides) | rename repair directive, never field-merge |
| Schema-before-data: call without readable schema | error / refuse — not guess types |
| `created` / `id` immutables | never invented or flipped by LWW against base identity |
| Empty/malformed stage YAML | error or quarantine signal; no silent half-merge |
| Equal both-sides scalar change (identical new value) | clean take; not false dispute |

Honest boundary: this trades beads' automatic Dolt cell-merge for a small custom
frontmatter merge plus human resolution of bodies. Good trade because one file per
project keeps same-file collisions rare and the frontmatter surface is tiny.

## Status and dependencies

DECISION: eight flat built-in statuses. Lowercase, hyphen-joined; no spaces or
underscores. `pj doctor` rejects a space in a status.

| status | meaning | in `pj next`? | in default `pj list`? |
|---|---|---|---|
| draft | authoring / not implementable yet | no | yes |
| backlog | someday/maybe, not committed | no | no (`--all`) |
| todo | committed + ordered, ready | yes (if `depends` all terminal) | yes |
| review | under review (plan or result) | no | yes |
| in-progress | actively worked | no | yes |
| blocked | manually set; reason in body | no | yes, flagged |
| done | complete (terminal) | no | no (`--all`) |
| cancelled | abandoned (terminal) | no | no (`--all`) |

DECISION: `draft` closes the gap between `backlog` (someday, not committed to the queue)
and `todo` (committed, ordered, next-eligible). `pj create` produces an incomplete scaffold
by design: valid frontmatter, an H1 from the title argument, and an otherwise empty body
to fill next. A bare scaffold is not ready work, so the create default is `draft`, not
`todo`. `draft` is not backlog: intent is often already committed, only the body is
unfinished. It is a built-in (not a CUE custom) so every scope gets an honest create
default without declaring customs; customs never enter `pj next` and cannot be the create
default without every scope opting in. Not a draft workflow engine — one label, clear
next/list rules, promote with ordinary `pj status`. Empty-body skip in `pj next` is not
required while `draft` is the default (optional defence in depth later, not v1).

DECISION: statuses are labels, not a workflow. No enforced transition graph; any jump is
legal (`todo -> done` directly). pj validates only that a value is known (built-in or
CUE custom); it never rejects a transition.

DECISION: `review` is one position-agnostic status meaning "the project itself is under
review" — before implementation (plan review) or after (result review). Not split into
`plan-review`/`review`, which would smuggle workflow position into a status name.

DECISION: `blocked` is manually set, human-owned, meaning "stalled, reason in the body."
pj never auto-writes it.

DECISION: `depends` is a separate runnability filter, not a status. A `todo` whose
dependencies are not all terminal is skipped by `pj next` and annotated "waiting on
<id>" in `pj list`; its status stays `todo`. This keeps dependency-gating derived
(never stale) while status stays manual.

DECISION: `depends` may be cross-scope. A `depends` id whose scope prefix differs from
the project's own addresses another registered scope; because one machine-wide index
holds every scope's rows (namespaced by a `scope` column), resolving the target's status
is a single query, not a second reconcile boundary. `pj next` extends its reconcile to
the transitive closure of the depended-on scopes so a cross-scope gate reads current
state, not a stale row. (The earlier same-scope-only restriction was justified by "keep
`pj next` a single-scope reconcile"; the single-index architecture dissolves that, so it
is lifted.)

DECISION: an unresolvable `depends` target is held, not surfaced. When a `depends` id's
scope is not registered on this machine (the repo was never cloned here), or the id
matches no project row, its terminal-ness cannot be confirmed, so the gate treats it as
unsatisfied: the dependent stays out of `pj next`, annotated `waiting on <id> (scope
<s> not registered here)` in `pj next`/`pj list`. Held-not-
surfaced is deliberate — for a work queue, telling the agent to start work whose
prerequisite cannot be confirmed done is a worse error than a false hold, and the
annotation is self-correcting (register or clone the scope, or clear the edge). Never
silent. `pj doctor` reports an unresolvable cross-scope `depends` informationally, not as
a hard error: it cannot distinguish "scope not cloned here" from "target never existed",
so it names the gap rather than condemning the config. A same-scope dangling `depends`
stays a hard flag (the scope is present, so the id is genuinely wrong).

DECISION: cross-scope edges are surfaced-not-auto-repaired by the id-collision repair.
The `pj sync` integrity step that renames an offline-concurrent duplicate id rewrites
in-scope references atomically (same repo, same rebase). A cross-scope reference lives in
another scope's repo, synced independently and possibly absent, so it cannot be rewritten
in the same operation; the repair rewrites the in-scope edges and records each cross-scope
inbound edge for `pj doctor` to flag. The subtlety it flags is a silent mispoint, not a
dangle: the kept side retains the original id, so a cross-scope reference meaning the kept
side stays correct, but one meaning the renamed side now resolves to a different project.
`pj doctor` surfaces it for human confirmation rather than letting it resolve wrong
silently. The collision-repair tie-break therefore counts cross-scope inbound `depends`
(read from the machine-wide `edges` table) at least as heavily as in-scope, since a
cross-scope-referenced id is the one it cannot auto-rewrite and so most wants to keep. Full
mechanics in "Project ids". This is a compound near-never — a newborn duplicate id that
also acquired a cross-scope reference before its first sync — so best-effort detection is
proportionate.

DECISION: `related` is a soft, non-gating project-to-project link that ships in v1. It
carries the same shape as `depends` (a list of project ids, same- or cross-scope) but
gates nothing — it is pure "see also"/provenance ("this project exists because of
<id>"). None of the `depends` runnability machinery (the terminal check, the reconcile-
closure extension, the held-not-surfaced trichotomy) applies to it; the only difference
that matters between the two edge kinds is that `depends` gates `pj next` and `related`
does not. It is distinct from `links`: `links` holds external artefacts as free-form
strings (PRs, issues, branches, URLs), `related` holds project ids, so the project graph
stays queryable separately from external references. It is a first-class indexed edge,
not frontmatter-only, so reverse lookup ("what relates to <id>?") and the planned viewer's
graph both read it from the index (see "Read interface"). An unresolvable `related`
target is cosmetic — it never gates — so `pj doctor` notes it only in passing.

DECISION: `pj deps <id>` (alias `pj depends <id>`) is the first-class read verb for a
project's edge neighbourhood. Pure read, git-free, after reconcile over the machine-wide
`edges` table (and project rows for status/label). It does not mutate frontmatter —
authoring `depends`/`related` remains a direct file edit. Id resolution matches `pj get`
(short form: exact short-id length 4–8 in ambient scope; full `<scope>-<short-id>` any
registered scope — same resolution as `pj get`). Unknown id →
non-zero exit, message on stderr, no neighbourhood on stdout.

Default output is flat, direct neighbours only (for `a → b → c` meaning "depends on",
`pj deps b` shows prerequisites **c** and dependents **a**). Always three sections:

1. **depends on** — direct outbound `depends` (prerequisites of the subject).
2. **is depended on by** — direct inbound `depends` (impact / who is waiting on the subject).
3. **related** — soft links both directions, clearly non-gating: outbound (→ target listed
   on the subject's `related`) and inbound (← other projects that list the subject). Related
   never participates in `pj next` gating and is never expanded into a depends tree.

Each neighbour line: id, status, short label (title or `summary`). Unresolvable or
unregistered cross-scope targets stay listed with an annotation (same spirit as list's
held-not-surfaced notes) — never silently dropped. Empty sides print a quiet one-liner
`(none)` so section structure stays stable for agents.

Flags (v1 closed set):

| Flag | Audience | Effect |
|---|---|---|
| *(none)* | agents and humans | direct edges only (above) |
| `--transitive` | mainly agents | expand **depends** both directions over the full chain; still **flat** lines (easy to scan). **Related stays direct** — soft links are not a runnability DAG |
| `--tree` | mainly humans | pretty-print the **depends** graph (implies transitive depth); related remains a flat section after the tree, not mixed into tree nodes |
| both | — | tree presentation; transitive expansion is already implied |

Walks are cycle-safe: on revisit, stop that branch (no infinite expansion). If the subject
is in a `depends` cycle, print one warning (stderr) pointing at `pj doctor` for detail —
`deps` does not dump a second cycle diagram. Full cycle reporting stays on doctor (and
`pj next`'s empty-because-blocked diagnostic). No mutation flags, no `--related` toggle,
no `--json`. Open a neighbour for edit with `pj get <id>` (path hand-off unchanged).

Rationale: outbound `depends` alone is already in the file; the index earns reverse impact
and transitive expansion. A dedicated verb beats teaching agents free-form `pj query` over
an unstable schema for a core graph question. Flat `--transitive` serves agents; `--tree`
serves human multi-level browsing without forcing agents to parse box-drawing.

DECISION: every terminal status satisfies a `depends` gate — built-in `done`/`cancelled`
and any custom with `category: done`. A `cancelled` (or custom done-category) dependency
satisfies the gate exactly as `done` — otherwise cancelling one project would permanently
strand every dependent (cancelled never becomes done). Because auto-unblocking a dependent
whose prerequisite was abandoned may be wrong, `pj doctor` flags any project depending on a
`cancelled` project so the human decides whether it still applies. (Custom done-category
abandonments are the same shape if the scope uses them; doctor can flag those the same
way once the category is known.)

DECISION: `pj next` diagnoses an empty-because-blocked queue: when no project is runnable
while `todo`s wait on unmet `depends` or a cycle, it reports `nothing ready; N todo(s)
waiting on unmet deps` rather than a bare "nothing ready", so a dependency-blocked scope
is not mistaken for a finished one.

DECISION: claiming is a status write, not new machinery. `pj next` is a pure read, so
two concurrent agent sessions on one scope (an ordinary setup) would both receive the
same project. The claim protocol: set `pj status <id> in-progress` immediately after
`pj next`, before starting work. `in-progress` is excluded from `pj next`, the write
serializes under the scope flock and (auto-commit) self-commits, so the second session's
`pj next` returns the next runnable project instead. `pj skill` states this
next-claim-work loop as the required agent workflow. The residual race — two `pj next`
calls in the seconds before either claims — is accepted: the window is small, and the
collision surfaces at the file (the second claim edits an already-in-progress project,
visible in `pj list` and at commit). Real claim semantics (`pj next --claim`) are
rejected: they would make a read command a writer, breaking the reads-never-touch-git
invariant to close a seconds-wide, self-surfacing window.

DECISION: abandoned claim detection (v1). An agent that claims then dies leaves
`in-progress` forever out of `pj next`. No auto-status change, no lease, no assignee
field — statuses stay labels. Hygiene is detect-and-surface only:
- Bare `pj doctor` soft-warns with stable token `stale_in_progress:` when a project's
  `status` is the built-in `in-progress` (not custom `category: wip` in v1 — customs stay
  out of this signal) and the project file's mtime is older than
  `STALE_IN_PROGRESS = 72h` wall-clock relative to doctor run time.
- Predicate uses file mtime (same class of cheap stat as reconcile), not a frontmatter
  `updated:` stamp (reconcile must not write; direct edits would leave stamps stale).
  Clock skew and restore-from-backup can false-positive or false-negative; doctor text
  may note the age; agents treat the token as "inspect and decide," not auto-reopen.
- Never auto-set `todo` / never clear claim. Recovery is an ordinary status write after
  human/agent review of the body: typically `pj status <id> todo` (return to queue) or
  `blocked` with a body reason, or continue work if still active.
- Optional human list affordance: `pj list in-progress` (or default board) may annotate
  the same stale predicate in the summary line; not required for agents who match the
  doctor token.

DECISION: CUE custom statuses are additive; the eight built-ins are immutable (including
`draft` — must not be redeclared under `statuses`). Each custom status declares a
`category` (active/wip/backlog/done) so views treat it correctly without knowing its name
(beads' `StatusCategory`, reused). Declaration form is
`statuses: { <name>: { category: <cat> } }` in `pj.cue` (see "Configuration").

Category matrix for customs (locked — implementers do not invent view behaviour):

| category | in `pj next`? | in default `pj list`? | terminal (`depends` + merge dispute)? |
|---|---|---|---|
| active | no | yes | no |
| wip | no | yes | no |
| backlog | no | no (`--all`) | no |
| done | no | no (`--all`) | yes |

Only built-in `todo` is ever next-eligible (and only when its `depends` are all terminal).
Customs never appear in `pj next` regardless of category — the agent queue stays one
status (`todo` -> claim `in-progress`), matching "statuses are labels, not a workflow".
Built-in `draft` is view-equivalent to category `active` (show in default list, not next,
not terminal) but remains a built-in name, not a custom category. `active` and `wip` both
mean "show in the default list"; the split is for human/viewer grouping (open vs in-flight
labels), not a second ready path. `backlog` hides like built-in `backlog`; `done` hides
like built-in `done`/`cancelled`.

Terminal is one predicate shared by `depends` satisfaction, default list exclusion for
done-class statuses, and merge dispute: built-in `done` or `cancelled`, or any custom
whose `category` is `done` (e.g. `shipped`, `wontfix`). There is no separate
`cancelled` category — abandonment-shaped customs use `category: done` (same as
`wontfix`). A custom done-category status satisfies `depends` and is excluded from
default `pj list` like `done`/`cancelled`; two machines that both-sides-change `status`
to different values where at least one is terminal dispute rather than last-writer-wins
(see "Merge conflict handling") — including done vs in-progress, not only done vs cancelled.

DECISION: CUE custom frontmatter fields ship in v1. A scope declares them under
`fields` in `pj.cue` with a closed type set (`string`/`int`/`bool`/`strings`) and an
optional `values` enum for string kinds. Keys are flat in project YAML (no nested
wrapper in the file). Agents read customs from the file. Merge typing follows the declaration
(list vs scalar). No required-field flag and no `pj set` verb in v1 — optional on every
project, authored by direct edit; header inspect is `pj meta` (read-only). Full shape
and validation rules in "Configuration".

DECISION: `pj create` defaults new projects to `draft`; optional second positional
status sets any known status (`todo` when the body is already known, `backlog` for
capture without intent to author soon). Promote to the ready queue with
`pj status <id> todo` after the body is implementable. Scaffold contents (locked):
built-in frontmatter with `id`, `status` (default `draft`), `order` (append key),
`created` (RFC3339 now), and empty or omitted list keys (`depends`, `related`, `tags`,
`links`) and empty/omitted `summary`; body is exactly one H1 line `# <title>` from the
create argument (slug frozen from that title); no other body sections. Agent fills the
project writing-guide shape under that H1. Two project-to-project edge kinds — `depends`
(blocks, gates `pj next`) and `related` (soft "see also", gates nothing) — not beads'
~15 types.

## Tags and lens

DECISION: the term is `tags` (free-form keywords), not `labels`. Projects carry
`tags: [...]` in frontmatter.

DECISION: a lens is a machine-local default tag view, per scope, in the XDG config,
keyed by scope name (scope names are machine-unique, so the name alone is a sufficient
key). It ships in v1. It shapes what you see; it is never a wall.

Motivating scenario: a monorepo scope `wc` holds frontend, backend, and style work. A
frontend developer sets `pj lens style frontend`; then `pj list`/`pj next` default to
projects whose tags intersect `[style, frontend]`. They can still manage everything else
(`pj list --tag backend`, `pj list --all`). A backend developer on the same scope sets
their own lens — same files, different default views, no per-user state in the shared
scope.

Safe by construction on `pj next` (the agent's work queue):
- An untagged project is never hidden by a lens (unclassified is not off-topic), so it
  stays runnable regardless of the lens.
- The active lens is echoed in human output.
- When the lens filters the ready queue to empty while unlensed ready work exists,
  `pj next` reports it (`nothing ready under lens [style, frontend]; N ready outside
  it`).
- `pj next --no-lens` / `pj list --no-lens` bypass it entirely (`--all` on list also
  includes done/backlog). The lens changes the default, never the reachable set.

A scope's `pj.cue` may declare a controlled tag vocabulary
(`knownTags: [frontend, backend, style, api]`), so `pj doctor` warns on typos
(`front-end` vs `frontend`) while free-form tags remain allowed (warn, not reject).

## Done and archive

DECISION: "done" is a filter, not a fate.
- `status: done` drops a project from default `pj list`; `--all` brings it back (same for
  `backlog` / `cancelled` / custom done-class and backlog-class). Files stay where they
  are. There is no `--archived` list flag — archived is a storage location, not a second
  board axis; `--all` already includes archived rows when status filters allow.
- Optional `pj archive <id>` physically moves a **terminal** project file into an
  `archive/` subdirectory of the dir (refuse if status is not terminal: built-in
  `done`/`cancelled` or custom `category: done` — same predicate everywhere). This is not
  a separate lifecycle: it is a filesystem declutter of work that is already finished.
  After archive the file is a historical record — still indexed, searchable, and
  resolvable (`pj get`/`pj meta`/`pj search`/`pj deps` still find it; default `pj list`
  hides done-class including archived; `--all` brings them back). There is no `pj unarchive`
  and no automatic move back to the flat dir on status change. Reopen is theoretically
  possible via `pj status` (labels, not a workflow) but is not the intended use; do not
  hand-rename files out of `archive/`. Archive is never required — `status: done` is
  enough for the queue. `archive/` is the lone tool-managed exception to the flat-scope
  rule; no other subdirectory is scanned. Ordinary English "move" for this filesystem
  relocate is correct (contrast `pj reorder`, which rewrites an `order` key only).
- DECISION: `pj doctor` soft-warns when a project file under `archive/` has a
  **non-terminal** status. Terminal is the same predicate as archive eligibility,
  `depends` satisfaction, and merge dispute: built-in `done` or `cancelled`, or a CUE
  custom with `category: done`. So `done` and `cancelled` (and shipped-style customs)
  are fine under `archive/`; `todo` / `in-progress` / `draft` / etc. under `archive/`
  are hygiene flags only — no auto-move, no status refuse, no `unarchive`. Optional
  stable token: `archive_non_terminal:` (add to the closed integrity set when
  implementing doctor report lines).
- Never delete. The record persists as the still-present file and in git history.

## CLI surface

DECISION: single-purpose CLI named `pj`. Project management only. Text on stdout —
locate/mutate verbs print a path (one line); `list` prints a summary (no paths by
default). pj does not support `--json`. No flag, no stable JSON envelope, on any command.
Warnings, doctor, empty-queue diagnostics: stderr text (and human stdout where
appropriate). Revisit only if concrete text pain appears later (not a v1 pillar).

DECISION (exit codes, v1 minimal): `0` success; non-zero failure. The only distinguished
code locked for v1 is **exit 2** for usage / unknown status name (e.g. `pj list` with an
unknown status positional). All other failures use a generic non-zero (conventionally
`1`) with the error message and, where applicable, a stable stderr token. No multi-code
map (not-found / conflict / config / …) in v1 — agents already get path hand-off, stderr
text, and closed warning tokens. Expand the map later only if concrete script/agent pain
appears; do not invent sysexits-style tables pre-implementation.

Product cut: pj indexes, queues, and locates; the filesystem is the editor. No "print
full project markdown" verb — the body is the file. `pj meta` is the allowed header
inspect (frontmatter + a fixed preamble; never the body). Filenames already embed the id
(`<id>-<slug>.md`). Agents edit with file tools; humans may use `$EDITOR` via `pj edit`.

DECISION: project verbs are top-level — the unit of work is the CLI's purpose, and
`list`/`next`/`get`/`meta`/`deps`/`create`/`status`/`edit`/`reorder`/`search`/`sync` are
the hot path. Scope administration (container management, not work; each command runs about
once per scope per machine) groups under `pj scope`: `init`, `import`, `use`, `rename`,
`forget`, `list`. `pj scopes` is accepted as an alias of `pj scope`, and the bare noun
with no subcommand runs `list`.

Hot path stdout contract:

| Command | Job | stdout |
|---|---|---|
| `list [status…] [--scope] [--tag]… [--all] [--no-lens]` | Board / inventory | Summary (id, title from H1, status, waiting-on, …) — no paths by default |
| `next [--no-lens]` | First ready `todo` (deps ok, order, lens) | Path |
| `get <id>` | Resolve short or full id | Path |
| `meta <id>` | Project header (frontmatter) | Preamble + raw frontmatter YAML (not body, not path-only) |
| `deps <id> [--transitive] [--tree]` | Edge neighbourhood (depends + related) | Summary (not paths) |
| `create <title> [status]` | Scaffold (default `draft`; frontmatter + H1) | Path |
| `status <id> <status>` | Set status (promote / claim / done / …) | Path (after write) |
| `edit <id>` | Open in `$EDITOR` | human convenience only |
| `reorder <id> …` | Rewrite integer+fraction `order` key | Path (after write) |

Errors: non-zero exit, message on stderr, no path on stdout when there is nothing to hand
off (unknown id, nothing ready, …).

Agent loop:

```text
pj create "Title"               → path (status: draft; frontmatter + H1)
# file tools: write body under H1 (project writing guide)
pj status <id> todo             → path (ready for the queue)
pj next                         → path
pj status <id> in-progress      → path (claim)
# file tools on that path
pj status <id> done             → path
# end of turn: pj sync only when auto-commit (see skill End of turn)
```

Known id: `pj get ab2c` → path; `pj meta ab2c` → header. Capture without authoring soon:
`pj create "Later" backlog`. Already-ready body in one shot: `pj create "Title" todo`.

- `pj scope init <dir> (--name <name> | --auto-name) [--code-root <path>]
  [--auto-commit]` — create and register a scope. Dir required; exactly one of
  `--name`/`--auto-name`; code-root by the matrix (`--code-root` always allowed, defaults to
  repo root in a repo else dir); `--auto-commit` writes `autoCommit: true`, omit writes
  false (or inherits siblings when the repo already has scopes). Never prompts, never
  runs git. In a dedicated pj repo, pass `--auto-commit` (omit = repo-driven).
- `pj scope import <dir> [--code-root <path>]` — register an existing on-disk scope,
  files in place; name and autoCommit come from its `pj.cue`. Hard-fails on a scope-name
  collision or malformed `pj.cue`. Symmetric errors with init.
- `pj scope use <scope>` — re-point an existing scope's single code-root to cwd (machine-local
  convenience; longest-prefix still resolves, no two scopes may share an identical
  code-root). A scope has one code-root; `use` moves it, it does not accumulate.
- `pj scope rename <old> <new>` — rename a scope in place: rewrites `pj.cue`, every
  project id, filename, and in-scope edge in one operation (auto-commit: one commit);
  records cross-scope inbound edges for `pj doctor` to flag; re-keys this machine's
  registry and lens. Cheap path: rename before other machines register. Post-share:
  other machines `pj scope forget <old>` then `pj scope import` (lens not preserved).
- `pj scope forget <name>` — unregister a scope (registry and lens entries, index
  rows); never touches the files.
- `pj scope list` — list every registered scope (all visible). Bare `pj scope` (or the
  alias `pj scopes`) runs `list`.
- `pj lens [tags...] | --clear` — set/show the machine-local default tag view for the
  resolved scope.
- `pj list [status…] [--scope S] [--tag T]… [--all] [--no-lens]` — list projects as a
  summary (no paths by default). Zero or more space-separated known status names = union
  filter. Bare `pj list` keeps the default active set (not a status name). Unknown status
  → exit 2. No CSV. No `--status` flag. `--tag T` may repeat; multiple tags are OR (match
  any listed tag). Lens still applies unless `--no-lens` (lens and `--tag` combine as
  AND between the two filters: project must pass the lens, and if any `--tag` is given
  must match at least one). `--all` remains "include done/backlog/…" board-wide, not a
  status token — also the way archived done projects reappear (no `--archived`). No date
  filters on list in v1. Examples: `pj list`, `pj list todo`, `pj list todo backlog`,
  `pj list in-progress blocked review`, `pj list --tag backend`,
  `pj list todo --tag api --tag network`.
- `pj get <id>` — resolve short or full id to the project file path; print that path.
  Resolution: see Project ids ergonomics (short form = exact short-id length 4–8 in
  ambient scope; full id anywhere registered). Read/locate only — no `--status` or other
  mutation flags (mutation stays on `status` / `create`). Optional later (not v1): path
  column on `list`; aliases `show`→`get`.
- `pj meta <id>` — inspect one project's header without opening the body. Pure read,
  git-free; never mutates files, index, or commits. Id resolution matches `pj get` (short
  id needs ambient scope / `--scope` / `PJ_SCOPE`; full id any registered scope). No flags
  and no aliases in v1 (`metadata` / `header` / `show` not accepted). No mutation form —
  inventing `pj meta … set` is the rejected `pj set` surface, not this verb.
  Reconcile the scope(s) needed for resolution (same class as `get`/`deps`); post-reconcile
  integrity warnings ride stderr. Malformed `pj.cue` does not block (reads stay available).
  Stdout (fixed shape):
  1. Preamble lines, then one blank line:
     ```
     id: <full-id>
     title: <H1 text or empty>
     path: <absolute path>
     ```
     `id` is always the full `<scope>-<short-id>` even when the user typed a short id.
     `title` is the first ATX H1 in the body (same extraction as `list`), empty if missing.
     `path` is absolute (same resolution as `get`) so a human glance does not require a
     second command; agents still use `get`/`next`/`status`/`create` for path hand-off and
     must not parse `path:` out of `meta` when `get` exists.
  2. Frontmatter YAML **exactly as stored** in the file: extract the interior of the first
     `---` … `---` document (file must open with a `---` fence line); print that interior
     without re-encoding and without the fence lines. Key order, quoting, comments, blank
     lines, built-ins, customs, undeclared keys, and `status_conflict` are all preserved
     when present. Do not print the markdown body after the closing fence.
  Not printed as synthetic frontmatter (derived / index-only): waiting-on, task counts,
  lens match, percent done — use `list` / `next` / `deps` / `query`. Edge neighbourhood
  status labels stay on `pj deps`; `meta` shows raw `depends`/`related` lists only.
  Exit and edge cases:
  - Unknown id, short id with no scope, unreachable scope for this id, usage/unknown flags:
    non-zero (usage → exit 2); empty stdout; message on stderr.
  - File missing while an index row still exists: non-zero; empty stdout; suggest doctor /
    reindex.
  - `parse_error` quarantine with an extractable frontmatter block: exit 0; preamble + raw
    YAML; stderr `parse_error: <id>: <message>` (and any scope unparseable count).
  - `parse_error` with no well-formed frontmatter block: non-zero; empty stdout; stderr
    `parse_error: …`.
  - `status_conflict` present: exit 0; key appears in the YAML dump; one stderr line naming
    the two terminals (same spirit as `get`/`doctor`).
  - Archived project: exit 0; normal output; `path` under `…/archive/…`.
  Explicit non-goals (v1): mutation; key filter (`pj meta <id> status`); `--json`; body
  dump; deps graph; lens filtering; re-serialize round-trip as the success path.
  Implementation note: share id resolution with `get`; prefer a raw fence-slice API so the
  success path does not YAML-decode for stdout; tests must preserve exact interior YAML
  (comments, order, customs, `status_conflict`).
- `pj deps <id> [--transitive] [--tree]` — edge neighbourhood for a project (alias
  `pj depends`). Pure read over the index after reconcile; summary on stdout (id, status,
  short label per neighbour), not paths. Default: direct **depends on**, **is depended on
  by**, and **related** (both directions, non-gating). `--transitive` expands depends both
  ways as a flat list (agent-friendly); related stays direct. `--tree` pretty-prints the
  depends graph for humans (implies transitive depth); related stays a flat section after
  the tree. Cycle-safe walks; if the subject is in a depends cycle, one stderr warning
  pointing at `pj doctor`. No edge mutation (author `depends`/`related` by direct
  frontmatter edit). Full rules in "Status and dependencies".
- `pj search <terms> [--scope S]` — full-text search (FTS5), machine-wide by default.
- `pj query <sql>` — read-only SQL over the index. Rejects writes. Schema is not a
  stable API (derived, rebuilt on any `schema_version` bump, may reshape between
  releases). `--help` says so; `pj query --schema` prints the current shape. Not for
  saved queries or tooling.
- `pj next [--no-lens]` — first runnable project by `order` with dependencies satisfied;
  prints its path. The primary agent entry point (beads' `ready`, renamed). Honours the
  lens by default and diagnoses an empty-because-blocked queue. A pure read; claim what
  it returns with an immediate `pj status <id> in-progress` (see "Status and
  dependencies"). Same spirit as rejecting `next --claim`: get/next are not mutators.
- `pj create <title> [status]` — scaffold a project: mint the id, write valid frontmatter
  (see Status and dependencies create scaffold), write H1 `# <title>`, write-through the
  index row, print the path for the agent to fill the rest of the body. Optional second
  positional is a known status if present; omitted → `draft` (authoring reserved, not
  next-eligible). Pass `todo` only when the body is already known; `backlog` for capture
  without intent to author soon. Title is one argv (quote multi-word); after trim it must
  be non-empty (usage exit 2); it sets the H1 and the frozen filename slug via
  `slugify(title)` (Project ids). No `--status` flag. No status-first order. Does not commit
  (incomplete by design; the skeleton reserves the id; committed at the next `pj sync`
  when auto-commit). Always appends on `order` (`keyBetween(last, null)`); no create order
  flags — use `pj reorder` for placement. The one create call; every edit after is direct
  file access. Promote with `pj status <id> todo` when implementable. Optional later (not
  v1): alias `add`→`create`.
- `pj status <id> <status>` — set status (word is status, not state). A complete-state
  write: auto-commit commits the one file synchronously (no push); non-auto-commit just
  writes the file. Prints the path after success.
- `pj edit <id>` — resolve id to path and open in `$EDITOR`. Human convenience only;
  agents use `get` / `meta` / `next` / `status` / `create` and their own file tools
  (`meta` for header inspect; path hand-off remains `get`/`next`/`status`/`create`).
- `pj reorder <id> (--before <id> | --after <id> | --first | --last)` — rewrite the
  integer+fraction `order` key to an explicit slot; the destination flag is required (no
  bare `pj reorder <id>`). pj reads the target neighbours from the index and writes
  `keyBetween(left, right)` into the reordered project's frontmatter only (integer step
  and/or fraction growth; never renumbers a band). `--first` / `--last` use open bounds
  and remain single-file under normal headroom (negative/positive integer heads). No
  relative counters, swap, or batch. Complete-state mutation: self-commits when
  auto-commit is on (same class as `status` / `archive`). Prints path after success;
  errors → stderr, no path. Not cross-scope relocation (id embeds scope; do not overload
  this verb). No v1 alias `move`→`reorder`.
- `pj sync [--all]` — reconcile now / done-for-now and the sole push boundary (auto-commit
  scopes only). Targets the ambient scope; refuses with a mode-named error if ambient is
  non-auto-commit. `--all` (or no ambient scope) syncs every auto-commit scope / git-root;
  skips non-auto-commit. Snapshot commits only the allowlist (project files, `pj.cue`,
  `.gitignore`, `AGENTS.md`); non-allowlist residue is warned, never force-committed.
  Skill: end-of-turn only when pj-driven.
- `pj doctor [--reindex] [--repair] [--re-space-order]` — diagnose (default) and optional
  integrity repair. Bare `pj doctor` **never mutates files**: report conflicts, same-scope
  dangling `depends` (hard), unresolvable cross-scope `depends`/`related` (informational —
  scope not registered here vs target gone are indistinguishable), cross-scope references
  whose target was collision-repaired or scope-renamed (verify — possible silent mispoint),
  `depends` cycles, depends-on-cancelled, registry/config drift (including remote rename:
  pj.cue name ≠ registry key — `name_drift:`; that scope is fail-closed unusable until
  `pj scope forget` then `pj scope import`, not auto-rekey; project verbs hard-error —
  see Registry), unparseable `pj.cue` (scope read-only; blocks `pj sync` for the whole shared
  git-root), autoCommit divergence across scopes sharing a derived git-root, frontmatter
  schema violations (unknown status, custom field type/`values` mismatch — hard;
  undeclared frontmatter keys and `knownTags` typos — warn), terminal-status dispute
  (`status_conflict` present — mid-rebase: resolve in-file; not mid-rebase: hard residue
  to clear), last-push error and sync health (repo/upstream not set up; marker at
  `<git-root>/.git/pj/last-push-error`; `sync_disabled:` when applicable), unparseable
  project files, non-allowlist residue under the dir, projects under `archive/` whose
  status is not terminal (soft — `archive_non_terminal:`), pathologically long `order`
  keys (soft threshold length > 64 — report only), stale built-in `in-progress` when the
  project file mtime is older than 72h (soft — `stale_in_progress:`; no auto-status
  change), and index health.
  - `--repair` — file-mutating twin of the auto-commit `pj sync` integrity step: id
    collision rename (in-scope reference-safe, cross-scope surfaced) and equal-`order`
    re-space only. Sole mutating path for non-auto-commit. Auto-commit + git-root:
    self-commits touched files (no push). See Invalidation / Project ids.
  - `--re-space-order` — explicit band re-space for over-long `order` keys only; not
    implied by `--repair`. Same self-commit rule when auto-commit + git-root.
  - `--reindex` — full index rebuild from the files; never touches project files.
  Text on stderr/stdout — no JSON envelope.
- `pj archive <id>` — physically move a terminal project into `archive/` (refuse
  otherwise — terminal = `done`/`cancelled` or custom `category: done`; write-through
  flags the row `archived`; reconcile scans `archive/`, so the
  project stays resolvable and searchable). Historical declutter of finished work — not a
  second status. No `unarchive` verb; do not hand-move the file back. Prints path after
  success when applicable.
- `pj skill` — print the Agent skill contract (below) to stdout as agent-facing workflow
  markdown. Discovery command: no ambient scope required. Never auto-writes into a tree.
  The contract section is the authoritative body; this bullet only names the verb.
- `pj skill install` / `pj skill list` / `pj skill uninstall` — reserved placeholders until
  agentdex-backed install. Appear in help and the command tree so agents do not invent
  paths. Each exits non-zero with a clear message (hard refuse, not a success no-op),
  e.g. `not implemented in v1 — use 'pj skill' to print the workflow; persistent install
  is planned via agentdex skills directories`. Same message for all three; no fake empty
  list. No install targets, no write into AGENTS.md / skill dirs, no agentdex dependency
  in the first build of these subcommands.

DECISION: no-scope error. When resolution yields no scope, scope-requiring commands
error with guidance (`no scope here — cd under a registered code-root, 'pj scope use
<scope>', 'pj scope import <dir>', or pass --scope`). The message does not
probe the tree for an unregistered `pj.cue` — registry only (see Scope lifecycle).
Discovery commands (every `pj scope` subcommand, `list --scope`, `search`, `query`,
`doctor`, `help`, `skill` and skill placeholders) never error on no-scope. `pj get`,
`pj meta`, and `pj deps` need no ambient scope when the id is full (`<scope>-<short-id>`);
a short form (exact short-id, length 4–8) still requires ambient scope, `--scope`, or
`PJ_SCOPE` to resolve.

## Discovery

DECISION: discovery is opt-in and user-initiated — pj never auto-writes a discovery
artifact into any tree, and never scans the tree for an unregistered scope. Mechanisms:
- The CLI auto-resolves the ambient scope from cwd via the registry only, so an agent
  just runs `pj` in a registered tree. An unregistered on-disk scope is invisible until
  `pj scope import` (no filesystem probe — see Scope lifecycle).
- `pj skill` (v1 real) prints the Agent skill contract (next section) to stdout on demand
  (the pattern beads' `onboard`/`prime` and webctl's `help <topic>` use). No pipe-to-jq.
  An agent that already knows pj exists can prime itself with no persistent state. The
  contract is where post-clone import is taught (`pj scope import <dir>`), since the CLI
  will not infer the path from the tree.
- `pj skill install|list|uninstall` are v1 hard-refuse placeholders (see CLI surface).
  Persistent install needs each agent's skills directory; that lookup is owned by
  agentdex (`agentdex get <id>` reports `skills_dir` / local skills paths; catalog is
  provider-agnostic). pj will use agentdex rather than hardcoding Claude/OpenCode/etc.
  paths. Until that integration exists, install must not ship half-baked. Deferred with
  real install (not v1): agentdex integration, concrete targets (global/local skills dirs
  per agent, optional AGENTS.md block), and list/uninstall semantics against what was
  installed. Installation remains user-initiated, never automatic: `pj scope init` writes
  no AGENTS.md block.

## Agent skill contract

DECISION: `pj skill` stdout is this contract — not a free-form help essay and not a
second source of truth that can drift from the rest of the design. Implementation may
render the section (or a maintained extract of it) as markdown; every required subsection
below must appear with its locked body. Rules that live elsewhere in this document are
referenced, not duplicated in conflicting form. The contract is complete for v1: do not
omit subsections, invent interim agent folklore, or reintroduce skeleton placeholders.

DECISION: path-centric interface. Locate/mutate verbs print a path; agents open that
path with file tools. There is no `--json` and no "print full project markdown" verb —
the body is the file. `pj meta <id>` is the read-only header inspect (preamble + raw
frontmatter); it is not path hand-off and not a body dump.

### Required sections (locked TOC)

Skill output must include these headings, in this order:

1. Core work loop
2. Capture
3. Frontmatter mutation
4. Body conventions
5. Title, slug, and filename
6. Ordering
7. List and filters
8. Dependencies and impact
9. Archive
10. End of turn (by autoCommit mode)
11. Conflicts and paused sync
12. Concurrent agents
13. Cold start and import
14. Cross-scope work
15. Waiting and external blockers
16. Unsupported operations
17. Doctor and integrity warnings

### Core work loop (locked)

Primary agent loop for project work in a registered ambient scope:

```text
pj create "Title"               → path (status: draft; frontmatter + H1)
# file tools: fill body under the H1 (project writing guide)
pj status <id> todo             → path (promote when implementable)
pj next                         → path of first runnable todo
pj status <id> in-progress      → path (claim immediately before implementing)
# file tools on that path
pj status <id> done             → path (or review / blocked / cancelled as appropriate)
# end of turn: see End of turn (by autoCommit mode) — not always pj sync
```

Rules:
- `pj next` is a pure read. Claiming is always a separate `pj status <id> in-progress`.
  Never assume a claim from `next` alone; never invent `pj next --claim`.
- Do not `pj next`-expect or claim a `draft`. Promote with `pj status <id> todo` only when
  the body is implementable.
- After claim, edit the file at the printed path. Do not re-resolve by guessing filenames.
- Known id: `pj get <id>` → path. Short form is an exact short-id match (length 4–8) in
  the ambient scope — including collision-repaired ids, not only create-length 4; full
  `<scope>-<short-id>` addresses any registered scope (no ambient needed for full id).
- Inspect header without opening the body: `pj meta <id>` (read-only; preamble + raw
  frontmatter). Do not parse `path:` from `meta` for hand-off — use `get`/`next`/`status`/
  `create`. Do not invent `pj meta` mutation.
- Status values are labels, not a workflow graph: any known status jump is legal
  (`draft -> todo`, `draft -> done`, `todo -> draft`, …); pj validates membership only
  (built-in or CUE custom).
- End of turn is mode-dependent (End of turn section). Do not cargo-cult `pj sync` on
  repo-driven or plain-files scopes.
- When stderr carries integrity or doctor-class warnings, run bare `pj doctor` first
  (report only). For `duplicate_id:` / `equal_order:`, run `pj doctor --repair` when ready
  to mutate; for over-long order, `pj doctor --re-space-order` only if the report calls
  for it. Escalate human-only classes (conflicts, drift, residue). Skill body: Doctor and
  integrity warnings.

### Capture (locked)

- `pj create "<title>"` scaffolds frontmatter (default status `draft`) plus H1 `# <title>`,
  prints path; does not self-commit. Fill the rest of the body via file tools.
- Durability after create (locked — do not assume more): on disk and in the index only.
  Not durable-in-git and not durable-remote until the mode-appropriate boundary:
  pj-driven → later `pj sync` (snapshot commits the allowlisted scaffold); repo-driven →
  host repo commit/PR; plain-files → disk is the whole story (no git). A crashed session
  after create without that boundary can leave an orphan draft on one machine only.
- Optional second positional: any known status. Use `todo` only when the body is already
  known in the same turn; `backlog` for capture without intent to author soon.
- After create: write the project writing-guide sections under the H1, then
  `pj status <id> todo` when implementable. Leaving a bare scaffold as `todo` is a misuse
  — that is what `draft` is for.
- Summary, depends, related, tags, links: set by direct frontmatter edit after create (no
  create flags for those in v1).
- Create always appends on `order`; placement is `pj reorder` after promote when needed.

### Frontmatter mutation (locked)

Inspect (read-only): `pj meta <id>` prints a fixed preamble (`id`, `title` from H1,
`path`) and the project's frontmatter YAML exactly as stored — never the body, never a
write. Use after direct edits to confirm; agents still locate for edit via `get`/`next`/
`status`/`create` paths.

| Key | How to change |
|---|---|
| `id` | Never. Minted at create; stable forever. |
| `created` | Never. Set once at create. |
| `order` | Only via `pj reorder`. Never hand-edit. |
| `status` | Prefer `pj status <id> <status>`. Direct edit only when resolving `status_conflict` or mid-file repair under human direction. |
| `status_conflict` | Only when resolving a status dispute (at least one terminal involved): set `status`, remove this key. Never invent it. |
| `depends`, `related` | Direct frontmatter edit. Inspect lists in `pj meta`; neighbourhood with `pj deps` (read-only). |
| `tags`, `links`, `summary` | Direct frontmatter edit. Inspect with `pj meta`. |
| Custom fields (`pj.cue` `fields`) | Direct frontmatter edit. No `pj set`. Inspect with `pj meta`. Absent is always legal. |
| Undeclared keys | Avoid; doctor warns. Do not invent schema. Still visible in `pj meta`. |

After direct edits on auto-commit scopes, end-of-turn `pj sync` commits them. Prefer verbs
for status/order so self-commit and validation run on the write path.

### Body conventions (locked)

The markdown body is the project document handed to a fresh implementer session. Shape it
with the project writing guide (`start get project/writing` / equivalent org guide). CLI
does not model tasks or sections — convention only.

`pj create` writes the H1 (`# <title>` from the create argument). Retitle that H1 freely
afterward (slug stays frozen). Under the H1, use the project writing guide section order
(`start get project/writing` / equivalent org guide):

1. Goal
2. Scope
3. Current State
4. References (omit if none)
5. Requirements
6. Constraints
7. Implementation Plan
8. Implementation Guidance (omit if nothing non-obvious)
9. Acceptance Criteria

Right-size: omit sections that do not apply. Bias the document at the principled
long-term solution; define what/why, not keystrokes; no conversation references
("as discussed").

Also:
- Optional checkbox tasks under Implementation Plan or a Tasks subsection for local
  progress — never via CLI.
- When `status` is `blocked`, put the reason in the body (a short Blocked note is fine).
- `summary` frontmatter: one-line what/why for list/search; keep in sync with Goal when
  practical.
- List/search "title" is the H1 (create provides it immediately). Fill the guide sections
  under the H1 before `pj status <id> todo`.

### Title, slug, and filename (locked)

- Filename shape: `<id>-<slug>.md`. Slug is `slugify(create-title)` once at create
  (closed grammar and algorithm in design Project ids) and never updated. Empty title
  after trim is a usage error; do not invent a slug by hand.
- Retitle freely in the H1/body. Do not rename the file; do not edit `id`.
- `pj doctor` reports structural id/filename/slug-shape mismatch only — not H1/slug drift.
- Always reopen via `pj get`/`next`/`status` paths, not by reconstructing a slug from the
  current title or re-running `slugify` on the H1.

### Ordering (locked)

- Never hand-edit `order`. Use `pj reorder <id> (--before <id> | --after <id> | --first |
  --last)` only (destination flag required).
- `pj create` always appends (`keyBetween(last, null)`). No create-time `--first` /
  `--before` / `--after` / `--last`.
- Typical flow: create (draft) → fill body → `pj status <id> todo` → `pj reorder …` if the
  new work should not sit at the end of the board.

### List and filters (locked)

- Default `pj list`: active board set (includes `draft`, `todo`, `review`, `in-progress`,
  `blocked`, and custom active/wip; excludes `backlog` / done-class unless filtered).
- Status filter: zero or more known status name positionals = union. Unknown status →
  exit 2. No `--status`, no CSV.
- Flags: `--scope S`, repeatable `--tag T` (OR across tags), `--all`, `--no-lens`.
- Lens applies by default; `--no-lens` bypasses it. Lens AND `--tag` when both apply.
- No `--archived`. No date filters on list — use `pj query` for ad-hoc cuts.
- Sort: `(order, id)`.

### Dependencies and impact (locked)

- Author `depends` and `related` by direct frontmatter edit after create (no `pj deps add`
  / remove; no create flags for edges in v1). `depends` holds project ids that gate
  runnability; `related` is soft "see also" and never gates.
- Inspect edges with `pj deps <id>` (alias `pj depends <id>`). Default: direct neighbours
  in three sections — depends on, is depended on by, related (both directions). Prefer this
  over free-form `pj query` (schema not stable).
- Before a large claim, cancel, or hub reorder: `pj deps <id> --transitive` for the full
  flat prerequisite and dependent sets. Humans browsing structure: `pj deps <id> --tree`.
- `pj next` skips a `todo` whose `depends` are not all terminal; `pj list` annotates
  waiting-on. Claiming remains `pj status <id> in-progress` after `pj next` — never via
  `deps`.
- Open a neighbour to edit: `pj get <dep-id>` → path → file tools. Do not invent filenames
  from titles.
- If `pj deps` warns of a depends cycle, run `pj doctor`. Do not ignore cycle or integrity
  warnings.
- Cross-scope edge ids use the full `<scope>-<short-id>` form; unresolvable targets stay
  listed and annotated (held for gates — see design Status and dependencies).

### Archive (locked)

- Use `pj archive <id>` only on terminal projects (`done`/`cancelled` or custom
  `category: done`) to declutter the flat authoring dir.
  The file becomes a historical record under `archive/`; it stays get/search/deps-able.
- No `pj unarchive`. Do not rename or move archive files by hand.
- Prefer leaving status done and archiving when the board is noisy. Resurrecting archived
  work with `pj status` is legal but not normal agent practice — treat archive as history.

### End of turn (by autoCommit mode) (locked)

Branch on the ambient scope's mode (from `pj.cue` `autoCommit` + whether the dir is in
git — labels: pj-driven / repo-driven / plain-files):

| Mode | End of turn |
|---|---|
| pj-driven (`autoCommit: true`) | `pj sync` when git+upstream exist (use `pj sync --all` when cross-scope gates need fresh remotes). If stderr shows `sync_disabled:`, set up the repo/remote with plain git first — file writes already landed; no inventing `git init` via pj. When ready, sync is also the first git/remote durability boundary for any `pj create` scaffold this turn. |
| repo-driven (`false` inside git) | Do **not** call `pj sync` (it refuses). Leave project files for the host repo commit/PR (including uncommitted creates). |
| plain-files (`false` outside git) | Do **not** call `pj sync` (it refuses). Run bare `pj doctor` if integrity warnings appeared or after multi-machine file sync; `pj doctor --repair` when acting on `duplicate_id:` / `equal_order:`. Creates are disk-only. |

Never invent `pj save` / `pj end`. Mode is a property of the scope, not a per-command flag.
Never treat `pj create`'s printed path as proof of a git commit or remote push.

### Conflicts and paused sync (locked)

Fail fast. Do not keep authoring on a conflicted or mid-rebase auto-commit git-root.

| Signal | Agent action |
|---|---|
| Body conflict markers in a project file | Stop. Report path. Do not pick a side or delete markers unless the human already directed the resolution. Human edits body → `pj sync` to resume. |
| `status_conflict` in frontmatter | Stop. Report path and the two disputed statuses (`pj meta` / `pj get` / doctor). Do not choose unless the human (or explicit task) already picked one; then set `status` (either listed value or another known status), remove `status_conflict`, `pj sync`. |
| Mutating command refuses mid-rebase / mid-sync-conflict | Stop. Do not retry writes. Report the refused command and named file/scope from the error. Resume only after human resolution + `pj sync`. |
| `pj sync` pauses / reports unresolvable conflict | Stop the turn's project work on that repo. Surface sync output. No parallel "fix it in the background". |

Never invent merge resolution heuristics (prefer-done, LWW body, etc.). Non-auto-commit scopes have no pj rebase seam; conflict markers that land via the host repo are still stop-and-report if the file is unparseable (`parse_error` / doctor).

### Concurrent agents (locked)

Design accepts a short pre-claim race (`next` is a pure read; no `next --claim`; no
assignee/lease). Scope `flock` serialises pj writes only — it does not cover two `next`
calls or body edits via file tools. No extra file-lock machinery in v1.

Safe practice:
- Prefer one writer agent per scope when possible.
- After `pj next`, claim immediately with `pj status <id> in-progress` before editing.
- If the project is already `in-progress` (or another agent clearly owns it), do not take
  it: run `pj next` again or stop and report. Do not double-edit the same path.
- Collision is self-surfacing (second claim / concurrent body edits); fix by coordinating
  agents, not by inventing locks in the project file.
- Abandoned claim: if work stops mid-claim, leave a clear body note when possible; doctor
  soft-warns `stale_in_progress:` after 72h without file mtime activity. Recovery is a
  deliberate `pj status` back to `todo` (or `blocked`), never automatic.

### Cold start and import (locked)

Registry only — pj never scans the tree for an unregistered `pj.cue` and never
auto-registers on clone.

When there is no ambient scope:
- Do not probe for scope dirs or invent paths.
- Do not treat `pj skill install` as available (v1 hard-refuse).
- Stop and use a path from the human or from project docs (e.g. a one-liner in AGENTS.md
  naming the scope dir). Then: `pj scope import <dir> [--code-root <path>]` or
  `pj scope use <scope>` / `--scope` / `PJ_SCOPE` as appropriate.
- `pj skill` itself needs no scope — print the contract, then fix registration before
  project verbs.

Supported handoff (human-authored, never written by pj): document the scope dir in the
repo (AGENTS.md or equivalent) so a cold agent can import without guessing.

### Cross-scope work (locked)

- Address other scopes with full ids (`<scope>-<short-id>`). Never invent a scope name;
  only use names from `pj scope list` / registered registry.
- If `name_drift:` appears (registry key ≠ `pj.cue` name after a remote scope rename):
  that scope is fail-closed — forget+import before any project verb. Do not rely on
  short ids under the old registration.
- Author `depends` / `related` by direct frontmatter edit (same- or cross-scope). Inspect
  with `pj deps <id>` — read-only; do not invent edge verbs.
- If `pj next` / `list` / `deps` annotate that a depended-on scope is not registered here:
  stop and ask for import/clone of that scope. Do **not** clear the edge to “unblock”
  yourself — the hold is intentional.
- Cross-scope gate freshness: bare `pj sync` only fetches the ambient auto-commit repo.
  When work depends on status in another auto-commit scope (especially after multi-machine
  edits), run `pj sync --all` or sync that scope before trusting the gate. Repo-driven /
  plain-files: no pj sync — freshness is the host/external sync of those trees.
- Shared git-root coupling: several auto-commit scopes in one repo share one push and one
  freeze domain. A conflict, `status_conflict`, or unparseable sibling `pj.cue` can block
  sync/writes for **all** those scopes until fixed. That is the price of one-push sync —
  not a bug. Need isolation → separate git-root (do not invent per-scope sync isolation
  flags). See Auto-commit DECISION on multi-scope messaging.

### Waiting and external blockers (locked)

Use the right mechanism — do not overload one label for every kind of wait:

| Situation | Use | Do not |
|---|---|---|
| This project cannot start until another **project** is terminal | `depends: [<id>]` in frontmatter (same- or cross-scope full id) | `blocked` alone for a project dependency |
| Stalled on a **human or external** factor with no project id | `pj status <id> blocked` and write the reason in the body; put PR/issue/URL in `links` | Fake a `depends` on a non-project |
| The **work product** is under review (plan or result) | `pj status <id> review` | `blocked` unless review is stuck on a person/process outside the review itself |
| Soft “see also” / provenance | `related: [<id>]` | Using `related` or tags as a runnability gate |
| Topic / area only | `tags` | Encoding wait state in tags |

`depends` is the only project-to-project gate for `pj next`. `blocked` is manual and
human-owned — pj never auto-sets it. Inspect edges with `pj deps`; edit edges in
frontmatter.

### Unsupported operations (locked)

Do not invent verbs or flags. v1 does not support:

| Do not | Instead |
|---|---|
| Transfer / split / merge / copy a project across scopes | `pj create` in the target scope; `related` or `depends` as needed; `cancelled` or leave the old one |
| Task-level CLI (checkboxes as objects) | Edit body checkboxes/sections with file tools |
| `--json` or machine envelopes | Paths + short text; open the file |
| `pj deps` mutation (`add`/`rm`) | Edit `depends` / `related` in frontmatter; `deps` is read-only |
| `pj set` / `pj field` / `pj meta` mutation | Direct frontmatter edit (customs per `pj.cue`); `meta` is read-only inspect |
| `pj unarchive` | Archive is historical; reopen only via `status` if ever (not normal) |
| `pj next --claim` | `pj next` then `pj status <id> in-progress` |
| `pj skill install` (v1) | `pj skill` print; human AGENTS.md path for import |
| Hand-edit `id`, `created`, or `order` | Verbs: create/status/reorder only for those concerns |
| Hand-rename `<id>-<slug>.md` to chase a new title | Slug frozen; retitle H1 (and optional `summary`) only — three names may diverge |

If a need is not on the CLI surface, stop and ask — do not improvise a parallel tool.

### Doctor and integrity warnings (locked)

DECISION: integrity and doctor-class warnings on stderr use a closed set of stable
machine-readable tokens as line prefixes. No `--json` envelope. Human-readable detail
may follow the token on the same line (or subsequent indented lines). Agents match the
token, not free prose. Adding a token is a conscious design change; do not invent ad-hoc
prefixes in implementation.

Closed v1 token set (prefix form, including the trailing colon):

| Token | Meaning |
|---|---|
| `duplicate_id:` | Two or more projects share an id in a reconciled scope |
| `equal_order:` | Two or more projects share an `order` key in a scope |
| `parse_error:` | Project file (or similar) failed parse / quarantined |
| `unreachable_scope:` | Registered scope dir could not be stated |
| `non_allowlist:` | Path under a scope dir outside the auto-commit allowlist |
| `config_unparseable:` | Scope `pj.cue` (or XDG registry file) will not load as CUE |
| `status_conflict:` | Status dispute key present on a project (both-sides change, at least one terminal; also visible in-file) |
| `depends_cycle:` | Cycle involving the subject or scope graph |
| `auto_commit_mismatch:` | Scopes sharing a git-root disagree on `autoCommit` (or preflight) |
| `archive_non_terminal:` | Project under `archive/` whose status is not terminal (done/cancelled / custom done-category) |
| `sync_disabled:` | Auto-commit: no git-root (write skipped self-commit and/or sync not runnable) or git-root without upstream (`pj sync` not runnable; local self-commit still runs). Files may still have been written |
| `stale_in_progress:` | Built-in `in-progress` and project file mtime older than 72h — possible abandoned claim; inspect body; do not auto-reopen |
| `name_drift:` | Registry key ≠ on-disk `pj.cue` name for that dir — scope unusable until forget+import; do not work around with short ids |

Example shape (illustrative):

```text
duplicate_id: wc-ab2c in scope wc (2 files) — run pj doctor
equal_order: 2 projects in scope wc share order "a1" — run pj doctor
```

Agent rules:
- Never ignore a line whose prefix is in the closed set. Run bare `pj doctor` (report
  only) and fix or escalate from the report. On pj-driven scopes, `non_allowlist:` paths
  are not committed by `pj sync` — move or remove them; do not invent a force-commit flag.
- `duplicate_id:` / `equal_order:` → after reviewing the report, `pj doctor --repair`
  (mutates; auto-commit self-commits when a git-root exists). Do not assume bare doctor
  rewrote files.
- Over-long `order` (doctor soft report) → `pj doctor --re-space-order` only when chosen;
  not part of `--repair`.
- Plain-files multi-machine: no `pj sync` seam — bare doctor when tokens appear and
  periodically after external file sync; `--repair` when acting on collision/equal-order.
- After human conflict resolution (body markers / `status_conflict`), run bare `pj doctor`
  if unsure, then `pj sync` on pj-driven scopes to resume.
- `pj doctor --reindex` only when the mtime heuristic is fooled (restore, clock skew) —
  rare escape hatch, never routine; never mutates project files.
- After `--repair` / `--re-space-order`, read the report; do not re-introduce hand-fixed
  ids that fight the repair.
- `stale_in_progress:` → open the path, check whether work is still live; if abandoned,
  `pj status <id> todo` (or `blocked` + body reason). Never invent auto-reopen or
  `next --claim`.
- `name_drift:` → stop project work on that scope. Run exactly
  `pj scope forget <old>` then `pj scope import <dir> [--code-root …]` (names/paths from
  the doctor/error text). Re-set `pj lens` if needed. Do not invent ambient short-id
  workarounds or auto-rekey.

## Borrowed from beads

beads got the interface right even though its Dolt storage is overkill here.
- `ready` as the primary verb -> `pj next` (prints path).
- Dependency-gating derived rather than a hand-set flag.
- `StatusCategory` -> CUE custom-status categories.
- A `prime`/`onboard` context dump -> `pj skill`.
- An agent-facing integration artefact -> user-initiated `pj skill install` (planned via
  agentdex; beads auto-maintained an AGENTS.md block; pj makes installation a deliberate
  user act, never an auto-write into a tree it does not own). v1 ships print + hard-refuse
  install family placeholders.
- One logical operation = one auto-commit, auto-messaged (when `autoCommit: true`).
- beads' `--json` everywhere: not adopted — path + short text is the agent interface.

Shrunk ruthlessly: ~15 dependency types become two (blocking `depends`, soft `related`);
`pinned`/`hooked` orchestration
statuses dropped (a closed set of eight, no lifecycle machinery).

## Anti-goals (avoid becoming beads)

- No database as source of truth. Frontmatter makes adding a field free and removing one
  a grep-and-delete — no migrations, no dead columns. The index is a derived view; the
  registry is small machine-local config; authority stays in the files.
- No scope explosion. beads grew molecules, swarms, gates, wisps, federation, and
  GitHub/Jira/Linear/Notion sync. Anchor: a small closed built-in set (eight statuses,
  two project-to-project edge kinds, a compact verb surface) over one file per project,
  no lifecycle machinery; CUE customs for anything beyond. Multi-machine recovery is a
  closed auto-repair budget (see "Project ids"), not an open-ended resilience layer.
- No double handling (the beans sin): files are edited in place; the CLI never asks for
  a temp file to be handed to it.

## Open questions

None outstanding. Resolved in the doc body and decisions log (non-exhaustive): soft
`related`; `pj sync` ambient default and `--all`; `pj scope use` single code-root;
autoCommit default/inherit; malformed `pj.cue` fail-closed writes; concrete `pj.cue` /
custom-field schema; `pj deps` with `--transitive` / `--tree`; built-in `draft` and create
default; create scaffold = frontmatter + H1; slug via closed `slugify` at create; list `--tag` (no
`--archived` / no list date filters); archive = historical terminal only (no unarchive);
full Agent skill contract; mode-aware end-of-turn and non-auto-commit `pj sync` refuse;
conflict fail-fast agent rules; waiting taxonomy (`depends` vs `blocked` vs `review`);
`pj meta` read-only header inspect (no frontmatter write API).

## Decisions log (locked)

- project = one markdown file; tasks live inside it; the CLI does not model tasks.
- Auto-repair budget (closed): detect-on-reconcile warn-only; automatic bit-identical
  id-collision rename (deterministic short-id extension, unique in-scope, cap 8) +
  in-scope edges and equal-order re-space on sync integrity / `pj doctor --repair` only
  (bare doctor is report-only); long-order re-space only via `--re-space-order`;
  auto-commit repairs self-commit when a git-root exists; same-id add/add auto-rename
  never field-merge; residual basename then SHA-256 tie-break kept for dual-repair /
  add/add agreement; `status_conflict` and cross-scope mispoint / residue are
  human-surfaced not auto-picked or cross-repo rewritten. No repair on pure reads; no
  auto cross-scope edge rewrite; no max+1 ids; no `crypto/rand` on repair ids. See
  Project ids.
- Vocabulary: scope / project / task. store == scope (no separate store container). A
  repo hosts a scope.
- Markdown files are always the source of truth; edited in place; no double handling.
- Per-project metadata in the file's YAML frontmatter; no separate index and no DB as
  truth. Built-in keys: `id`, `status`, `order`, `depends`, `related`, `tags`,
  `created`, `links`, `summary`, plus transient merge-only `status_conflict` (exactly two
  distinct post-edit status names; written only by auto-commit merge when both sides
  change `status` and at least one value is terminal — includes done vs in-progress, not
  only done vs cancelled; resolution is set `status` and remove the key; refuse
  `rebase --continue` while present; doctor-hard if present when not mid-rebase).
- ids are `<scope>-<short-id>` (create: random 4-char, human-typeable alphabet, first
  char a letter, even letter/digit split; repair may extend to length ≤ 8 via
  deterministic lex-first free suffix — see Project ids); ambient short form is exact
  short-id match length 4–8 (not create-only 4); full id needs no ambient scope; id
  canonical in frontmatter, filename mirrors as `<id>-<slug>.md` with slug from pure
  `slugify(title)` at create (NFKC, ASCII alnum tokens joined by `-`, empty→`x`,
  `SLUG_MAX` 48, closed `^[a-z0-9]+(-[a-z0-9]+)*$`; not unique-ified; empty title is
  usage error; pure package + fixtures) and frozen thereafter (retitle H1 freely; never
  rename the file to chase the title; doctor flags structural id/filename/slug-shape
  mismatch only, not H1/slug drift). `pj create` redraws on a local hit (online creation
  never collides);
  offline-concurrent collisions (no git conflict): detect via post-reconcile index query
  every command (all scopes, warn only); file-mutating repair by the `pj sync` integrity
  step (auto-commit) and `pj doctor --repair` (every scope — sole path for
  non-auto-commit): rename the side nothing depends on (inbound checked in- and
  cross-scope; if both/neither, newer by RFC3339 `created:`, then lexicographic basename,
  then SHA-256 of raw file/stage bytes — never the shared id string) via deterministic
  short-id extension (append the lex-first free extension over the typeable alphabet;
  grow length up to `SHORT_ID_MAX` 8; no `crypto/rand`; hard-fail only past the cap),
  atomically rewrite in-scope `depends`/`related`; cross-scope edges (another repo,
  unrewritable here) are not touched but recorded, and doctor flags them to verify
  against a silent mispoint; report. Bare `pj doctor` reports only. Plain-files
  multi-machine uses detect + `--repair` (no sync seam); external conflict-copy names
  are doctor-flagged residue. `created` is RFC3339 at `pj create`, immutable; doctor
  flags missing/non-RFC3339.
- `order` is a frontmatter integer+fraction rank key (fractional indexing; Rocicorp /
  Figma-style construction). Wire format is frozen durable protocol, not package-private:
  base-62 alphabet `0-9A-Za-z`; key = integer_part + fractional_part; integer head
  encodes width (`a`–`z` non-negative, `Z`–`A` negative); `INTEGER_ZERO` = `a0`;
  `SMALLEST_INTEGER` = `A` + 26 × `0`; fractional part may be empty and must not end with
  `0`; byte-wise string order = rank order (no custom SQLite collation). Initial key `a0`
  (`keyBetween(null,null)`). `keyBetween` for open ends and unequal valid neighbours
  succeeds via integer inc/dec and/or fraction growth — never multi-file renumber on
  `pj reorder`/`pj create`; theoretical floor/ceiling exhaustion is an error, not hot-path
  rebalance. Closed grammar validated by doctor and order-setting writes. Format change =
  designed migration of all keys, not a quiet library bump. Prefer one shared generation
  algorithm (Rocicorp port). Pure package + table-driven fixtures required before CLI
  reorder wiring (prepend/append loops, densify, equal-key, invalid reject, integer widen,
  floor/ceiling exhaust, sort==rank, round-trip validate — not mid-board only).
  `pj create` always appends (`keyBetween(last, null)`); no create order flags —
  placement is `pj reorder` only. Equal keys (offline concurrent)
  break by id for reads, warn on post-reconcile detection, and are re-spaced by the
  `pj sync` integrity step (auto-commit) / `pj doctor --repair` (all scopes) preserving
  `(order, id)` relative order among the tied set; pathologically long keys (soft
  threshold length > 64) report on bare doctor and re-space only via
  `--re-space-order`, never implicit on reorder or bare doctor.
- Scope name `^[a-z0-9]{1,12}$`, machine-unique, never silently defaulted; it is the
  address and id prefix, not a directory name. Fleet-global in effect (stated
  assumption: one user registers names consistently across machines). `pj scope rename`
  is the tooled clash remedy: in-scope rewrite under the flock (pj.cue name, frontmatter
  ids, filenames, in-scope edges; auto-commit one commit); cross-scope inbound edges
  recorded for `pj doctor` (surfaced-not-auto-repaired); authoring machine registry and
  lens re-keyed. Cheap path: rename before other machines register. Post-share recovery
  is deliberate, not auto-healed: while drift lasts the scope is fail-closed (`name_drift:`;
  no half-working ambient); recovery is `pj scope forget <old>` then `pj scope import`
  (lens dropped; re-set with `pj lens` if wanted). `pj scope forget` unregisters
  (registry + lens entries, index rows) without touching files.
- Storage: a scope is a directory of flat `.md` files plus `pj.cue` (renamed from
  `config.cue`, namespaced) and a `.gitignore` covering `.pj.lock` (written by
  `pj scope init`). The dir is intended pj-only, user-chosen at init (recommend
  `.agents/pj/`), never defaulted; auto-commit enforces membership via the snapshot
  allowlist rather than trusting the directory label. A git repo may host several scopes
  at distinct code-roots (monorepo one-scope-per-team; central pj repo with sibling
  scopes); the unit is the scope, not the repo. Every scope sharing a repo agrees on its
  autoCommit.
- Everything visible: every registered scope is reachable machine-wide; there is no
  private/local class. Registration is deliberate (`pj scope init`/`pj scope import`), never
  automatic. A no-ambient-scope error never probes the tree for an unregistered `pj.cue` —
  registry only; post-clone import is taught by `pj skill` / the user, not inferred.
- Resolution is a registry lookup: longest-prefix code-root match for the ambient scope;
  direct name lookup for `--scope`. No up-scan, no filesystem marker, no blessed default
  location. No two scopes share a code-root; nested code-roots resolve by longest prefix.
- Registry is machine-local durable config in the XDG config directory
  (`registry.cue`, machine-written — see the Config bullet), not synced, not in a
  repo. Records per scope: name, dir, and a single code-root. dir and
  code-root are independent (dir need not sit under root); the git repo is not stored but
  derived from dir via `git rev-parse`. `pj scope use` re-points the single code-root, it
  does not accumulate a list. Rebuilding the index walks the registry; losing it means
  scopes are unknown until re-registered.
- pj is non-interactive — never prompts; the only TTY-sensitive behaviour is colour.
- `pj scope init <dir> (--name <name> | --auto-name) [--code-root <path>]
  [--auto-commit]`: `--code-root` always allowed (this is what lets scopes share a
  repo), defaulting to repo root in a repo else the dir; exactly one of `--name`
  (explicit, `^[a-z0-9]{1,12}$`) or `--auto-name` (derive from code-root basename, hard error on
  a derived-name collision); `--auto-commit` sets `autoCommit: true`, omit sets false
  (or inherits siblings when the repo already has scopes — omit does not re-default to
  false against true siblings). Accepted tradeoff: first scope in a git repo + omit =
  repo-driven. autoCommit consistency per derived git-root enforced; code-root collisions
  rejected and nested code-roots allowed, while dirs must be mutually disjoint (identical,
  nested, or containing a registered dir all rejected — a load-bearing invariant the
  `pj sync` snapshot relies on). `pj scope import` is symmetric for an existing scope
  (name and autoCommit from its `pj.cue`), hard-failing on a scope-name collision or
  malformed `pj.cue`.
- Config: CUE is an owner hard lock-in for both tiers — not under review, no reserved
  alternate format. XDG (`registry.cue`, `lens.cue`) and scope `pj.cue` are read and
  written only through `cuelang.org/go` (no string-templated CUE, no JSON/YAML codec on
  those paths). XDG: machine-written package, wholesale per-file regeneration via
  `cue/ast`+`cue/format`, temp + atomic rename, machine-global flock `.pj.lock`; no
  `editor` key — `pj edit` uses `$EDITOR`; malformed XDG file is a hard error (bootstrap).
  Scope `pj.cue` shape: required `name` + `autoCommit`; optional `knownTags`, `statuses`
  (each `{category}` in active|wip|backlog|done; additive, no built-in redeclare;
  category drives default list and terminal/`depends` only — never `pj next` membership,
  which is built-in `todo` alone; see Status decisions), and `fields` (each `type` in
  string|int|bool|strings, optional `values` enum for string kinds; keys
  `^[a-z][a-z0-9_]{0,31}$`, no built-in shadow including `status_conflict`). Custom
  fields sit flat in project YAML; agents read them from the file; merge uses list
  set-merge for `strings`, scalar rules otherwise; undeclared frontmatter keys are doctor
  warnings. No required-field flag and no `pj set` verb in v1 (direct edit); header
  inspect is `pj meta` (read-only). Env/flags override. Scope config CUE evaluation is
  cached in the index keyed by its import
  closure's `(path, mtime, size)`; XDG is evaluated in-process each command (registry
  bootstrap) — fixed `cuecontext.New()` cost accepted, not an escape hatch. A malformed
  scope `pj.cue` makes its scope read-only until fixed — fail fast on write, not a silent
  degrade: autoCommit and the custom schema both live in `pj.cue` (autoCommit is not
  cached in the registry), so with neither trustworthy pj refuses every mutating command
  on that scope rather than write under a guessed autoCommit/schema, while reads stay
  fully available so no machine-wide command is bricked and no work is lost. Sibling
  scopes' ordinary mutations stay up. Exception: `pj sync` preflight fails closed for the
  whole shared git-root if any co-located scope's `pj.cue` is unparseable (autoCommit
  unverifiable — same class as autoCommit disagreement), rather than omitting the sibling
  and pushing under an incomplete proof. Loud (`pj doctor` + a read warning); fixing
  `pj.cue` restores writes and sync.
- One machine-wide SQLite index at `${XDG_STATE_HOME:-~/.local/state}/pj/index.db`
  (`modernc.org/sqlite`, FTS5): an owner hard requirement standing on v1's own query
  surface (one-corpus FTS, `WITH RECURSIVE`, ad-hoc `pj query`), not contingent on the
  planned viewer, which reinforces but does not carry it. A materialized view derived from
  the files, in XDG state
  so no VC or filesystem sync ever carries it and WAL always runs on local disk. Rows
  namespaced by scope; cross-scope queries and one-corpus FTS are native. Reconcile at
  command start, scoped to what is read, git-free (mtime+size; full rebuild only on genuine
  index-integrity signals — DB missing/corrupt, integrity-check failure, `schema_version`
  mismatch — walking the registry and skipping unreachable scopes); after reconcile, cheap
  index aggregates detect duplicate ids and equal `order` keys (warn only — no file writes
  on the read path); write-through on pj's own mutations; a per-file parse failure and an
  unreachable dir are both isolated in layer 1 (quarantined `parse_error` row /
  skipped scope with rows kept and a terse warning, `pj doctor` owning the durable drop),
  never a rebuild trigger. File-mutating id/`order` integrity repair: `pj sync` for auto-commit scopes,
  `pj doctor --repair` for every scope (sole path for non-auto-commit; bare doctor
  report-only). Schema change = rebuild,
  not migration. Query surface:
  `pj search` (FTS5), `pj deps` (edge neighbourhood over `edges`; direct / `--transitive` /
  `--tree`), `pj query` (read-only SQL, schema not a stable API — prefer `deps` for graph
  inspect), rich `pj list` filters (status union, `--tag` OR, `--scope`, `--all`,
  `--no-lens`; no `--archived`, no date flags), `WITH RECURSIVE` dependency/rollup. WAL
  and `busy_timeout` from day one (single intentional writer class: CLI); viewer
  second-writer protocol deferred until that process exists.
- Sync (auto-commit) split along the commit/push seam; no snapshot machinery on every
  command. Reads git-free. Writes that yield a complete state write the file always;
  self-commit one file (no push) when auto-commit and a git-root exist; if auto-commit
  but no git-root, skip commit with `sync_disabled:` (exit 0 on the write).
  `pj create` scaffolds without committing. The file write and id draw serialize on a
  per-dir `flock`; the git commit/rebase/push (repo-granular) additionally serialize
  on a git-root lock when a git-root exists, so several `pj` scopes sharing a repo cannot
  corrupt its index and one `pj sync` pushes the whole repo. Mid-rebase mutation refuse
  is auto-commit only when a git-root exists (self-commit path); non-auto-commit keep
  writing files mid-rebase, with conflict markers handled by per-file `parse_error`
  quarantine.
  `pj sync` is the sole push boundary: preflight refuses if any co-located scope's
  `pj.cue` is unparseable (autoCommit unreadable) or autoCommit values disagree across the derived
  git-root (the invariant is enforced at init but keys on a runtime-derived git-root, so a
  later git-topology change can violate it — refuse rather than push under a broken or
  unverifiable invariant; `pj doctor` runs the same checks off-sync) -> snapshot dirty
  under auto-commit dirs (by autoCommit, not git-root membership; never the whole working
  tree) but commit only the closed allowlist (project `<id>-<slug>.md` at dir root or
  `archive/`, `pj.cue`, `.gitignore`, `AGENTS.md`); non-allowlist residue left uncommitted
  with stderr + doctor warning — never `pj: sync <path>` catch-all, no
  `--force-unknown` in v1 -> unconditional fetch + integrate (rebase with inline
  frontmatter-merge) -> integrity repair -> blocking push if ahead -> report.
  Targets the ambient scope; `--all` for every auto-commit scope.
  pj shells out to external `git` (auto-commit, write/sync paths only); it never creates
  or manages the repo, reporting sync disabled when the repo/upstream is missing.
- Auto-commit in `pj.cue` as `autoCommit: bool` (per scope, synced; consistent across
  all scopes sharing a derived git-root). true = pj-driven: self-commit when a git-root
  exists (upstream not required); `pj sync` when git+upstream exist; without git-root,
  complete-state writes still land and self-commit is skipped with `sync_disabled:` —
  never create the repo); false inside git = repo-driven; false outside git = plain
  files. One flag `--auto-commit` at init; omit is false (or inherit siblings).
- Frontmatter merge (auto-commit only): pure-function package on canned stage blobs
  (no git/fs inside); table-driven adversarial fixtures required before live rebase
  wiring; rebase driver only loads stages and applies results. Post-rebase stage parsing
  (`git show :1/:2/:3`), not a merge driver. Schema-before-data: within one integrate,
  resolve every conflicted `pj.cue` first (text merge or human pause — no silent CUE
  field-merge); project `.md` field-merge runs only after that scope's on-disk `pj.cue`
  is readable, and types custom fields from that declaration (fail closed if config still
  conflicted/unparseable — same class as sync preflight). Frontmatter always resolved to
  clean YAML (stays indexable);
  body clean -> staged, conflicting -> markers in the body only, unstaged, paused rebase,
  human resolves, next `pj sync` resumes. Every field 3-way merged against the git base:
  lists set-merged; a scalar changed on only one side is taken uncontested (the common
  one-sided completion/reorder, never reverted by the other side's commit timestamp), a
  scalar changed on both sides is last-writer-wins by git commit timestamp; a both-sides
  `status` disagreement where at least one value is terminal is routed to the
  paused-rebase handoff, not auto-merged (includes terminal vs non-terminal, so LWW
  cannot erase completion); pure non-terminal status pairs stay LWW; terminal means
  built-in `done`/`cancelled` or any custom with `category: done` (same predicate as
  `depends` satisfaction and done-class list filters); frontmatter kept clean at
  merge-base `status` plus built-in `status_conflict: [a, b]` on the same file (never
  markers, never out-of-band sync-state; the file remains source of truth so index
  rebuilds still see the dispute), and `pj sync` refuses to continue the rebase while
  `status_conflict` is present — human sets `status` and removes the key. Last-push-error
  is separate operational state at `<git-root>/.git/pj/last-push-error`, not project
  metadata.
  Custom frontmatter fields merge by declared type (`strings` = set merge;
  string/int/bool = scalar rules); undeclared keys fall back to scalar LWW.
- Eight flat built-in statuses (draft, backlog, todo, review, in-progress, blocked, done,
  cancelled); labels, not a workflow; built-ins immutable (including `draft` — not
  redeclared under CUE `statuses`), CUE customs additive with a category via `pj.cue`
  `statuses`. Category matrix for customs: only built-in `todo` is ever in `pj next`;
  `active`/`wip` show in default list (not next); `backlog`/`done` hide like built-in
  backlog/done; built-in `draft` is view-equivalent to category `active` (list yes, next
  no, not terminal) but is a built-in name; terminal = built-in `done`/`cancelled` or
  custom `category: done` (`depends` satisfaction, done-class list exclusion, merge
  dispute — no separate cancelled category for customs). Custom frontmatter fields via
  `pj.cue` `fields` (string|int|bool|strings, optional values enum) ship in v1; flat in
  YAML; no required flag, no `pj set` verb; inspect with `pj meta`. `pj create` defaults
  to `draft` (optional second positional any known status; promote with
  `pj status <id> todo` when implementable); scaffold is frontmatter
  (`id`/`status`/`order`/`created`, empty list keys and summary as omitted/empty) plus
  H1 `# <title>` only. `blocked` manual;
  `depends` a separate runnability filter satisfied by any terminal state. Claiming is a
  status write: `pj next` stays a pure read; agents claim with an immediate
  `pj status <id> in-progress` (the loop `pj skill` teaches); the seconds-wide pre-claim
  race is accepted — no `pj next --claim` (a read must not become a writer). No extra
  claim file-lock. Abandoned `in-progress`: doctor soft `stale_in_progress:` when file
  mtime older than 72h; no auto-reopen — recover with ordinary `pj status`.
- Two project-to-project edge kinds, both same- or cross-scope, materialized in one
  shared index `edges` table (`kind` in `depends|related`): `depends` gates `pj next`,
  `related` (ships v1) is soft "see also" and gates nothing. Cross-scope `depends` is
  read from the one machine-wide index (reconcile extends to the depended-on scopes);
  an unresolvable target (scope not registered here, or id gone) is held-not-surfaced
  with a loud annotation and flagged informationally by `pj doctor`, while a same-scope
  dangling `depends` stays a hard flag. Cross-scope edges are surfaced-not-auto-repaired
  by the id-collision repair (the referencing file is in another repo). `related` is
  distinct from `links` (external strings): it holds project ids, indexed for reverse
  lookup and the planned viewer's graph.
- First-class edge inspection: `pj deps <id>` (alias `pj depends`). Pure read, git-free,
  summary stdout (not paths). Default flat direct: depends on, is depended on by, related
  both directions (related never gates, never expanded into a depends tree).
  `--transitive` expands depends both ways as a flat list (agent-friendly; related stays
  direct). `--tree` pretty-prints the depends graph for humans (implies transitive depth;
  related stays flat after the tree). Cycle-safe walks; subject-in-cycle → one stderr
  warning pointing at `pj doctor`. No edge mutation (frontmatter direct edit). Preferred
  over free-form `pj query` for graph questions (query schema not stable).
- `tags` (not `labels`). Lens ships in v1: a machine-local default tag view per scope in
  the XDG config, keyed by scope name, never a wall; `knownTags` for typo warnings.
- Done is a filter; `pj archive` moves a terminal project (`done`/`cancelled` or custom
  `category: done`) into a single `archive/` subdirectory (refuse otherwise) that
  reconcile also scans (row flagged `archived`, still indexed/searchable/resolvable);
  doctor soft-warns non-terminal under `archive/` (`archive_non_terminal:`); optional
  historical declutter; no unarchive verb; never delete.
- Single-purpose CLI `pj`; text only — no `--json`. Locate/mutate verbs print a path;
  `list` and `deps` print summaries; `meta` prints preamble + raw frontmatter (not body).
  Project verbs top-level (`list`/`next`/`get`/`meta`/`deps`/`create`/`status`/`edit`/
  `reorder`/…); scope administration under `pj scope`. No-scope error on scope-requiring
  commands (registry only); discovery commands never error on no-scope. `get`/`meta`/
  `deps` accept full id without ambient scope. Exit codes v1 minimal: `0` ok, non-zero
  fail; only exit `2` distinguished (usage / unknown status); no multi-code map until
  concrete pain. Integrity/doctor stderr uses a closed stable token-prefix set
  (`duplicate_id:`, `equal_order:`, `parse_error:`, `stale_in_progress:`, … — see Agent
  skill Doctor section); agents match tokens not free prose. `pj skill` prints the full
  locked Agent skill contract on demand (authoritative body in that section); `skill
  install|list|uninstall` are hard-refuse placeholders until agentdex. Never an
  auto-written AGENTS.md block.
- `pj meta <id>`: pure read-only header inspect. Id resolution matches `get`. Stdout =
  fixed preamble (`id` full, `title` from H1, absolute `path`) + blank line + frontmatter
  YAML exactly as stored (raw fence interior; no re-encode; no body; no fences). No flags,
  no aliases, no mutation in v1. Agents keep path hand-off on `get`/`next`/`status`/
  `create` (do not parse `path:` from meta). `parse_error` with extractable YAML → exit 0
  + stderr token; without → non-zero. `status_conflict` appears in YAML and on stderr.
  Complements `get` (path) and `deps` (edge neighbourhood). No general frontmatter write
  API (`set`/`field` still rejected).
- Pure Go, no cgo.
