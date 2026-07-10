# pj design friction

Open design questions raised against `design.md`. Not locked decisions — discussion
notes to work through one at a time.

Status: open. Resolve each item, then fold agreed changes into `design.md` (or
explicitly close as "keep as designed").

---

## 1. "files-path" → `<dir>`

**Problem:** "files-path" is awkward. It tries to say "directory that holds the markdown +
`pj.cue`", as distinct from code-root (ambient cwd match) and git-root (derived). The
compound is inventy and heavy in CLI help and prose.

### Plan (locked for discussion → fold into `design.md`)

**CLI / docs:** replace `<files-path>` with **`<dir>`**.

```text
pj scope init <dir> (--name <name> | --auto) [--code-root <path>] [--auto-commit]
pj scope import <dir> [--code-root <path>]
```

**Registry CUE:** rename the path key from `files:` to **`dir:`** so flag, docs, and
registry share one word.

```cue
scopes: {
    wc: {
        dir:  "/home/grant/projects/webctl/.agents/pj"
        root: "/home/grant/projects/webctl"
    }
}
```

**Prose:** on first mention, "scope directory (`dir`)" — thereafter **dir**. Avoid
"files-path", "project-path", and other compounds. Do not call it "root" (reserved for
code-root / git-root).

**Unchanged concepts:**

| Term | Meaning |
|---|---|
| **dir** | Where the `.md` files and `pj.cue` live; what reconcile stats |
| **code-root** (registry `root:`) | Where the scope is ambient for bare `pj` |
| **git-root** | Derived from `dir` via `git rev-parse --show-toplevel`; not stored |

The important split stays: *where the markdown lives* (`dir`) vs *where bare `pj` is
ambient* (code-root).

When folding into `design.md`, replace "files-path" / `files:` throughout (Storage,
Registry, Scope lifecycle, sync snapshot, decision summary).

---

## 2. `--owner host|pj|none` → `--auto-commit`

**Problem:** `owner` is opaque and wrong-shaped. It collides with "who owns the work" /
"repo owner". The real question is not three agents — it is **whether pj commits**.

**Insight:** `host` and `none` are the same pj behaviour (never commit, never `pj sync`).
They differ only by environment:

| Old value | In a git repo? | Who is expected to commit |
|---|---|---|
| `host` | required | surrounding repo / human / PR |
| `none` | no | nobody (or Dropbox/Syncthing/etc.) |
| `pj` | yes (or planned) | **pj** |

So the useful control is one bit. Host vs none is **derived** from "is the files-path
inside a git repository?", not a third stored choice.

### Plan (locked for discussion → fold into `design.md`)

**CLI:** one flag only — **`--auto-commit`**. No `--no-auto-commit`, no enum values.

| Flag | Meaning |
|---|---|
| `--auto-commit` | pj commits complete mutations and uses `pj sync` as the fetch/push boundary (old `pj`) |
| omitted | pj never commits; durability is up to you (old `host` ∪ `none`) |

**`pj.cue`:** replace `owner: "host"|"pj"|"none"` with a bool:

```cue
autoCommit: bool   // true only when init got --auto-commit (or inherited true)
```

Import still reads whatever is on disk; no flag on import.

**Init matrix:**

| Situation | `--auto-commit`? | Result |
|---|---|---|
| Outside git | omit | plain files (old `none`) |
| Outside git | set | pj-driven; sync disabled until repo/remote exist |
| First scope **in** a git repo | omit | repo-driven (old `host`) |
| First scope **in** a git repo | set | pj-driven (old `pj`) |
| Repo already has scopes | omit | inherit siblings' `autoCommit` |
| Repo already has scopes | set | must match siblings; contradict → error |

**Per git-root consistency:** every scope sharing a derived git-root has the same
`autoCommit`. Inheritance when adding a sibling; explicit `--auto-commit` that disagrees
errors. Omit on a sibling → inherit (do not re-default to false).

**Doc / error labels only** (not stored): "repo-driven" when `autoCommit: false` and
inside git; "plain files" when false and outside git; "pj-driven" / "auto-commit" when
true.

