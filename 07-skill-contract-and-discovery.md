# P7 Skill contract and discovery

## Goal

Ship `pj skill` — the authoritative agent contract that prints the locked 18-section
workflow to stdout — plus the discovery paths and the hard-refuse `skill install` family
placeholders. After this project an agent can prime itself from `pj skill` and follow the
same behaviour the rest of `pj` now implements.

## Scope

In scope:
- U26 the agent skill contract: `pj skill` printing the full locked 18-section output as
  agent-facing markdown; the discovery mechanisms; and the `pj skill install` / `list` /
  `uninstall` v1 hard-refuse placeholders.

Out of scope (named siblings own the behaviour the skill describes):
- Every behaviour the skill documents is already implemented by P1–P6. This project renders
  the contract; it must not change, re-decide, or re-specify any rule. Where the skill would
  restate a rule that lives elsewhere in `design.md`, prefer a reference over a divergent
  restatement.
- The real `pj skill install` backend (agentdex integration, concrete skills-directory
  targets, list/uninstall semantics) is deferred by the design; v1 ships only the
  hard-refuse placeholders.
- The web viewer and Windows support are design-deferred and not in this or any v1 project.

## Current State

P1–P6 are complete: the full `pj` CLI runs — pure contract packages; config, registry,
resolution, and scope admin; the read engine and board; the authoring hot path with local
self-commit; integrity repair, doctor, and scope rename; and the sync/merge boundary. Every
verb, token, mode, and rule the skill contract references is real and behaves as the design
specifies. The repo is otherwise as built by P1–P6.

`design.md` is the source of truth. The Agent skill contract section is the authoritative
body of the skill output; the skill extract is subordinate to the earlier `DECISION:` /
section prose. When a skill subsection and an earlier body rule disagree, the body rule wins
and the extract is fixed — never the reverse.

## References

- `design.md` — read these sections:
  - Agent skill contract — the entire locked TOC (Required sections) and all 18 subsections
    with their locked bodies: Core work loop, Capture, Frontmatter mutation, Body
    conventions (including the title-extraction DECISION), Title/slug/filename, Ordering,
    List and filters, Search, Dependencies and impact, Archive, End of turn (by autoCommit
    mode), Conflicts and paused sync, Concurrent agents, Cold start and import, Cross-scope
    work, Waiting and external blockers, Unsupported operations, and Doctor and integrity
    warnings (with the closed token table). This is the authoritative body to render.
  - Discovery — the three bootstrap styles (use `pj skill` directly; the planned
    `skill install`; own handoff), registry-only resolution, no auto-slurp, no tree probe,
    and that `pj scope init` writes no AGENTS.md block.
  - CLI surface — the `pj skill` bullet and the `pj skill install`/`list`/`uninstall`
    hard-refuse placeholder bullet.
  - Borrowed from beads — the provenance of `pj skill` (a `prime`/`onboard` dump) and the
    user-initiated `skill install` intent.
- `AGENTS.md` — repo state; pure Go no cgo.
- Project writing guide — `start get project/writing`.
- Go CLI design guide — `start get golang/design/cli`. Advisory; subordinate to `design.md`.
  The guide's `<cli> help agents` + embedded-markdown technique is fine under `pj skill`; the
  embed technique may be used, but `pj help` stays ordinary Cobra help.

## Requirements

1. `pj skill` prints the agent skill contract to stdout as agent-facing workflow markdown. It
   is a discovery command: no ambient scope is required and it never errors on no-scope. It
   never auto-writes into any tree.
2. The output includes all 18 locked sections, in the design's locked order: Core work loop,
   Capture, Frontmatter mutation, Body conventions, Title/slug/filename, Ordering, List and
   filters, Search, Dependencies and impact, Archive, End of turn (by autoCommit mode),
   Conflicts and paused sync, Concurrent agents, Cold start and import, Cross-scope work,
   Waiting and external blockers, Unsupported operations, and Doctor and integrity warnings.
   No subsection is omitted, no interim folklore is invented, and no skeleton placeholder is
   reintroduced. The contract is complete for v1.
3. Each rendered subsection is faithful to its locked design body — including the core work
   loop, the create-durability-boundary-is-mandatory rule, the frontmatter-mutation table,
   the title-extraction rule, the ordering rules, the list/search/deps contracts, the
   archive rules, the per-mode End-of-turn table (pj-driven / repo-driven / plain-files), the
   conflict/paused-sync handoff table, concurrent-agent rules, cold-start/import, cross-scope
   work, the waiting/external-blockers table, the unsupported-operations table, and the
   Doctor-and-integrity-warnings section with the closed token table and agent rules. Where
   a rule lives elsewhere in the design, reference it rather than restating it in a form that
   could drift; the closed token strings must match P5's authoritative catalogue exactly.
