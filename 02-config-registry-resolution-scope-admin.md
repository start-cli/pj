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
  the optional `--scope` surface on ambient verbs, the no-scope error, and the name-drift
  fail-closed refusal.
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
  registration checks; it never commits.
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
   return errors and main maps them.
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
   `${XDG_CONFIG_HOME:-~/.config}/pj/.pj.lock`. Instantiate `cuecontext.New()` once per
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
   optional additive `statuses` (each with a `category` of `active`/`backlog`/`done`; must
   not redeclare a built-in), and optional `fields` (closed type set
   `string`/`int`/`bool`/`strings`, optional `values` enum for string kinds; must not shadow
   a built-in frontmatter key). Expose the evaluated result as a `ScopeSchema` value that
   later projects (index materialization, merge typing, list membership) consume. Redeclaring
   a built-in status, shadowing a built-in frontmatter key, a custom status name colliding
   with a built-in, or a field name outside the alphabet are hard config errors at evaluate
   time. A malformed `pj.cue` makes its scope read-only (writes refuse) while reads stay
   available; emit `config_unparseable:`.
10. U11 — ambient resolution with the `--scope` > `PJ_SCOPE` > longest-prefix-code-root
    precedence. Wire the optional `--scope <name>` override onto the ambient verbs the
    design lists (here that surfaces on `pj scope list` behaviour and is plumbed as the
    shared mechanism the later project verbs use). When no scope resolves, scope-requiring
    commands fail with the design's no-scope guidance (registry only — never probe the tree
    for an unregistered `pj.cue`); discovery commands still run.
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
    the full scope alphabet; `--auto-name` runs P1's derivation and hard-errors when it
    yields empty/illegal or when the derived name is already registered. The code-root
    follows the design's default matrix (`--code-root` always allowed; defaults to the
    derived git repo root inside git, else the dir). `--auto-commit` writes `autoCommit:
    true`, omitted writes `false` (or inherits siblings' value when the repo already hosts
    scopes; an explicit contradicting flag errors). Never prompts; never runs `git init` or
    creates a remote.
13. U12 — `pj scope import <dir> [--code-root <path>]`: register an existing on-disk scope,
    files in place; name and autoCommit come from its `pj.cue` (no `--name`/`--auto-commit`).
    Hard-fail on a scope-name collision or a malformed `pj.cue` (this is the one place an
    untrusted scope's config is first read).
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
    git), or `unknown` (autoCommit unreadable or dir gone) so one bad scope never fails the
    listing. Empty registry → exit 0, empty stdout. Soft tokens ride stderr without
    interleaving the TSV.
17. U12 — registration checks shared by `init`/`import` (and the relevant ones by `rebind`):
    scope-name collision is a hard fail (no rename-on-import; remedy is `pj scope rename` at
    the source, which is P5); code-root collision rejected (nested roots fine, identical
    rejected); dir disjointness (a dir identical to, nested within, or containing any
    registered scope's dir is rejected — dirs must be mutually disjoint, unlike nested
    code-roots); and autoCommit consistency per derived git-root (every scope sharing a
    git-root agrees on `autoCommit`; a new scope inherits siblings; a contradicting explicit
    flag errors). The autoCommit check re-derives the git-root at runtime (never stored).

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
   validation matrix and the unparseable → scope-read-only behaviour (`config_unparseable:`).
   Evaluate correctly per command; do not cache (no index yet).
4. Implement ambient resolution (U11): the precedence chain, optional `--scope`, the
   no-scope error, and the name-drift fail-closed refusal (`name_drift:`).
5. Implement the scope verbs (U12) in dependency order — `init` and `import` first (with the
   registration checks and the git-root autoCommit consistency preflight), then `rebind`,
   `forget` (registry+lens only), and `list`. Register `rename` in the command tree as a
   placeholder that hard-refuses (its rewrite is P5) so the help tree is complete.
6. Verify end to end: register a scope, resolve it ambiently and by full id, hit each
   registration-check failure, and confirm `pj scope list` mode labelling across pj-driven,
   repo-driven, plain-files, and unknown.

## Implementation Guidance

- Keep the `ScopeSchema` value shape deliberate: it is consumed by P3 (index
  materialization and list membership) and P6 (merge typing). Model it so a later project
  can ask "is this status known / terminal", "what type is this custom field", and "what is
  the autoCommit value" without re-reading `pj.cue`.
- The git-root autoCommit-consistency check is not init-only in effect — the git-root is
  derived at runtime. Implement the check as a reusable per-git-root evaluation so P4
  (write preflight context) and P5/P6 (sync/doctor preflights) can call the same logic
  rather than forking it.
- Emit `name_drift:`, `config_unparseable:`, and `auto_commit_mismatch:` with the exact
  token strings from the design's closed token table (owned as doctor's contract in P5).
  Do not invent local variants; these same conditions are re-surfaced by doctor later.

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
  underivable basename it hard-errors telling the user to pass `--name`.
- `pj scope import` reads name and autoCommit from `pj.cue`, and hard-fails on a scope-name
  collision and on a malformed `pj.cue`.
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
- Ambient resolution follows `--scope` > `PJ_SCOPE` > longest-prefix code-root; a no-scope
  scope-requiring command fails with the registry-only guidance (no tree probe); and a
  drifted scope hard-errors on every using command with `name_drift:` and the exact
  forget+import recovery line, while `scope list`/`forget`/`import`/`skill`/`help` still run.
- A malformed `pj.cue` refuses that scope's writes with `config_unparseable:` while its
  reads and sibling scopes stay available.
