# P1 Foundation contract packages

## Goal

Establish the `pj` Go module and land the wire-contract algorithms as pure, table-tested
packages with zero I/O: id predicates and minting, `slugify`, the `order` fractional-index
grammar, the frontmatter model, the status set and terminal predicate, title extraction,
and scope auto-name derivation. These are the load-bearing primitives every later project
composes, so they must be correct and fixture-verified before any CLI, config, index, or
git work exists.

## Scope

In scope:
- U1 Id predicates and generation: `IsScopeName`, `IsShortID`, `IsFullProjectID` as one
  shared pure helper; `crypto/rand` mint of a length-4 short-id; the deterministic
  collision-repair short-id extension (append-only, cap 8).
- U2 `slugify`: the closed slug grammar and deterministic algorithm (NFKC, 48-byte cap).
- U3 `order` wire format: the base-62 integer+fraction grammar, validity check, and
  `keyBetween` (plus the integer inc/dec and midpoint helpers it needs).
- U4 Frontmatter model: fence/body split, YAML parse and serialize, a raw fence-slice API
  (verbatim interior extraction for later `pj meta`), the closed built-in key set, and
  tolerance of the transient `status_conflict` key.
- U5 Status set and terminal predicate: the eight built-in statuses, their category
  matrix, and a terminal predicate that takes schema-provided custom-status categories.
- U6 Title extraction: the ATX-H1-only shared helper.
- U7 Auto-name derivation: the closed procedure over a code-root basename string.
- Module bootstrap: initialise the Go module, standard layout, `.golangci.yml`, and update
  `AGENTS.md` with real build/test/lint commands and the Go-CLI-guide advisory note.

Out of scope (named siblings own these):
- Any filesystem, CUE, SQLite, or git I/O. These packages take values in and return values
  out. Reading `pj.cue` to obtain custom statuses/fields is P2; U5's terminal predicate
  consumes the parsed categories, it does not load them.
- The id-collision loser-pick total order, `edge_verify:` reporting, and multi-file rewrite
  durability — those belong to the repair machinery in P5 (and the merge package in P6).
  U1 provides only the extension algorithm (given a prefix and an occupied set, produce the
  next unique short-id); it does not decide which side loses or rewrite files.
- The CLI framework, Cobra wiring, exit-code shim, and colour/TTY handling — P2 (U27).
- Any `order` re-space / doctor repair behaviour — P5. U3 provides generation and
  validation only.

## Current State

The repository is pre-implementation. It holds `design.md` (the authoritative landed
design), `AGENTS.md` (repo context and intended stack), `LICENSE`, and the seven project
documents. There is no Go module, source, tests, or build yet. This is the first project in
the wave; nothing else is built. The repo is otherwise greenfield.

