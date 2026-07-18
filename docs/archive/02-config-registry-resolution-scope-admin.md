# P2 Config, registry, resolution and scope admin

## Goal

Make `pj` run and make scopes addressable: a Cobra-based CLI with the design's platform
rules (exit codes, colour/TTY, absolute path hand-off, signal handling, OS guard); the CUE
config engine for the machine-local XDG tier and for each scope's `pj.cue` schema; the
machine-local registry; ambient scope resolution; and the scope-administration verbs
`init`, `import`, `rebind`, `forget`, and `list`. After this project a user can register,
resolve, and address scopes, and every id-taking verb has a working resolution path to
build on.

## Scope

In scope:
- U27 CLI framework and platform: the root command, one-file-per-command Cobra structure
  with `RunE` and `SilenceUsage`/`SilenceErrors` (main formats and exits), the exit-code
  shim, signal handling, the colour/TTY rules, absolute path hand-off, the help tree, and
  the macOS/Linux OS guard.
- U8 CUE engine for the XDG tier: read and write `registry.cue` and `lens.cue` via
  `cuelang.org/go` (load/compile/evaluate; `cue/ast` + `cue/format` for regeneration);
  atomic write (temp in the same directory, then rename); the machine-global flock over all
  XDG-config writes.
- U9 scope `pj.cue` schema: the name/autoCommit/knownTags/statuses/fields shape, its
  validation, the exported `ScopeSchema` value later projects consume, and the
  unparseable-config → scope-read-only behaviour. In this project the evaluation is correct
  but uncached (there is no index yet to cache it in).
- U10 registry model: the per-scope `{dir, root}` records, the XDG paths, and the cached
  scope name (authoritative name lives in `pj.cue`).
- U11 ambient resolution: the `--scope` > `PJ_SCOPE` > longest-prefix code-root precedence,
  the optional `--scope` override plumbing, the no-scope error, and the name-drift
  fail-closed refusal — built as the shared mechanism for P3's ambient verbs and covered by
  unit tests, since no P2 verb consumes it (all ambient project verbs are P3; `pj scope
  list` lists every scope and takes no `--scope`).
- U12 scope verbs `init`, `import`, `rebind`, `forget`, `list` and the registration checks:
  scope-name collision, code-root collision, dir disjointness, and autoCommit consistency
  per derived git-root.

Out of scope (named siblings own these):
- `pj scope rename` — P5 (it needs the `edges` table and shares the multi-file in-scope
  rewrite / `edge_verify:` machinery). Register `rename` in the command tree only if it can
  hard-refuse cleanly; do not implement the rewrite here.
- The SQLite index and any config-evaluation cache — P3. In this project `pj.cue`
  evaluation is correct but re-evaluated per command; do not build a cache in an index that
  does not exist yet.
- `pj scope forget` dropping index rows — P3. Here `forget` drops the registry and lens
  entries only; state that explicitly. Index rows for forgotten scopes are pruned by P3
  reconcile/rebuild once the index exists.
- All read/query verbs (`list` for projects, `next`, `get`, `meta`, `deps`, `search`,
  `query`) — P3. This project ships `pj scope list`, not project `pj list`.
