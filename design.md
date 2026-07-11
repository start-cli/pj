# Agent Project Management CLI (pj) — Design

This document states the landed design after the a few iterations and
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
the short-id is a random 4-character string, not a content hash. Ids are typed by a
human, so they are short and unambiguous.

Short-id alphabet (typeability):
- Letters (23): `abcdefghjkmnpqrstuvwxyz` (drops i, l, o).
- Digits (8): `23456789` (drops 0, 1).
- First character is always a letter; positions 2-4 are a 50/50 coin-flip between a
  letter and a digit.
- Lowercase, alphanumeric, no symbols.

Generation and stability:
- Drawn from `crypto/rand` at `pj create`, which checks the ids already present in the
  scope and redraws on any local hit. The draw -> check -> write runs under the scope
  `flock`, so two concurrent local `pj create`s serialise and cannot both reserve the
  same id. Online creation therefore never collides; only offline-concurrent creation
  across machines can. Repair of those duplicates is file-mutating and auto-commit-aware (see
  Uniqueness and collisions below) — never implicit on the read path.
- Not derived from the title or content, so editing a title never changes the id. The
  id is stable by construction; other projects reference it by that value (`depends`,
  `links`).
- Canonical in frontmatter (`id: wc-ab2c`); the filename mirrors it as `<id>-<slug>.md`
  (e.g. `wc-ab2c-network-output-redesign.md`) for human browsing. The title feeds the
  slug only. `pj doctor` flags a filename/frontmatter mismatch.

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
  conflict: the filename is `<id>-<slug>.md` and the slug derives from the title, so two
  same-id projects with different titles land at different paths and the rebase merges them
  clean.
- Detect vs repair (all scopes): after reconcile, pj runs a cheap index query over the
  scopes just reconciled (duplicate `id` rows; equal `order` keys). Hits ride a terse
  warning on the command (stderr) — they never rewrite files on a read
  command. File-mutating repair is confined to:
  - auto-commit: the integrity step at the end of `pj sync` (automatic after integrate), and
  - every scope: `pj doctor`, which runs the identical repair off-sync (the only repair
    path for non-auto-commit scopes, which have no `pj sync` seam).
  Plain-files multi-machine (Dropbox/Syncthing/NFS) is supported on that basis: no sync
  engine, but the same disk-visible duplicates are detected every command and repaired
  when the user or agent runs `pj doctor`. `pj skill` tells agents to run `pj doctor` when
  those warnings appear (and periodically for plain-files). External sync may also drop
  vendor conflict-copy names that do not match `<id>-<slug>.md`; those never enter the id
  namespace — reconcile leaves them unindexed (or `parse_error` if they look like projects),
  and `pj doctor` flags non-project residue under the dir for human cleanup.
- Repair procedure (sync integrity and `pj doctor`):
  - Choose the side to rename by inbound `depends`, checked both in-scope and — via the
    machine-wide `edges` table — cross-scope: rename the side nothing depends on,
    preserving a referenced id. Cross-scope inbound weighs at least as heavily as in-scope,
    because the repair can rewrite in-scope edges but not cross-scope ones, so a
    cross-scope-referenced id is the more valuable one to keep. If both or neither are
    referenced, rename the newer by `created:` (RFC3339 timestamp set at `pj create`; see
    Metadata). If the timestamps are equal — same second, or clock skew that lands on the
    same instant — fall through to lexicographic full id: rename the side whose id string
    sorts greater. That secondary key needs no new metadata and always total-orders the
    pair, so the repair never stalls or picks non-deterministically.
  - Rename by extending to 5 chars (append one char from the restricted alphabet),
    keeping the recognisable prefix.
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
the same `id`) and resolves it automatically with the same rename-to-5-chars repair above
— keeping both files, renaming one side, staging both so the rebase continues, and
reporting the duplicate — never a field-merge (see "Merge conflict handling", layer 3).
The id draw is from `crypto/rand`, independent of the title, so this compound stays gated
by the near-never id collision; the guard exists so that when it does occur the outcome is
two preserved projects, not one silently merged.

Ergonomics: the short-id is unique within a scope, so `pj get ab2c` resolves in the
current scope (type 4 chars); the full `pj get wc-ab2c` addresses another scope.

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

Auto-derivation of a proposed name (what `--auto` proposes): split the code-root basename on `[-_. ]+` and camelCase boundaries; two or
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
status: in-progress        # backlog|todo|review|in-progress|blocked|done|cancelled (+ CUE customs)
order: "n"                 # lexicographic rank key (quoted string); execution order
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
written only by the auto-commit frontmatter merge when both sides change `status` to
different terminal values; it is never set by `pj create`, `pj status`, or ordinary authoring.
Shape: a YAML sequence of exactly two distinct terminal status names (built-in
`done`/`cancelled` or custom `category: done`), e.g. `status_conflict: [done, cancelled]`.
While present, the project is mid-terminal-dispute: `status` holds the merge-base
(last-agreed) value, `pj get`/`pj doctor` surface the choice, and `pj sync` refuses to
continue the rebase until the key is gone. Resolution is in-file — set `status` to the
chosen terminal (either listed value, or another known terminal) and remove
`status_conflict` — the same class of direct edit as resolving a body conflict. The file
remains the source of truth, so an index rebuild still sees the dispute. A
`status_conflict` present when the git-root is not mid-rebase is doctor-hard residue
(stale hand-edit or interrupted cleanup); clear it. Custom `fields` must not shadow the
name.

DECISION: `created` is an RFC3339 timestamp written once at `pj create` and never updated.
It is provenance for humans and the residual total-order key for id-collision repair when
inbound-`depends` does not decide (see "Project ids"). Local wall-clock is fine — the
single-user fleet accepts clock skew as a near-never residual, closed by the
lexicographic-id fallback when two timestamps compare equal. `pj doctor` flags a missing
or non-RFC3339 `created` (date-only values included) so a hand-edited file cannot silently
weaken the repair order.

DECISION: `order` is the single sequencing key; there is no separate `priority`.
`pj next` and default `pj list` sort by `(order, id)`. Urgency is expressed by moving
a project earlier with `pj reorder`, not by a second sort axis. Banded triage, if ever
wanted, returns as a tag or a CUE custom field, not a built-in.

DECISION: `order` is a lexicographic rank key (fractional indexing), not a dense
integer. The value is an opaque string sorted byte-wise over a fixed rank alphabet
(implementation chooses the alphabet and encoding — e.g. a standard fractional-index
library; the design locks the invariants, not a particular package). Inserting or moving
computes a new key strictly between the two neighbours (`keyBetween(left, right)`), so a
reorder writes only the reordered project's file — no neighbour is renumbered. `pj create`
appends with `keyBetween(last, null)`; `--first` uses `keyBetween(null, first)`.

Invariants (load-bearing for merge avoidance):
- Single-file write: `pj reorder` and `pj create` never rewrite a neighbour's `order`. There is
  no multi-file renumber on the hot path.
