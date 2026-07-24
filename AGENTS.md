# pj — Agent Project Management CLI

Guidance for AI agents working in this repository. `pj` is a single-purpose CLI
that tracks feature work as plain markdown files, one project per file, edited in
place. See `design.md` for the full, authoritative design.

## Project status

P1 through P5 and P6a have landed. `pj` runs as a Cobra CLI with the machine-local CUE
registry, scope `pj.cue` evaluation, ambient resolution, and the full `pj scope`
verb set (`init`, `import`, `rebind`, `forget`, `list`, `rename`); the machine-wide
SQLite index with reconcile, FTS5 search, and the read/board verbs (`list`, `get`,
`meta`, `next`, `deps`, `search`, `query`, `lens`); the authoring hot path
(`create`, `status`, `reorder`, `edit`, `next --claim`) with local git self-commit;
`pj doctor` with its integrity repairs and the closed token catalogue; and P6a's
frontmatter merge package (`internal/fmmerge`), the rebase driver
(`internal/rebasedriver`), and the read/integrate/push half of the git wrapper — all
proven before `pj sync` exists.

Not built yet: `pj sync` and the push boundary (P6b) — no command pushes yet, though
`internal/git` and `internal/gitstate` now expose the full fetch/rebase/push/stage-read
surface P6b builds on; and `pj skill` (P7).

- `design.md` is the source of truth for architecture and every locked decision.
  Read it before proposing or writing code.
- Short-ids are letter-first by construction (the `IsShortID` predicate and the
  mint both forbid a leading digit); any `<scope>-<short-id>` example follows
  that rule.
- Do not invent behaviour that contradicts `design.md`. If the design is silent
  or ambiguous on a point, flag it rather than guessing.

## Project documents and archiving

Project documents are numbered `NN-*.md` files (a split project takes a letter
suffix, `NNa-`/`NNb-`). An in-progress or not-yet-started project lives at the repo
root; a completed project is archived.

- When a project is complete (all its acceptance criteria pass and verification
  is green), move its document to `docs/archive/` — preserve history with
  `git mv`, do not copy-and-delete.
- After moving, update any references to that document. Cross-project references
  use logical labels (`P1`…`P7`), which stay valid after a move; only path or
  filename references need rewriting.
- See `docs/archive/` for the completed projects (P1–P5).
- The sync and merge boundary is split across two documents: `06a` (frontmatter
  merge package, rebase driver, git plumbing) and `06b` (`pj sync`). Documents
  written before that split refer to the pair as `P6`; a `P6` reference to the
  merge package, the driver, or `internal/git` plumbing means P6a, and one to
  `pj sync`, its integrity step, or its push means P6b. The labels were kept as
  `P6a`/`P6b` rather than renumbering so every existing `P6` and `P7` reference
  in the archive stays valid.

## Module and layout

- Module path: `github.com/start-cli/pj`
- Go version: 1.26 (pure Go, no cgo)
- `cmd/pj/main.go` — minimal entry point: run, map a signal or error to an exit
  code, exit (all command logic is in `internal/cli`)
- `internal/` — pure wire-contract primitives, then the engines built on them:
  - `id` — scope/short-id/full-id predicates, `crypto/rand` mint, collision-repair extension
  - `slug` — `Slugify` and the closed slug grammar
  - `order` — the fractional-index `order` wire format and `KeyBetween`
  - `frontmatter` — fence split, YAML parse/serialize, raw fence-slice API
  - `status` — built-in statuses, the `Category` set, and the terminal predicate
  - `title` — ATX-H1 title extraction
  - `scope` — `--auto-name` derivation
  - `token` — the closed stderr token strings (`name_drift:`, `config_unparseable:`, …)
  - `pathutil` — boundary-safe path predicates (nesting, disjointness)
  - `xdg` — XDG config dir resolution and the machine-global flock
  - `flock` — the POSIX advisory-lock helper behind the scope and git-root locks
  - `atomicfile` — same-dir temp write plus rename, so no reader sees a half-written file
  - `gitroot` — `git rev-parse` code-root/git-root derivation
  - `scopeconfig` — scope `pj.cue` evaluation into the validated `ScopeSchema`
  - `registry` — the XDG registry/lens model, CUE read + atomic regenerate
  - `resolve` — ambient scope resolution and name-drift fail-closed
  - `scopeadmin` — scope verbs and the shared registration checks
  - `index` — the machine-wide SQLite read model (WAL, FTS5, projects + edges)
  - `reconcile` — git-free read-through that brings the index up to date from the files
  - `git` — the external-git wrapper; full read/integrate/push surface (fetch, rebase,
    stage enumeration and reads, blob merge, author date, push, unpushed count)
  - `gitstate` — per-git-root XDG ops state (`sync.lock`, `last-push-error` read/write/clear)
  - `selfcommit` — the single reusable self-commit step for auto-commit scopes
  - `rewrite` — the shared multi-file rewrite durability engine
  - `repair` — deterministic integrity repairs (collision pick, re-space, archive move);
    exports the shared `KeepBefore` loser pick the merge package reuses
  - `fmmerge` — the pure 3-way frontmatter merge over raw stage blobs (P6a)
  - `rebasedriver` — resolves one conflicted project `.md` at a paused rebase (P6a)
  - `cli` — Cobra command tree, exit codes, signals, colour/TTY, path hand-off

## Build, test, lint, format

| Task | Command |
|---|---|
| Build | `go build ./...` |
| Test | `go test ./...` |
| Format check | `gofmt -l .` (empty output = clean) |
| Format write | `gofmt -w .` |
| Vet | `go vet ./...` |
| Lint | `golangci-lint run ./...` (config in `.golangci.yml`, schema v2) |

## Intended stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go | Pure Go, no cgo (a git subprocess is not cgo). |
| Frontmatter/config YAML | `github.com/goccy/go-yaml` | Actively maintained pure Go; AST/style control for the force-quoted `order` and undeclared-key retention. |
| Unicode | `golang.org/x/text` | NFKC normalisation for `slugify` (Go has no stdlib normalisation). |
| Config | CUE (`cuelang.org/go`) | Typed, validated schema for scope config and frontmatter. |
| Index | SQLite (`modernc.org/sqlite`) | Pure Go, FTS5 compiled in, WAL mode. |
| Version control | External `git` binary | Shelled out, owner `pj` scopes only. Full commit and read/integrate/push surface built (P6a); `pj sync` wires it in P6b. |

TIP: Both `modernc.org/sqlite` and `cuelang.org/go` are pure Go by design. Do not
introduce a cgo-based SQLite driver (e.g. `mattn/go-sqlite3`) — it breaks the
"pure Go, no cgo" invariant.

## Go CLI design guide is advisory

The Go CLI design guide (`start get golang/design/cli`) is advisory only.
Adopt its repo-shape conventions — standard layout (`cmd/pj/main.go` minimal,
`internal/…`), table-driven tests with `testdata/`, and a `.golangci.yml`.
`design.md` overrides it on every conflict; a later project does not restate
this. Known override points where `design.md` wins:

- Exit codes and error classes — `design.md` fixes them (usage/bad-id `exit 2`,
  unknown id generic non-zero, `duplicate_id:` refuse) over the guide's mapping.
- Output contract — `design.md` is path-centric with TSV/stdout hand-off, not
  the guide's JSON-envelope-first model.
- Configuration model — per-scope `pj.cue` plus a machine-wide registry, not the
  guide's XDG/profile precedence chain.
- Command semantics — one-op-one-commit and path hand-off, not the guide's
  async-job ledger.