4. The skill output is produced from a maintained rendering (embedded markdown or a
   maintained extract of the section) such that it cannot silently drift from the body:
   changing a locked rule requires updating every skill subsection that restates it (or
   switching that subsection to a pure reference) in the same change. Build the rendering so
   this coupling is enforceable in review — the skill is not a second, independently editable
   source of truth.
5. `pj skill install`, `pj skill list`, and `pj skill uninstall` are registered in the help
   and command tree so agents do not invent paths, and each hard-refuses: a non-zero exit
   with the same clear message (e.g. `not implemented in v1 — use 'pj skill' to print the
   workflow; persistent install is planned via agentdex skills directories`). No fake empty
   list, no success no-op, no write into AGENTS.md or any skills directory, and no agentdex
   dependency in this build.
6. Discovery behaviour matches the design: resolution is registry-only (no tree probe, no
   auto-register on clone, no auto-write of a discovery artifact); the three bootstrap styles
   are supported (print `pj skill` on demand; the hard-refuse install placeholders; a
   human-authored handoff pj neither writes nor auto-commits). `pj scope init` writes no
   AGENTS.md block.

## Constraints

- Pure Go, no cgo. The skill body may be an embedded markdown asset compiled into the binary;
  `goldmark` or any markdown renderer is not required — the output is emitted text.
- The skill extract is subordinate to the design body: when a skill line and an earlier
  `DECISION:` / section prose disagree, the body wins and the extract is corrected. Do not
  let the skill introduce or soften any rule.
- `design.md` overrides the Go CLI design guide. Carry these into this project:

  | Go CLI guide default | design.md rule (authoritative) |
  |---|---|
  | Agent help via `<cli> help agents` + embedded markdown | `pj skill` is the agent contract with its own locked 18-section output; `pj help` stays ordinary Cobra help; the embed-markdown technique is fine under `pj skill` |
  | `--json` + JSON envelope | No `--json`; `pj skill` emits agent-facing markdown text; paths + short text + closed tokens elsewhere |
  | Aliases and interactive prompts | `pj skill install`/`list`/`uninstall` are hard-refuse placeholders (non-zero, same message), never a prompt or a success no-op |

## Implementation Plan

1. Author the skill rendering as an embedded, maintained markdown asset containing all 18
   locked sections in order, faithful to the design body, with the closed token table and
   agent rules from P5's catalogue. Prefer references to the design's own rules over
   divergent restatements where the design already carries the authoritative wording.
2. Wire `pj skill` to emit that rendering to stdout with no ambient-scope requirement and no
   tree writes.
3. Implement the `pj skill install`/`list`/`uninstall` hard-refuse placeholders in the
   command tree with the single shared message and non-zero exit.
4. Confirm discovery behaviour: registry-only resolution, no auto-write, no auto-slurp, and
   `pj scope init` writing no AGENTS.md block (already true from P2 — verify it holds).
5. Verify the output against the design: every locked section present and in order, the token
   strings matching P5's catalogue, and the End-of-turn and Conflicts tables matching the
   real per-mode behaviour P4/P6 implement.

## Acceptance Criteria

- `pj skill` prints all 18 locked sections in the design's order with no scope required and
  no tree write; the output is agent-facing markdown and contains no skeleton placeholders.
- The Doctor-and-integrity-warnings section lists the closed token set using the exact token
  strings from P5's catalogue, and no skill line contradicts an earlier design `DECISION:`.
- The End-of-turn section's per-mode guidance (pj-driven sync mandatory after a create;
  repo-driven host-commit and `uncommitted:`; plain-files disk-only and one-machine
  `--repair`) matches the behaviour P4 and P6 implement; the Conflicts-and-paused-sync
  section matches the mid-rebase refuse and `status_conflict` handoff.
- `pj skill install`, `pj skill list`, and `pj skill uninstall` each exit non-zero with the
  same clear message, write nothing into any tree or skills directory, and pull in no
  agentdex dependency.
- Resolution remains registry-only: no unregistered on-disk scope is discovered, cloning
  auto-registers nothing, and `pj scope init` writes no AGENTS.md block.
