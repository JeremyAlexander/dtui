# ADR-0022: Exception issues are a client-derived, sampled grouping

**Status:** Proposed · **Date:** 2026-11-26

## Context

The exceptions lens on the traces view lists the newest spans that carry an
exception span event, one row per span, showing the first event's type and raw
message. The list is capped at the newest 200 spans, so one loud bug fills
every row and pushes the others out; nothing answers "which distinct problems
are there, and which is loudest".

Span exceptions carry no grouping key in the data. The type alone merges
unrelated bugs (every `NullPointerException` is one row), and the raw message
or stack splits one bug into thousands (ids in messages, line numbers that
move with every deploy, generated proxy class names). The fields a grouping
needs are free text inside `span.events`
([../dev/learnings.md](../dev/learnings.md) §1.11b).

## Decision

dtui derives an **issue key** for each exception-bearing span itself, in Go,
over a bounded sample. The first seven points say how the key is computed, the
rest how the view uses it.

- **The key is versioned.** `e1:` plus the first 12 hex characters of a
  SHA-256 over a canonical string of length-prefixed fields: the rung, the
  type, then the evidence (the parameterised message, or a module and a
  function per frame), so no two inputs can run together. It depends on no
  locale and no map order. `e1` is frozen by the first dtui release that shows
  a key. From then on a change that can move an exception to another key ships
  as `e2`, and a change to a table the key reads is such a change: the vendor
  prefixes, the pass-through names, the message rules. Until that release `e1`
  may be repinned. Two golden files make the step a conscious one: the `e1`
  keys of a corpus, and one digest per table, expression and limit the key is
  computed from. The digests cover declarations; the code of a function is
  covered by the corpus and the tests alone.
- **One event per span is keyed.** Of a span's exception events that say
  anything (a type, a message or a stack), take the last one flagged
  `exception.is_caused_by_root`, else the last one. Where that event's stack
  text holds a whole chain (`Caused by:` in Java, an inner exception in .NET,
  an earlier traceback in Python), the frames and the type are the root
  cause's, read from the text, so type and frames belong to one exception. A
  JavaScript `[cause]` chain is not followed.
- **A fallback ladder picks what is hashed.** In order:
  1. *app frames*: the type, the throw-site frame, and the application's
     frames below it that are not pass-through, until five frames that count
     are in. The throw site is one of the five when it is the application's
     own and not pass-through; otherwise it is in the key on top of them, so a
     key holds at most six frames. A frame is its module and function, never a
     line number, and a frame repeated by recursion counts once.
  2. *frames*, when no application frame counts: the type, the throw site and
     the frames below it, five in all, whatever they are.
  3. *message*, when no frame was read: the type and the parameterised
     message.
  4. *type*: the type alone.

  Every row shows the rung that produced it, so a coarse group is visibly
  coarse.
- **A Go stack that is not a panic's is not used.** It was taken where the
  error was recorded, not where it arose, and every error that passes there
  has the same. Such an event is keyed on its message.
- **The throw site is always in a frame key, and pass-through frames do not
  count among the application's.** Filter chains, middleware continuations,
  interceptors and decorator wrappers are shared by every error that passes
  through them; keyed on those alone, unrelated errors of one type merge (see
  "Observed so far"). They are skipped on the app-frames rung only. On the
  frames rung nothing is skipped, because there a handler's or a filter's own
  frame may be all that tells it from the next. A pass-through frame that is
  the throw site stays in the key, and an application's pass-through throw
  site with no other application frame below it still gives an app-frames key
  of that one frame. The lists are per notation, match the method name alone,
  and leave out names an application commonly gives its own entry point
  (`invoke`, `step`, `dispatch`, `__call__`).
- **What counts as application code is built in and part of the key version.**
  There is no setting that replaces the rules and no second classifier, so
  every user computes the same key for the same event.
- **The key lives in a leaf package.** `internal/exckey` imports the standard
  library only (`TestExceptionKeyPackageIsALeaf`). The catalog may import it
  and nothing else of this module (`TestCatalogIsFrameworkFree`), so the key
  is testable without a tenant.
- **The view is a catalog `Spec` of its own.** It is not a lens of `:traces`:
  `Decode` belongs to a `Spec` ([ADR-0003](0003-declarative-view-catalog.md)),
  so a grouping lens would group every other lens with it. It is a second list
  over the spans of the traces noun, not a new tab, a chart or a bespoke
  screen, which keeps it inside the depth cap of
  [ADR-0014](0014-breadth-not-depth.md).