- Length growth, not rebalance, for adjacent unequal keys: when two neighbours are
  unequal but have no single-character midpoint in the alphabet (classic `a`/`b` case),
  `keyBetween` always succeeds by appending characters (keys grow in length). Unequal
  neighbours therefore always admit a between-key. An implementation that errors or
  renumbers a band when "no space" remains is non-conforming — that would reintroduce
  multi-file conflicts on reorder, undoing layer 1 of "Merge conflict handling".
- Equal keys are the only "no value strictly between" case: two machines offline can
  compute the same key for the same slot. For reads the tie breaks deterministically by
  id (`(order, id)` sort). Generation still has no string strictly between two equal
  keys, so a later `pj reorder` into that slot would have nothing legal to write. Detect vs
  repair matches ids: reconcile-time index detection warns on equal keys (all scopes);
  file-mutating re-space is confined to the `pj sync` integrity step (auto-commit) and
  `pj doctor` off-sync (every scope, including non-auto-commit with no sync seam), rewriting
  only the tied files. This keeps `pj reorder` a single-file write on the hot path and never
  renames or rewrites ranks from a pure read.
- Pathological length (optional escape): repeated inserts into the same microscopic gap
  can grow a key long. `pj doctor` may report over-long `order` values and offer a
  re-space of a local band as an explicit repair (same shape as equal-key re-space: only
  the rewritten files, one commit under auto-commit). It is never implicit on `pj reorder`.
- Why not dense integers: no value between 3 and 4, so an insert rewrites every
  displaced project — reintroducing the identity/order coupling the id scheme escaped
  and turning every offline reorder into a conflict source. A rank key with length growth
  keeps `pj reorder` a single-file edit forever for unequal neighbours.
- Always quoted (`order: "n"`). A bare key that happens to be `n`, `y`, `no`, `yes`,
  `on`, `off`, `null`, or `~` is coerced by a YAML 1.1 parser; quoting keeps it a
  string. `pj doctor` flags an unquoted/non-string `order`.

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
- The dir is pj-only (source of truth files live here only). Source code never lives in it. It is typically a
  subdirectory of the code it tracks (`<repo>/.agents/pj/`), or a standalone directory
  for personal/cross-cutting work.
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
hand-editing; a `settings.cue` appears only when a real setting and its verb do. Writes
go through the CUE Go API (`cuelang.org/go` ast/format): load, mutate, regenerate the
whole owned file, write to a temp file in the same directory, atomically rename.
Wholesale per-file regeneration is safe precisely because the files are machine-owned —
there is no hand-authored formatting to preserve. All XDG-config writes serialize under
one machine-global flock (`${XDG_CONFIG_HOME:-~/.config}/pj/.pj.lock`); the per-scope
flock protects scope files, not this machine-global state, so without it two concurrent
`pj scope init`s could silently drop a registration. Hand-editing still works (it is
plain CUE, read back like anything else), but an XDG file that will not parse is a hard
error naming the file — the registry is the bootstrap that locates every scope, so
unlike a scope's `pj.cue` there is nothing to degrade to.

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

## Scope lifecycle

DECISION: `pj scope init <dir> (--name <name> | --auto) [--code-root <path>]
[--auto-commit]` creates a new scope and registers it. `pj scope import <dir>
[--code-root <path>]` registers an existing on-disk scope (post-clone), files in place.
They are symmetric entrances to the registered state; init writes a fresh `pj.cue` and
a `.gitignore` covering `.pj.lock` (authoring its own dir, not managing the
repo), import reads an existing scope as it ships (name and autoCommit come from its
`pj.cue`, so import takes neither `--name` nor `--auto-commit`).

pj is non-interactive — it never prompts. Everything it needs is a flag or a deterministic
default; the only TTY-sensitive behaviour anywhere in pj is colour. So init takes the name
and auto-commit choice as flags, not prompts.

Name (init only): exactly one of `--name <name>` or `--auto` is required; supplying
neither is an error (the name is never silently defaulted — "always a conscious choice"
survives, and `--auto` is that conscious choice to accept derivation). `--name` is
validated against `^[a-z0-9]{1,12}$`. `--auto` derives from the code-root basename (the
algorithm in "Scope names") and sanitizes to the alphabet; because code-root may now be a
subdirectory, `--auto` reads well for monorepos (`/org/mono/teamA` -> `ta`). A derived
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
| Outside git | set | pj-driven; sync disabled until repo/remote exist |
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
name; `pj doctor` flags pj.cue/registry drift. There is no auto-rekey and no silent
absorb — registration is deliberate, and a bare `pj scope import` of the same dir
would hit the dir disjointness guard while the old registration still exists. The
recovery is conscious re-registration:

```
pj scope forget <old>
pj scope import <dir> [--code-root <path>]
```

`forget` drops the old registry and lens entries and the index rows; `import` registers
under the name now in `pj.cue`. The machine-local lens is not preserved across that
boundary — re-set with `pj lens` if wanted. That cost is accepted: post-share rename is
the expensive path, not a multi-machine operation the registry tries to heal.

DECISION: `pj scope forget <name>` unregisters a scope: removes its registry and lens
entries and drops its index rows. It never touches the scope's files or repo — the files simply
become unknown to this machine until re-registered with `pj scope import`. This is the
deliberate permanent exit; a merely unreachable dir (unmounted drive) stays
registered and is reported by `pj doctor` (see "Invalidation and reconcile").

## Configuration (CUE)

DECISION: config is CUELang (`cuelang.org/go`, pure Go, no cgo). Two tiers, least to
most specific; later overrides earlier:

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

Why CUE earns its weight: the custom statuses and fields a scope declares become the
schema `pj doctor` (and every mutating write) validates every project's frontmatter
against. CUE is a typed, validated schema, not a fancy TOML.

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
field mutation. A verb family can return later without schema change.

Custom fields live in the project file (flat frontmatter) and are materialized in the
index for `pj query` / filters. Agents read them from the file via path from `get`/`next`
/ `create`/`status` — there is no nested JSON `fields` object to document. The index
implementation may use a JSON column; `pj query` schema is not a stable API either way.

TRADEOFF: CUE is a heavier dependency and evaluating it is materially heavier than
decoding TOML — `cuecontext.New()` plus compiling and unifying stands up an evaluator on
a command an agent may call dozens of times a session. Justified only because validated
custom config/frontmatter is wanted, which is exactly what CUE is best at. The
per-command cost is removed from the steady state by caching (below).

DECISION: the CUE evaluation of each scope's `pj.cue` is cached in the index,
keyed by the `(path, mtime, size)` of every file in that config's import closure — not
just the entry file, so an edit to an imported `schema.cue` or a `cue.mod` module
invalidates the cache rather than validating against a stale schema. A steady-state
command re-evaluates a scope's config only when a file in its closure changed; otherwise
it deserializes the cached values. The XDG tier is small and optional and is evaluated
in-process each command (it holds the registry, so it must be read before any scope's
files are located — caching it in the index would be a bootstrap circle).

