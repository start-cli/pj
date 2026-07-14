# design.md fitness issues

Date: 2026-07-14
Source: `/home/grant/Projects/start-cli/pj/design.md` (~3228 lines, ~34k words, ~79 `DECISION:` markers)
Context: pre-implementation design review. Repository holds `design.md`, `LICENSE`, and agent guidance only; no Go module or source yet.
Scope: things that do not quite fit — internal tensions, underspecification that forces guessing, and smaller inconsistencies. Not a re-litigation of locked tradeoffs that already have stated rationale (e.g. SQLite for small corpora, CUE cost, multi-scope auto-commit coupling).

Open questions section in design says none outstanding. This report is independent of that claim.

## Obo progress

Last updated: 2026-07-14 (session mid-walk).

Resume: read this section, then design.md (fixes already landed for Fixed rows). Continue the
one-by-one skill on remaining `pending` rows in walk order below. Say e.g. `obo issues.md`
or `continue obo from finding 6`.

Mode: Continue (C). Walk total T = 15 (pending + already Fixed in walk; safe/info separate).

Phase 1 safe (applied, not in T): #12, #15, #16.
Info-only (not walked): #18, #19, #21, #22.

Walk order (severity-first): 1, 2, 5, 3, 4, then 6, 7, 8, 9, 10, 11, 13, 14, 17, 20.

Cursor: finding **#6** presented; recommendation was **A** (closed TSV list columns); awaiting
decision (A/B/C/R/N/P/S). No design.md edit for #6 yet.

| # | Finding | Outcome |
|---|---------|---------|
| 12 | Read-commands list omit `meta` | Fixed (Phase 1 safe) |
| 15 | OPEN / IDEA legend unused | Fixed (Phase 1 safe) |
| 16 | `archive_non_terminal:` optional prose stale | Fixed (Phase 1 safe) |
| 1 | create + terminal status vs self-commit | Fixed — A: create never self-commits (any status); terminal at create is label shortcut; durability always sync/host; skill orphan scaffold |
| 2 | Archive vs `pj next` eligibility | Fixed — D: location follows terminal; no `pj archive`/`unarchive`; status moves root↔archive/; doctor `--repair` + sync integrity heal drift; `next` never under archive/; tokens `archive_non_terminal:` + `archive_terminal_at_root:` |
| 5 | `pj search` stdout contract | Fixed — B: TSV hit lines `full-id\tstatus\ttitle\tsummary\tabsolute-path`; bm25 desc + id tie-break; empty = exit 0; no lens; archive included; no score column |
| 3 | Collision depends-only vs all-edges verify | Fixed — A: loser pick depends-only; edge_verify all cross-scope inbound (depends+related); rationale locked |
| 4 | Full-id regex vs short-id grammar | Fixed — A: named `IsScopeName` / `IsShortID` / `IsFullProjectID`; no loose `a-z0-9{4,8}` as validator; edges + links share predicate |
| 6 | `list` columns | pending — next; rec A (TSV: full-id, status, title, summary, waiting-on; no path) |
| 7 | Create append `last` set | pending |
| 8 | Colour / TTY policy | pending |
| 9 | Skill `start get` coupling | pending |
| 10 | Doctor repair scope selection | pending |
| 11 | `pj edit` stdout / commit | pending |
| 13 | Config precedence wording | pending |
| 14 | Git state layout split | pending |
| 17 | AGENTS.md allowlist vs discovery | pending |
| 20 | Malformed full id vs unknown | pending |
| 18 | Document scale / dual-source | info |
| 19 | Self-related token | info |
| 21 | create + flock same title | info |
| 22 | Planned auto-commit outside git | info |

Note: body sections below are the original fitness report (pre-fix wording). Treat the
progress table as source of truth for what was decided; do not re-litigate Fixed rows from
the body text alone.

## Method

- Full read of `design.md` (Problem through Decisions index).
- Cross-checks on: create durability, archive vs next, collision repair vs edge kinds, full-id grammar, search/list stdout, order append set, colour/TTY, read-command lists, config precedence wording, git state layout, skill/external coupling, token table completeness, dead OPEN/IDEA legend.
- AGENTS.md and local environment notes used only for product intent and agent-doc style expectations; design body remains source of truth for product rules.

## What fits (baseline)

These pieces reinforce each other and are not listed as defects:

