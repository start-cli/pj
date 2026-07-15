# pj design issues

Review of `design.md` (landed design, pre-implementation). Items are places the design
does not quite fit — internal contradictions, goal tension, agent-safety sharp edges,
semantic friction, and residual gaps. The core model (files as source of truth, scope
unit, path-centric CLI, closed repair budget) is coherent; this list is not a rejection
of the design.

Priority guide:

| Priority | Meaning |
|---|---|
| P0 | Internal contradiction; implementers will guess wrong |
| P1 | Agent safety or CLI contract ambiguity that should close before code |
| P2 | Semantic / UX friction that is intentional or near-intentional but costly |
| P3 | Small gaps, polish, or documentation honesty |

Status: open unless noted. Body of `design.md` remains authoritative until a DECISION
revises it. Closed items keep a short resolution line; design.md is the full rule.

---

## P0 — Contradictions

### ISS-001: Body conflict markers vs `parse_error` quarantine

**Status:** closed (2026-07-15) — option A body-aware parse.

**Where:** Merge conflict handling (layer 4); Invalidation and reconcile.

**Resolution:** DECISION (body-aware conflict markers vs `parse_error`) in Invalidation:
markers quarantine only when inside the FM fence or FM YAML fails; body-only markers do
not set `parse_error`. Layer 4, skill Conflicts, and `parse_error:` token text aligned.
Mid-body-conflict files stay on list/next/search from FM; body prose untrusted until
markers gone. Auto-commit mutators still refuse mid-rebase via freeze, not parse quarantine.

---

### ISS-002: Mid-rebase freeze vs `pj edit` / what counts as mutating

**Status:** closed (2026-07-15) — option A explicit mid-rebase command classes.

**Where:** Sync model (mid-rebase refuse); CLI surface (`pj edit`); Agent skill
(Conflicts and paused sync).

**Resolution:** DECISION (mid-rebase command classes) under Sync model: "mutating
command" = self-commit / complete-state path. Mid-rebase refuse: `status`/`reorder`/
`create` / integrity rewrites; allow: `edit`, reads, bare doctor, `sync` resume.
`doctor --repair` refuses until ISS-011. Skill Conflicts and layer 4 wording aligned.

---

## P1 — Contract and agent safety

### ISS-003: Global `--scope` vs CLI surface tables

**Status:** closed (2026-07-15) — option A universal ambient `--scope` (not doctor).

**Where:** Resolution (ambient precedence); doctor scope selection; CLI hot-path table;
skill work loop.

**Resolution:** DECISION (optional `--scope` on ambient verbs): every ambient project
verb accepts `--scope` as top ambient override; doctor stays no `--scope` (`PJ_SCOPE`/
cwd). Hot-path table, verb docs, skill Core work loop, and doctor wording aligned.

---

### ISS-004: `pj doctor --repair` with no ambient = machine-wide mutation

**Status:** closed (2026-07-15) — option B explicit `--all` for machine-wide mutation.

**Where:** Invalidation (doctor scope selection); Agent skill (Doctor and integrity
warnings).

**Resolution:** Report stays ambient-or-all (discovery). Mutating flags (`--repair`,
`--re-space-order`) require ambient / `PJ_SCOPE` or explicit `--all`; bare mutate with
no ambient is usage error (exit 2). CLI, skill agent rules, and Project ids target-set
prose aligned. Still no `pj doctor --scope`.

---

### ISS-005: Create durability vs terminal / archive appearance

**Status:** closed (2026-07-15) — option A keep scaffold rule; mandatory durability +
terminal-create stderr cue.

**Where:** Writes / Auto-commit; create scaffold; Done and archive; skill Capture /
End of turn.

**Resolution:** Create still never self-commits. Capture + End of turn: any create this
turn makes mode-appropriate durability mandatory. Terminal create: terse stderr note
(not a closed token) that scaffold is not git-durable until sync/host commit. CLI create
and Auto-commit help-text honesty aligned.

---

### ISS-006: `pj get` exit code under `parse_error` quarantine

**Where:** Invalidation (`get` prints path + `parse_error:` on stderr); CLI exit codes;
`pj meta` edge cases (explicit 0 vs non-zero).