- **One primary query; the grouping is done before the rows reach the view.**
  The query keeps the spans that carry an exception event, newest first.
  Fingerprinting and grouping run in the query's command, off the UI
  goroutine, and the view receives groups; `Decode` as it runs today, inside
  `tableView.Update`, must not parse a thousand stacks. That step does not
  write into a record another goroutine can read (the cache `SpanExceptions`
  keeps in the record is such a write). A result for a superseded request is
  dropped by its sequence number like any other; a running query is not
  cancelled. The unit is the span throughout: one key per span, from the one
  event the rules above pick, however many exception events the span carries.
  The exceptions lens keeps showing a span's first event, so the two can name
  different types for one span.
- **One query, with a bounded response.** One query per open meets the count
  of [ADR-0013](0013-query-budget.md); that ADR also bounds a query's shape,
  and an unprojected span comes with all of its `span.events` and full stack
  traces. So the query is projected to the fields the key and a row read, it
  has a row limit, and the request sets an explicit result size limit. The
  mechanism is decided here. The numbers are provisional: 1000 rows and 8 MB,
  from one measurement on an environment with very few exception spans (see
  the open list). 1000 rows is the upper bound whatever the measurement says.
- **A group row is small.** It keeps its key, its rung, its count, its times
  and the display fields of its newest member, not the member spans or their
  stack text. The view builds no list of a group's occurrences.
- **The header says what the sample is.** It gives the number of spans that
  arrived and the time of the oldest of them, for example
  `412 spans, newest first, back to 14:02`: a loud bug can fill the sample in
  seconds, and a count alone hides that. A group's count is the number of
  sampled spans with its key, a count within the sample and not within the
  timeframe. When the scan limit, the result size limit or the fetch timeout
  cut the result short, the header says which, from the same response as the
  rows. A result cut by the size limit arrives with a notice the notice
  classifier does not know by name today; the view has to recognise it, and
  must not present it as a row limit. A result that was cut short can be empty
  and is not known to hold the newest spans, so an empty one reads "the scan
  gave up", never "no exceptions". Groups are ordered by count, then by newest
  span, then by key, so equal counts do not move between refreshes. This
  extends [ADR-0007](0007-scope-honesty.md) from scope to completeness.
- **Facets and search act on spans; the key is local.** A facet narrows the
  query before sampling, so it uses fields of the fetched spans, collected
  before grouping. The key, the rung and the count are stamped under `__` keys
  ([ADR-0016](0016-detail-panes-show-real-grail-keys.md)) and are never
  offered as facets or sent to the server. `/` filters the visible groups, the
  key included, and on this view Enter keeps that local filter instead of
  promoting the text to a server `search`, which would look for a key no span
  holds. `--search` and a preset's searches stay server-side, on the spans.
- **A row opens its newest span, and the exits hand over the sample.** Enter
  opens the trace of the group's newest span. `y` yanks the key. `o` opens the
  primary query in a Notebook and `c` echoes it, both labelled as the
  ungrouped sample: no DQL computes the key. That is a stated limit on the two
  exits of [ADR-0012](0012-navigate-analyze-mutate.md), not a third exit and
  not an override of [ADR-0001](0001-dql-is-the-substrate.md): what is handed
  over is still DQL.

Open, to be settled before this is accepted. This record stays Proposed, and
its pull request open, until this list is empty:

- The row limit and the result size limit as numbers. 1000 rows and 8 MB were
  measured on an environment with very few exception spans. They are confirmed
  or changed on a busier one (bytes scanned, bytes returned, time to first
  rows, against the budgets in [vision.md](../design/vision.md)), and the
  numbers are written into this record by the view's pull request.
- The view's name, its alias and how it is reached. A name in the command bar
  and a drill from the exceptions lens are the candidates; the view's pull
  request chooses.

Left undecided on purpose. Nothing above depends on these, and none of them
needs a further ADR:

- Exact counts over the whole timeframe. They need a server-side key that
  every member of a group shares, and the event's `exception.type` is not one:
  a wrapped and an unwrapped throw of one root cause share a key and differ in
  type.
- Whether RUM errors use their own `error.id` instead of this key. That field
  has not been checked on a live environment.
- Whether the code eventually lives in dtctl's SDK, so other tools compute the
  same key. It is a leaf package for that reason; moving it means the import
  allowlist of `internal/tui` gains the SDK package.

