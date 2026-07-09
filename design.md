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
(`ready`, status categories, `--json`, one-op-one-commit, onboarding dump) is worth
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
- Drawn from `crypto/rand` at `pj add`, which checks the ids already present in the
  scope and redraws on any local hit. The draw -> check -> write runs under the scope
  `flock`, so two concurrent local `pj add`s serialise and cannot both reserve the
  same id. Online creation therefore never collides; only offline-concurrent creation
  across machines can (handled by the sync integrity step below).
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
  ids, ~0.15% at ~30) apply only to uncoordinated draws. `pj add` redraws on a local
  hit, so the only uncoordinated draws are offline-concurrent creates on two machines
  before a sync — a small window for a single user, so a real collision is near-never.
- Resolution is reference-safe within the scope, and surfaced (not silently rewritten)
  across scopes. An offline-concurrent duplicate produces no git conflict (the two ids
  live in two differently-slugged files, so the rebase merges them clean). The integrity
  step at the end of `pj sync` repairs it automatically (`pj doctor` runs the identical
  repair off-sync):
  - Choose the side to rename by inbound `depends`, checked both in-scope and — via the
    machine-wide `edges` table — cross-scope: rename the side nothing depends on,
    preserving a referenced id. Cross-scope inbound weighs at least as heavily as in-scope,
    because the repair can rewrite in-scope edges but not cross-scope ones, so a
    cross-scope-referenced id is the more valuable one to keep. If both or neither are
    referenced, rename the newer by `created:`.
  - Rename by extending to 5 chars (append one char from the restricted alphabet),
    keeping the recognisable prefix.
  - In the same operation (same repo, same commit) rewrite every in-scope
    `depends`/`related`/`links` that pointed at the renamed id, so no in-scope edge
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

Ergonomics: the short-id is unique within a scope, so `pj show ab2c` resolves in the
current scope (type 4 chars); the full `pj show wc-ab2c` addresses another scope.

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
  (see "Visibility"), and the id namespace is flat, so `pj show wc-ab2c` must resolve
  to exactly one scope. A name collision at `pj init`/`pj import` is a hard error (see
  "init and import").
- Typical is 2-4 chars for ergonomics; a readable word up to 12 (`webctl`) is fine.
- Never silently defaulted (the beads mistake: an auto-assigned junk name to rename
  later). Always a conscious choice at init.