**Gap:** `meta` locks exit for extractable vs non-extractable frontmatter under
`parse_error`. `get` is only said to print the path and ride `parse_error:` on stderr —
exit code not locked.

**Suggested close:** e.g. `get` always exit 0 when the file exists and path is handed off
(even quarantined); non-zero only when unknown id / missing file / usage. Align skill
doctor rules with that.

---

### ISS-007: `pj sync` / `--all` when no auto-commit scopes apply

**Where:** Sync model; CLI `pj sync [--all]`.

**Gap:** Non-auto-commit ambient → refuse with mode-named error. `--all` / no ambient
syncs every auto-commit git-root and skips non-auto-commit. Behaviour when **zero**
auto-commit scopes exist (or all skipped) is not said: exit 0 empty report vs error.

**Suggested close:** Exit 0, terse stderr that nothing was synced (discovery-friendly),
unless ambient was non-auto-commit (existing refuse).

---

## P2 — Semantic / UX friction (mostly intentional)

### ISS-008: Global `order` domain includes archive and all statuses

**Where:** Metadata (`keyBetween` left,null / rank domain); create always appends;
skill Ordering.

**Behaviour:** `last` / `--last` / create append use max valid `order` over **all**
projects in the scope (every status, root and `archive/`). New scaffolds sit after
historical done work until `pj reorder`.

**Fit:** Matches "one rank space; filters change visibility only." Fights everyday
"end of the active board."

**Suggested close:** Keep behaviour; add one explicit DECISION sentence that this is
intentional UX cost, not an oversight; skill already has the promote → reorder flow.

---

### ISS-009: Only built-in `todo` is next-eligible

**Where:** Status and dependencies; CUE custom status categories.

**Behaviour:** Customs never enter `pj next` regardless of category. Queue is always
`todo` → claim `in-progress`.

**Fit:** Keeps the agent queue one status; customs are list/filter/terminal labels only.

**Suggested close:** Name as non-goal in Status section: no custom "ready" synonym for
`next` in v1.

---

### ISS-010: Location follows terminal-ness vs free hand-edit of status

**Where:** Done and archive; Frontmatter mutation skill.

**Behaviour:** Terminal ↔ `archive/` is a projection of status. `pj status` moves the
file; direct FM status edit does not; hand `mv` is forbidden; drift tokens until
`--repair` / status verb.

**Fit:** Load-bearing for next (never under `archive/`) and allowlist layout. Does not
fit "edit markdown freely" for the `status` key.

**Suggested close:** No product change required; keep "prefer `pj status`" as
load-bearing, not style. Optionally doctor hard-flag hand layout moves more loudly.

---

### ISS-011: `pj doctor --repair` during mid-rebase / paused sync

**Where:** Sync integrity; doctor `--repair`; multi-file rewrite durability.

**Gap:** Integrity repair mutates project files and may self-commit. Interaction with
paused rebase (unstaged conflicted paths, temporary HEAD) is not closed. Auto-commit
mutators refuse mid-rebase; doctor repair is a separate path.

**Suggested close:** Refuse `--repair` (and equal-order / archive moves that self-commit)
when the derived git-root is mid-rebase, with the same class of error as mutator refuse;
or document that repair is only safe off-rebase and skill says wait for sync resume.

---

### ISS-012: Anti-goals vs operational complexity mass

**Where:** Anti-goals; Project ids auto-repair budget; entire sync/merge/doctor surface.

**Tension:** Anti-goals reject beads-scale explosion and open-ended recovery. The design
keeps a **closed** budget, but the operational surface is still large: dual-file id
repair, add/add mid-rebase rename, `status_conflict`, archive projection, name-drift
fail-closed, multi-scope git-root freeze, plain-files dual-repair rules, full token
catalogue, 18-section skill.

**Fit:** Simple files, complex CLI — coherent product, weak fit with a casual reading of
"ruthlessly shrunk."

**Suggested close:** One honest Anti-goals paragraph: complexity is pushed into the CLI
so markdown stays dumb; "small" applies to the file model and verb count, not to the
implementation or skill dump size.

---

### ISS-013: `pj query` free-form SQL on unstable schema

**Where:** Query surface; skill Unsupported operations / Dependencies.