**Accepted tradeoff:** first scope in a git repo + omit flag = repo-driven, with no
separate "I meant that on purpose" signal. That is the silent-host default the old design
refused. With a single positive flag it is the only coherent rule: off is default; on is
opt-in. Wrong omit on a dedicated pj repo → files change on disk, no self-commit, no sync
warnings. Mitigate in docs / `pj skill` / init help ("in a dedicated pj repo, pass
`--auto-commit`"), not a second flag.

**Help-text honesty:** "auto-commit" means pj owns the commit path, not every keystroke:

- `status` / `move` / `archive` → self-commit
- `add` → scaffold only; complete project commits at `pj sync`
- direct agent / `$EDITOR` edits → committed at `pj sync`
- push only in `pj sync`, never automatic

When folding into `design.md`, rename the "Sync owner" concept throughout (owner-`pj`
shorthand → auto-commit / pj-driven), update scope lifecycle, `pj.cue` shape, sync
preflight wording, and the decision summary.

---

## 3. `show` vs the Go CLI design guide

From `golang/design/cli` (`start get golang/design/cli`):

- Canonical detail verb is **`get`**
- Aliases of `get`: `view`, `show`, `describe`, `info`
- Example shape: `tool page get 12345`, `tool item get ITEM-123`
- Text-first; detail = key-value (or body), not a separate "show" concept

So **`pj get <id>`** should be the primary name; `show` only as an alias if muscle
memory is wanted.

---

## 4. `pj get <id>` — project content on stdout

Matches both the guide and how agents work (read file content without inventing paths).

Default payload options:

| Mode | Behaviour |
|---|---|
| Default text | Full file: frontmatter + body (what the agent needs to work) — closest to `cat` of the project |
| `--meta` / key-value | Frontmatter only, formatted (status, order, depends, …) |
| Path only | `pj edit` already "locate"; or `pj get --path` if a thin locator without opening `$EDITOR` is wanted |

Design today splits:

- `show` — "print a project" (underspecified: meta vs body vs both)
- `edit` — resolve path, open `$EDITOR` or print path for agents

A clean cut:

- **`pj get <id>`** — stdout the project markdown (full file by default)
- **`pj edit <id>`** — interactive edit only; agents use file tools on the path from
  `get` or a `--path` flag
- Optional **`--path`** on `get` if path-only without opening an editor is wanted

That removes the ambiguous "show" and makes "get" the content verb.

---

## 5. `pj move` and order

Misreading is the design smell: **move** already means "relocate entity" (Jira status,
issues between projects, files between scopes). Here it means **reorder**.

The job: write a new fractional `order` key between neighbours.

Name options:

| Verb | Feel |
|---|---|
| `pj rank <id> --before\|--after\|--first\|--last` | Order-as-rank; matches "rank key" in the design |
| `pj reorder <id> ...` | Explicit; slightly long |
| `pj before <id> <other>` / `pj after ...` | Very clear; two verbs or subcommands |
| `pj queue ...` | Only if leaning into queue metaphor |
| `pj bump` / `pj sink` | Relative only; design wants explicit slots |

Avoid: `move-order`, `change-order`, bare `order` (noun-as-verb is weak), and **`move`**
(wrong mental model).

Lean: **`rank`** if keeping the fractional-index language, or **`reorder`** if zero
jargon is preferred. Flags stay as today (`--before` / `--after` / `--first` / `--last`).

Cross-scope relocation is a different product decision and currently does not exist
(id embeds scope). Do not overload one verb for both.

---

## 6. What `pj archive` does

From the design:

- **Not** delete, not a status change by itself.
- Physically moves a **done** project file into `<scope-dir>/archive/`.
- Reconcile still scans `archive/`; row is flagged `archived`.
- Still findable via `get` / `search`; hidden from default `list`; `--all` /
  `--archived` bring it back.
- Purpose: declutter the flat authoring directory after completion.

So it is **filesystem declutter of completed work**, not "archive" as in tape/backup,
and not Jira's workflow archive. If that name confuses, alternatives: `pj stash`
(worse), `pj shelve`, or drop the command and only use `status: done` + keep files flat
(archive is optional in the design).

---

## 7. Skill suite — scaffold with placeholders

Lock the surface even if install is deferred:

```text
pj skill              # print workflow markdown (v1 real)
pj skill install      # placeholder: not implemented / planned
pj skill list
pj skill uninstall
```

Placeholder behaviour: exit non-zero with a clear "not implemented in v1" (or implement
as no-ops that print the plan). Better than omitting from help so agents invent paths.

---

## 8. `--json` is token-heavy, not "agent-centric"

Aligned with the Go CLI design guide:

> Text by default; JSON envelope with `--json`. Text is the priority mode — agents
> read it directly more often than they parse JSON. Use `--json` only when parsing
> (iterating a list, extracting an ID, branching on status).
>
> Agent help: "Only use `--json` when piping to jq."

So the design's "JSON is the primary agent interface" should flip to:

- **Text is primary** (tables, key-value, full markdown for `get`)
- **`--json` for scripting / jq / structured automation**, not every agent turn
- Keep the envelope **stable when present**, but stop marketing it as the main agent path
- `pj skill` should teach: prefer text; use `--json` only when a field is needed
  mechanically

Token pressure matters more for LLMs than for beads-era agents.

---

## 9. What `show` does today (in the design)

Thinly specified:

> `pj show <id>` — print a project.

Elsewhere: used for cross-scope addressing examples, surface `status_conflict`, print
unparseable projects flagged. No contract for:

- full file vs frontmatter summary
- whether body is included
- human layout vs structured

So `show` is both non-canonical and underspecified. **Replace with `get`** and define
stdout as full project markdown (plus optional flags), and stop treating "show" as a
separate concept.

---

## Suggested direction (still discussion)

| Topic | Lean |
|---|---|
| files-path | **`<dir>`** everywhere; registry key **`dir:`** (was `files:`) |
| owner | Drop; one flag **`--auto-commit`** → `autoCommit: bool`; host/none derived from git topology |
| show | Drop as primary; **`get`** + aliases |
| get | Full project to stdout; optional `--path` / meta flags |
| move | Rename to **`rank`** or **`reorder`** |
| archive | Keep meaning; maybe rename later if "archive" confuses; optional declutter |
| skill * | Scaffold install/list/uninstall as placeholders |
| --json | Secondary, for jq/scripting; text-first for agents |