Rejected:

- **Grouping in DQL.** DQL cannot pick the root of a cause chain across a
  span's events, read frames out of five stack notations or drop pass-through
  frames. A coarser grouping on the server, by the first event's type and the
  service, would give exact counts over the whole timeframe and a far smaller
  response, and it merges every bug of one type. Its cost was not measured.
- **A lens of `:traces`.** See "The view is a catalog `Spec` of its own".
- **A setting that replaces the in-app rules.** Keys would stop matching
  between users, and a key that carried the classifier's identity would still
  look like any other.
- **Type only.** Unrelated bugs that throw the same type become one issue.
- **The full normalised stack.** One bug reached through two callers becomes
  two issues.
- **Hashing line numbers.** Every deploy that moves a line starts a new issue
  for an old bug.

## Consequences

- Issue keys are stable across refreshes, sessions and machines for the same
  key version, but they are dtui's, not the tenant's: nothing in Grail can be
  filtered by one.
- An `e1` key copied into a ticket stops matching once `e2` ships. dtui stores
  no keys, so nothing migrates. If Grail gains a grouping key for exceptions,
  this ADR is superseded.
- A rare exception outside the sample is absent, not counted as zero; the
  header says how far back the sample reaches, and narrowing the scope is how
  the user reaches it.
- The command echo (`c`) is `dtctl query` with the view's primary query: it
  returns the sampled spans, ungrouped. The keys, rungs and counts are
  computed in Go and no DQL reproduces them, so the echo hands over the input
  of the grouping, not the grouping.
- Grouping quality depends on the stack readers. A notation none of them reads
  (Rust, Swift, PHP, Ruby) yields no frames and is keyed on the message rung
  rather than failing. The pull request that adds the key lists what the
  readers are known not to read in [../dev/tech-debt.md](../dev/tech-debt.md).
- Grouping off the UI goroutine needs a step the table engine does not have
  today, next to `Spec.Decode`. This record states the constraint and leaves
  the shape of that step to the view's pull request.
- This is a new subsystem (five stack readers, a message parameteriser, a
  versioned key), which [vision.md](../design/vision.md) answers with no by
  default; this record is the override, for the reason in Context. Against the
  gate: it shows what is happening, reads only, costs one primary query, is a
  catalog `Spec` and needs no configuration. At its depth cap it hands over
  the sample, not the grouping.
- The golden files of the key are not layout snapshots.
  [CONTRIBUTING.md](../../CONTRIBUTING.md) allows them as versioned
  compatibility fixtures.
- The view's pull request owes tests for: a result of many older spans of one
  key and a full sample of newer spans of another, which shows the newer one
  and claims nothing about the older; a partial response of exactly the row
  limit; a late clean response after a partial one; a result cut by the size
  limit, named as that in the header; typing a key, pressing Enter and
  asserting that no DQL holds it; two spans of one key with disjoint fields in
  the facet picker; a refresh while a group list is on screen, under the race
  detector.

## Observed so far

Checked against exception events on one small live environment. These are
facts about that environment, not about every tenant kind.

- The events carried `exception.type`, `exception.message`,
  `exception.stack_trace`, `exception.id`, `exception.is_caused_by_root`,
  `exception.escaped`, `exception.file.full` and `exception.line_number`.
  `exception.caused_by_id` was on no event, so the key does not read it: the
  root is the flagged event, else the last one.
- OneAgent-captured Java stacks use the notation
  `pkg.Class.method (File.java:123)`, with no `at ` prefix. The Java reader
  reads that form as well as the JVM's own.
- Pass-through frames are a known weakness of "top N in-app frames". For an
  error sent with a servlet `sendError`, the top in-app frames were two filter
  `doFilter` frames, not the throw site. Every error that passes through the
  same filters would share those frames, so unrelated errors of one type would
  merge into one issue. This is why the throw site is in the key and
  pass-through frames are skipped.
- A status code in a message (`HTTP 404 ...`) carries meaning. The message
  rule keeps a `SQLSTATE`, an Oracle-style code, and a number that follows
  HTTP, status or errno wording or `code` behind the word that says whose it
  is (`error code`, `exit code`), so a 404 and a 500 stay apart on the message
  rung; a bare `code 12345` is a value.
- The view's query, projected and limited as above, was measured once, on an
  environment with very few exception spans. That says nothing yet about a
  busy one.