TRADEOFF (accepted): because the registry cannot be cached — it is the bootstrap that
locates every scope — every invocation, the hot `pj next` included, stands up a CUE
evaluator (`cuecontext.New()` plus a compile) to read a machine-written map that uses none
of CUE's validation, the same fixed per-command cost the scope-config cache removes
elsewhere. Accepted, not format-split, for two reasons. `cuecontext.New()` is instantiated
once per process and amortizes across the registry read and any cache-miss scope-config
evaluation in the same command, so the tax is one evaluator startup per command, not one
per file. And keeping the whole config surface in one language preserves the plain-CUE
hand-editable fallback the XDG tier depends on — a malformed file is read back and reported
like any other CUE, not through a second parser. Storing the machine-written
`registry.cue`/`lens.cue` as JSON to skip the evaluator was weighed and set aside on that
uniformity/fallback ground; it is the reserved escape if profiling later shows the fixed
`cuecontext.New()` cost dominates real command latency, since those files use none of CUE's
schema validation and only the scope `pj.cue` — where the user's custom statuses/fields are
validated — genuinely needs CUE.

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
`pj get`/`next`/`list`/`search` work against the scope, and because only that one scope's
writes are blocked, machine-wide commands that reconcile many scopes (cross-scope
`search`/`list`) are never bricked by one broken config. Per-scope file mutations on a
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

The planned pj viewer — a web-based project monitor read/written by a second, long-lived
process concurrently with the CLI — is a real second consumer the same store already fits,
and it reinforces the decision without carrying it. An in-memory index rebuilt per
invocation cannot back a separate process; a shared, concurrently-readable on-disk store
can, which is why WAL is on from day one and the DB is one machine-wide file the viewer
opens and watches rather than N per-scope connections. Even if the viewer never ships, the
v1 query surface above already earns SQLite. (Forward note: the viewer, having no
per-command boundary, needs its own change-observation — a file watcher or poll —
reintroducing for that process the watcher the CLI does not need. Viewer-design deferred,
recorded so it is not a surprise.)

Alternatives rejected: scan-only, and a gob/json snapshot cache. Both serve simple reads
but neither provides FTS5 search, ad-hoc SQL, or `WITH RECURSIVE` dependency/rollup, and
neither is a durable store a second process can attach to — each would have to be rebuilt
into SQLite the moment the query surface or the viewer pressed on it.

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
   FTS-indexed. The project stays addressable (`pj get` prints it flagged, `pj doctor`
   lists it, a terse `N unparseable` warning rides affected reads) rather than being
   silently dropped or triggering a scope-wide rebuild loop.
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
Hits surface as a terse warning on the command (stderr); they do not
auto-repair. File-mutating repair stays on `pj sync` (auto-commit integrity step) and
`pj doctor` (all scopes). Rationale: the read path must stay free of multi-file rewrites
(a `pj next` must not rename projects), while plain-files multi-machine still learns
about collisions without waiting for a sync that does not exist. See "Project ids" and
"Metadata" for the repair procedure itself.

DECISION: manual validate/rebuild lives on `pj doctor --reindex`, not a top-level verb.
The index auto-reconciles; `--reindex` is the rare escape hatch for when the mtime
heuristic is fooled (a restore-from-backup that resets mtimes, a clock reset, a manual
DB edit). Always safe (derived), never touches the files.

### Query surface

- `pj search <terms> [--scope S]` — full-text over titles and bodies via FTS5 (bm25;
  phrase/prefix/boolean). Machine-wide by default, `--scope` to bound.
- `pj query <sql>` — read-only SQL over the index, for ad-hoc inspection. Rejects
  writes. The schema is explicitly not a stable API: derived, rebuilt on any
  `schema_version` bump, may reshape between releases with no migration. `--help` says
  so; `pj query --schema` prints the current shape. Not for saved queries or tooling.
- Rich `pj list` filters (status/tag/scope/date) compile to index queries.
- Dependency and rollup queries — transitive `depends` via `WITH RECURSIVE`, counts by
  status/scope — come from the index.
- `depends` and `related` are materialized as rows in one shared `edges` table
  (`from_id, from_scope, to_id, to_scope, kind`, `kind` in `depends|related`), populated
  by reconcile from frontmatter. One table backs `WITH RECURSIVE` traversal, reverse
  lookup in either direction (`what blocks X` / `what relates to X`), and the planned
  viewer's project graph. Cross-scope edges are just rows where `from_scope != to_scope`
  — one machine-wide index, no special casing. An edge whose `to_id` matches no project
  row (unregistered scope, or a not-yet-synced target) is kept as a dangling row so the
  viewer can render it as an external node and `pj doctor` can surface the unresolvable
  ones.

WAL mode from day one, so the future viewer reads concurrently with pj's writes. The
viewer also writes (its file-watch reconcile is a delta upsert), so it is a second
writer in a separate process the CLI's `flock` cannot reach; the two serialize through
SQLite's single-writer WAL lock, and every connection sets a `busy_timeout` so a
contended reconcile write waits and retries rather than erroring.

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

DECISION: read commands (`next`/`list`/`get`/`search`/`query`) are git-free. A read
reconciles the index from the files and answers; it does not commit, push, or run any
git subprocess. A direct agent edit is reflected because reconcile stats the files.
Consequence: a cross-machine read can be stale until the next `pj sync` fetch —
acceptable for a single user working mostly one machine at a time.

### Writes commit their own change

DECISION: a mutating command that produces a complete state (`status`/`reorder`/`archive`)
writes the file, write-throughs its row, then commits just that file (`git add <file>` +
`git commit -m "pj: wc-ab2c -> done"`). Adding the specific path (not `-A`) leaves
unrelated dirty files untouched. Synchronous, tens of milliseconds, no push.