- Files as source of truth; SQLite index derived; registry machine-local XDG.
- Flat project ids (`<scope>-<short-id>`), mint length 4, collision budget, bit-identical repair, read-path never mutates.
- Single-file reorder via integer+fraction `order` and `keyBetween`.
- autoCommit trichotomy (pj-driven / repo-driven / plain-files) and honest repo-level coupling for sync/freeze.
- `name_drift:` fail-closed (forget + import; no auto-rekey).
- `parse_error` quarantine vs complete-state mutator refuse; `get` still hands path.
- On-disk `depends`/`related` full ids only; cross-scope gate held-not-surfaced when unresolvable.
- Absolute path hand-off; no `--json` in v1.
- Pure Go, `modernc.org/sqlite`, CUE end-to-end, external git subprocess.
- Diagnose-by-default doctor; mutating repair only on named seams (`sync` integrity / `doctor --repair` / explicit re-space).

The friction is mostly where a later product story was layered onto an earlier principle without a reconciliation pass.

---

## Real tensions

### 1. `pj create … done` vs complete-state self-commit

Severity: high — product story and durability rule disagree.

Locked facts:

- `pj create` defaults to `draft`; optional second positional may be **any** known status, including `done` / `cancelled` / custom done-class, to **record work already finished** without fake draft→todo→done ceremony (Status and dependencies; CLI surface; skill Capture).
- Scaffold is still frontmatter + H1; agent may fill a short body the same turn.
- `pj create` is the deliberate exception: incomplete artifact by design and **does not commit**. Complete project commits at next `pj sync` snapshot when git is ready (Sync model, Writes).
- Principle stated immediately after: self-commit when the verb yields a **complete** state and git self-commit is available; defer to sync when it yields a scaffold.
- Complete-state mutators named elsewhere: `status` / `reorder` / `archive` only.
- Skill durability after create: on disk and in the index only; not durable-in-git / not durable-remote until mode-appropriate boundary. Explicit orphan-draft risk if session crashes before that boundary.

Tension:

Recording finished work is sold as complete work, then treated as an incomplete scaffold for commit purposes. On auto-commit, “I just finished X” is not local-git durable until end-of-turn sync — same durability class as a bare draft. That is coherent only if create is *always* body-incomplete; then the “record finished work” story overclaims. It is incoherent if create-with-terminal is meant to be a durable record in the same sense as `pj status … done`.

Resolution options (design must pick one and align skill/CLI/sync prose):

1. Create never self-commits, including terminal status — keep scaffold rule; document as explicit exception to complete-state principle; tone down “record finished work” durability expectations (still needs sync/host commit).
2. Create with terminal status is complete-state and self-commits (body still optional short note; awkward if agent fills body after create and expects one commit).
3. Create never takes terminal status; recording finished work is `create` + body edit + `pj status done` (status self-commits on auto-commit).

Today none of these are stated cleanly; (1) is closest to the letter of “does not commit” but fights the finished-work marketing in the status positional.

Relevant anchors: create exception and principle (~1501–1507); create status positional (~2297–2309, ~2569–2579); skill Capture durability (~2785–2789).

### 2. Archive location vs `pj next` / board eligibility

Severity: high — rules allow historical paths to re-enter the agent queue.

Locked facts:

- “Done is a filter, not a fate.” Archive is optional filesystem declutter of **terminal** projects into `archive/` (Done and archive).
- After archive: still indexed, searchable, resolvable (`get` / `meta` / `search` / `deps`). Default list hides done-class including archived; `--all` brings them back.
- No `unarchive`; no automatic move back to flat dir on status change. Reopen theoretically possible via `pj status` (labels, not workflow) but not intended; do not hand-rename out of `archive/`.
- Doctor soft-warns non-terminal under `archive/` (`archive_non_terminal:`). Explicitly: hygiene only — **no auto-move, no status refuse, no unarchive**.
- `pj next` eligibility: only built-in `todo` with deps terminal, ordered, lens (and empty-queue diagnostics). No mention of excluding `archived` flag.
- Index: move into `archive/` re-keys by unchanged id and flags `archived` (Invalidation).
- Archive is “storage location, not a second board axis”; no `--archived` list flag; `--all` includes archived rows when status filters allow.

Tension:

Sequence allowed by the letter of the design:

1. `pj archive <id>` (terminal).
2. Later `pj status <id> todo` (or `in-progress`) — not refused.
3. File remains under `archive/`; doctor may warn `archive_non_terminal:`.
4. `pj next` can return that project as first ready work while the path is still historical.