`design.md` is fully landed (`DECISION:` markers throughout, "Open questions: None
outstanding"). Treat it as the source of truth for intent.

## References

- `design.md` — authoritative design. Read these sections for this project:
  - Core model → Project ids (id predicates, short-id alphabet, mint length 4, the
    deterministic collision-repair extension procedure and `SHORT_ID_MAX`, `slugify`
    grammar and algorithm and its fixtures).
  - Metadata (frontmatter) — the built-in key set and `status_conflict`; and the `order`
    wire format, validity grammar, `keyBetween` rules, and the `keyBetween` fixture table.
  - Status and dependencies — the eight built-in statuses, the custom-status category
    matrix, and the terminal predicate definition.
  - Scope names — the `--auto-name` closed derivation procedure and its alphabet.
  - Agent skill contract → Body conventions — the title-extraction DECISION (ATX-H1-only).
- `AGENTS.md` — repo state and the pure-Go/no-cgo invariant.
- Project writing guide — `start get project/writing`. Each output document follows it.
- Go CLI design guide — `start get golang/design/cli`. Advisory. Adopt its standard Go
  layout (`cmd/pj/main.go` minimal, `internal/…`), table-driven tests with `testdata/`, and
  `.golangci.yml`. It is subordinate to `design.md` on every conflict.

## Requirements

1. Initialise the Go module for `pj` (module path per the repository's canonical import
   path) targeting a current stable Go release, and establish the standard layout: a
   minimal `cmd/pj/main.go` placeholder and `internal/` packages for the primitives below.
   The module must build with no dependencies beyond what these pure packages need
   (standard library plus a YAML library for U4 — no CUE, SQLite, or git dependency yet).
2. Add `.golangci.yml` and wire `gofmt`, `go vet`, `go build ./...`, and `go test ./...`
   as the repository's verification commands.
3. Update `AGENTS.md` to record: the real build, test, lint, and format commands; the
   module path and Go version; and a one-time note that the Go CLI design guide is
   advisory and `design.md` overrides it on the points listed in that guide's override
   table (so later project documents inherit this baseline rather than restating it).
4. U1 — one shared pure helper implements `IsScopeName` (`^[a-z0-9]{1,12}$`), `IsShortID`
   (length 4 through 8; every character in `abcdefghjkmnpqrstuvwxyz23456789`; first
   character a letter from that alphabet, never a digit — not equivalent to
   `^[a-z0-9]{4,8}$`), and `IsFullProjectID` (exactly one `-` at the split; scope before it
   passes `IsScopeName`, remainder passes `IsShortID` and contains no `-`). Every call site
   in later projects uses this one helper; there is no standalone full-id regex.
5. U1 — a `crypto/rand` short-id mint producing length-4 ids: first character always a
   letter from the 23-letter subset; positions 2–4 each a 50/50 coin-flip between the
   letter subset and the digit subset (`23456789`), uniform within the chosen class. The
   mint is a pure generator given a randomness source; the draw→check→redraw loop against
   existing ids belongs to `pj create` (P4) under the scope flock, not here.
6. U1 — the deterministic collision-repair short-id extension: given a loser's current
   short-id (`prefix`, length N) and the occupied short-id set, append characters from the
   ordered 31-character alphabet `abcdefghjkmnpqrstuvwxyz23456789`. For target length
   L = N+1 … `SHORT_ID_MAX` (8), enumerate every extension string of length L−N in
   lexicographic order and return the first `prefix+extension` not in the occupied set. Use
   no `crypto/rand` on this path (two machines must mint the same repaired id). If no free
   candidate exists at any L ≤ 8, return a hard failure — never invent a non-prefix id,
   max+1, or a renumber scheme.
7. U2 — `slugify(title)` as a pure package implementing the closed grammar
   (`^[a-z0-9]+(-[a-z0-9]+)*$`, length 1–48 bytes) and the exact deterministic algorithm:
   NFKC normalise, map ASCII `A–Z`→`a–z`, keep ASCII alnum, treat every other character as
   a separator, join non-empty tokens with single `-`, fall back to `x` when empty,
   truncate to ≤48 bytes preferring a cut at the last `-` ≤48 (hard-cut a single long
   token; strip any trailing `-` a hard cut leaves, and fall back to `x` if truncation
   yields empty). It never consults the filesystem, locale, or a uniqueness pass — identical
   titles yield identical slugs. An empty title after trimming is a usage-level condition
   the caller (P4 `create`) rejects before calling; the package itself contracts on a
   non-empty input.
8. U3 — the `order` grammar and `keyBetween` as a pure package: the 62-character alphabet
   (ascending = ASCII byte order = rank order), the head-encoded integer part with its
   length formula, the fractional part rule (may be empty; must not end in `0`), the closed
   validity check, `INTEGER_ZERO` = `a0` and `SMALLEST_INTEGER`, and `keyBetween(left,
   right)` for all four bound combinations including integer widen on overflow/underflow and
   fraction growth on densify. Byte-wise string comparison must equal rank order. Prefer a
   faithful port of the Rocicorp / Figma fractional-indexing construction that emits this
   exact format. Invalid or exhaustion cases return errors (no neighbour renumber).
9. U4 — a frontmatter model package: split a file into the leading `---`…`---` fence
   interior and the body; parse the interior YAML into the built-in keys and any additional
   keys; serialize back to clean YAML (the serializer must emit `order` quoted —
   `order: "a0"` — so the mixed digit/letter key stays a string and cannot be YAML-coerced,
   per the design's always-quoted wire-contract rule); and expose a raw fence-slice API that
   returns the verbatim interior bytes without re-encoding (for P3's `pj meta`). The built-in key set is
   the closed, immutable list `id, status, order, depends, related, tags, created, links,
   summary` plus the transient `status_conflict`. The model tolerates `status_conflict`
   being present (it is not an error) and preserves undeclared keys rather than dropping
   them.
10. U5 — the status package: the eight built-in statuses (`draft, backlog, todo, review,
    in-progress, blocked, done, cancelled`) with their `pj next` / default-list / terminal
    behaviour, and a terminal predicate. This package defines the closed `Category` set
    (`active`, `backlog`, `done`) that the predicate keys on; P2 parses `pj.cue` into this
    type and does not redefine it. Terminal is true for built-in `done`/`cancelled`
    or any custom status whose category is `done`; the predicate takes the custom
    status→category mapping as an argument (P2 supplies it from `pj.cue`; this package does
    not load CUE). Only built-in `todo` is ever next-eligible.
11. U6 — the title-extraction helper: scan the body only (after the closing fence),
    recognise ATX H1 only (`^#\s+.+`, one `#`), strip the marker and surrounding
    whitespace, return the first such line's text; ignore setext underlines and later H1s;
    return an empty string when none matches (never an error, never a slug/summary
    fallback).
12. U7 — the auto-name derivation: a pure function over a code-root basename string
    implementing the closed procedure — tokenise on `[-_. ]+` and camelCase boundaries,
    build the initials/first-two seed, restrict to the auto-name alphabet (letters
    `abcdefghjkmnpqrstuvwxyz`, digits `23456789`), ensure a letter leads, cap at 12, and
    accept only `^[a-z][a-z0-9]{0,11}$` within that alphabet. Otherwise return a hard error
    signalling "cannot derive — pass `--name`". Never auto-suffix or invent a fallback name.
    The registration-collision check (derived name already registered) is P2's, not this
    function's.

## Constraints

- Pure Go, no cgo (per `AGENTS.md`). These packages use only the standard library plus a
  YAML library for U4. Do not introduce CUE, SQLite, or a git dependency in this project.
- Zero I/O in every package here: no filesystem, network, environment, time-of-day, or
  randomness except U1's mint, which takes its randomness source as an input so it stays
  testable and deterministic under test. Push all nondeterminism to the caller's edge.
- The three shared grammars are wire contracts, not display helpers: `slugify`, the `order`
  format, and the id predicates produce durable, synced, cross-machine bytes. Do not
  "improve" the alphabet, head rule, sort order, or slug mapping — a change is a versioned
  migration, not a dependency bump. Swapping the `order` library must not change emitted
  strings' sort meaning.
- `SHORT_ID_MAX = 8`, `SLUG_MAX = 48`, `INTEGER_ZERO = a0`, and the fractional-index
  alphabet are fixed constants from the design; encode them as named constants, not magic
  literals.

## Implementation Plan

1. Initialise the module, create the `cmd/pj/main.go` placeholder and the `internal/`
   package skeletons, add `.golangci.yml`, and confirm `go build ./...` and
   `go test ./...` run clean on the empty skeleton. Update `AGENTS.md` (Requirement 3).
2. U3 order and U2 slugify first, fixtures-first: the design mandates each ships as a pure
   package with its table-driven fixtures passing before any wiring. Encode the design's
   `keyBetween` fixture table verbatim (null/null → `a0`; repeated append strictly
   increasing; repeated prepend strictly decreasing; same-integer densify grows length and
   never ends in `0`; open interval across an integer boundary; equal keys → no between;
   invalid keys rejected; integer overflow/underflow widen; floor/ceiling exhaustion hard
   errors; byte-wise sort equals rank order; every emitted key round-trips through
   validation). Encode the slugify fixtures verbatim (empty → caller usage error;
   `"Network Output Redesign"` → `network-output-redesign`; punctuation/emoji as
   separators; CJK-only → `x` or NFKC-ASCII; long title → ≤48 and valid grammar; identical
   titles → identical slugs).
3. U1 id predicates and mint and the extension algorithm, with table-driven tests that
   include the illegal cases the loose regex would wrongly accept (`api-10il`, `wc-0000`,
   digit-leading short parts, dropped-alphabet characters) and the extension's normal N=4→5
   growth, occupied-set skips, and cap-8 exhaustion hard fail.
4. U4 frontmatter model: fence split, parse, serialize, and the raw fence-slice API, with
   tests that preserve exact interior bytes (key order, quoting, comments, blank lines,
   customs, `status_conflict`) through the raw API and round-trip the parsed model.
5. U5 status set and terminal predicate, U6 title extraction, U7 auto-name derivation, each
   with table-driven tests (U7 including `web-control`→`wc`, `webctl`→`we`, and
   `ill`/`101`/empty → hard error).
6. Run the full verification suite and confirm every package is I/O-free (a quick check
   that nothing imports `os`, `net`, `time` for wall-clock, or a CUE/SQLite/git package).

## Implementation Guidance

- Prefer one shared fractional-index algorithm (a Rocicorp-style port) so the exact
  midpoint strings match the design's fixtures and stay stable across any later
  re-implementation or agent test. Sort correctness only needs grammar + valid-key +
  byte-order agreement, but identical generation is what keeps fixtures and fleet behaviour
  boring — aim for it.
- Interfaces belong at the consumer. These packages export concrete pure functions and
  types; later projects declare whatever narrow interfaces they need over them.
- For U4, keep the raw fence-slice path independent of the YAML decode path: `pj meta`
  (P3) must print the interior verbatim without a re-encode round trip, while writers (P4)
  need the decoded model. Do not force one through the other.

## Acceptance Criteria

- `go build ./...` and `go test ./...` pass; `.golangci.yml` is present and the module is
  initialised with the layout above.
- `AGENTS.md` records the real build/test/lint/format commands and the one-time Go-CLI-guide
  advisory note.
- U3 passes the design's full `keyBetween` fixture table, including: `keyBetween(null,
  null)` = `a0`; repeated append yields strictly increasing valid keys; repeated prepend
  yields strictly decreasing valid keys; same-integer densify grows the fraction, stays
  strictly between, and never ends in `0`; equal-key input produces no between (error/
  undefined, never an invented key); invalid keys (trailing-`0` fraction, bad head, empty,
  non-alphabet) are rejected; integer overflow (`a9`→`b00` class) and underflow (`Z0`→wider
  negative head) widen; true floor/ceiling exhaustion hard-errors; random valid key pairs
  sort byte-wise in rank order; every emitted key round-trips through validation.
- U2 passes the design's slugify fixtures (empty caller-rejected; the network-output
  example; punctuation/emoji as separators; CJK-only → `x`; long → ≤48 and valid grammar;
  identical titles → identical slugs).
- `IsShortID` rejects `10il`, `0000`, digit-leading, and dropped-alphabet strings while
  accepting create-length-4 and repaired length-5–8 ids; `IsFullProjectID` rejects
  `api-10il` and `wc-0000` and accepts `wc-ab2c` and a repaired `wc-ab2c9`.
- The collision-repair extension turns a length-4 loser into the first free length-5 id
  over the ordered alphabet, skips occupied candidates, grows length when a length is fully
  blocked, and hard-fails at cap 8 exhaustion — producing the same id on repeated runs with
  the same occupied set.
- U4's raw fence-slice API returns the frontmatter interior byte-for-byte (comments, key
  order, quoting, `status_conflict`, custom keys all preserved) and the parsed model
  exposes exactly the closed built-in key set plus retained undeclared keys. The serializer
  emits `order` quoted (`order: "a0"`) so a round-tripped model keeps `order` a string.
- U5's terminal predicate returns true for `done`, `cancelled`, and a custom status mapped
  to category `done`, and false for `todo`/`in-progress`/`draft` and a custom `active`
  status; only built-in `todo` reports next-eligible.
- U6 returns the first ATX-H1 text, ignores setext and later H1s, and returns empty (not an
  error) when no ATX H1 exists.
- U7 returns `wc` for `web-control`, `we` for `webctl`, and a hard "pass `--name`" error for
  `ill`, `101`, and an empty basename, using only the auto-name alphabet.
