# Scope directory path model (registry dir vs code-root)

Status: deferred from design review / obo walk (finding 9). Not landed in `design.md`.
Pick this up before implementing registry path handling or any `pj scope dir` / relocate verb.

## Problem

The registry records two independent absolute paths per scope:

```text
scopes: {
  wc: {
    dir:  "/abs/path/to/.agents/pj"   // markdown + pj.cue; reconcile, flock, git derive
    root: "/abs/path/to/repo"         // ambient bare-pj longest-prefix match
  }
}
```

Today:

- `pj scope use` re-points **code-root only**.
- There is no first-class way to update **dir** after a filesystem move.
- Recovery is `pj scope forget` + `pj scope import` (lens dropped).
- A naive fix — add `pj scope dir` / `relocate` — treats a **registry shape** problem as a **missing subcommand**.

The common user action is not “I only changed ambient.” It is “I moved or recloned the tree.” With two free absolute pointers, one update path (`use`) leaves the load-bearing path (`dir`) stale: reconcile, allowlist, `git rev-parse`, and project files all key off `dir`.

## Why not just add `pj scope dir`

- Multiplies CLI surface without reducing two-absolute-path fragility.
- Encourages operators to treat path as three different stories (init/import, use, dir) instead of one layout model.
- forget+import already works for rare recovery; the gap is the **common** whole-tree move, which should fall out of a better representation.
- Overloading `use` to “find pj.cue under cwd and rewrite dir” without a relative-dir model reintroduces scanning ambiguity and fights registry-only discovery.

## What “move” actually is

| User action | What should update |
|-------------|-------------------|
| Clone / move whole repo to a new absolute prefix | Usually **both** paths, same relative layout |
| Same files, different ambient cwd policy | **root** only (today’s `use`) |
| Move only the scope folder inside the same tree (e.g. `.agents/pj` → `.agents/projects`) | **dir relative to root** |
| dir never under root (exotic) | True independent paths — must not drive the default model |

Most layouts already implied by `design.md` are dir-under-root (`.agents/pj/`, monorepo team dirs). Independent `dir` ∉ `root` is allowed today but is not the load-bearing story. Auto-commit derives git-root from `dir`; ambient is `root` — the normal case is one tree.

## Recommended direction (not yet DECISION)

### 1. Relative `dir` under `root` (core)

Registry wire (illustrative):

```cue
scopes: {
  wc: {
    root: "/home/grant/projects/webctl"
    dir:  ".agents/pj"   // relative to root when under root
  }
}
```

Rules to lock when implementing:

- At init/import/write: if the scope directory is under `root`, store **`dir` relative to `root`** (normalise; no `..` escape).
- Effective path: `filepath.Join(root, dir)` (or absolute only for the escape case below).
- **`pj scope use` re-points `root` and keeps relative `dir`** so whole-tree move/clone keeps files and ambient aligned without a second verb.
- Import after clone: set `root` (and relative `dir` from the imported path); one registration, no forget dance for “same layout, new prefix.”

### 2. Optional hard lock: `dir` must live under `root`

Stronger variant: refuse dir outside root entirely.

- One ambient tree owns its project files.
- Matches auto-commit mental model.
- “Central pj repo + ambient in another repo” becomes two registrations or an anti-pattern — better than eternal dual absolutes if v1 has no real need for dir∉root.

**Open choice:** keep absolute dir as a second-class escape (no fancy relocate; forget+import only) vs hard-require dir under root.

### 3. Bounded rediscovery (recovery, not primary)

If effective dir is missing but `root` is reachable:

- Search under `root` only for `pj.cue` whose `name` matches the registry key (bounded depth and/or fixed candidates: `.agents/pj`, `.agents/projects`).
- Unique hit → rewrite relative `dir`, report on stderr; 0 or >1 → fail closed with guidance.
- Applies only to **already registered** scopes — not cold-start auto-import (still no machine-wide scan).

Complements relative dir; does not replace deliberate import.

### 4. Lens survival (orthogonal)

If forget+import remains for some recoveries:

- Consider machine-local scope id (uuid at import) for lens keying, or
- On import, offer/copy lens when `pj.cue` name matches a just-forgotten entry.

Do not use lens tricks to avoid fixing path representation.

### 5. Explicitly reject as primary design

- New `pj scope dir` / `relocate` while both paths stay independent absolutes.
- Auto-rekey of **name** drift (already out of budget in `design.md`); path repair ≠ name identity.
- Free-form filesystem walk for unregistered scopes.

## Interaction with existing design

- **Resolution:** ambient still longest-prefix on `root`; no up-scan for markers.
- **Registration deliberate:** relative dir does not auto-register clones; import/init still required once per machine.
- **Name drift (`name_drift:`):** fail-closed until forget+import — separate from path move; do not conflate.
- **Git-root:** still derived from effective `dir` via `git rev-parse`; relative dir under root keeps that stable when only absolute prefixes change.
- **Dir disjointness:** still enforce on effective absolute paths at init/import/use.
- **XDG registry:** machine-written CUE; shape change is a registry format bump / rewrite on next scope mutation or doctor note for hand-edited files.

## Implementation sketch (when picked up)

1. Decide: dir must be under root (hard) vs relative-when-possible + absolute escape.
2. Define registry CUE shape and migration from today’s two absolutes (on read: if both absolute and dir under root, normalise to relative on next registry write).
3. Update `pj scope init` / `import` / `use` / `forget` docs and behaviour.
4. Update `design.md` Registry + Scope lifecycle + decisions log; skill if recovery text changes.
5. Optional: bounded rediscovery on unreachable effective dir.
6. Only if still needed: rare relative-path rebind verb for “moved folder inside tree without changing root.”

## Acceptance criteria (for later landing)

- Moving a git clone to a new absolute prefix and running a single deliberate re-bind (`use` with new root, or re-import) leaves project verbs working without lens wipe when only paths changed and name is unchanged.
- `use` cannot leave `dir` pointing at the old tree while `root` points at the new tree in the normal dir-under-root layout.
- No new cold-start filesystem auto-registration.
- Name drift remains fail-closed and separate.
- Design doc states the wire format and the dir∉root policy explicitly.

## Origin

Design review of `design.md` (obo finding 9). Recommendation rejected “whack a new subcommand on scope” in favour of relative-dir / layout-relative identity under code-root. See also fail-closed `name_drift:` already landed in `design.md` for post-share rename.
