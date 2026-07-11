# pj design friction

Design questions raised against `design.md`. Each item has a locked plan; fold them
into `design.md` (or close as keep-as-designed — §6 only).

Status: all items locked. Next step: fold §1–§3, §5, §7–§8 into `design.md` (§4/§9
folded into §3; §6 is keep-as-designed).

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

So the useful control is one bit. Host vs none is **derived** from "is the dir
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

- `status` / `reorder` / `archive` → self-commit
- `create` → scaffold only; complete project commits at `pj sync`
- direct agent / `$EDITOR` edits → committed at `pj sync`
- push only in `pj sync`, never automatic

When folding into `design.md`, rename the "Sync owner" concept throughout (owner-`pj`
shorthand → auto-commit / pj-driven), update scope lifecycle, `pj.cue` shape, sync
preflight wording, and the decision summary.

---

## 3. CLI surface — path-centric agent workflow

**Problem:** Design underspecifies `show` ("print a project") and treats JSON as the
agent interface. The primary job of pj for agents is **locate project files** (filenames
already embed the id: `<id>-<slug>.md`). Agents edit with file tools; humans may use
`$EDITOR`.

From `golang/design/cli`: canonical verbs include **`get`**, **`list`**, **`create`**,
**`status`**. Prefer those names (`show`/`add` only as optional aliases if wanted later).

### Plan (locked for discussion → fold into `design.md`)

**Hot path:**

```text
pj list [status…] [--scope S] [--all] [--no-lens] …
pj next [--no-lens]
pj get <id>
pj create <title> [status]
pj status <id> <status>
pj edit <id>                 # human only
```

| Command | Job | stdout |
|---|---|---|
| **`list [status…]`** | Board / inventory | **Summary** (id, title, status, waiting-on, …) — **no paths** by default |
| **`next`** | First ready `todo` (deps ok, order, lens) | **Path** |
| **`get <id>`** | Resolve short or full id | **Path** |
| **`create <title> [status]`** | Scaffold project (default status `todo`) | **Path** |
| **`status <id> <status>`** | Set status (claim / done / …) | **Path** (after write) |
| **`edit <id>`** | Open in `$EDITOR` | human convenience only |

**Product cut:** pj indexes, queues, and locates; the filesystem is the editor. No
"print full project markdown" verb — the file *is* the content.

**Agent loop:**

```text
pj next                         → path
pj status <id> in-progress      → path (claim)
# file tools on that path
pj status <id> done             → path
```

Known id: `pj get ab2c` → path. Capture: `pj create "Title"` / `pj create "Later" backlog`.

**Rename map:**

| Old (design today) | New |
|---|---|
| `pj show <id>` | `pj get <id>` → path |
| `pj add <title>` [`--backlog`] | `pj create <title> [status]` |
| `pj status <id> <state>` | `pj status <id> <status>` (word is **status**, not state) |
| `pj edit` prints path for agents | agents use `get` / `next` / `status` / `create` |

**`list` status filter:** zero or more space-separated known status names = **union**
filter. Bare `pj list` keeps today's default active set (not a status name). Unknown
status → exit 2. No CSV. No `--status` flag. `--all` remains "include done/backlog/…"
board-wide, not a status token. Lens still applies unless `--no-lens`.

```text
pj list
pj list todo
pj list todo backlog
pj list in-progress blocked review
```

**`create` status:** optional second positional; must be a known status if present;
omitted → `todo`. Title is one argv (quote multi-word). Replaces special-case
`--backlog`. No `--status` flag. No status-first order.

**Not allowed:** `pj get <id> --status …` (get is read/locate only; mutation stays on
`status` / `create`). Same spirit as rejecting `next --claim`.

**`edit`:** humans + `$EDITOR` only.

**Errors:** non-zero exit, message on stderr, **no path on stdout** when there is nothing
to hand off (unknown id, nothing ready, …).

**Optional later (not v1):** path column on `list`; aliases `show`→`get`, `add`→`create`.

When folding into `design.md`: replace CLI surface, agent discovery loop, `show`/`add`
mentions, edit behaviour, and drop "print a project" ambiguity.

---

## 4. (folded into §3)

`get` payload, `list`/`next` roles, `create` vs `add`, and `status` naming are specified
in **§3**. This section kept only so later numbers stay stable.

---

## 5. `pj move` → `pj reorder`

**Problem:** `move` means "relocate entity" (Jira status, issues between projects, files
between scopes). Here the job is rewrite a fractional `order` key between neighbours.
`pj archive` already uses "move" for a real filesystem relocate — two senses of one verb.

### Plan (locked for discussion → fold into `design.md`)

**CLI:** rename to **`reorder`**. Flags unchanged.

```text
pj reorder <id> (--before <id> | --after <id> | --first | --last)
```

**Unchanged behaviour** (already in design under `move`):

- Destination flag required (no bare `pj reorder <id>`).
- Single-file write: `keyBetween(left, right)` into the reordered project's frontmatter
  only; length-grows when neighbours are alphabet-adjacent; never renumbers a band.
- No relative counters, swap, or batch.
- Complete-state mutation: self-commits when auto-commit is on (same class as `status` /
  `archive`).
- With §3: path on stdout after success; errors → stderr, no path.

**Rejected:** `rank` (jargon; "score" reading), `before`/`after` as separate verbs,
`queue`, `bump`/`sink`, bare `order`, keeping `move`, alias `move`→`reorder` in v1.