`pj create` is the deliberate exception: it scaffolds frontmatter and returns the path for
the agent to fill the body directly, so it produces an incomplete artifact by design and
does not commit. Writing the skeleton reserves the id; the complete project is committed
at the next `pj sync` snapshot. Principle: self-commit when the verb yields a complete
state; defer to sync when it yields a scaffold to be completed by a direct edit.

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
per-dir lock. The locks cannot coordinate every index writer (a read command
reconciles without them, and the viewer is a separate process), so cross-writer
coordination on the index is SQLite's own single-writer WAL lock plus `busy_timeout`, not
the flock.

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
   finds every file pj did not just author (direct edits, `$EDITOR` edits, filled `create`
   skeletons, and the scope's own non-project files) and commits each, one per file, message
   derived from its class and porcelain code:
   - A project `.md` (parseable frontmatter with an `id`): `??` -> `pj: add <id> <slug>`,
     modified -> `pj: edit <id>`.
   - A recognised scope file (`pj.cue`, `AGENTS.md`): a fixed message
     (`pj: config <scope>`, `pj: agents <scope>`).
   - Anything else under a dir: a generic `pj: sync <path>`, reported, so nothing
     pj owns is silently left uncommitted. `pj.cue` must sync so a second machine
     validates against the same schema.
   Scoping the snapshot to the auto-commit dirs is what makes the repo-wide push
   safe: such a dir is pj-only by construction (see "Storage" — source never lives
   in it) and disjoint from every other scope's dir (the disjointness invariant
   enforced at registration; see "Scope lifecycle"), so "anything else" inside it is
   legitimately this scope's to commit and cannot be another scope's files, while anything
   outside every auto-commit dir — unrelated source in a shared repo, a co-located
   non-auto-commit scope's tree — is never touched. The disjointness invariant is what
   forbids the one case that would break this — a sibling scope's dir nested inside
   this one, whose files a recursive `git status` would otherwise sweep under the wrong
   scope. This holds the blast radius by
   construction rather than trusting the git-root to be pj-dedicated, a property init cannot
   enforce against files added later or scopes created on another machine. A repo holding
   several pj scopes snapshots the union of their auto-commit dirs, so "one `pj sync`
   pushes the whole repo" still means every auto-commit scope in it, just not the non-pj
   remainder.
   Crucially, the snapshot's file set is defined by autoCommit, not by the autoCommit-consistency
   invariant continuing to hold: the safety does not assume every scope under this git-root
   is auto-commit. That invariant (enforced at init; see "Scope lifecycle") keys on a
   git-root that is derived at runtime, so a later git-topology change — a `git init` at a
   parent, a moved dir, a new remote — can bring a non-auto-commit scope under an
   auto-commit scope's git-root after both were registered. Sync must therefore not sweep by
   git-root membership alone. As a preflight, `pj sync` re-derives the git-root of every
   scope sharing this root and refuses to proceed if (a) any of those scopes has an
   unparseable `pj.cue` — autoCommit unreadable, same fail-closed class as a mismatch; see
   "Configuration" — or (b) their declared autoCommit values disagree (`scope <x>
   (autoCommit false) shares this git repository with auto-commit scopes — split it into
   its own repo or re-declare autoCommit`), rather than pushing under a silently violated
   or unverifiable invariant;
   `pj doctor` runs the same per-git-root checks off-sync (unparseable sibling + autoCommit
   divergence) and flags both.
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
   recorded for `pj doctor` to verify, not rewritten; re-space tied `order` keys) rather
   than deferring to a manual `pj doctor`. Both write only the files they touch and commit
   under a fixed message. (Detection of the same conditions is cheaper and universal —
   every command's post-reconcile index check, all scopes — and never mutates files; see
   "Invalidation and reconcile". Non-auto-commit repair only via `pj doctor`.)
4. Push synchronously (blocking) if ahead. Step 2 already integrated the remote, so an
   ordinary sync fast-forwards; a reject means the remote moved in the fetch->push race,
   handled by looping to step 2 once more. A sync with nothing to push (a read-only
   machine) skips the push — it already pulled in step 2.
5. Report unpushed count, conflicts, and repairs.

Blocking on the push (~100ms-1.5s, dropped toward ~100ms by SSH `ControlMaster` reuse)
is negligible against LLM latency and is what makes sync reliable: when it returns, the
remote has the work and any conflict has surfaced in sync's output. `pj skill` tells
agents to run `pj sync` at the end of every turn. Forgetting it costs a delayed push,
never data. No `pj save`/`pj end` verb — `pj sync` is that boundary.

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
create — the repo itself: for auto-commit, sync first checks the scope is a git repo with
an upstream (a `.git` stat, then `git rev-parse --abbrev-ref @{u}`), and if not, reports
sync disabled with a professional warning (`sync is disabled until this scope is a git
repository with a remote; set one up with git, then pj sync`) rather than a raw git
error. A terse warning also rides write commands and `pj sync` on write commands and `pj sync` (stderr). Reads stay git-free and do not carry it.

### Auto-commit

DECISION: each scope declares `autoCommit: bool` in `pj.cue`. It is a scope property,
identical on every machine, so it is synced, not machine-local. The useful control is
one bit: whether pj commits. Host vs plain-files is derived from "is the dir inside a
git repository?", not a third stored choice.

| `autoCommit` | In a git repo? | Behaviour |
|---|---|---|
| `true` | yes (or planned) | pj-driven: complete mutations self-commit; `pj sync` is the fetch/push boundary |
| `false` | yes | repo-driven: surrounding repo / human / PR commits; pj never runs git |
| `false` | no | plain files: no VC (or external Dropbox/Syncthing/NFS); pj never runs git |

Help-text honesty: "auto-commit" means pj owns the commit path, not every keystroke:
- `status` / `reorder` / `archive` → self-commit when `autoCommit: true`
- `create` → scaffold only; complete project commits at `pj sync`
- direct agent / `$EDITOR` edits → committed at `pj sync`
- push only in `pj sync`, never automatic

When `autoCommit: false` inside git (repo-driven): a single host repo may carry many
scopes (a monorepo, one scope per team/area). pj never runs git.

When `autoCommit: false` outside git (plain files): single machine, or cross-machine
handled externally. The index still serves reads; only sync is absent. Multi-machine
integrity is detect-on-reconcile + repair-via-`pj doctor` (no automatic repair seam —
there is no `pj sync`). External sync conflict-copy filenames are doctor-flagged residue,
not merged. Prefer `autoCommit: true` when git-shaped multi-machine merge and automatic
integrity matter.

