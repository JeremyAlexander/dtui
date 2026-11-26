# ADR-0022: Exception issues are a client-derived, sampled grouping

**Status:** Proposed · **Date:** 2026-11-26

## Context

The exceptions lens on the traces view lists the newest spans that carry an
exception span event, one row per span, showing the first event's type and
raw message. Ten thousand throws of one bug are ten thousand rows; nothing
answers "which distinct problems are there, and which is loudest".

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
  linked by ids), else the last. Then: type + top five in-app frames (module
  and function, never line numbers, recursion collapsed); else type + top
  five of all frames; else type + parameterised message; else the type
  alone. Every row shows the rung that produced it, so a coarse group is
  visibly coarse.
- **One primary query, grouped through `Spec` + `Decode`.** The view fetches
  the newest 1000 exception events in scope and groups them in `Decode`
  ([ADR-0003](0003-declarative-view-catalog.md)); it is a catalog view, not
  a bespoke screen, and costs one query per open
  ([ADR-0013](0013-query-budget.md)).
- **The header always says the counts come from a sample.** A group's count
  is its share of the newest 1000 events, not of the timeframe. This extends
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
- A rare exception outside the newest 1000 events is absent, not counted as
  zero; the header says so, and narrowing the scope is how the user reaches
  it.
- Grouping quality depends on the stack parsers. A runtime they do not
  recognise falls down the ladder to the message rung rather than failing.
- The fingerprint functions are pure and live in the catalog package, so
  they are testable without a tenant and usable from `Decode`
  (`TestCatalogIsFrameworkFree`).