Auto-derivation of a proposed name (the interactive default at `pj init`, or
`--auto`): split the code-root basename on `[-_. ]+` and camelCase boundaries; two or
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
status: in-progress        # backlog|todo|review|in-progress|blocked|done|cancelled
order: "n"                 # lexicographic rank key (quoted string); execution order
depends: [wc-9k3m]         # project ids that block this one (same- or cross-scope)
related: [wc-7x4p, api-3m9k] # soft "see also" project ids; never gates (same- or cross-scope)
tags: [network, cdp]
created: 2026-06-20
updated: 2026-07-01
links: [PR#142, issue#88, branch:network-redesign] # external artefacts only, never project ids
summary: One-line what/why.
---

# Network output redesign

Tasks as checkboxes below...
```

DECISION: `order` is the single sequencing key; there is no separate `priority`.
`pj next` and default `pj list` sort by `(order, id)`. Urgency is expressed by moving
a project earlier with `pj move`, not by a second sort axis. Banded triage, if ever
wanted, returns as a tag or a CUE custom field, not a built-in.

DECISION: `order` is a lexicographic rank key (fractional indexing), not a dense
integer. The value is an opaque string sorted byte-wise. Inserting or moving computes a
new key strictly between the two neighbours (`keyBetween(left, right)`), so a reorder
writes only the moved project's file — no neighbour is renumbered. `pj add` appends with
`keyBetween(last, null)`.

- Ties: two machines offline can compute the same key for the same slot. For reads the
  tie breaks deterministically by id (`(order, id)` sort). The generation side still has
  two equal keys with no value strictly between them, so a later `pj move` into that
  slot would have nothing to write; the `pj sync` integrity step re-spaces equal keys
  within a scope, rewriting only the tied files (`pj doctor` off-sync). This keeps
  `pj move` a single-file write on the hot path and confines tie repair to sync, where
  the tie is created.
- Why not dense integers: no value between 3 and 4, so an insert rewrites every
  displaced project — reintroducing the identity/order coupling the id scheme escaped
  and turning every offline reorder into a conflict source. A rank key keeps `pj move` a
  single-file edit.
- Always quoted (`order: "n"`). A bare key that happens to be `n`, `y`, `no`, `yes`,
  `on`, `off`, `null`, or `~` is coerced by a YAML 1.1 parser; quoting keeps it a
  string. `pj doctor` flags an unquoted/non-string `order`.

Derived, never in frontmatter: task counts, percent done, next runnable project, blocked
count. Materialized in the index, recomputed on reconcile, so they never go stale and
never pollute the source of truth.

## Storage

DECISION: a scope is a directory holding `pj.cue` plus the project `.md` files,
flat. No subdirectory per scope — the directory is the scope.

```
<files-path>/
  pj.cue                          # scope name, schema, sync owner, knownTags
  wc-ab2c-network-output-redesign.md
  wc-9k3m-cdp-session-pool.md
  ...
```

- `pj.cue` (renamed from the old `config.cue`) is namespaced so it cannot
  collide with a repo's own `config.cue` or another tool's, now that the files-path may
  be any directory the user points at, not a pj-dedicated one.
- The files-path is pj-only. Source code never lives in it. It is typically a
  subdirectory of the code it tracks (`<repo>/.agents/pj/`), or a standalone directory
  for personal/cross-cutting work.
- Recommended files-path: `.agents/pj/` (beside other agent tooling) or
  `.agents/projects/`. Not enforced — the user names the path at init.
- A git repo may host several scopes, each rooted at a distinct code-root — a large
  monorepo carries one scope per team/area (`/org/mono/teamA`, `/org/mono/teamB`), and a
  personal pj repo carries several scopes as sibling subdirectories. The only per-repo
  constraint is sync ownership: every scope sharing a repo agrees on its owner (see "Sync
  owner"). The unit is the scope (a files-path), not the repo. (Superseding the earlier
  "one repo = one scope", which was an artefact of welding code-root to the repo root; the
  files-path/code-root decoupling in "Resolution" removes it.)

## Visibility

DECISION: every registered scope is visible from anywhere on the machine. There is no
private/local class. `pj scopes`, cross-scope `pj search`, `pj list --scope`, and
`pj show <scope>-<id>` reach any registered scope. This is the payoff of the flat id
namespace and the machine-wide index: an agent in one repo answers "what is in flight
in wc?" without leaving.

Consequences accepted:
- Scope names must be machine-unique (see "Scope names").
- Registering an existing scope is a deliberate act (`pj import`), never automatic, so
  cloning a repo does not silently pull its scope into the machine-wide view.

## Resolution

DECISION: resolution is a registry lookup, not a filesystem walk. There is no up-scan
for a marker and no blessed default location. A scope becomes known only by `pj init`
or `pj import`, which records it in the machine-local registry.

The registry (see "Registry") records, per scope: its name, its files-path (where the
`.md` and `pj.cue` live), and its code-root (the directory tree under which the
scope is ambient).

Ambient scope (bare `pj list`, `pj next`, `pj add`): the scope whose code-root is the
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
- Cross-scope addressing (`--scope wc`, `pj show wc-ab2c`) is a direct registry lookup
  by name, independent of cwd.

The lookup is cheap (an in-memory prefix match over a handful of registry entries), and
because it is a registry read rather than a stat-walk, it never depends on filesystem
markers or git.

## Registry

DECISION: the registry is machine-local durable state in the XDG user config
(`${XDG_CONFIG_HOME:-~/.config}/pj/config.cue`). It is not synced and never lives in a
repo. Each machine registers its own scopes: the scope's files travel (git clone,
Dropbox), but "this machine knows about this scope, at these paths" is a per-machine
fact.

It is the one thing the index cannot rebuild — a scope's files record their content and
state, not the fact of registration or the code-root binding — so it lives in a real
config file, not the derived index. Drop the index and it rebuilds by walking the
registry; drop the registry and the scopes are simply unknown until re-`init`/`import`.

Shape (CUE):

```cue
scopes: {
    wc: {                                             // single-scope repo, files under root
        files: "/home/grant/projects/webctl/.agents/pj"
        root:  "/home/grant/projects/webctl"
    }
    ta: {                                             // one of several scopes in a monorepo
        files: "/org/mono/teamA/.agents/pj"
        root:  "/org/mono/teamA"
    }
    home: {                                           // standalone, files == root
        files: "/home/grant/notes/home"
        root:  "/home/grant/notes/home"
    }
}
lens: {wc: ["frontend", "style"]}   # machine-local default tag view, per scope
editor: "nvim"
```

Each scope records exactly two paths, and they are independent:
- `files` (files-path): where the `.md` and `pj.cue` physically live; what reconcile
  stats. Must be distinct per scope.
- `root` (code-root): a single path — where the scope is ambient for bare-`pj`
  resolution. Not a list (a scope has one root); `pj use` re-points it. `files` need not
  live under `root` — they are matched in different steps and never interact.

The git repo is not recorded. It is derived on demand from `files`
(`git rev-parse --show-toplevel`), so moving or renaming the repo never staples the
registry; several scopes whose `files` derive the same repo share that repo as their sync
unit. The scope name is cached here for fast `--scope` lookup; the authoritative name is
in each scope's `pj.cue`. `pj doctor` reconciles the two and flags drift (a scope whose
`pj.cue` name no longer matches its registry key, or a registry entry whose files-path is
gone).

## init and import

DECISION: `pj init <files-path> (--name <name> | --auto) [--code-root <path>]
[--owner host|pj|none]` creates a new scope and registers it. `pj import <files-path>
[--code-root <path>]` registers an existing on-disk scope (post-clone), files in place.
They are symmetric entrances to the registered state; init writes a fresh `pj.cue`,
import reads an existing one (name and owner come from it, so import takes neither `--name`
nor `--owner`).

pj is non-interactive — it never prompts. Everything it needs is a flag or a deterministic
default; the only TTY-sensitive behaviour anywhere in pj is colour. So init takes the name
and owner as flags, not prompts.

Name (init only): exactly one of `--name <name>` or `--auto` is required; supplying
neither is an error (the name is never silently defaulted — "always a conscious choice"
survives, and `--auto` is that conscious choice to accept derivation). `--name` is
validated against `^[a-z0-9]{1,12}$`. `--auto` derives from the code-root basename (the
algorithm in "Scope names") and sanitizes to the alphabet; because code-root may now be a
subdirectory, `--auto` reads well for monorepos (`/org/mono/teamA` -> `ta`). A derived
name that is already registered is a hard error naming the clash and telling you to pass
`--name` — never an auto-suffix (the beads junk-name mistake).

The files-path is required (never defaulted). The code-root defaults from git or is given
explicitly, by this matrix — `--code-root` is always allowed (it is what makes several
scopes share a repo), and defaults are just conveniences:

| files-path in a git repo? | `--code-root` given? | result |
|---|---|---|
| yes | no | code-root = the repo root (`git rev-parse --show-toplevel`) — single-scope default |
| yes | yes | code-root = the given path — the sub-scoping case (monorepo team, sibling in a pj repo) |
| no | no | code-root = the files-path — standalone, ambient in its own tree |
| no | yes | code-root = the given path |

Errors teach the fix, e.g.:

```
--code-root /elsewhere is not inside the git repository /foo/bar that holds the
files-path. A code-root is where the scope is ambient; keep it inside the repo, or omit
it to default to the repo root:
  pj init /foo/bar/.agents/pj --name <n> --code-root /foo/bar/teamA
```

Registration checks (both commands):
- Scope-name collision: DECISION hard fail. If the name is already registered, refuse.
  There is no rename-on-import — the name is baked into every id, filename, and
  in-scope reference, so a local rename would diverge from every other clone. Resolving
  a genuine clash is the user's job (rename at the source, before sharing). A same-store
  re-clone (same name, same ids at a new path) is refused too, with guidance to
  `--code-root`/re-point rather than double-register.
- Code-root collision: reject a code-root identical to an existing scope's (see
  "Resolution"). Files-path collision is likewise rejected — two scopes cannot share one
  files-path. Nested code-roots are fine (longest-prefix resolves).
- Owner consistency per repo: every scope sharing a derived git-root has the same owner.
  Git ownership is a property of the branch/remote, not a subdirectory, so one repo cannot
  be part `pj` (pushed by pj) and part `host` (PR-managed). A scope added to a repo that
  already hosts scopes inherits their owner (so `--owner` is optional there and an explicit
  contradicting value errors); the first scope in a repo sets it. A `pj`-synced scope that
  must live beside a `host` tree belongs in its own repo (sibling or submodule), which
  derives a different git-root. The error names the existing owner and points at that fix.
- Malformed `pj.cue` (import only): fail the import cleanly, naming the parse
  error, rather than registering a scope whose schema will not load. This is the one
  place an untrusted scope's config is first read.

`--owner host|pj|none` records the sync owner in `pj.cue` (init only; see "Sync owner").
It is never prompted. It is resolved by inference where the answer is unambiguous, and
required only where it genuinely is not — the "conscious choice" discipline applied
exactly where a real choice exists:
- files-path not in a git repo: default `none` (no VC to drive; unambiguous and safe).
- files-path in a repo that already hosts scopes: inherit their owner (forced by the
  per-repo consistency rule below). An explicit `--owner` that contradicts it is an error.
- first scope in a git repo: `--owner` is required. This is the only ambiguous case —
  `host` (the repo's own git/PR commits) and `pj` (pj syncs a dedicated repo) are both
  common, and a silently-wrong `host` default fails quietly (a `host` scope carries no
  sync warnings), so pj refuses to guess. Omitting it errors with the three choices.

Discoverability without auto-slurping: running a scope-requiring command in a directory
that contains a `pj.cue` for an unregistered scope prints "unimported pj scope
here — run `pj import`" rather than registering it silently.

## Configuration (CUE)

DECISION: config is CUELang (`cuelang.org/go`, pure Go, no cgo). Two tiers, least to
most specific; later overrides earlier:

1. XDG user config `${XDG_CONFIG_HOME:-~/.config}/pj/config.cue` — machine-local: the
   registry, the lens, editor. Optional; pj runs on built-in defaults when absent. (No
   configurable default owner — a new init's owner is derived deterministically, `host`
   in a repo else `none`, so there is nothing to configure.)
2. Scope config `<files-path>/pj.cue` — the scope name, sync owner, custom
   statuses/fields, and the optional controlled tag vocabulary (`knownTags`). This is
   the tier that validates each project's frontmatter.

Env (`PJ_SCOPE`) and flags (`--scope`) override.

Why CUE earns its weight: the custom statuses and fields a scope declares become the
schema `pj doctor` validates every project's frontmatter against. CUE is a typed,
validated schema, not a fancy TOML.

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

DECISION: malformed config is quarantined, not fatal — the same discipline applied to
unparseable project files (the `parse_error` rows in "Invalidation"), so a broken scope
config is a flagged part, not a special case. A `pj.cue` that will not compile degrades
that one scope to built-in-schema validation (the seven built-in statuses only, no custom
statuses/fields/`knownTags`) while every other scope stays healthy — a broken edit cannot
brick machine-wide commands that reconcile multiple scopes (cross-scope `search`/`list`).
The scope stays fully readable (`pj show`/`next`/`list` work against built-ins). The one
consequence to hold: writes to a degraded scope validate against built-ins only, so a
project carrying a custom status won't validate until the `pj.cue` is fixed. So the
degradation must be loud — `pj doctor` reports it prominently and a terse warning rides
the affected scope's reads/writes — rather than silently masking a custom-status typo as
"unknown status". Fix the config and the next command re-evaluates it (cache keyed by the
import closure's `(path, mtime, size)`) and restores full validation.

## Read interface (SQLite index)

DECISION: SQLite is the read/query interface in v1, via `modernc.org/sqlite` (pure Go,
no cgo; FTS5 is compiled in by default). It is a materialized view derived from the
files, not a second source of truth.

DECISION: one machine-wide index at a fixed, machine-local path:

```
${XDG_STATE_HOME:-~/.local/state}/pj/index.db
```

- It lives in XDG state, never inside any scope's files-path, so no version-control or
  filesystem-sync mechanism (git, Dropbox, Syncthing, NFS) ever carries it. The
  "derived, never synced, rebuildable" invariant is true by construction for every
  owner, and WAL always runs on a local disk (WAL is unsafe on a network/synced
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

The decisive reason SQLite earns v1 — beyond the query surface, and the answer to "it
is only tens of files, why not scan in memory?" — is the planned pj viewer: a web-based
project monitor read/written by a second, long-lived process concurrently with the CLI.
An in-memory index rebuilt per invocation cannot back a separate process; a shared,
concurrently-readable on-disk store is required, and that is SQLite + WAL (hence WAL
from day one). A single machine-wide DB is what the viewer wants anyway — one file to
open and watch, one query surface across everything, rather than enumerating scopes and
holding N connections. (Forward note: the viewer, having no per-command boundary, needs
its own change-observation — a file watcher or poll — reintroducing for that process
the watcher the CLI does not need. Viewer-design, deferred, recorded so it is not a
surprise.)

Alternatives rejected: scan-only, and a gob/json snapshot cache. Both serve simple reads
but neither provides FTS5 search, ad-hoc SQL, or `WITH RECURSIVE` dependency/rollup, and
neither can back the concurrent viewer.

### Invalidation and reconcile

DECISION: pj reconciles the index at the start of each command, scoped to what the
command reads (`pj next` in `wc` reconciles only `wc`; a cross-scope query reconciles
all registered scopes it reads). Git-free — reconcile never runs a git subprocess.

Two layers:
1. mtime + size per file. The DB stores each file's nanosecond mtime and size; reconcile
   stats the scope's files-path, reparses only files whose `(mtime, size)` changed,
   deletes rows for files gone from disk, indexes new files. The last-index timestamp is
   stored and any file with `mtime >= that` treated as dirty (git's racy-index rule),
   closing the same-tick hole. A reparse that fails (malformed YAML, leftover git
   conflict markers, an unquoted-`order` coercion) is quarantined, not fatal: reconcile
   writes a minimal error-row — id from the filename prefix, a `parse_error` flag with
   the parser message, `(mtime, size)` recorded so a fix re-indexes it, raw body still
   FTS-indexed. The project stays addressable (`pj show` prints it flagged, `pj doctor`
   lists it, a terse `N unparseable` warning rides affected reads) rather than being
   silently dropped or triggering a scope-wide rebuild loop.
2. Full rebuild. DB missing, failing an integrity check, `schema_version` mismatch, or
   any error during the stat pass -> walk the registry and repopulate every scope from
   its files-path. Layer 1 is the optimization; layer 2 is always safe (derived). A
   per-file parse failure is not a rebuild trigger — it is handled in layer 1.

A registry entry whose files-path is gone (deleted repo, unmounted drive) is reported by
`pj doctor` and its rows are dropped; the scope reappears when the path is reachable
again.

Write-through: a `pj` mutation upserts its own row right after writing the file
(including `pj add`, so a just-scaffolded project is queryable before its body exists).
Direct agent edits are the read-through half, caught by reconcile via mtime.

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

Applies only to scopes with sync owner `pj`. Owner `host` rides the surrounding repo's
own git (human/PR-managed); owner `none` does no syncing.

DECISION: durability and sync split along the commit/push seam.
- Durable-local: a mutating command commits its own change synchronously to the scope's
  repo; a direct agent/human edit is committed at the next `pj sync`. Work is never lost
  and no command blocks on the network.
- Remote sync: happens only in `pj sync`, whose push is synchronous and reported.
  Ordinary commands never push.

### Reads never touch git

DECISION: read commands (`next`/`list`/`show`/`search`/`query`) are git-free. A read
reconciles the index from the files and answers; it does not commit, push, or run any
git subprocess. A direct agent edit is reflected because reconcile stats the files.
Consequence: a cross-machine read can be stale until the next `pj sync` fetch —
acceptable for a single user working mostly one machine at a time.

### Writes commit their own change

DECISION: a mutating command that produces a complete state (`status`/`move`/`archive`)
writes the file, write-throughs its row, then commits just that file (`git add <file>` +
`git commit -m "pj: wc-ab2c -> done"`). Adding the specific path (not `-A`) leaves
unrelated dirty files untouched. Synchronous, tens of milliseconds, no push.

`pj add` is the deliberate exception: it scaffolds frontmatter and returns the path for
the agent to fill the body directly, so it produces an incomplete artifact by design and
does not commit. Writing the skeleton reserves the id; the complete project is committed
at the next `pj sync` snapshot. Principle: self-commit when the verb yields a complete
state; defer to sync when it yields a scaffold to be completed by a direct edit.

Concurrent writes in a scope serialize through a scope-level `flock` on
`<files-path>/.pj.lock`, held for the whole reconcile -> write span. `pj add` takes it too
(without committing) so its draw -> check-local-ids -> write-skeleton is atomic and two
concurrent adds cannot draw the same id. Because a scope's ids and files live under its
own files-path, this per-scope lock is sufficient for the file write and id draw even when
several scopes share a repo.

The git commit is the part that is repo-granular, not scope-granular: `git add`/`commit`
mutate the one shared index of the derived git-root, so for owner `pj` the committing span
additionally takes a git-root lock (`<git-root>/.git/pj-sync.lock`). Two scopes in the
same repo therefore serialize their commits (and `pj sync`'s rebase/push) instead of
corrupting each other's index, while owner `host`/`none` never commit and need only the
per-files-path lock. The locks cannot coordinate every index writer (a read command
reconciles without them, and the viewer is a separate process), so cross-writer
coordination on the index is SQLite's own single-writer WAL lock plus `busy_timeout`, not
the flock.

A mutating command refuses at startup if the scope's repo is mid-rebase (a stat of
`.git/rebase-merge`|`rebase-apply`) — a self-committing write would land on the
rebase's temporary HEAD and corrupt it. It fails fast with `store is mid-sync-conflict
— resolve <file> then run pj sync`. Reads are git-free and unaffected.

### pj sync: the sole push boundary

DECISION: `pj sync` is the "done for now / reconcile now" command and the only place pj
pushes. It targets the ambient scope's repo; `pj sync --all` syncs every owner-`pj`
scope, and with no ambient scope `pj sync` syncs all. Because sync is repo-granular, both
the ambient case and `--all` operate per derived git-root, deduplicated: syncing the
ambient scope syncs its whole repo (every scope sharing it), and `--all` visits each
git-root once rather than re-fetching a shared repo per scope. Ambient-only is deliberate:
it matches the end-of-turn pattern (push where you worked), keeps the hot path to one repo,
and `--all` covers the world when wanted. It is bidirectional by construction — always
fetch, then push only if ahead — because reads are git-free, so sync is the sole point a
machine learns of another's work. Steps:

Caveat, cross-scope freshness: because bare `pj sync` fetches only the ambient scope, a
cross-scope `depends` target living in another owner-`pj` scope is only as fresh as that
scope's last fetch on this machine. Its status can lag until that scope is synced
(`pj sync --all`, or syncing it directly). This is the same "a cross-machine read can be
stale until the next sync" limitation reads already accept — documented here so a
cross-scope gate reading a stale target is a known bound, not a surprise. Not worth
splitting sync into ambient-push/all-fetch in v1.

1. Snapshot: `git status --porcelain` finds every file pj did not just author — direct
   edits, `$EDITOR` edits, filled `add` skeletons, and the scope's own non-project files
   — and commits each, one per file, message derived from its class and porcelain code:
   - A project `.md` (parseable frontmatter with an `id`): `??` -> `pj: add <id> <slug>`,
     modified -> `pj: edit <id>`.
   - A recognised scope file (`pj.cue`, `AGENTS.md`): a fixed message
     (`pj: config <scope>`, `pj: agents <scope>`).
   - Anything else: a generic `pj: sync <path>`, reported, so nothing is silently left
     uncommitted. `pj.cue` must sync so a second machine validates against the
     same schema.
   The index and lock are in XDG state / gitignored and never appear here.
2. Fetch and integrate, unconditionally. Always fetch; if the remote advanced, rebase
   local commits onto it, running the frontmatter merge on any conflicted file. This
   runs whether or not step 1 produced a commit, so a read-only machine still pulls
   others' work. An unresolvable body conflict leaves the store in a paused rebase,
   marked and reported, never discarded — nothing is pushed until it resolves, mutating
   commands refuse meanwhile, and a later `pj sync` resumes the paused rebase.
3. Integrity repair over the merged tree, per scope touched: duplicate ids and tied
   `order` keys — the offline-concurrent artefacts that land at different paths, so the
   rebase merges them clean and no git conflict fires. Sync owns the repair (rename the
   side nothing depends on and rewrite in-scope `depends`/`related`/`links` atomically —
   in-scope reference-safe; cross-scope edges are recorded for `pj doctor` to verify, not
   rewritten; re-space tied `order` keys) rather than deferring to a manual `pj doctor`.
   Both write only the files they touch and commit under a fixed message.
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

Health: `git rev-list --count @{u}..HEAD` gives the unpushed count; a stored last-push
-error marker records failures. `pj doctor` reports both. Before the count is meaningful
there is the precondition pj does not create — the repo itself: for owner `pj`, sync
first checks the scope is a git repo with an upstream (a `.git` stat, then
`git rev-parse --abbrev-ref @{u}`), and if not, reports sync disabled with a
professional warning (`sync is disabled until this scope is a git repository with a
remote; set one up with git, then pj sync`) rather than a raw git error. A terse
warning also rides write commands and `pj sync` and appears in `--json` as
`unpushed`/`push_error`/`sync_disabled`. Reads stay git-free and do not carry it.

### Sync owner

DECISION: each scope declares its sync owner in `pj.cue`. It is a scope property,
identical on every machine (the repo either is or is not the host), so it is synced, not
machine-local.
- `host` — the surrounding code repo commits the files as part of normal work; pj never
  runs git. Requires the files-path to be inside that repo. A single host repo may carry
  many host scopes (a monorepo, one scope per team/area).
- `pj` — a git repo pj syncs local-first (the engine above). May be a standalone
  single-scope repo, or one repo holding several pj scopes as sibling subdirectories; sync
  is repo-granular either way (see below), so one `pj sync` pushes the whole repo.
- `none` — plain files, no version control. Single machine, or cross-machine handled
  externally (Dropbox/Syncthing/NFS). The index still serves reads; only sync is absent.

Owner is a per-repo fact: every scope sharing a derived git-root must declare the same
owner (enforced at init). Because owner `pj` sync operates on the git-root, syncing any
scope in a multi-scope `pj` repo fetches/rebases/pushes that repo once and its snapshot
step commits every scope's dirty files under the one push — the "one push syncs
everything" behaviour a central pj repo wants.

DECISION: pj never creates or manages the git repo — no `git init`/`git remote`/
`git clone`. For an owner-`pj` scope the user creates the repo and its remote with plain
git first, then runs `pj init` inside it, and clones onto other machines themselves
(then `pj import`). pj shells out to git for commit/fetch/push but owns none of the
repo's lifecycle. When the repo or upstream is missing, sync is reported disabled (the
warning above); the file writes still land on disk.

### Git dependency

DECISION: pj shells out to the external `git` binary rather than driving git in-process.
It uses the user's git version, credential helpers, and SSH config for free — including
`ControlMaster` connection reuse — and carries no git library. git is required only for
owner `pj` scopes and only on the write and `pj sync` paths; reads and reconcile are
git-free. This satisfies "pure Go, no cgo" (a subprocess is not cgo).

## Merge conflict handling

Owner `pj` only (where pj drives the rebase). Owner `host` defers to git plus the human
on the PR; owner `none` never merges (external filesystem sync clobbers whole-file).
Four layers, lightest first.

1. Structure to avoid: one file per project means edits to different projects never
   touch the same file. Reordering holds too, because `order` is a rank key — `pj move`
   rewrites only the moved file. There is no registry inside the repo to contend on.
2. Shrink the window: `pj sync` fetches and rebases inline before pushing, so git
   auto-merges non-overlapping text and any conflict surfaces in sync's own output.
3. Semantic merge of frontmatter, by post-rebase stage parsing (not a git merge driver —
   a driver fires on every merge in the repo, including a host PR, and would require the
   pj binary there). pj lets the rebase produce standard conflicts, then for each
   conflicted project file reads the three stages (`git show :1/:2/:3:<f>`), splits each
   into frontmatter and body, and field-merges the frontmatter:
   - List fields (`tags`, `depends`, `related`, `links`): 3-way set merge against the base
     stage — base plus either side's additions, minus either side's removals; an
     add/remove clash keeps. This honours a concurrent removal (a pruned `depends` stays
     pruned, not resurrected).
   - Scalars, neither side terminal: last-writer-wins by git commit timestamp (author
     date). `order` follows this; a tied key resolves at read time by `(order, id)`.
   - Scalar `status` where either side is terminal (`done`/`cancelled`) and the two
     differ: do not auto-merge, do not pick a winner. Route the file into layer 4's
     paused-rebase handoff so a human decides — auto-resolving would silently erase a
     completion or cancellation.
   pj always resolves the frontmatter to clean YAML (never leaves markers in it, so the
   file stays parseable and indexable); the body is layer 4's concern, resolved
   independently within the one file.
   The arbiter is the commit timestamp, not `updated:`. pj maintains `updated:` only on
   its own mutations; a direct agent edit leaves it stale, so judging by it would keep
   the older edit on the common path. Git records a commit timestamp for every change,
   direct or pj-authored, so the arbiter is always present. The merge base is git's
   stage-1, never an in-frontmatter `previous:` snapshot (which would go stale like
   `updated:` and reintroduce dead metadata).
   Residual: for a direct edit the timestamp is the commit time, not the keystroke time,
   so two machines editing the same project offline can invert if their snapshot order
   disagrees with their edit order — the same bounded, single-user, concurrent-offline
   window the id analysis treats as near-never.
4. Surface, never hide: a body (prose) conflict git could not merge, or a
   terminal-status disagreement layer 3 declined, lands here. pj writes the file with
   its frontmatter already field-merged and (for a body conflict) git markers confined
   to the body region, and leaves it unstaged so the rebase stays paused. Nothing is
   pushed, every mutating command refuses while the rebase is in progress (fail fast),
   and the file is reported via `pj doctor` / `pj sync --status`. The human resolves it
   with standard git tooling; the next `pj sync` resumes the rebase (`git rebase
   --continue`) and pushes. Reads stay git-free, so `pj next`/`show`/`search` keep
   working — only mutation is blocked, correct while the base is inconsistent. Because
   the frontmatter was resolved before the file was written, the index can still read
   the project while its body awaits a human.

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
<s> not registered here)` in `pj next`/`pj list` and flagged in `--json`. Held-not-
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

DECISION: both terminal states clear runnability. A `cancelled` dependency satisfies the
gate exactly as `done` — otherwise cancelling one project would permanently strand every
dependent (cancelled never becomes done). Because auto-unblocking a dependent whose
prerequisite was abandoned may be wrong, `pj doctor` flags any project depending on a
`cancelled` project so the human decides whether it still applies.

DECISION: `pj next` diagnoses an empty-because-blocked queue: when no project is runnable
while `todo`s wait on unmet `depends` or a cycle, it reports `nothing ready; N todo(s)
waiting on unmet deps` rather than a bare "nothing ready", so a dependency-blocked scope
is not mistaken for a finished one.

DECISION: CUE custom statuses are additive; the seven built-ins are immutable. Each
custom status declares a `category` (active/wip/backlog/done) so views treat it
correctly without knowing its name (beads' `StatusCategory`, reused).

DECISION: `pj add` defaults new projects to `todo`; `--backlog` captures without
committing. Two project-to-project edge kinds — `depends` (blocks, gates `pj next`) and
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
- The active lens is echoed in human output and `--json`.
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
- Optional `pj archive <id>` physically moves the file into an `_archive/`
  subdirectory of the scope so lookups still resolve. Decluttering only.
- Never delete. The record persists as the still-present file and in git history.

## CLI surface

DECISION: single-purpose CLI named `pj`. Project management only. `--json` on every
command; JSON is the primary agent interface.

- `pj init <files-path> (--name <name> | --auto) [--code-root <path>]
  [--owner host|pj|none]` — create and register a scope. Files-path required; exactly one
  of `--name`/`--auto`; code-root by the matrix (`--code-root` always allowed, defaults to
  repo root in a repo else files-path); owner in `pj.cue`, inferred where unambiguous
  (`none` outside a repo, inherit inside a repo that already has scopes) and required only
  for the first scope in a repo. Never prompts, never runs git.
- `pj import <files-path> [--code-root <path>]` — register an existing on-disk scope,
  files in place; name and owner come from its `pj.cue`. Hard-fails on a scope-name
  collision. Symmetric errors with init.
- `pj use <scope>` — re-point an existing scope's single code-root to cwd (machine-local
  convenience; longest-prefix still resolves, no two scopes may share an identical
  code-root). A scope has one code-root; `use` moves it, it does not accumulate.
- `pj lens [tags...] | --clear` — set/show the machine-local default tag view for the
  resolved scope.
- `pj list [--scope S] [--all] [--no-lens]` — list projects (default: active, resolved
  scope, lens-filtered).
- `pj show <id>` — print a project.
- `pj search <terms> [--scope S]` — full-text search (FTS5), machine-wide by default.
- `pj query <sql>` — read-only SQL over the index. Rejects writes. Schema not a stable
  API; `pj query --schema` prints the current shape.
- `pj next [--no-lens]` — first runnable project by `order` with dependencies satisfied.
  The primary agent entry point (beads' `ready`, renamed). Honours the lens by default
  and diagnoses an empty-because-blocked queue.
- `pj add <title>` — scaffold a project: mint the id, write valid frontmatter, write
  through the index row, print the path for the agent to fill the body. `--json` returns
  `{id, path, content}`. Does not commit (incomplete by design; the skeleton reserves
  the id; committed at the next `pj sync`). `--backlog` captures without committing to
  `todo`. The one create call; every edit after is direct file access.
- `pj status <id> <state>` — set status. A complete-state write: owner `pj` commits the
  one file synchronously (no push); `host`/`none` just write the file.
- `pj edit <id>` — resolve id to path and open in `$EDITOR`, or print the path for an
  agent's own edit tool. The tool locates; it does not intermediate.
- `pj move <id> (--before <id> | --after <id> | --first | --last)` — reorder to an
  explicit slot; the destination flag is required. pj reads the target neighbours from
  the index and writes `keyBetween(left, right)` into the moved file only. No relative
  counters, swap, or batch move (KISS: four flags, each a single-file write).
- `pj scopes` — list every registered scope (all visible).
- `pj sync [--all]` — reconcile now / done-for-now and the sole push boundary (owner
  `pj`). Targets the ambient scope; `--all` (or no ambient scope) syncs every owner-`pj`
  scope. `pj skill` tells agents to run it at end of turn.
- `pj doctor [--reindex]` — report conflicts, same-scope dangling `depends` (hard),
  unresolvable cross-scope `depends`/`related` (informational — scope not registered here
  vs target gone are indistinguishable), cross-scope references whose target was
  collision-repaired (verify — possible silent mispoint), `depends` cycles,
  depends-on-cancelled, registry/config drift, sync state (including a repo/upstream not
  set up), unparseable project files, and index health; runs the id-collision (in-scope
  reference-safe, cross-scope surfaced) and tied-`order` repairs for the off-sync cases.
  `--reindex` forces a full index rebuild from the files.
- `pj archive <id>` — move a done project into `_archive/`.
- `pj skill` — print agent-facing markdown instructions to stdout (see Discovery).

DECISION: no-scope error. When resolution yields no scope, scope-requiring commands
error with guidance (`no scope here — cd under a registered code-root, 'pj use
<scope>', or pass --scope`). Discovery commands (`scopes`, `list --scope`, `search`,
`query`, `init`, `import`, `doctor`, `help`, `skill`) never error on no-scope.

For an agent, `pj next --json` + `pj show <id>` is the whole discovery loop: find the
next thing, read only what is needed, mark it done.

## Discovery

Three mechanisms:
- The CLI auto-resolves the ambient scope from cwd via the registry, so an agent just
  runs `pj` in a registered tree.
- `pj skill` prints the workflow instructions on demand (the pattern beads' `onboard`/
  `prime` and webctl's `help <topic>` use).
- Auto-maintained AGENTS.md block: for an owner-`host` scope, `pj init` writes/updates
  an integration block into the AGENTS.md at the scope's code-root (not the repo root) so
  an agent working in that subtree discovers the workflow via nearest-AGENTS.md-up-the-
  tree — a monorepo's per-team scopes each own the block at their own root. For an owner-
  `pj` scope the block lives in the scope's own AGENTS.md in the files-path (the code repo,
  if any, is left untouched).

## Borrowed from beads

beads got the interface right even though its Dolt storage is overkill here.
- `ready` as the primary verb -> `pj next`.
- Dependency-gating derived rather than a hand-set flag.
- `StatusCategory` -> CUE custom-status categories.
- A `prime`/`onboard` context dump -> `pj skill`.
- Auto-maintained AGENTS.md block.
- One logical operation = one auto-commit, auto-messaged.
- `--json` everywhere.

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
scope, `pj use` code-root count, sync-owner default, malformed `pj.cue` handling) are all
resolved in the doc body and the decisions log.

## Decisions log (locked)

- project = one markdown file; tasks live inside it; the CLI does not model tasks.
- Vocabulary: scope / project / task. store == scope (no separate store container). A
  repo hosts a scope.
- Markdown files are always the source of truth; edited in place; no double handling.
- Per-project metadata in the file's YAML frontmatter; no separate index and no DB as
  truth.
- ids are `<scope>-<short-id>` (random 4-char, human-typeable alphabet, first char a
  letter, even letter/digit split); id canonical in frontmatter, filename mirrors as
  `<id>-<slug>.md`. `pj add` redraws on a local hit (online creation never collides);
  offline-concurrent collisions (no git conflict) are repaired by the `pj sync`
  integrity step (`pj doctor` off-sync): rename the side nothing depends on (inbound
  checked in- and cross-scope) to 5 chars, atomically rewrite in-scope
  `depends`/`related`/`links`; cross-scope edges (another repo, unrewritable here) are not
  touched but recorded, and `pj doctor` flags them to verify against a silent mispoint;
  report.
- `order` is a frontmatter lexicographic rank key (fractional indexing); `pj move`
  writes only the moved file; ties break by id via `(order, id)` for reads, re-spaced by
  the `pj sync` integrity step.
- Scope name `^[a-z0-9]{1,12}$`, machine-unique, never silently defaulted; it is the
  address and id prefix, not a directory name.
- Storage: a scope is a directory of flat `.md` files plus `pj.cue` (renamed from
  `config.cue`, namespaced). The files-path is pj-only, user-chosen at init (recommend
  `.agents/pj/`), never defaulted. A git repo may host several scopes at distinct
  code-roots (monorepo one-scope-per-team; central pj repo with sibling scopes); the unit
  is the scope, not the repo. Every scope sharing a repo agrees on its owner.
- Everything visible: every registered scope is reachable machine-wide; there is no
  private/local class. Registration is deliberate (`pj init`/`pj import`), never
  automatic.
- Resolution is a registry lookup: longest-prefix code-root match for the ambient scope;
  direct name lookup for `--scope`. No up-scan, no filesystem marker, no blessed default
  location. No two scopes share a code-root; nested code-roots resolve by longest prefix.
- Registry is machine-local durable config in the XDG user config, not synced, not in a
  repo. Records per scope: name, files-path, and a single code-root. files-path and
  code-root are independent (files need not sit under root); the git repo is not stored but
  derived from files-path via `git rev-parse`. `pj use` re-points the single code-root, it
  does not accumulate a list. Rebuilding the index walks the registry; losing it means
  scopes are unknown until re-registered.
- pj is non-interactive — never prompts; the only TTY-sensitive behaviour is colour.
- `pj init <files-path> (--name <name> | --auto) [--code-root <path>]
  [--owner host|pj|none]`: `--code-root` always allowed (this is what lets scopes share a
  repo), defaulting to repo root in a repo else the files-path; exactly one of `--name`
  (explicit, `^[a-z0-9]{1,12}$`) or `--auto` (derive from code-root basename, hard error on
  a derived-name collision); `--owner` inferred where unambiguous (`none` outside a repo,
  inherit inside a repo with existing scopes) and required only for the first scope in a
  repo. Owner consistency per derived git-root enforced; files-path/code-root collisions
  rejected, nested code-roots allowed. `pj import` is symmetric for an existing scope
  (name and owner from its `pj.cue`), hard-failing on a scope-name collision.
- Config: two CUE tiers — XDG user config (registry, lens, editor) < scope `pj.cue`
  (name, sync owner, schema, `knownTags`), env/flags override. The
  scope config's CUE evaluation is cached in the index keyed by its import closure's
  `(path, mtime, size)`; the XDG tier is evaluated in-process (it holds the registry
  needed to locate scopes). Malformed config is quarantined not fatal: the affected scope
  degrades to built-in-schema validation (built-ins only) and stays readable while other
  scopes stay healthy; degradation is loud (`pj doctor` + a read/write warning) so a
  custom-status typo is not silently masked; fixing `pj.cue` restores full validation.
- One machine-wide SQLite index at `${XDG_STATE_HOME:-~/.local/state}/pj/index.db`
  (`modernc.org/sqlite`, FTS5): a materialized view derived from the files, in XDG state
  so no VC or filesystem sync ever carries it and WAL always runs on local disk. Rows
  namespaced by scope; cross-scope queries and one-corpus FTS are native. Reconcile at
  command start, scoped to what is read, git-free (mtime+size, then full rebuild walking
  the registry); write-through on pj's own mutations; parse failures quarantined as
  visible `parse_error` rows. Schema change = rebuild, not migration. Query surface:
  `pj search` (FTS5), `pj query` (read-only SQL, schema not a stable API), rich
  `pj list` filters, `WITH RECURSIVE` dependency/rollup. WAL from day one with a
  `busy_timeout` for the concurrent viewer.
- Sync (owner `pj`) split along the commit/push seam; no snapshot machinery on every
  command. Reads git-free. Writes that yield a complete state self-commit one file (no
  push); `pj add` scaffolds without committing. The file write and id draw serialize on a
  per-files-path `flock`; the git commit/rebase/push (repo-granular) additionally serialize
  on a git-root lock, so several `pj` scopes sharing a repo cannot corrupt its index and
  one `pj sync` pushes the whole repo.
  `pj sync` is the sole push boundary: snapshot dirty -> commit each -> unconditional
  fetch + integrate (rebase with inline frontmatter-merge) -> integrity repair -> blocking
  push if ahead -> report. Targets the ambient scope; `--all` for every owner-`pj` scope.
  pj shells out to external `git` (owner `pj`, write/sync paths only); it never creates
  or manages the repo, reporting sync disabled when the repo/upstream is missing.
- Sync owner in `pj.cue` (per scope, synced; consistent across all scopes sharing a
  derived git-root): `host` (surrounding repo commits), `pj` (repo pj syncs, one or many
  scopes, repo-granular), `none` (no VC).
- Frontmatter merge (owner `pj` only): post-rebase stage parsing (`git show :1/:2/:3`),
  not a merge driver. Frontmatter always resolved to clean YAML (stays indexable); body
  clean -> staged, conflicting -> markers, unstaged, paused rebase, human resolves, next
  `pj sync` resumes. Lists 3-way set-merged against the git base; scalars last-writer-
  wins by git commit timestamp (not `updated:`); terminal-state disagreements routed to
  the paused-rebase handoff, not auto-merged.
- Seven flat built-in statuses (backlog, todo, review, in-progress, blocked, done,
  cancelled); labels, not a workflow; built-ins immutable, CUE customs additive with a
  category. `pj add` defaults to `todo` (`--backlog` otherwise). `blocked` manual;
  `depends` a separate runnability filter satisfied by either terminal state.
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
- Done is a filter; `pj archive` is optional decluttering; never delete.
- Single-purpose CLI `pj`; `--json` on every command. No-scope error on scope-requiring
  commands; discovery commands never error on no-scope. `pj skill` prints agent
  instructions; AGENTS.md block auto-maintained (host scope -> code repo, pj scope ->
  files-path).
- Pure Go, no cgo.