`autoCommit` is a per-repo fact: every scope sharing a derived git-root must declare the
same value (enforced at init). Because auto-commit sync operates on the git-root,
syncing any scope in a multi-scope auto-commit repo fetches/rebases/pushes that repo once
and its snapshot step commits every scope's dirty files under the one push — the "one
push syncs everything" behaviour a central pj repo wants.

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
   touch the same file. Reordering holds too, because `order` is a rank key — `pj reorder`
   rewrites only the reordered file, and `keyBetween` length-grows rather than renumbering
   neighbours when the alphabet has no single-character midpoint (see "Metadata"). There
   is no registry inside the repo to contend on.
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
     "Project ids": keep the side nothing depends on renamed (its id extended to 5 chars, a
     new path), keep the other at its path, rewrite in-scope `depends`/`related` and record
     cross-scope inbound edges for `pj doctor` to verify, then stage both files so the
     rebase continues, and report a repaired duplicate. This is not a layer-4 human handoff
     — it is the same automatic repair the sync integrity step runs, triggered here because
     the shared slug made the paths coincide instead of landing as two clean files.
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
   - Scalars, both sides changed and not a terminal-status dispute (below): a genuine
     two-sided edit, so last-writer-wins by git commit timestamp (author date). `order`
     follows this; a tied key resolves at read time by `(order, id)`. Non-terminal
     `status` pairs (e.g. `todo` vs `in-progress`) use this path too. Custom scalars use
     this path only — there is no terminal-style dispute for custom fields.
   - A frontmatter key that is undeclared (not a built-in and not in `pj.cue` `fields`) is
     merged as a scalar string-ish last-writer-wins when both sides touch it, otherwise
     one-side-changed wins; doctor already warns on it. Prefer declaring the field so the
     typed list/scalar rule applies.
   - Scalar `status`, both sides changed to different terminal values: do not auto-merge,
     do not pick a winner. Terminal is one definition shared with `depends` satisfaction,
     done-class list filters, and merge dispute: a value is terminal when it is built-in
     `done` or `cancelled`, or a CUE custom status whose declared `category` is `done`
     (e.g. `shipped`, `wontfix`). So `done` vs `cancelled`, `done` vs `shipped`, and
     `shipped` vs `wontfix` all dispute; customs do not reopen silent erasure under
     concurrent multi-machine edit. Keep the frontmatter clean YAML — never conflict
     markers in it — so the file stays parseable and indexable: write `status` to the
     merge-base (last-agreed) value and write the disputed pair into the built-in
     `status_conflict: [a, b]` key on the same file (see Metadata). Route the file into
     layer 4's paused-rebase handoff for a human to decide. The dispute lives in the
     project file — source of truth — not in out-of-band "sync-state" or as index-only
     memory; reconcile materializes it like any other frontmatter, so rebuilds cannot
     drop the choice. This fires only on a real dispute — both machines drove the project
     to a different terminal state — not on a one-sided completion, which the
     one-side-changed rule above takes cleanly.
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
   `pj sync` resumes the rebase (`git rebase --continue`) and pushes. A terminal-status
   disagreement layer 3 declined: there is no body conflict and pj writes no markers at all
   — the frontmatter carries merge-base `status` plus `status_conflict: [a, b]` (any two
   distinct terminal values — built-in or custom done-category), clean and indexable, so
   `pj get`/`pj doctor` surface "terminal-status conflict — set status to one of: <a>,
   <b> and remove status_conflict in <file>". The path is left unstaged, so the rebase
   stays paused at the git level; the fail-fast that closes the silent-erasure hole is that
   `pj sync` refuses to `git rebase --continue` while `status_conflict` is still present on
   the file, rather than sailing past a file that only looks resolved. The human makes the
   call by editing `status:` to either disputed terminal value (or another known terminal)
   and deleting `status_conflict` — a direct file edit, exactly as a body conflict is
   resolved in-file, and correct because a `pj status` mutation on an auto-commit scope is
   refused mid-rebase; the next `pj sync` sees the key gone, stages the file, continues
   the rebase, and pushes. Common to both: nothing is pushed, every auto-commit mutating
   command refuses while the rebase is in progress (fail fast), and the file is reported
   via `pj doctor`. Reads stay git-free, so `pj next`/`get`/`search` keep working — only
   auto-commit mutation is blocked, correct while the base is inconsistent. Because the
   frontmatter is resolved to clean YAML before the file is written, the index can read the
   project throughout — whether a body or a status decision awaits a human.

Honest boundary: this trades beads' automatic Dolt cell-merge for a small custom
frontmatter merge plus human resolution of bodies. Good trade because one file per
project keeps same-file collisions rare and the frontmatter surface is tiny.

## Status and dependencies

DECISION: seven flat built-in statuses. Lowercase, hyphen-joined; no spaces or
underscores. `pj doctor` rejects a space in a status.

| status | meaning | in `pj next`? | in default `pj list`? |
|---|---|---|---|
| backlog | someday/maybe, not committed | no | no (`--all`) |
| todo | committed + ordered, ready | yes (if `depends` all terminal) | yes |
| review | under review (plan or result) | no | yes |
| in-progress | actively worked | no | yes |
| blocked | manually set; reason in body | no | yes, flagged |
| done | complete (terminal) | no | no (`--all`) |
| cancelled | abandoned (terminal) | no | no (`--all`) |

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