**Out of scope:** cross-scope relocation (id embeds scope; do not overload this verb).

When folding into `design.md`: replace every project-verb `pj move` / `` `move` `` with
`reorder`; keep ordinary English "move" only for archive/filesystem prose.

---

## 6. `pj archive` — keep as designed

**Question:** Is the name or the command wrong? "Archive" can sound like tape/backup or
Jira workflow archive; alternatives were rename (`shelve`, `stash`) or drop and rely on
`status: done` alone.

### Plan (locked — keep as designed)

**Keep** `pj archive <id>` and the name. No rename, no drop in v1.

Behaviour (already in `design.md`; restated for friction closure):

- **Not** delete, not a status change by itself.
- Physically moves a **done** project file into `<dir>/archive/`.
- Reconcile still scans `archive/`; row is flagged `archived`.
- Still findable via `get` / `search`; hidden from default `list`; `--all` /
  `--archived` bring it back.
- Purpose: optional declutter of the flat authoring directory after completion.
- Optional: `status: done` is enough for the queue; archive is never required.

**Rejected:** `stash` (git-stash semantics), `shelve` (extra jargon, little gain), drop
the command (loses a real human affordance for long-lived scopes).

When folding into `design.md`: no behavioural change. Optionally one louder doc line:
"declutters the authoring dir; record stays indexed and resolvable." Ordinary English
"move" for the filesystem relocate stays correct here (contrast §5 `reorder`).

---

## 7. Skill suite — print real; install family placeholders

**Context:** Persistent install needs to know each agent's skills directory. That
lookup is owned by **agentdex** (`agentdex get <id>` reports `skills_dir` / local
skills paths; catalog is provider-agnostic). agentdex is nearly finished; pj will
use it rather than hardcoding Claude/OpenCode/etc. paths. Until that integration
exists, install must not ship half-baked.

### Plan (locked for discussion → fold into `design.md`)

```text
pj skill              # v1 real: print agent workflow markdown to stdout
pj skill install      # placeholder until agentdex-backed install
pj skill list         # placeholder
pj skill uninstall    # placeholder
```

**`pj skill` (v1):** on-demand workflow dump (beads onboard/prime). Path-centric
loop from §3; claim via `status`; `doctor` / `sync` / post-clone `scope import`
guidance; no `--json` / jq. Discovery command: no ambient scope required. Still
never auto-writes into a tree.

**Placeholders (`install` / `list` / `uninstall`):**

- Appear in help and the command tree (agents do not invent paths).
- Exit **non-zero** with a clear message (hard refuse, not a success no-op), e.g.
  `not implemented in v1 — use 'pj skill' to print the workflow; persistent install
  is planned via agentdex skills directories`.
- Same message for all three; no fake empty `list`.
- No install targets, no write into AGENTS.md / skill dirs, no agentdex dependency
  in the first build of these subcommands.

**Deferred with real install (not v1):** agentdex integration, concrete targets
(global/local skills dirs per agent, optional AGENTS.md block), and list/uninstall
semantics against what was installed.

When folding into `design.md`: update Discovery / CLI surface so v1 ships `pj skill`
plus reserved install family stubs; name agentdex as the planned skills-path source
instead of only "planned expansion" with no reason.

---

## 8. No `--json`

**Problem:** Design makes `--json` a semver-stable contract on every command and markets
it as the agent interface (beads inheritance). Path + short text (§3) remove the need.

### Plan (locked → fold into `design.md`)

- **pj does not support `--json`.** No flag, no stable JSON envelope, on any command.
- Locate/mutate verbs print a **path** (one line). `list` prints a **summary** (no
  paths by default).
- Warnings, doctor, empty-queue diagnostics: **stderr text** (and human stdout where
  appropriate), not a JSON envelope.
- `pj skill` teaches the path-centric loop; no "pipe to jq" guidance.
- Custom frontmatter fields: validated and present in the file; no nested JSON
  `fields` object to document for agents.
- `pj query` stays (read-only SQL); its schema remains non-stable — rephrase without
  contrasting to `--json`.
- Revisit only if concrete text pain appears later (not a v1 pillar).

When folding into `design.md`: remove `--json` from CLI surface, field-exposure,
warnings-on-json, agent loop examples, borrowed-from-beads, and the stable-envelope
DECISION (~20 sites).

---

## 9. (folded into §3)

Old `show` underspecification is resolved by **§3** (`get` → path). No separate plan.

---

## Locked summary (fold into `design.md`)

| Topic | Decision |
|---|---|
| files-path | **`<dir>`** everywhere; registry key **`dir:`** (was `files:`) |
| owner | Drop; **`--auto-commit`** → `autoCommit: bool`; host/none derived from git topology |
| CLI hot path | **`list` / `next` / `get` / `create` / `status` / `edit`** — see §3 |
| list | Summary (no paths); optional **`[status…]`** space-separated union filter |
| next / get / create / status | stdout = **path** |
| create | **`pj create <title> [status]`** (was `add`; default `todo`) |
| status | **`pj status <id> <status>`**; prints path after write |
| edit | Human + `$EDITOR` only |
| --json | **None** — text only |
| move | **`pj reorder`** (was `move`); flags unchanged |
| archive | **Keep** `pj archive` as designed (declutter done → `archive/`) |
| skill * | **`pj skill` real**; install/list/uninstall hard-refuse placeholders (agentdex later) |