That fights skill/archive prose: “treat archive as history,” “resurrecting archived work with status is legal but not normal,” and “historical record.” The third behaviour (still next-eligible under `archive/`) is what the rules produce without saying so.

Also underspecified for list: `pj list todo` with an archived non-terminal — does the archived flag filter, or only status? “Storage location not board axis” suggests status filters alone apply, so archived todos appear in `list todo` without a location marker unless list columns include one (they currently do not; see underspec).

Resolution options:

1. `next` (and default runnable / active board semantics) exclude `archived` until a designed unarchive or path move exists.
2. Status change to non-terminal under `archive/` is refused (or auto-unarchives — needs a verb or automatic reverse move).
3. Reopen is intentionally next-eligible while path stays under `archive/`; document that agents must accept archive paths from `next`/`get` and that `archive_non_terminal:` is expected until human relocates the file.

Relevant anchors: Done and archive (~2351–2378); next eligibility (~2032–2044, ~2273–2275, ~2564–2568); skill Archive (~2919–2926); invalidate archived flag (~1319).

### 3. Collision loser pick counts only `depends`; verify records all inbound edges

Severity: medium — asymmetric edge-kind policy without an explicit rationale.

Locked facts:

- Repair loser selection: choose side by inbound **`depends`**, in-scope and cross-scope via machine-wide `edges` table. Rename the side nothing depends on. Cross-scope inbound weighs at least as heavily as in-scope because repair cannot rewrite other repos (Project ids repair procedure).
- Later restatement: tie-break “counts cross-scope inbound **depends**” (Status and dependencies, cross-scope edges DECISION).
- After rename: rewrite every in-scope `depends`/`related` that pointed at old full id.
- Record **every** cross-scope inbound edge to the collided id for doctor confirmation (“target was collision-repaired — verify this reference”); silent mispoint hazard for references that meant the renamed side.
- `edge_verify:` token: cross-scope edge may mispoint after id-collision repair or scope rename.
- `related` is soft, non-gating; same on-disk full-id shape as `depends`; rewrite in-scope related on rename.

Tension:

A project referenced only via cross-scope `related` can lose the id race (depends-only pick) while still generating `edge_verify:` noise for those soft edges (if “every inbound edge” includes related). Alternatively, if verify is depends-only, the prose “every cross-scope inbound edge” is over-broad.

If intentional: gates protect identity; related is cosmetic; still flag related mispoints for humans. Say that explicitly.

If related silent mispoint is as bad as depends for provenance graphs: loser pick should count related inbound (at least cross-scope) the same way.

Relevant anchors: repair procedure (~184–244); cross-scope DECISION (~2136–2150); token table `edge_verify:`.

### 4. Full-id “closed shape” regex is looser than the real short-id grammar

Severity: medium — invites wrong validators and noisy `links` warnings.

Locked facts:

- Short-id alphabet drops `i`/`l`/`o`/`0`/`1`; first character always a letter; length 4–8; create mints 4 (Project ids).
- On-disk edges: full project id only; closed shape given as  
  `^[a-z0-9]{1,12}-[a-z0-9]{4,8}$`  
  **with** the short-id alphabet/length rules from Project ids (not merely “contains a hyphen”) (~2099–2104).
- Validation: non-legal full id → hard `schema_error:`; do not materialize into `edges`.
- `links` soft-warn if entry matches “full project-id shape (`^[a-z0-9]{1,12}-[a-z0-9]{4,8}$` per short-id grammar)” (~2130–2134).

Tension:

The regex alone accepts invalid short-ids (`api-10il`, `wc-0000`, ids with `i`/`l`/`o` in the short part, digit-leading short-id after the hyphen, etc.). Correct implementation must always apply the real grammar; the “closed shape” line still looks like a complete validator. Links soft-warn using the loose shape increases false positives on ticket-like strings beyond the real id space.

Prefer one closed production (or a named grammar reference without a misleading regex), and make links soft-warn use the same predicate as edge validation.

---

## Underspecification that will force guessing

### 5. `pj search` has no stdout contract

Severity: high for a first-class agent verb.

Locked facts:

- Path-centric interface: locate/mutate verbs print one absolute path; list prints summary; no `--json` (CLI surface; skill).
- Hot-path stdout table covers list, next, get, meta, deps, create, status, edit, reorder — **not** search.
- Query surface / CLI: `pj search <terms> [--scope S]` — FTS5 over titles and bodies, bm25, phrase/prefix/boolean, machine-wide by default, `--scope` to bound. No line format, no ranking display, no path column decision.
- Skill: `summary` is one-line what/why for list/search; title extraction shared for list/meta/search display — still no search line shape.
- Archive: search still finds archived projects.