- Complete-state write verbs, self-commit, and the full git subprocess wrapper — P4. This
  project shells `git rev-parse` only, to derive a code-root and a git-root for the
  registration checks; it never commits. A `git rev-parse` that fails because the dir is not
  in a repo, or because `git` is absent from `PATH`, is treated uniformly as "no git-root"
  (design's Git dependency rule) — the same derivation and failure semantics at every call
  site. Under it, `init`'s code-root matrix defaults to the dir (plain-files), the git-root
  autoCommit-consistency check (§17) finds no shared git-root, and `pj scope list` labels the
  scope `plain-files` — `unknown` stays reserved for its §16 cases (autoCommit unreadable or
  dir gone). Neither condition is a hard error on its own.
- `pj doctor` — P3/P5. This project may emit the resolution/registration tokens
  (`name_drift:`, `config_unparseable:`, `auto_commit_mismatch:`) where those conditions
  are produced, using the exact token strings; doctor's report surface is a later project.

## Current State

P1 is complete. It provides, as pure I/O-free packages: the id predicates
(`IsScopeName`/`IsShortID`/`IsFullProjectID`) and short-id mint/extension; `slugify`; the
`order` grammar and `keyBetween`; the frontmatter model with its raw fence-slice API; the
status set and terminal predicate (the predicate takes custom-status categories as input);
title extraction; and the scope auto-name derivation function (pure over a basename string).
The Go module, standard layout, `.golangci.yml`, and the `AGENTS.md` baseline (build/test/
lint commands and the Go-CLI-guide advisory note) exist. The repo is otherwise greenfield —
no CLI runs yet, no config engine, no registry, no index, no git integration.

`design.md` is the source of truth. The two CUE config tiers are independent concerns, not
an override stack: the XDG tier records which scopes are registered on this machine and the
per-scope lens; the scope `pj.cue` tier holds the scope's name, autoCommit, and schema.
Ambient scope resolution is a separate precedence chain, not a third config tier.

## References

- `design.md` — read these sections:
  - Registry — XDG location, the `registry.cue` / `lens.cue` shape, machine-global flock,
    atomic CUE writes, the cached-name-vs-authoritative-`pj.cue`-name rule, and the
    name-drift fail-closed DECISION.
  - Resolution — the ambient precedence chain, longest-prefix code-root, optional `--scope`
    on ambient verbs (and doctor's exception), and cross-scope full-id lookup.
  - Configuration (CUE) and Scope `pj.cue` shape — the two-tier model, CUE-only I/O via
    `cuelang.org/go`, the `pj.cue` schema (name/autoCommit/knownTags/statuses/fields),
    validation rules, and the unparseable-`pj.cue` → scope-read-only DECISION (plus the
    sync-preflight coupling note, which P6 enforces).
  - Scope lifecycle — `init`/`import`/`rebind`/`forget` shapes and rules, the init
    code-root default matrix, the registration checks (name collision, code-root collision,
    dir disjointness, autoCommit consistency), the colour/TTY DECISION, and the multi-file
    rewrite durability note (for context; `rename` itself is P5).
  - Scope names — the `--auto-name` procedure and the derived-name-already-registered hard
    error (the derivation function is P1's; the registration collision check is this
    project's).
  - Visibility — every registered scope is visible from anywhere.
  - CLI surface — the scope verbs' stdout/behaviour, `pj scope list` TSV shape, exit codes,
    path hand-off, the no-scope error, and the colour/TTY purity rules.
  - Platform and portability — macOS/Linux only, POSIX flock (machine-local), XDG state on
    local disk, external git binary.
  - Git dependency — the external `git` binary is shelled out; when `git` is absent from
    `PATH`, treat it like a missing git-root (the derivation rule this project applies to
    `git rev-parse`).
- `AGENTS.md` — pure Go no cgo; CUE via `cuelang.org/go`; external git binary.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Adopt: standard layout; Cobra with
  one-file-per-command, `RunE`, `SilenceUsage`/`SilenceErrors`; `ExitError` + the design's
  minimal exit mapping; XDG via env-var helpers (not `os.UserConfigDir`); `syscall.Flock`;
  `golang.org/x/term` for TTY detection and `github.com/fatih/color` constrained to the
  design's colour rules; interfaces at the consumer; table-driven tests with `t.TempDir` /
  `t.Setenv`. Advisory and subordinate to `design.md` — see Constraints for the overrides.

## Requirements

1. U27 — a Cobra root command with one file per subcommand, `RunE` handlers, and
   `SilenceUsage`/`SilenceErrors` set so `cmd/pj/main.go` is the sole place that formats
   errors and calls `os.Exit`. Provide an `ExitError` type carrying an exit code so handlers
   return errors and main maps them. Cobra also produces its own usage errors — missing
   required flag, unknown flag, unknown subcommand, `Args` validator failures — before any
   `RunE` runs; with `SilenceErrors`/`SilenceUsage` these return from `Execute()` as ordinary
   errors, not `ExitError`, so a naive "`ExitError` → its code, else 1" map would send them to
   exit 1. Route them to exit 2 at the source: install `SetFlagErrorFunc` and use `Args`
   validators that wrap flag- and argument-parse failures into `ExitError{2}`, so the
   `ExitError` map stays the single source of exit codes and "missing required flag → 2" (§2)
   holds by construction rather than by each handler re-checking.
2. U27 — exit codes per the design's minimal map: `0` success; `2` for usage / bad CLI
   input (unknown status name, malformed project id — a `-`-containing token failing
   `IsFullProjectID` or a short form failing `IsShortID` shape, empty create title, missing
   required flags); unknown-but-well-formed project id is generic non-zero (conventionally
   `1`), not `2`. No broader multi-code map. The machine signal is the closed stderr token
   set, not exit codes.
3. U27 — colour/TTY rules: default auto (colour only when the target stream is a TTY and
   colour is not disabled); honour `NO_COLOR` (any value, presence alone) to suppress all
   ANSI; no `--color` flag and no `FORCE_COLOR` in v1; stdout (paths, all TSV) is never
   ANSI even on a TTY; closed token prefixes are emitted as literal ASCII at line start and
   never wrapped, coloured, or interrupted by ANSI. Any human-stderr colour applies only
   after the token and its trailing space.
4. U27 — signal handling: a signal goroutine installed by `Execute()` plus the exit-code
   shim in `cmd/pj/main.go` translate interrupts to exit `128+signum` (SIGINT `130`,
   SIGTERM `143`). This applies to every command; it is POSIX convention, orthogonal to the
   error map, and matters most later for verbs that block on the git subprocess.
5. U27 — absolute path hand-off: provide the single helper every path-printing verb uses to
   emit a cleaned absolute path (via `filepath.Abs`/equivalent; no cwd-relative forms, no
   `~`). No `--relative` flag.
6. U27 — the macOS/Linux OS guard: on an unsupported OS, a clear startup error is preferred
   over silent half-behaviour; document the supported-OS limit in README and help.
7. U8 — the XDG CUE engine: read and write `registry.cue` and `lens.cue` under
   `${XDG_CONFIG_HOME:-~/.config}/pj/` exclusively through `cuelang.org/go` (no
   string-templated CUE, no JSON/YAML round-trip of these paths). Writes regenerate the
   whole owned file via `cue/ast` + `cue/format`, write a temp file in the same directory,
   and atomically rename. All XDG-config writes serialize under one machine-global flock at
   `${XDG_CONFIG_HOME:-~/.config}/pj/.pj.lock`. The flock spans the whole read-modify-write
   cycle, not just the rename: a mutating scope verb acquires the flock first, then loads the
   registry, runs the §17 registration checks against that just-loaded state, regenerates the
   owned file, atomically renames, and only then releases. The checks and the write are one
   critical section — never a check-then-lock-then-write split. Because each write regenerates
   the whole owned file from the model loaded inside the lock, a check that ran before the
   flock (or a load that predates it) would let two concurrent `init`/`import` runs both pass
   a collision check and the second clobber the first's registration; holding the flock across
   load, validate, and write is what makes the §17 invariants and the whole-file regeneration
   concurrency-safe. Instantiate `cuecontext.New()` once per
   process. An XDG CUE file that will not parse is a hard error naming the file — the
   registry is the bootstrap that locates every scope, so there is nothing to degrade to.
8. U10 — the registry model: per scope, `dir` (where the `.md` and `pj.cue` live; distinct
   per scope) and `root` (the single code-root where the scope is ambient), both stored as
   absolute paths, plus the cached scope name (authoritative name is in `pj.cue`). The git
   repo is not stored — it is derived on demand from `dir`. `lens.cue` holds the per-scope
   machine-local lens keyed by scope name (the lens data model; the `pj lens` verb and lens
   application are P3).
9. U9 — the scope `pj.cue` schema and its evaluation: parse and validate name
   (`^[a-z0-9]{1,12}$`, authoritative), `autoCommit` (required bool), optional `knownTags`,
   optional additive `statuses` (each name `^[a-z][a-z0-9-]{0,31}$` with a `category` of
   `active`/`backlog`/`done`; must not redeclare a built-in), and optional `fields` (each
   name `^[a-z][a-z0-9_]{0,31}$`, closed type set `string`/`int`/`bool`/`strings`, optional
   `values` enum legal only when `type` is `string`/`strings`; must not shadow a built-in
   frontmatter key). Expose the evaluated result as a `ScopeSchema` value that later projects
   (index materialization, merge typing, list membership) consume. Redeclaring a built-in
   status, shadowing a built-in frontmatter key, a custom status name colliding with a
   built-in, a status or field name outside its alphabet, or a `values` enum on an
   `int`/`bool` field are hard config errors at evaluate time. A `pj.cue` that will not
   compile as CUE and one that compiles but fails any of these schema checks are the same
   class of failure — there is no distinction downstream between "syntactically unparseable"
   and "semantically invalid": both put the scope in one read-only/unusable state (writes
   refuse) while reads stay available, and both ride `config_unparseable:`. `ScopeSchema`
   exposes a single "config usable?" signal for consumers (P3 index materialization, P6 merge
   typing) to branch on; it never asks callers to tell a parse failure apart from a schema
   violation. P2 ships no project read or write verb, so this read-only behaviour has no P2
   command that drives write-refusal (just as the resolver has no P2 verb) — prove it at the
   `ScopeSchema` evaluation layer with unit tests (evaluation yields the read-only/unusable
   state and the `config_unparseable:` token for both an uncompilable file and a
   schema-violating one), and observe it end to end only through `pj scope list` riding the
   token. Do not pull a P3/P4 write verb forward to exercise it.
10. U11 — ambient resolution with the `--scope` > `PJ_SCOPE` > longest-prefix-code-root
    precedence, built as the shared mechanism the later project verbs consume. No verb
    shipped in this project is an ambient project verb — every ambient verb (`list`,
    `next`, `get`, `meta`, `deps`) is P3, and `pj scope list` enumerates all registered
    scopes and takes no `--scope` (design closes `--scope` off the `scope *` family) — so
    P2 has no command that resolves ambiently or by full id. Land the resolver (the
    precedence chain, the optional `--scope <name>` override plumbing, and the no-scope
    error that fails registry-only — never probing the tree for an unregistered `pj.cue`,
    while discovery commands still run) and cover it with unit tests; do not surface it on
    `pj scope list` or pull a P3 verb forward to exercise it. An explicit override
    (`--scope`/`PJ_SCOPE`) that names a scope with no registry entry hard-errors with an
    unknown-scope message — it never falls through to the code-root tier. A named override is
    a deliberate target, so a miss is a mistake, not a request to auto-resolve; this fails
    closed like every other resolver refusal (registry-only, no tree probe, and the §11
    drift hard-error for an override landing on an unusable entry). Fall-through to
    longest-prefix code-root applies only when no override is set.
11. U11 — name-drift fail-closed: when a registered scope's registry key and the on-disk
    `pj.cue` `name` for that `dir` disagree, that scope is unusable until deliberate
    re-registration. Hard-error every command that would use it (ambient code-root hit,
    `--scope`/`PJ_SCOPE` naming the key, any id resolution landing in that dir, including a
    full id whose prefix is the new name). The error names both names, the dir, and the
    exact `pj scope forget <old> && pj scope import <dir> [--code-root …]` recovery, with the
    stable `name_drift:` token. Never auto-rekey. Recovery/diagnosis commands (`scope list`,
    `scope forget`, `scope import`, `skill`, `help`) still work.
12. U12 — `pj scope init <dir> (--name <name> | --auto-name) [--code-root <path>]
    [--auto-commit]`: create and register a scope, writing a minimal valid `pj.cue`
    (name + autoCommit) and a `.gitignore` covering `.pj.lock`. Exactly one of
    `--name`/`--auto-name` is required (never silently defaulted); `--name` validates against
    the full scope alphabet. The code-root
    follows the design's default matrix (`--code-root` always allowed; defaults to the
    derived git repo root inside git, else the dir). When the dir is inside a git repo an
    explicit `--code-root` must resolve inside that same repo; a path outside it is a hard
    error whose message teaches the fix (keep it inside the repo, or omit to default to the
    repo root). Name resolution runs after the code-root is resolved: `--auto-name` feeds
    P1's derivation the basename of the resolved code-root (the last path element of the
    flag-given or matrix-defaulted code-root — never the dir basename, so
    `/org/mono/teamA/.agents/pj` with `--code-root /org/mono/teamA` derives from `teamA` →
    `ta`), and hard-errors when the derivation yields empty/illegal or when the derived name
    is already registered. `--auto-commit` writes `autoCommit:
    true`, omitted writes `false` (or inherits siblings' value when the repo already hosts
    scopes; an explicit contradicting flag errors). Never prompts; never runs `git init` or
    creates a remote. Init authors a fresh scope: if `<dir>` already contains a `pj.cue`, it
    hard-errors rather than regenerating (overwriting) that file, and the message points at
    `pj scope import <dir>` (adopt the existing scope) or choosing a different dir. This is a
    pre-write `stat`, separate from the registered-scope registration checks in §17, which
    cannot see an unregistered on-disk scope.
13. U12 — `pj scope import <dir> [--code-root <path>]`: register an existing on-disk scope,
    files in place; name and autoCommit come from its `pj.cue` (no `--name`/`--auto-commit`).
    Hard-fail on a scope-name collision or an unusable `pj.cue` — one that will not compile
    or that compiles but fails schema validation (§9) — refusing registration rather than
    admitting a scope whose `ScopeSchema` is born read-only (this is the one place an
    untrusted scope's config is first read). Import also runs the §17 autoCommit-consistency
    check: because import has no `--auto-commit` flag and cannot inherit — the value is fixed
    in the on-disk `pj.cue` — an imported `autoCommit` that disagrees with an existing sibling
    sharing its derived git-root is a hard fail with `auto_commit_mismatch:` naming both
    values, not a silent admit. A disagreement is a refusal at registration, so the mixed-repo
    violation surfaces here rather than deferring to `pj sync`'s later whole-git-root refusal.
14. U12 — `pj scope rebind <dir> --name <name> [--code-root <path>]`: rewrite the
    machine-local registry paths for an already-registered scope. `<dir>` always updates the
    registry `dir` (absolute); `--name` selects the entry (required; unknown name errors);
    `--code-root` updates `root` when set and leaves it unchanged when omitted (do not re-run
    the init code-root defaults on a dir-only move). Validate the post-rebind paths (new
    `dir` opens and contains a `pj.cue` whose name equals `--name` and the key; code-root
    collision; dir disjointness). Preserve the lens (same registry key). Not implemented as
    forget+import; not name repair (`name_drift:` still needs forget+import). Idempotent.
15. U12 — `pj scope forget <name>`: unregister a scope by dropping its registry and lens
    entries. It never touches the scope's files or repo. In this project it drops registry
    and lens only; dropping index rows is P3 (state this in the verb's help/behaviour so the
    boundary is legible). A merely unreachable dir stays registered.
16. U12 — `pj scope list` (bare `pj scope` and the alias `pj scopes` run it): parse-stable
    TSV, one line per registered scope sorted by name ascending:
    `<name>\t<dir>\t<root>\t<mode>`. `name`/`dir`/`root` are pure registry reads (cleaned
    absolute paths). `mode` stats each dir and derives its git-root: `pj-driven`
    (autoCommit true), `repo-driven` (false, dir in git), `plain-files` (false, dir outside
    git), or `unknown` (autoCommit untrusted — the `pj.cue` is absent, will not compile, or
    compiles but fails schema validation (§9) — or dir gone) so one bad scope never fails the
    listing. A drifted scope (registry key ≠ on-disk `pj.cue` name) still lists — its `mode`
    derives from the readable `pj.cue` — and rides `name_drift:` on stderr; a gone dir rides
    `unreachable_scope:`; a `pj.cue` that will not compile or fails schema validation (§9)
    rides `config_unparseable:` and lists as `unknown`. Empty registry
    → exit 0, empty stdout. These soft tokens ride stderr without wrapping or interleaving
    the TSV.
17. U12 — registration checks shared by `init`/`import` (and the relevant ones by `rebind`):
    scope-name collision is a hard fail (no rename-on-import; remedy is `pj scope rename` at
    the source, which is P5); code-root collision rejected (nested roots fine, identical
    rejected); dir disjointness (a dir identical to, nested within, or containing any
    registered scope's dir is rejected — dirs must be mutually disjoint, unlike nested
    code-roots); and autoCommit consistency per derived git-root (every scope sharing a
    git-root agrees on `autoCommit`). That invariant fails one of two ways at registration,
    both emitting `auto_commit_mismatch:` naming the existing and offered values: on `init`, a
    new scope inherits siblings when `--auto-commit` is omitted, and an explicit flag that
    contradicts siblings errors; on `import`, there is no flag and no inheritance — the fixed
    on-disk `autoCommit` must already agree with siblings, and a disagreeing value errors. The
    flag-contradiction wording is the init-only ergonomic sub-case; the underlying rule is
    uniform — every scope sharing the git-root must agree, however its `autoCommit` was set.
    The autoCommit check re-derives the git-root at runtime (never stored).
    When a scope sharing the derived git-root has an unusable `pj.cue` — one that will not
    compile or that fails schema validation (§9), so its `autoCommit` cannot be trusted — the
    registration refuses rather than proceeding: an untrusted autoCommit is the same class of
    failure as a disagreeing one (a config declared unusable supplies no safe value to
    assume, even when the `autoCommit` line itself is syntactically present), matching
    `pj sync`'s repo-granular preflight. The refusal emits `config_unparseable:` naming the
    broken sibling's `dir`. An unreachable-dir sibling is not
    this case: its git-root cannot be derived, so it does not share the root and drops out of
    the check under the uniform no-git-root rule. Implement this as the single reusable
    per-git-root evaluation P4/P5 also call, so the register-time and sync-time rules never
    fork. These checks read the registry, so they run inside the same held flock as the write
    that follows (§7): load, validate against the just-loaded state, regenerate, and rename are
    one critical section, or two concurrent registrations can each pass a check the other's
    write then invalidates.

## Constraints

- Pure Go, no cgo: CUE via `cuelang.org/go`; the external `git` binary is shelled out only
  for `git rev-parse` here (code-root and git-root derivation). Never a cgo SQLite driver;
  no SQLite in this project at all.
- CUE-only I/O for both config tiers: load/compile/evaluate and `cue/ast` + `cue/format`
  for writes; no string-built CUE, no `fmt.Fprintf` of CUE syntax, no JSON/YAML round-trip
  of these paths, no second hand-rolled parser. Malformed CUE surfaces as CUE errors, never
  half-parsed by a fallback.
- macOS and Linux only. POSIX `syscall.Flock` (machine-local, does not coordinate across
  synced/network filesystems). No Windows substitutes, no `gofrs/flock`.
- XDG config via env-var helpers (`${XDG_CONFIG_HOME:-~/.config}/pj/`), not
  `os.UserConfigDir` (it returns the wrong path on macOS).
- `design.md` overrides the Go CLI design guide. Carry these into this project — do not let
  an implementer apply the guide's defaults:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | `--json` + JSON envelope on every command | No `--json`, no envelope. stdout is a path or closed TSV; diagnostics + closed tokens on stderr |
  | `--color=auto\|always\|never`; honour `FORCE_COLOR`/`CLICOLOR_FORCE` | No `--color` flag in v1; honour `NO_COLOR` only; never `FORCE_COLOR`; stdout (TSV/path) never ANSI; token prefixes never coloured |
  | Rich exit map (3/4/5/75/78) + `ErrorPayload.Code` namespace | Minimal: `0` ok; `2` usage / malformed id / unknown status; unknown-but-well-formed id = generic non-zero (1); no broader map. Machine signal is the closed stderr token set |
  | Aliases (get→view/show, create→add/new, delete→rm) | v1 aliases only `scopes`→`scope` and `depends`→`deps`; `add`/`show`/`move` deferred |
  | Async jobs ledger, API client, secrets, profiles | Not applicable — no network API beyond git; no secrets; registry+lens replace profiles; the two CUE tiers are independent, not a flag>env>profile>config override chain |
  | Agent help via `<cli> help agents` + embedded markdown | `pj skill` is the agent contract; `pj help` stays ordinary Cobra help |
  | Interactive prompt mode | pj is strictly non-interactive; never prompts |

- Signal handling from the guide is adopted where the design is silent: exit `128+signum`
  (SIGINT `130`, SIGTERM `143`), via the root command's signal goroutine and the
  `cmd/pj/main.go` exit shim.

## Implementation Plan

1. Stand up the CLI framework (U27): root command, `cmd/pj/main.go` exit shim, `ExitError`
   and the exit-code map, signal handling, colour/TTY helpers, the absolute-path hand-off
   helper, the help tree, and the OS guard. Confirm `pj help` and an unsupported-OS startup
   error behave correctly.
2. Build the XDG CUE engine (U8) and the registry model (U10): load/evaluate the XDG
   package, the machine-global flock, atomic regeneration of `registry.cue` and `lens.cue`,
   and the hard-error-on-unparseable-XDG rule. Instantiate `cuecontext.New()` once per
   process.
3. Implement the `pj.cue` schema evaluation (U9) exposing `ScopeSchema`, with the full
   validation matrix and the unusable-config → scope-read-only behaviour (uncompilable or
   schema-violating, one state, `config_unparseable:`).
   Evaluate correctly per command; do not cache (no index yet).
4. Implement ambient resolution (U11): the precedence chain, optional `--scope`, the
   no-scope error, and the name-drift fail-closed refusal (`name_drift:`).
5. Implement the scope verbs (U12) in dependency order — `init` and `import` first (with the
   registration checks and the git-root autoCommit consistency preflight), then `rebind`,
   `forget` (registry+lens only), and `list`. Register `rename` in the command tree as a
   placeholder that hard-refuses (its rewrite is P5) so the help tree is complete.
6. Verify end to end: register a scope, hit each registration-check failure, and confirm
   `pj scope list` mode labelling across pj-driven, repo-driven, plain-files, and unknown,
   including a drifted scope still listing while riding `name_drift:` on stderr. The
   resolver (precedence chain, `--scope`/`PJ_SCOPE`/code-root, no-scope error, name-drift
   fail-closed) has no P2 verb to drive it — cover it with unit tests, not an end-to-end
   command.

## Implementation Guidance

- Keep the `ScopeSchema` value shape deliberate: it is consumed by P3 (index
  materialization and list membership) and P6 (merge typing). Model it so a later project
  can ask "is this status known / terminal", "what type is this custom field", and "what is
  the autoCommit value" without re-reading `pj.cue`.
- The git-root autoCommit-consistency check is not init-only in effect — the git-root is
  derived at runtime. Implement the check as a reusable per-git-root evaluation so P4
  (write preflight context) and P5/P6 (sync/doctor preflights) can call the same logic
  rather than forking it.
- Emit `name_drift:`, `config_unparseable:`, `auto_commit_mismatch:`, and (from `pj scope
  list`) `unreachable_scope:` with the exact token strings from the design's closed token
  table (owned as doctor's contract in P5). Do not invent local variants; these same
  conditions are re-surfaced by doctor later.
- Exit-code coverage: P2 ships no id-taking project verb (`get`/`meta`/… land in P3), so the
  malformed-id `exit 2` vs unknown-well-formed-id generic-non-zero split is exercised end to
  end only in P3. In P2, land the shared exit map / `ExitError` and the shared id-parse
  helper (over P1's `IsFullProjectID`/`IsShortID`) and cover the classification with unit
  tests; the CLI-level `exit 2` paths that P2 does exercise are usage errors — missing
  required flags and a malformed `--name` (fails `IsScopeName`). The missing-required-flag
   path is a Cobra-generated error, not an `ExitError`; §1's `SetFlagErrorFunc`/`Args`-validator
   wrapping is what carries it to exit 2, so cover it with a test that asserts the code (a
   naive main map would ship it as `1`). An unknown-but-well-formed
  scope name to `rebind` is not `exit 2`: it is generic non-zero (conventionally `1`), the
  same split the design fixes for ids (malformed selector → `2`; well-formed-but-not-found →
  `1`). Keep the exit contract uniform — `2` means malformed input, `1` means a well-formed
  target that does not resolve — across both project ids and scope names.

## Acceptance Criteria

- `pj help` prints the command tree including the `pj scope` family; `pj scopes` aliases
  `pj scope`; bare `pj scope` runs `list`.
- On an unsupported OS, `pj` exits with a clear startup error rather than half-running.
- A usage error and a malformed id exit `2`; an unknown-but-well-formed id exits generic
  non-zero (not `2`); `NO_COLOR` suppresses ANSI on both streams; no path or TSV line ever
  carries ANSI, and a token prefix is never coloured.
- Interrupting a command exits `130` (SIGINT) / `143` (SIGTERM).
- `pj scope init` writes a minimal valid `pj.cue` and a `.pj.lock` `.gitignore`, requires
  exactly one of `--name`/`--auto-name`, applies the code-root default matrix, and never
  prompts or runs `git init`. `--auto-name` on `web-control` derives `wc`; on an
  underivable basename it hard-errors telling the user to pass `--name`. When the dir is in
  a git repo, an explicit `--code-root` outside that repo is a hard error that teaches the
  fix.
- `pj scope import` reads name and autoCommit from `pj.cue`, and hard-fails on a scope-name
  collision, on a malformed `pj.cue`, and on an imported `autoCommit` that disagrees with an
  existing sibling sharing its git-root (`auto_commit_mismatch:`; import cannot inherit).
- Registration rejects a duplicate scope name, an identical code-root, and a dir that is
  identical to / nested in / containing an existing scope's dir; it accepts nested
  code-roots. Adding a scope to a repo that already hosts scopes inherits their autoCommit,
  and an explicit contradicting `--auto-commit` errors with `auto_commit_mismatch:`.
- `pj scope rebind` updates `dir` (and `root` only when `--code-root` is given), preserves
  the lens, is idempotent, and refuses a wrong tree (post-rebind `pj.cue` name ≠ `--name`).
- `pj scope forget` removes the registry and lens entries and leaves the scope's files
  untouched.
- `pj scope list` prints sorted `name\tdir\troot\tmode` TSV with `mode` correctly labelled
  across pj-driven / repo-driven / plain-files / unknown, and an empty registry exits 0 with
  empty stdout.
- The resolver, unit-tested (no P2 verb drives it), follows `--scope` > `PJ_SCOPE` >
  longest-prefix code-root, fails registry-only with no tree probe when no scope resolves,
  hard-errors (unknown scope, no fall-through) when an explicit `--scope`/`PJ_SCOPE` override
  names an unregistered scope, and hard-errors with `name_drift:` and the exact forget+import
  recovery line for a drifted scope on any using path — while
  `scope list`/`forget`/`import`/`skill`/`help` still run.
  In P2 a drifted scope is observable only through `pj scope list`, which lists it and rides
  `name_drift:` on stderr.
- A `pj.cue` that will not compile or that compiles but fails schema validation (redeclared
  built-in status, shadowed built-in key, name outside its alphabet, `values` enum on
  `int`/`bool`) puts the scope in one read-only/unusable state: it refuses that scope's writes
  with `config_unparseable:` and lists as `unknown`, while its reads and sibling scopes stay
  available — verified at the `ScopeSchema` evaluation layer by unit tests (no P2 verb drives
  write-refusal), and observed end to end only via `pj scope list` riding the token.
