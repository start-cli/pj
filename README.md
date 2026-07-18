# pj — Agent Project Management CLI

`pj` tracks feature work as plain markdown files, one project per file, edited in
place. It indexes, queues, and locates projects; the filesystem is the editor.

See `design.md` for the authoritative design and every locked decision.

## Supported platforms

macOS and Linux only. Windows is not supported — there is no flock or path
substitute, and `pj` fails with a clear startup error on an unsupported OS rather
than half-running. This is a deliberate v1 scope limit.

## Install / build

```sh
go build ./...          # build
go build -o pj ./cmd/pj # produce the binary
```

Requires Go 1.26. Pure Go, no cgo. The external `git` binary is used only to
derive a code-root / git-root; it is shelled out, never linked.

## Scopes

A scope is a directory of project markdown files plus its `pj.cue` (the scope's
name, auto-commit mode, and schema). Scopes are registered per machine in the XDG
config directory (`${XDG_CONFIG_HOME:-~/.config}/pj/`).

```sh
pj scope init <dir> (--name <name> | --auto-name) [--code-root <path>] [--auto-commit]
pj scope import <dir> [--code-root <path>]
pj scope rebind <dir> --name <name> [--code-root <path>]
pj scope forget <name>
pj scope list          # bare `pj scope` and `pj scopes` also run list
```

- `init` creates and registers a new scope, writing a minimal `pj.cue` and a
  `.gitignore` covering `.pj.lock`. Exactly one of `--name` / `--auto-name` is
  required. In a dedicated pj repo, pass `--auto-commit` (omitting it registers
  repo-driven).
- `import` registers an existing on-disk scope, files in place; its name and
  auto-commit mode come from the on-disk `pj.cue`.
- `rebind` rewrites a registered scope's paths after a move or clone.
- `forget` unregisters a scope (registry and lens entries only); it never touches
  the scope's files.
- `list` prints parse-stable TSV, one line per scope: `name\tdir\troot\tmode`,
  where `mode` is `pj-driven`, `repo-driven`, `plain-files`, or `unknown`.

## Output and exit codes

- stdout is a path or closed TSV; diagnostics and closed tokens go to stderr.
- Exit `0` success; `2` for usage / bad CLI input; other failures are generic
  non-zero. There is no `--json` and no colour on stdout. `NO_COLOR` suppresses
  all ANSI.

## Development

| Task | Command |
|---|---|
| Build | `go build ./...` |
| Test | `go test ./...` |
| Format | `gofmt -w .` |
| Vet | `go vet ./...` |
| Lint | `golangci-lint run ./...` |