Missing for implementers and agents:

- Does search print id, title, summary, score, scope, status, path, or some subset?
- One line per hit? How ordered (bm25 only)?
- How does an agent open a hit — parse id then `pj get`, or is path on the line?
- Empty result: exit code and stderr?
- Interaction with lens? (Not mentioned; probably none.)
- Duplicate-id rows: “board/search verbs may surface both rows” (~328–329) — confirm search is in that set and what “surface” means.

This is the largest hole on a verb the design treats as load-bearing for machine-wide FTS (SQLite justification).

### 6. `list` columns are ellipsis

Severity: medium.

Locked facts:

- Hot-path: `Summary (id, title from body H1 rules, status, waiting-on, …) — no paths by default`.
- Frontmatter `summary`: optional one-line what/why for lists; three human-facing names may diverge (slug / H1 / summary).
- Title extraction: do not use slug or summary as fallback title; summary is its own field **when shown**.
- Sort: `(order, id)`.
- Lens echoed in human output; waiting-on annotations for unmet deps; optional stale `in-progress` annotation on list.
- No paths by default; optional later path column (not v1).

Missing:

- Closed column set for v1 (is `summary` shown? tags? order key? archived marker? scope when `--scope`?).
- Whether non-default filters change columns.
- Stable field order for agents that parse text (design rejects JSON but still needs a stable text shape if agents use list).

### 7. Create append: what is `last`?

Severity: medium.

Locked facts:

- `pj create` always appends with `keyBetween(last, null)`; no create order flags.
- `keyBetween` text: left = **current last key**, or null,null when scope has no projects yet (~538–539).
- Skill: reorder after promote if work should not sit at the **end of the board**.

Missing:

- Is `last` the maximum `order` among **all** projects in the scope (including done, cancelled, backlog, archived)?
- Or among active / non-archived / non-terminal-excluded sets?

Consequences:

- All projects: create always appends after historical max — fine for global rank space, but “end of the board” is not “end of active queue.” Done projects reordered `--last` push new creates further out.
- Active-only: need a precise set definition (default list set? non-archived? non-done-class?).

Without a rule, two implementers diverge on create keys and on whether “append” matches agent intuition.

### 8. Colour is TTY-only but unspecified

Severity: medium for agent token stability.

Locked facts:

- pj is non-interactive; never prompts; everything is flag or deterministic default; **only TTY-sensitive behaviour is colour** (~878–880).
- Machine-actionable integrity uses closed stable tokens as line prefixes on stderr (and doctor lines). No `--json`.
- Agents must never ignore closed-set tokens; prefer matching prefixes.

Missing:

- When colour is on/off (TTY on stdout? stderr? both?).
- Whether ANSI may wrap or interrupt token prefixes (e.g. coloured `duplicate_id:`).
- `NO_COLOR` / `FORCE_COLOR` / `--color` policy.
- Guarantee: non-TTY (agent pipes) never emits ANSI on token lines.

Recommendation direction (not a design decision, only fitness note): tokens must remain regex-stable; colour only on human decoration, never inside the token prefix, off when not a TTY.

### 9. External org coupling inside a product skill contract

Severity: low–medium (portability / product boundary).

Locked facts:

- Body conventions (locked skill section) require project writing-guide shape via `start get project/writing` / equivalent org guide (Goal, Scope, Current State, …).
- CLI does not model tasks or sections — convention only.

Tension:

`pj` is designed as a standalone CLI (GitHub `start-cli/pj`). Locking skill body to a `start` org guide couples the product contract to another tool/org. Fine as an example or recommended default for this author’s fleet; odd as a hard skill subsection for all consumers. Prefer “org project writing guide if any; otherwise minimal Goal / Acceptance” or keep sections as recommendation without naming `start get`.

### 10. Doctor scope selection for repair

Severity: low.

Locked facts:

- `pj doctor [--reindex] [--repair] [--re-space-order]` — no `--scope` in the flag set.
- `--repair` scope: ambient scope, or every registered scope when no ambient / when doctor runs as discovery over the machine (~1385–1388 area).

Missing:

- Explicit statement that ambient resolution for doctor uses the same precedence (`--scope` if added later / `PJ_SCOPE` / longest-prefix code-root).
- How to repair exactly one non-ambient scope without repairing all (today: make it ambient via cwd or `PJ_SCOPE`, or run with no ambient and repair all — heavy).

Probably acceptable for v1; should be stated so agents do not invent `pj doctor --scope`.

### 11. `pj edit` stdout and commit behaviour (minor)

Severity: low.

- Hot-path table: edit opens `$EDITOR`; “human convenience only” with no stdout contract line.
- Edit does not rewrite frontmatter itself; may open parse_error paths.
- Editor saves are direct edits → committed at `pj sync` (auto-commit), not self-commit on editor exit.

Mostly consistent; a one-line “no path on stdout / no self-commit” would close agent inventiveness.

---

## Smaller inconsistencies and dead framing

### 12. Read-commands lists omit or include `meta` unevenly

- “Reads never touch git” DECISION lists: `next` / `list` / `get` / `deps` / `search` / `query` — **omits `meta`** (~1465).
- Repo-driven dirty health pure-reads list: `next` / `list` / `get` / `meta` / `deps` / `search` / `query` (~1761).
- `meta` is pure read, git-free, elsewhere.

Same intent; lists should match. Also confirm `skill` / `doctor` / `scope list` / `help` stay outside “project read commands” cleanly (discovery).

### 13. Config “later overrides earlier” overclaims

- Two tiers: (1) XDG registry/lens, (2) scope `pj.cue`; “least to most specific; later overrides earlier” (~1079).
- Env `PJ_SCOPE` and flags `--scope` override ambient resolution.

XDG and `pj.cue` are different concerns (machine registration vs scope schema/autoCommit), not layered overrides of the same keys. Precedence language fits ambient scope resolution (flag > env > registry match), not the two config tiers. Misleading for implementers building a config stack.

### 14. Git state layout split

- Git-root lock: `<git-root>/.git/pj-sync.lock` (~1518).
- Last-push-error: `<git-root>/.git/pj/last-push-error` (~1699).

Two pj-owned layouts under `.git` without a single convention (all under `.git/pj/…` vs top-level lock). Works, but slightly untidy; easy to unify when implementing.

### 15. Status legend OPEN / IDEA unused

- Legend still defines OPEN and IDEA (~7–10).
- Body has no `OPEN:` / `IDEA:` markers in practice (only the legend lines).
- Open questions: “None outstanding for product open design.”

Dead legend machinery. Either drop OPEN/IDEA from the legend or use them for residual design debt (this report’s issues could become OPEN markers if desired).

### 16. `archive_non_terminal:` “optional add when implementing” is stale

- Done and archive: “Optional stable token: `archive_non_terminal:` (add to the closed integrity set when implementing doctor report lines)” (~2376–2378).
- Closed v1 token set already includes `archive_non_terminal:` (~3100).

Prose should treat the token as locked, not optional-to-add.

### 17. Snapshot allowlist vs recommended `AGENTS.md` paths

- Allowlist includes optional `AGENTS.md` at **dir root only** (scope directory).
- Discovery encourages human-authored AGENTS.md in the **repo** for cold start (often repo root, not scope dir).

Not a contradiction (repo root AGENTS.md is outside allowlist and host-managed), but agents may expect pj to commit repo-root AGENTS.md on sync — it will not. Worth one sentence under allowlist or discovery.

### 18. Document scale and repetition

- ~3228 lines / ~34k words for a pre-implementation design.
- Agent skill contract (~§ Agent skill contract through doctor tokens) largely restates CLI, status, sync, and integrity rules with “locked” framing.

Fit note (not a logic bug): right if `pj skill` is a rendered extract of the design; heavy as the sole agent context (local agent-doc guidance prefers token efficiency). Risk of drift between skill locked body and earlier DECISION sections if one is edited without the other. Design claims skill is authoritative extract and body wins over Decisions index — skill vs earlier sections is the real dual-source risk.

### 19. Related hygiene vs depends (clarity only)

- Self-depends: hard `depends_self:`.
- Self-related: soft `schema_warn:`.
- Duplicate list entries: soft `schema_warn:` for depends/related/tags/links/customs.

Consistent with soft related, but token table does not give a dedicated self-related token (folded into `schema_warn:`). Fine if implementers do not invent `related_self:`.

### 20. Id token parsing edge cases (implementer detail)

