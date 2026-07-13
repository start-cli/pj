# pj — Agent Project Management CLI

Guidance for AI agents working in this repository. `pj` is a single-purpose CLI
that tracks feature work as plain markdown files, one project per file, edited in
place. See `design.md` for the full, authoritative design.

## Project status

IMPORTANT: This repository is pre-implementation. It currently holds only
`design.md` (the landed design) and `LICENSE`. There is no Go module, no source,
no tests, no build yet.

- `design.md` is the source of truth for architecture and every locked decision.
  Read it before proposing or writing code.
- When implementation begins, initialise the Go module and update this file with
  the real build/test/lint commands.
- Do not invent behaviour that contradicts `design.md`. If the design is silent or
  ambiguous on a point, flag it rather than guessing.

## Intended stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go | Pure Go, no cgo (a git subprocess is not cgo). |
| Config | CUE (`cuelang.org/go`) | Typed, validated schema for scope config and frontmatter. |
| Index | SQLite (`modernc.org/sqlite`) | Pure Go, FTS5 compiled in, WAL mode. |
| Version control | External `git` binary | Shelled out, owner `pj` scopes only. |

TIP: Both `modernc.org/sqlite` and `cuelang.org/go` are pure Go by design. Do not
introduce a cgo-based SQLite driver (e.g. `mattn/go-sqlite3`) — it breaks the
"pure Go, no cgo" invariant.

