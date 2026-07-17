# pj — Agent Project Management CLI

Guidance for AI agents working in this repository. `pj` is a single-purpose CLI
that tracks feature work as plain markdown files, one project per file, edited in
place. See `design.md` for the full, authoritative design.

## Project status

The foundation project (P1) has landed: the Go module is initialised and the
pure wire-contract packages live under `internal/`. The CLI itself is still a
placeholder — no Cobra wiring, config, index, or git yet.

- `design.md` is the source of truth for architecture and every locked decision.
  Read it before proposing or writing code.
- Short-ids are letter-first by construction (the `IsShortID` predicate and the
  mint both forbid a leading digit); any `<scope>-<short-id>` example follows
  that rule.
- Do not invent behaviour that contradicts `design.md`. If the design is silent
  or ambiguous on a point, flag it rather than guessing.

## Project documents and archiving

Project documents are numbered `NN-*.md` files. An in-progress or not-yet-started
project lives at the repo root; a completed project is archived.

- When a project is complete (all its acceptance criteria pass and verification
  is green), move its document to `docs/archive/` — preserve history with
  `git mv`, do not copy-and-delete.
- After moving, update any references to that document. Cross-project references
  use logical labels (`P1`…`P7`), which stay valid after a move; only path or
  filename references need rewriting.
- See `docs/archive/` for the completed projects.

## Module and layout

- Module path: `github.com/start-cli/pj`
- Go version: 1.26 (pure Go, no cgo)
- `cmd/pj/main.go` — minimal entry point (placeholder until P2)
- `internal/` — the P1 primitive packages, each pure and I/O-free:
  - `id` — scope/short-id/full-id predicates, `crypto/rand` mint, collision-repair extension
  - `slug` — `Slugify` and the closed slug grammar
  - `order` — the fractional-index `order` wire format and `KeyBetween`
  - `frontmatter` — fence split, YAML parse/serialize, raw fence-slice API
  - `status` — built-in statuses, the `Category` set, and the terminal predicate
  - `title` — ATX-H1 title extraction
  - `scope` — `--auto-name` derivation

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
| Config | CUE (`cuelang.org/go`) | Typed, validated schema for scope config and frontmatter. Arrives in P2. |
| Index | SQLite (`modernc.org/sqlite`) | Pure Go, FTS5 compiled in, WAL mode. Arrives later. |
| Version control | External `git` binary | Shelled out, owner `pj` scopes only. Arrives later. |

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