**Tension:** Agent interface is path + short text, no `--json`, unstable index schema —
yet `pj query <sql>` is a first-class verb (read-only). Power-user / debug tool wearing
a product-verb hat.

**Suggested close:** Skill: "debug / human ad-hoc only; agents prefer `deps` / `list` /
`search`." Optional help text: not for automation. No need to remove v1 if SQLite stays.

---

### ISS-014: Skill body couples to `start get project/writing`

**Where:** Agent skill contract (Body conventions).

**Tension:** Skill locks project writing-guide section order via `start get
project/writing` / equivalent org guide. Fine for start-cli fleet; odd for a standalone
open `pj` skill that claims to be self-contained.

**Suggested close:** Inline a minimal section list in the skill (already partially
there) and treat start-cli as optional equivalent, not the primary pointer — or state
explicitly that the skill assumes the org writing guide.

---

## P3 — Smaller gaps and polish

### ISS-015: Archive nesting depth

**Where:** Storage; reconcile scan; snapshot allowlist.

**Gap:** Reconcile stats dir root and "one `archive/` subdirectory." Project files "under
`archive/`" with basename shape. Nested `archive/foo/<id>-<slug>.md` — indexed, residue,
or non-allowlist? Flat-only is implied but not closed.

**Suggested close:** Closed rule: only immediate children of `archive/` with project
basename shape; deeper paths are non-allowlist residue (doctor/`non_allowlist:`).

---

### ISS-016: Open questions claims "None"

**Where:** Open questions section.

**Gap:** Residual items in this file contradict a hard "none outstanding."

**Suggested close:** Point Open questions at this file, or restore a short list of
deferred product choices (Windows, viewer, skill install) and link design gaps here.

---

### ISS-017: Concurrent agents + file tools not covered by flock

**Where:** Concurrent agents; scope flock; claim protocol.

**Note:** Design accepts pre-claim race and flock only on pj writes. Body edits via file
tools are unserialised. Skill says prefer one writer. Not a bug; residual risk.

**Suggested close:** No v1 change. Optional later: document "no file-tool write without
claim" more firmly in Concurrent agents.

---

### ISS-018: Fleet-global scope names by convention only

**Where:** Scope names; skill Cross-scope work.

**Note:** Same short name on two machines can name different scopes; cross-scope
`depends` silently gates wrong. Accepted single-user-fleet assumption.

**Suggested close:** No v1 change. Keep skill warning. Optional later: doctor cannot
detect this across machines without a fleet registry (out of budget).

---

### ISS-019: Name drift fail-closed includes correct new full ids

**Where:** Registry name drift.

**Behaviour:** After remote rename, registry key ≠ `pj.cue` name → scope unusable until
forget+import, including full ids under the **new** name (no registry key yet).

**Fit:** Intentional fail-closed vs agent-hostile half-mode. Harsh but consistent.

**Suggested close:** Keep; ensure error text always prints the exact forget+import
command with both names and dir.

---

### ISS-020: Planned auto-commit outside git then git appears

**Where:** Init matrix; Writes (autoCommit true, git not ready).

**Behaviour:** File writes succeed; self-commit skipped with `sync_disabled:` until
git-root; create still never self-commits after git appears.

**Note:** Consistent with scaffold rule; easy to misread as "once git exists, all writes
commit."

**Suggested close:** Already documented; keep create exception in the auto-commit help
table prominent.

---

### ISS-021: Lens + empty ready queue diagnostic

**Where:** Tags and lens.

**Behaviour:** When lens filters ready queue empty while unlensed ready work exists,
`next` reports it. Untagged projects never hidden by lens.

**Note:** Fits "lens is not a wall." No issue beyond ensuring skill Concurrent/List
mention it (skill has list/next lens rules).

**Status:** Likely closed by existing skill text; keep as regression-test checklist item.

---

### ISS-022: Token `schema_warn:` overloaded

**Where:** Doctor token table.

**Behaviour:** One soft token covers undeclared keys, `knownTags` typos, self-`related`,
duplicate list entries, id-shaped `links`. Agents must read the rest of the line.

**Suggested close:** Accept for v1 (closed token set bias). Split later only if agent
automation needs it.