- Token containing `-` always full id: scope = `^[a-z0-9]{1,12}$` before first `-`; remainder = short-id (~330–332).
- Invalid remainder (wrong alphabet, length, digit-leading short-id): treat as unknown id / non-zero exit — not fully spelled as schema vs unknown.
- Full id with repaired length 5–8: accepted. Good.

Minor: distinguish “malformed full id” (exit 2 usage?) vs “well-formed but unknown” for agents.

### 21. `create` + flock + same title

- Same-title creates: same slug, different ids; basename unique because of id prefix. Good.
- Online concurrent create serialised by scope flock; draw → check local ids → write. Good.
- Offline same-id same-title add/add: repair, never field-merge. Good.

No defect found; noted as solid.

### 22. Planned auto-commit outside git

- Init outside git + `--auto-commit`: planned; writes succeed; self-commit and sync disabled with `sync_disabled:` until repo/remote exist.
- Aligns with never `git init` via pj.

Consistent; create still does not commit even after git appears until sync (scaffold rule) — same tension as issue 1 for terminal create.

---

## Token / doctor surface cross-check

Closed v1 tokens (design table) cover the major classes. Notes:

| Token | Fit note |
|---|---|
| `duplicate_id:` / `equal_order:` | Align with post-reconcile warn + refuse id-taking + repair seams. Solid. |
| `order_long:` | Soft; re-space only via `--re-space-order`. Solid. |
| `parse_error:` | Quarantine + mutator refuse + get path. Solid. |
| `unreachable_scope:` | Single token for all dir-unusable modes. Solid. |
| `non_allowlist:` | Sync/doctor residue. Solid. |
| `config_unparseable:` | Covers pj.cue and XDG CUE. Solid. |
| `status_conflict:` | Mid-rebase and residue. Solid. |
| `depends_*` family | Cycle, dangling, self, unresolvable, on_cancelled. Solid. |
| `edge_verify:` | See issue 3 (depends vs all edges). |
| `related_unresolvable:` | Soft. Solid. |
| `auto_commit_mismatch:` | Sync preflight + doctor. Solid. |
| `archive_non_terminal:` | See issues 2 and 16. |
| `sync_disabled:` / `last_push_error:` / `uncommitted:` | Mode-specific. Solid. |
| `stale_in_progress:` | 72h mtime; no auto-reopen. Solid. |
| `name_drift:` | Fail-closed. Solid. |
| `schema_error:` / `schema_warn:` | Catch-all soft/hard. Self-related and links id-like live under warn. |

No missing token identified for a major already-described class, except possible desire for distinct “malformed full id on CLI” vs unknown id (not required if all non-zero with stderr text).

---

## Suggested cleanup priority

If revisiting the design before implementation:

1. Reconcile **create + terminal status** with self-commit / durability / skill Capture (issue 1).
2. Define **archived participation** in `next` and status-filtered list when non-terminal under `archive/` (issue 2).
3. Specify **`pj search` stdout** (and agent open path) (issue 5).
4. Pin **list columns** and create **`last` order set** (issues 6–7).
5. One **full-id grammar**; align links soft-warn (issue 4).
6. Clarify **depends-only loser pick** vs **all-edges `edge_verify:`** (issue 3).
7. Colour/TTY guarantees for **token lines** (issue 8).
8. Housekeeping: read-command lists, config precedence wording, git `.git/pj/` layout, OPEN/IDEA legend, stale “optional token” sentence, skill `start get` coupling (issues 9–18).

---

## Out of scope for this report

- Whether SQLite / CUE / fractional indexing are “worth it” (locked with rationale).
- Multi-scope auto-commit freeze domain (accepted TRADEOFF).
- Single-user fleet name uniqueness assumption (accepted).
- Windows out of scope (locked).
- Viewer deferred design.
- agentdex skill install placeholders (hard-refuse until integration).
- Implementation PR plan (design has none; open questions empty).
- Editorial-only prose nits that do not change behaviour.

---

## Summary

The core model (files as SoT, flat ids, fractional order, machine-wide index, deliberate registration, closed repair budget) is internally consistent.

The main fitness failures are edge reconciliations:

- **Create-as-done** is complete work in product language and incomplete scaffold in commit language.
- **Archive** is historical storage but does not exclude reopened projects from **next**.
- **Search** is justified as a pillar of the index yet has no agent stdout contract.
- **List / order-append / colour / full-id regex** leave implementers room to invent behaviour the design claims is closed.

Address those before coding the CLI surface and skill renderer, or the first implementation will freeze accidental choices.
