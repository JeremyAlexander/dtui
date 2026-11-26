# ADR-0022: Exception issues are a client-derived, sampled grouping

**Status:** Proposed · **Date:** 2026-11-26

## Context

The exceptions lens on the traces view lists the newest spans that carry an
exception span event, one row per span, showing the first event's type and
raw message. The list is capped at the newest 200 spans, so one loud bug
fills every row and pushes the others out; nothing answers "which distinct
problems are there, and which is loudest".

Span exceptions carry no grouping key in the data. The type alone merges
unrelated bugs (every `NullPointerException` is one row), and the raw message
or stack splits one bug into thousands (ids in messages, line numbers that
move with every deploy, generated proxy class names). The fields a grouping
needs are free text inside `span.events`
([../dev/learnings.md](../dev/learnings.md) §1.11b).

## Decision

dtui derives an **issue key** for each exception-bearing span itself, in Go,
over a bounded sample.

- **The key is versioned.** `e1:` plus the first 12 hex characters of a
  SHA-256 over a canonical string. It depends on no locale and no map order.
  Any change to the algorithm that can move an exception to another group
  ships as `e2`; a golden file of `e1` keys makes that a conscious step.
- **A fallback ladder picks what is hashed.** From a span's exception events
  take the root of the cause chain (the flagged event, or the end of a chain
  linked by ids), else the last. The id-linked step is unverified; see
  "Observed so far". Then: type + top five in-app frames (module and
  function, never line numbers, recursion collapsed); else type + top five
  of all frames; else type + parameterised message; else the type
  alone. Every row shows the rung that produced it, so a coarse group is
  visibly coarse.
- **One primary query, grouped through `Spec` + `Decode`.** The view fetches
  the newest 1000 exception-bearing spans in scope and groups them in
  `Decode` ([ADR-0003](0003-declarative-view-catalog.md)); it is a catalog
  view, not a bespoke screen. The unit is the span throughout: one key per
  span, from the one event the ladder picks, however many exception events
  the span carries.
- **One query, but a heavier one.** The view costs one query per open, which
  is what [ADR-0013](0013-query-budget.md) counts. It is the exceptions
  lens's query with `limit 1000` where the lens has `limit 200`, so it
  returns up to five times the records, each with its `span.events` and
  their full stack traces. That cost is not measured yet. It is bounded by
  the limit, by the timeframe every span query carries and any pinned
  scope, and by Grail's scan limit, which already surfaces as a partial-result notice.
- **The header always says the counts come from a sample.** A group's count
  is its share of the spans the query returned, at most the newest 1000, not
  of the timeframe. A result cut short by the scan limit is a smaller
  sample, and the header gives the number that arrived. This extends
  [ADR-0007](0007-scope-honesty.md) from scope to completeness: the header
  names what the query actually covered.
- **Interactive facets are restricted to span-level fields.** A facet
  narrows the query before sampling, so it can only use fields the server
  can filter on; the issue key and its rung exist only after `Decode`.
- **Exact counts are a later, optional enrichment**, keyed on a strictly
  coarser server-side key (one that every member of a group shares), so the
  exact number is an upper bound for the group and never contradicts it.

Open, to be settled before this is accepted:

- The in-app frame rule: a built-in list of runtime and framework prefixes,
  or a `.dtui.yaml` setting. The classifier sits behind an interface so
  either can supply it.
- Whether the throw-site frame is always part of the key, whatever the
  classifier says about it. It is the candidate remedy for the pass-through
  frames in "Observed so far"; it changes keys, so it must be settled before
  `e1` is pinned.
- Whether this view fits the traces noun's depth cap
  ([ADR-0014](0014-breadth-not-depth.md)). A noun is done at list, detail,
  drills and exits, and beyond that the answer is an exit. A grouped list is
  neither a new tab nor a chart, but it is a second way to list the same
  spans: the maintainers decide whether it is a lens within the cap, a noun
  of its own, or depth that should stay an exit.
- Whether an echo that cannot reproduce the grouping is an acceptable `c`
  exit ([ADR-0001](0001-dql-is-the-substrate.md),
  [ADR-0011](0011-strictly-read-only.md)); see Consequences.
- Whether the payload of 1000 spans with stack traces is acceptable once
  measured, or the sample shrinks or the query projects only the fields the
  key reads.
- Whether RUM errors use their own `error.id` instead of this key.
- Whether the fingerprint code should eventually live in dtctl's SDK, so
  other tools compute the same key.

Rejected:

- **Grouping in DQL.** Frame filtering and proxy collapsing are not
  realistic there, and every refresh would scan stack strings.
- **Type only.** Unrelated bugs that throw the same type become one issue.
- **The full normalised stack.** One bug reached through two callers
  becomes two issues.
- **Hashing line numbers.** Every deploy that moves a line starts a new
  issue for an old bug.

## Consequences

- Issue keys are stable across refreshes, sessions and machines for the same
  algorithm version, but they are dtui's, not the tenant's: nothing in Grail
  can be filtered by one.
- A rare exception outside the newest 1000 spans is absent, not counted as
  zero; the header says so, and narrowing the scope is how the user reaches
  it.
- The command echo (`c`) is `dtctl query` with the view's primary query: it
  returns the sampled spans, ungrouped. The keys, rungs and counts are
  computed in Go and no DQL reproduces them, so the echo hands over the
  input of the grouping, not the grouping.
- Grouping quality depends on the stack parsers. A runtime they do not
  recognise falls down the ladder to the message rung rather than failing.
- The fingerprint functions are pure and live in the catalog package, so
  they are testable without a tenant and usable from `Decode`
  (`TestCatalogIsFrameworkFree`).

## Observed so far

Checked against exception events on one small live environment. These are
facts about that environment, not about every tenant kind.

- The events carried `exception.type`, `exception.message`,
  `exception.stack_trace`, `exception.id`, `exception.is_caused_by_root`,
  `exception.escaped`, `exception.file.full` and `exception.line_number`.
  Neither `exception.caused_by_id` nor `span.exit_by_exception_id` was
  present, so the id-linked step of the root pick has not been seen to work
  on real data; only the root flag and the last-event fallback have inputs
  there.
- OneAgent-captured Java stacks use the notation
  `pkg.Class.method (File.java:123)`, with no `at ` prefix. A parser that
  keys on the prefix reads no frames from them, and those exceptions fall to
  the message rung.
- Pass-through frames are a known weakness of "top N in-app frames". For an
  error sent with a servlet `sendError`, the top in-app frames were two
  filter `doFilter` frames, not the throw site. Every error that passes
  through the same filters would share those frames, so unrelated errors
  of one type would merge into one issue.
- A status code in a message (`HTTP 404 …`) is parameterised away, so on
  the message rung two messages that differ only in the code share a key.