---

### ISS-023: Colour only useful on stderr in practice

**Where:** Colour / TTY DECISION.

**Behaviour:** No ANSI on stdout paths/TSV/token prefixes; colour optional human polish;
v1 may use no colour on stdout at all.

**Note:** Fine; implementers should not invent board colour on list TSV.

**Suggested close:** N/A — implementation note.

---

### ISS-024: CUE load on every command (registry bootstrap)

**Where:** Configuration (accepted cost).

**Note:** Owner hard lock-in; every invocation loads XDG via CUE. Accepted latency.

**Suggested close:** No design change; benchmark early so the acceptance is measured.

---

### ISS-025: Index FTS corpus includes parse_error / archive / all statuses

**Where:** Query surface; search.

**Note:** Search includes archive and all statuses; no lens. parse_error may still
FTS-index raw body. Confirm after ISS-001 whether quarantined files appear in search
with what status field.

**Suggested close:** After ISS-001, one sentence: search may return quarantined hits;
status field empty or last-known; path still openable via search path column / `get`.

---

## What fits well (not issues)

Recorded so the list is not only faults:

- Files sole source of truth; index/registry derived or machine-local — consistent.
- Closed id predicates (`IsFullProjectID` not a loose regex) — correct and implementable.
- Auto-repair budget table + out-of-budget list — real discipline.
- Full ids only in `depends`/`related`; short form CLI-only — clean wire contract.
- Multi-scope git-root coupling documented as accepted tradeoff.
- Colour / token purity / absolute path hand-off — agent-safe.
- Skill subordinate to body DECISIONs — right authority order.
- AGENTS.md stack matches design (pure Go, CUE, modernc sqlite, external git).

---

## Suggested fix order

1. **ISS-001** body markers vs `parse_error` (block implementation ambiguity).
2. **ISS-002** mid-rebase command classes + **ISS-010** repair during rebase.
3. **ISS-003** `--scope` surface.
4. **ISS-004** machine-wide `--repair` gate.
5. **ISS-006**, **ISS-007**, **ISS-015** small closed rules.
6. **ISS-005**, **ISS-008**, **ISS-009**, **ISS-012**, **ISS-013**, **ISS-014** docs /
   honesty / skill tightening (can land without behaviour change where preferred).
7. Remainder as implementer notes or post-v1.

---

## Index

| ID | Priority | Title |
|---|---|---|
| ISS-001 | P0 | Body conflict markers vs `parse_error` — **closed** |
| ISS-002 | P0 | Mid-rebase freeze vs `pj edit` / mutating class — **closed** |
| ISS-003 | P1 | Global `--scope` vs CLI tables — **closed** |
| ISS-004 | P1 | Doctor `--repair` machine-wide without ambient — **closed** |
| ISS-005 | P1 | Create durability vs terminal/archive appearance — **closed** |
| ISS-006 | P1 | `get` exit under `parse_error` |
| ISS-007 | P1 | Sync with zero auto-commit scopes |
| ISS-008 | P2 | Global order domain includes archive |
| ISS-009 | P2 | Only built-in `todo` next-eligible |
| ISS-010 | P2 | Location follows terminal vs free status edit |
| ISS-011 | P2 | Doctor `--repair` during mid-rebase |
| ISS-012 | P2 | Anti-goals vs operational complexity |
| ISS-013 | P2 | `pj query` vs agent interface |
| ISS-014 | P2 | Skill couples to start-cli writing guide |
| ISS-015 | P3 | Archive nesting depth |
| ISS-016 | P3 | Open questions claims none |
| ISS-017 | P3 | Concurrent agents / file tools |
| ISS-018 | P3 | Fleet scope names by convention |
| ISS-019 | P3 | Name drift fail-closed harshness |
| ISS-020 | P3 | Planned auto-commit then git appears |
| ISS-021 | P3 | Lens empty-queue diagnostic (checklist) |
| ISS-022 | P3 | `schema_warn:` overloaded |
| ISS-023 | P3 | Colour on stdout (implementation note) |
| ISS-024 | P3 | CUE every command (benchmark) |
| ISS-025 | P3 | Search / FTS for quarantined rows |