DECISION: CUE custom statuses are additive; the seven built-ins are immutable. Each
custom status declares a `category` (active/wip/backlog/done) so views treat it
correctly without knowing its name (beads' `StatusCategory`, reused). Declaration form
is `statuses: { <name>: { category: <cat> } }` in `pj.cue` (see "Configuration").

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
`active` and `wip` both mean "show in the default list"; the split is for human/viewer
grouping (open vs in-flight labels), not a second ready path. `backlog` hides like
built-in `backlog`; `done` hides like built-in `done`/`cancelled`.

Terminal is one predicate shared by `depends` satisfaction, default list exclusion for
done-class statuses, and merge dispute: built-in `done` or `cancelled`, or any custom
whose `category` is `done` (e.g. `shipped`, `wontfix`). There is no separate
`cancelled` category — abandonment-shaped customs use `category: done` (same as
`wontfix`). A custom done-category status satisfies `depends` and is excluded from
default `pj list` like `done`/`cancelled`; two machines that both-sides-change `status`
to different terminals dispute rather than last-writer-wins (see "Merge conflict
handling").

DECISION: CUE custom frontmatter fields ship in v1. A scope declares them under
`fields` in `pj.cue` with a closed type set (`string`/`int`/`bool`/`strings`) and an
optional `values` enum for string kinds. Keys are flat in project YAML (no nested
wrapper in the file). Agents read customs from the file. Merge typing follows the declaration
(list vs scalar). No required-field flag and no `pj set` verb in v1 — optional on every
project, authored by direct edit. Full shape and validation rules in "Configuration".

DECISION: `pj create` defaults new projects to `todo`; optional second positional
status (e.g. `backlog`) captures without committing to the ready queue. Two project-to-project edge kinds — `depends` (blocks, gates `pj next`) and
`related` (soft "see also", gates nothing) — not beads' ~15 types.

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
- `status: done` drops a project from default `pj list`; `--all`/`--archived` brings it
  back. Files stay where they are.
- Optional `pj archive <id>` physically moves a done project file into an `archive/`
  subdirectory of the dir. Reconcile scans that one fixed subdirectory alongside the flat
  dir and flags the moved project's row `archived`, so it stays indexed, searchable, and
  resolvable (`pj get`/`pj search` still find it; default `pj list` hides it,
  `--archived`/`--all` brings it back). Purpose: optional declutter of the flat authoring
  directory after completion; the record stays indexed and resolvable. Archive is never
  required — `status: done` is enough for the queue. `archive/` is the lone tool-managed
  exception to the flat-scope rule; no other subdirectory is scanned. Ordinary English
  "move" for this filesystem relocate is correct (contrast `pj reorder`, which rewrites
  an `order` key only).
- Never delete. The record persists as the still-present file and in git history.

## CLI surface

DECISION: single-purpose CLI named `pj`. Project management only. Text on stdout —
locate/mutate verbs print a path (one line); `list` prints a summary (no paths by
default). pj does not support `--json`. No flag, no stable JSON envelope, on any command.
Warnings, doctor, empty-queue diagnostics: stderr text (and human stdout where
appropriate). Revisit only if concrete text pain appears later (not a v1 pillar).

Product cut: pj indexes, queues, and locates; the filesystem is the editor. No "print
full project markdown" verb — the file is the content. Filenames already embed the id
(`<id>-<slug>.md`). Agents edit with file tools; humans may use `$EDITOR` via `pj edit`.

DECISION: project verbs are top-level — the unit of work is the CLI's purpose, and
`list`/`next`/`get`/`create`/`status`/`edit`/`reorder`/`search`/`sync` are the hot path.
Scope administration (container management, not work; each command runs about once per
scope per machine) groups under `pj scope`: `init`, `import`, `use`, `rename`, `forget`,
`list`. `pj scopes` is accepted as an alias of `pj scope`, and the bare noun with no
subcommand runs `list`.

Hot path stdout contract:

| Command | Job | stdout |
|---|---|---|
| `list [status…]` | Board / inventory | Summary (id, title, status, waiting-on, …) — no paths by default |
| `next` | First ready `todo` (deps ok, order, lens) | Path |
| `get <id>` | Resolve short or full id | Path |
| `create <title> [status]` | Scaffold project (default status `todo`) | Path |
| `status <id> <status>` | Set status (claim / done / …) | Path (after write) |
| `edit <id>` | Open in `$EDITOR` | human convenience only |
| `reorder <id> …` | Rewrite fractional `order` key | Path (after write) |

Errors: non-zero exit, message on stderr, no path on stdout when there is nothing to hand
off (unknown id, nothing ready, …).

Agent loop:

```text
pj next                         → path
pj status <id> in-progress      → path (claim)
# file tools on that path
pj status <id> done             → path
```

Known id: `pj get ab2c` → path. Capture: `pj create "Title"` / `pj create "Later" backlog`.

- `pj scope init <dir> (--name <name> | --auto) [--code-root <path>]
  [--auto-commit]` — create and register a scope. Dir required; exactly one of
  `--name`/`--auto`; code-root by the matrix (`--code-root` always allowed, defaults to
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
- `pj list [status…] [--scope S] [--all] [--no-lens]` — list projects as a summary (no
  paths by default). Zero or more space-separated known status names = union filter.
  Bare `pj list` keeps the default active set (not a status name). Unknown status →
  exit 2. No CSV. No `--status` flag. `--all` remains "include done/backlog/…" board-wide,
  not a status token. Lens still applies unless `--no-lens`. Examples: `pj list`,
  `pj list todo`, `pj list todo backlog`, `pj list in-progress blocked review`.
- `pj get <id>` — resolve short or full id to the project file path; print that path.
  Read/locate only — no `--status` or other mutation flags (mutation stays on `status` /
  `create`). Optional later (not v1): path column on `list`; aliases `show`→`get`.
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
- `pj create <title> [status]` — scaffold a project: mint the id, write valid frontmatter,
  write through the index row, print the path for the agent to fill the body. Optional
  second positional is a known status if present; omitted → `todo`. Title is one argv
  (quote multi-word). Replaces special-case `--backlog`. No `--status` flag. No
  status-first order. Does not commit (incomplete by design; the skeleton reserves the
  id; committed at the next `pj sync` when auto-commit). The one create call; every edit
  after is direct file access. Optional later (not v1): alias `add`→`create`.
- `pj status <id> <status>` — set status (word is status, not state). A complete-state
  write: auto-commit commits the one file synchronously (no push); non-auto-commit just
  writes the file. Prints the path after success.
- `pj edit <id>` — resolve id to path and open in `$EDITOR`. Human convenience only;
  agents use `get` / `next` / `status` / `create` and their own file tools.
- `pj reorder <id> (--before <id> | --after <id> | --first | --last)` — rewrite the
  fractional `order` key to an explicit slot; the destination flag is required (no bare
  `pj reorder <id>`). pj reads the target neighbours from the index and writes
  `keyBetween(left, right)` into the reordered project's frontmatter only (length-grows
  when neighbours are adjacent in the alphabet; never renumbers a band). No relative
  counters, swap, or batch. Complete-state mutation: self-commits when auto-commit is on
  (same class as `status` / `archive`). Prints path after success; errors → stderr, no
  path. Not cross-scope relocation (id embeds scope; do not overload this verb). No v1
  alias `move`→`reorder`.
- `pj sync [--all]` — reconcile now / done-for-now and the sole push boundary (auto-commit
  scopes). Targets the ambient scope; `--all` (or no ambient scope) syncs every auto-commit
  scope. `pj skill` tells agents to run it at end of turn.
- `pj doctor [--reindex]` — report conflicts, same-scope dangling `depends` (hard),
  unresolvable cross-scope `depends`/`related` (informational — scope not registered here
  vs target gone are indistinguishable), cross-scope references whose target was
  collision-repaired or scope-renamed (verify — possible silent mispoint), `depends` cycles,
  depends-on-cancelled, registry/config drift (including remote rename: pj.cue name ≠
  registry key — recovery is `pj scope forget` then `pj scope import`, not auto-rekey),
  unparseable `pj.cue` (scope read-only; blocks `pj sync` for the whole shared git-root),
  autoCommit divergence across scopes sharing a derived git-root (the init-time invariant
  broken by a later git-topology change), frontmatter schema violations (unknown status,
  custom field type/`values` mismatch — hard; undeclared frontmatter keys and
  `knownTags` typos — warn), terminal-status dispute (`status_conflict` present —
  mid-rebase: resolve in-file; not mid-rebase: hard residue to clear), last-push error
  and sync health (repo/upstream not set up; marker at `<git-root>/.git/pj/last-push-error`),
  unparseable project files, non-project residue under the dir (e.g. external-sync
  conflict-copy names that do not match `<id>-<slug>.md`), and index health; runs the
  id-collision (in-scope reference-safe, cross-scope surfaced) and tied-`order` repairs
  for every scope — this is the only file-mutating integrity path for non-auto-commit, and
  the off-sync twin of the auto-commit `pj sync` integrity step; may report pathologically
  long `order` keys and re-space a local band on request (never implicit on `pj reorder`).
  `--reindex` forces a full index rebuild from the files. Text on stderr/stdout — no JSON
  envelope.
- `pj archive <id>` — physically move a done project into `archive/` (write-through flags
  the row `archived`; reconcile scans `archive/`, so the project stays resolvable and
  searchable). Declutters the authoring dir; record stays indexed and resolvable. Prints
  path after success when applicable.
- `pj skill` — print agent-facing workflow markdown to stdout (see Discovery). Discovery
  command: no ambient scope required. Path-centric loop; claim via `status`;
  `doctor` / `sync` / post-clone `scope import` guidance. Never auto-writes into a tree.
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
`doctor`, `help`, `skill` and skill placeholders) never error on no-scope.

## Discovery

DECISION: discovery is opt-in and user-initiated — pj never auto-writes a discovery
artifact into any tree, and never scans the tree for an unregistered scope. Mechanisms:
- The CLI auto-resolves the ambient scope from cwd via the registry only, so an agent
  just runs `pj` in a registered tree. An unregistered on-disk scope is invisible until
  `pj scope import` (no filesystem probe — see Scope lifecycle).
- `pj skill` (v1 real) prints the workflow instructions to stdout on demand (the pattern
  beads' `onboard`/`prime` and webctl's `help <topic>` use): path-centric loop from CLI
  surface; claim via `status`; `doctor` / `sync` / post-clone `scope import` guidance;
  no pipe-to-jq. An agent that already knows pj exists can prime itself with no persistent
  state. That print is also where post-clone import is taught (`pj scope import <dir>`),
  since the CLI will not infer the path from the tree.
- `pj skill install|list|uninstall` are v1 hard-refuse placeholders (see CLI surface).
  Persistent install needs each agent's skills directory; that lookup is owned by
  agentdex (`agentdex get <id>` reports `skills_dir` / local skills paths; catalog is
  provider-agnostic). pj will use agentdex rather than hardcoding Claude/OpenCode/etc.
  paths. Until that integration exists, install must not ship half-baked. Deferred with
  real install (not v1): agentdex integration, concrete targets (global/local skills dirs
  per agent, optional AGENTS.md block), and list/uninstall semantics against what was
  installed. Installation remains user-initiated, never automatic: `pj scope init` writes
  no AGENTS.md block.

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
statuses dropped (a closed set of seven, no lifecycle machinery).

## Anti-goals (avoid becoming beads)

- No database as source of truth. Frontmatter makes adding a field free and removing one
  a grep-and-delete — no migrations, no dead columns. The index is a derived view; the
  registry is small machine-local config; authority stays in the files.
- No scope explosion. beads grew molecules, swarms, gates, wisps, federation, and
  GitHub/Jira/Linear/Notion sync. Anchor: a small closed built-in set (seven statuses,
  two project-to-project edge kinds, a compact verb surface) over one file per project,
  no lifecycle machinery; CUE customs for anything beyond.
- No double handling (the beans sin): files are edited in place; the CLI never asks for
  a temp file to be handed to it.

## Open questions

None outstanding. The items raised while drafting (soft `related` link, `pj sync` default
scope, `pj scope use` code-root count, autoCommit default, malformed `pj.cue` handling,
concrete `pj.cue` / custom-field schema) are all resolved in the doc body and the
decisions log.

## Decisions log (locked)

- project = one markdown file; tasks live inside it; the CLI does not model tasks.
- Vocabulary: scope / project / task. store == scope (no separate store container). A
  repo hosts a scope.
- Markdown files are always the source of truth; edited in place; no double handling.
- Per-project metadata in the file's YAML frontmatter; no separate index and no DB as
  truth. Built-in keys: `id`, `status`, `order`, `depends`, `related`, `tags`,
  `created`, `links`, `summary`, plus transient merge-only `status_conflict` (exactly two
  terminal names; written only by auto-commit terminal dispute merge; resolution is set
  `status` and remove the key; refuse `rebase --continue` while present; doctor-hard if
  present when not mid-rebase).
- ids are `<scope>-<short-id>` (random 4-char, human-typeable alphabet, first char a
  letter, even letter/digit split); id canonical in frontmatter, filename mirrors as
  `<id>-<slug>.md`. `pj create` redraws on a local hit (online creation never collides);
  offline-concurrent collisions (no git conflict): detect via post-reconcile index query
  every command (all scopes, warn only); file-mutating repair by the `pj sync` integrity
  step (auto-commit) and `pj doctor` (every scope — sole path for non-auto-commit): rename
  the side nothing depends on (inbound checked in- and cross-scope; if both/neither,
  newer by RFC3339 `created:`, then lexicographic full id) to 5 chars, atomically rewrite
  in-scope `depends`/`related`; cross-scope edges (another repo, unrewritable here) are
  not touched but recorded, and `pj doctor` flags them to verify against a silent mispoint;
  report. Plain-files multi-machine uses detect + doctor (no sync seam); external
  conflict-copy names are doctor-flagged residue. `created` is RFC3339 at `pj create`,
  immutable; doctor flags missing/non-RFC3339.
- `order` is a frontmatter lexicographic rank key (fractional indexing over a fixed
  alphabet); `keyBetween` always succeeds for unequal neighbours by length growth — never
  multi-file renumber on `pj reorder`/`pj create`. Equal keys (offline concurrent) break by id
  for reads, warn on post-reconcile detection, and are re-spaced by the `pj sync`
  integrity step (auto-commit) / `pj doctor` (all scopes); optional doctor re-space for
  pathologically long keys, never implicit on reorder.
- Scope name `^[a-z0-9]{1,12}$`, machine-unique, never silently defaulted; it is the
  address and id prefix, not a directory name. Fleet-global in effect (stated
  assumption: one user registers names consistently across machines). `pj scope rename`
  is the tooled clash remedy: in-scope rewrite under the flock (pj.cue name, frontmatter
  ids, filenames, in-scope edges; auto-commit one commit); cross-scope inbound edges
  recorded for `pj doctor` (surfaced-not-auto-repaired); authoring machine registry and
  lens re-keyed. Cheap path: rename before other machines register. Post-share recovery
  is deliberate, not auto-healed: `pj scope forget <old>` then `pj scope import` (lens
  dropped; re-set with `pj lens` if wanted). `pj scope forget` unregisters (registry +
  lens entries, index rows) without touching files.
- Storage: a scope is a directory of flat `.md` files plus `pj.cue` (renamed from
  `config.cue`, namespaced) and a `.gitignore` covering `.pj.lock` (written by
  `pj scope init`). The dir is pj-only, user-chosen at init (recommend
  `.agents/pj/`), never defaulted. A git repo may host several scopes at distinct
  code-roots (monorepo one-scope-per-team; central pj repo with sibling scopes); the unit
  is the scope, not the repo. Every scope sharing a repo agrees on its autoCommit.
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
- `pj scope init <dir> (--name <name> | --auto) [--code-root <path>]
  [--auto-commit]`: `--code-root` always allowed (this is what lets scopes share a
  repo), defaulting to repo root in a repo else the dir; exactly one of `--name`
  (explicit, `^[a-z0-9]{1,12}$`) or `--auto` (derive from code-root basename, hard error on
  a derived-name collision); `--auto-commit` sets `autoCommit: true`, omit sets false
  (or inherits siblings when the repo already has scopes — omit does not re-default to
  false against true siblings). Accepted tradeoff: first scope in a git repo + omit =
  repo-driven. autoCommit consistency per derived git-root enforced; code-root collisions
  rejected and nested code-roots allowed, while dirs must be mutually disjoint (identical,
  nested, or containing a registered dir all rejected — a load-bearing invariant the
  `pj sync` snapshot relies on). `pj scope import` is symmetric for an existing scope
  (name and autoCommit from its `pj.cue`), hard-failing on a scope-name collision or
  malformed `pj.cue`.
- Config: two CUE tiers — XDG config directory (machine-written by pj, one CUE package,
  per-concern files: `registry.cue`, `lens.cue`; CRUD via the CUE Go API with wholesale
  per-file regeneration, temp file + atomic rename, all writes under one machine-global
  flock `.pj.lock`; no `editor` key — `pj edit` uses `$EDITOR`; a malformed XDG file is
  a hard error, it is the bootstrap) < scope `pj.cue` with a concrete shape: required
  `name` + `autoCommit`; optional `knownTags`, `statuses` (each `{category}` in
  active|wip|backlog|done; additive, no built-in redeclare; category drives default list
  and terminal/`depends` only — never `pj next` membership, which is built-in `todo`
  alone; see Status decisions), and `fields` (each `type` in string|int|bool|strings,
  optional `values` enum for string kinds; keys `^[a-z][a-z0-9_]{0,31}$`, no built-in
  shadow including `status_conflict`). Custom fields sit flat in project YAML; agents
  read them from the file; merge uses list set-merge for `strings`, scalar rules
  otherwise; undeclared frontmatter keys are doctor warnings.
  No required-field flag and no `pj set` verb in v1 (direct edit). Env/flags override.
  Scope config CUE evaluation is cached in the index keyed by its import closure's
  `(path, mtime, size)`; the XDG tier is evaluated in-process (it holds the registry
  needed to locate scopes), so a CUE evaluator starts up on every command — accepted (one
  fixed `cuecontext.New()` per command, amortized across the registry read and any scope
  eval; kept CUE for a single hand-editable config surface), with a JSON split of the
  machine-written tier reserved should profiling show that startup dominates. A malformed
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
  `pj doctor` for every scope (sole path for non-auto-commit). Schema change = rebuild,
  not migration. Query surface:
  `pj search` (FTS5), `pj query` (read-only SQL, schema not a stable API), rich
  `pj list` filters, `WITH RECURSIVE` dependency/rollup. WAL from day one with a
  `busy_timeout` for the concurrent viewer.
- Sync (auto-commit) split along the commit/push seam; no snapshot machinery on every
  command. Reads git-free. Writes that yield a complete state self-commit one file (no
  push); `pj create` scaffolds without committing. The file write and id draw serialize on a
  per-dir `flock`; the git commit/rebase/push (repo-granular) additionally serialize
  on a git-root lock, so several `pj` scopes sharing a repo cannot corrupt its index and
  one `pj sync` pushes the whole repo. Mid-rebase mutation refuse is auto-commit only
  (self-commit path); non-auto-commit keep writing files mid-rebase, with conflict markers
  handled by per-file `parse_error` quarantine.
  `pj sync` is the sole push boundary: preflight refuses if any co-located scope's
  `pj.cue` is unparseable (autoCommit unreadable) or autoCommit values disagree across the derived
  git-root (the invariant is enforced at init but keys on a runtime-derived git-root, so a
  later git-topology change can violate it — refuse rather than push under a broken or
  unverifiable invariant; `pj doctor` runs the same checks off-sync) -> snapshot
  dirty (scoped to the auto-commit dirs under the git-root by autoCommit, not by git-root
  membership, never the whole working tree, so unrelated source or a co-located non-auto-commit
  tree is never swept in) -> commit each -> unconditional fetch + integrate (rebase with
  inline frontmatter-merge) -> integrity repair -> blocking push if ahead -> report.
  Targets the ambient scope; `--all` for every auto-commit scope.
  pj shells out to external `git` (auto-commit, write/sync paths only); it never creates
  or manages the repo, reporting sync disabled when the repo/upstream is missing.
- Auto-commit in `pj.cue` as `autoCommit: bool` (per scope, synced; consistent across
  all scopes sharing a derived git-root). true = pj-driven (self-commit + `pj sync`);
  false inside git = repo-driven; false outside git = plain files. One flag
  `--auto-commit` at init; omit is false (or inherit siblings).
- Frontmatter merge (auto-commit only): post-rebase stage parsing (`git show :1/:2/:3`),
  not a merge driver. Schema-before-data: within one integrate, resolve every conflicted
  `pj.cue` first (text merge or human pause — no silent CUE field-merge); project `.md`
  field-merge runs only after that scope's on-disk `pj.cue` is readable, and types custom
  fields from that declaration (fail closed if config still conflicted/unparseable — same
  class as sync preflight). Frontmatter always resolved to clean YAML (stays indexable);
  body clean -> staged, conflicting -> markers in the body only, unstaged, paused rebase,
  human resolves, next `pj sync` resumes. Every field 3-way merged against the git base:
  lists set-merged; a scalar changed on only one side is taken uncontested (the common
  one-sided completion/reorder, never reverted by the other side's commit timestamp), a
  scalar changed on both sides is last-writer-wins by git commit timestamp; a both-sides
  terminal-state disagreement is routed to the paused-rebase handoff, not auto-merged —
  terminal means built-in `done`/`cancelled` or any custom with `category: done` (same
  predicate as `depends` satisfaction and done-class list filters), so customs do not
  reopen silent erasure; frontmatter kept clean at merge-base `status` plus built-in
  `status_conflict: [a, b]` on the same file (never markers, never out-of-band sync-state;
  the file remains source of truth so index rebuilds still see the dispute), and
  `pj sync` refuses to continue the rebase while `status_conflict` is present — human
  sets `status` to the chosen terminal and removes the key. Last-push-error is separate
  operational state at `<git-root>/.git/pj/last-push-error`, not project metadata.
  Custom frontmatter fields merge by declared type (`strings` = set merge;
  string/int/bool = scalar rules); undeclared keys fall back to scalar LWW.
- Seven flat built-in statuses (backlog, todo, review, in-progress, blocked, done,
  cancelled); labels, not a workflow; built-ins immutable, CUE customs additive with a
  category via `pj.cue` `statuses`. Category matrix for customs: only built-in `todo` is
  ever in `pj next`; `active`/`wip` show in default list (not next); `backlog`/`done` hide
  like built-in backlog/done; terminal = built-in `done`/`cancelled` or custom
  `category: done` (`depends` satisfaction, done-class list exclusion, merge dispute —
  no separate cancelled category for customs). Custom frontmatter fields via `pj.cue`
  `fields` (string|int|bool|strings, optional values enum) ship in v1; flat in YAML;
  no required flag, no `pj set` verb. `pj create` defaults to `todo` (optional second
  positional status otherwise). `blocked` manual; `depends` a separate runnability
  filter satisfied by any terminal state.
  Claiming
  is a status write: `pj next` stays a pure read; agents claim with an immediate
  `pj status <id> in-progress` (the loop `pj skill` teaches); the seconds-wide
  pre-claim race is accepted — no `pj next --claim` (a read must not become a writer).
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
- `tags` (not `labels`). Lens ships in v1: a machine-local default tag view per scope in
  the XDG config, keyed by scope name, never a wall; `knownTags` for typo warnings.
- Done is a filter; `pj archive` moves a done project into a single `archive/` subdirectory
  that reconcile also scans (row flagged `archived`, still indexed/searchable/resolvable);
  optional decluttering; never delete.
- Single-purpose CLI `pj`; text only — no `--json`. Locate/mutate verbs print a path;
  `list` prints a summary. Project verbs top-level (`list`/`next`/`get`/`create`/
  `status`/`edit`/`reorder`/…); scope administration under `pj scope`. No-scope error on
  scope-requiring commands (registry only); discovery commands never error on no-scope.
  `pj skill` prints agent workflow on demand; `skill install|list|uninstall` are
  hard-refuse placeholders until agentdex. Never an auto-written AGENTS.md block.
- Pure Go, no cgo.
