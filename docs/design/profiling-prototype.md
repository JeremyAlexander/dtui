# Profiling view (prototype)

`:hotspots` shows code-level profiling of one process: where its threads spend
samples, per thread state, and where it allocates. It is a prototype on a
draft branch. This page says how to look at it and which decisions it leaves
open; it becomes an ADR, or goes away, with the branch.

## What is there

| Reading | Opens with | Shows |
|---|---|---|
| `:hotspots PROCESS-…` (`:hot`, `:cpu`, `:profile`) | `h` on a `:processes` row, or a pinned process | functions by self samples, one lens per thread state (running, lock, net_io, disk_io, wait) |
| the same, focused | `enter` on a function | the function's total and self, its 25 largest callers and callees; `enter` walks on, `esc` walks back |
| the `allocations` lens | `[` / `]` | allocation sites with their caller path, allocated and survived bytes; `enter` inspects a row |
| the flame graph | `tab` on a thread state, `tab` again to return | the call tree of the same analysis, one line per level; see [profiling-flamegraph.md](profiling-flamegraph.md) |

It is one view. `h` exists on a process row only; allocations are a lens, so
switching to them keeps the place on the trail and the way back to the thread
state. A focused function has no allocations reading, and the lens says so.

The view reads dtctl's `pkg/resources/profiling` through one adapter,
`profiling_dtctl.go`; nothing else in dtui imports that package. Every request
is a GET. `c` copies the `dtctl exec profiling …` command that prints what the
view holds: the same process, window, thread state and row limit.

Every bar is a share of what was counted, on one scale: of all samples in
the functions table, of all bytes allocated in the allocations lens. The table holds the 300 functions with the most self samples and its
caption says so when there are more.

## Looking at it without the permission

An analysis needs `storage:profiles:read` and `storage:entities:read`, granted
by an IAM policy. The binary has no other way to an analysis, and the
repository has no development-only entry point to put a recording behind.

What shows the view without credentials is the test suite. The tests replay a
synthetic recording, `testdata/profiling/java-webapp`, through the same dtctl
handler a live result goes through:

```sh
go test . -run TestProfiling                 # the sources over the recording
go test ./internal/tui -run Profiling        # the view over what they returned
ls internal/tui/testdata/profiling-*.golden  # the rendered screens, four sizes
```

A recording is what dtctl prints with `--raw`:

```sh
dtctl exec profiling hotspots PROCESS-… --show-waiting --raw -o json > hotspots.json
dtctl exec profiling allocations PROCESS-… --raw -o json > allocations.json
```

## Decisions this leaves to the maintainers

1. **A first result after minutes.** ADR-0013 budgets one query and first
   rows in about 2s. An analysis is submitted, polled, and throttled by the
   environment. The view says what it waits for, for how long, and what the
   environment answered last; it cannot be made fast.
2. **A new drill key, `h`.** vision.md freezes the key vocabulary. `h` was free
   on tables (ADR-0008) and has one meaning: the hotspots of the process on
   the row. `b` is taken by fleet.
3. **An experimental API.** The endpoints live under `platform-reserved` and
   dtctl marks `exec profiling` experimental; their shape may change in any
   release.
4. **Calls that are cancelled.** A profiling view that leaves the breadcrumb
   trail stops asking for its analysis (`Spec.CancelOnLeave`), after a 2s
   grace so a lens switch does not restart it. No request tells the
   environment; what it does with an analysis nobody polls is not known. A
   view off the trail starts none, and auto-refresh (`R`) skips the view.
   Every other view still drops stale results.
5. **A 2h window.** A longer selection is analysed for its last 2h, and the
   caption says so ("last 2h of the selected 24h", ADR-0007). A window in the
   future is refused before anything is asked.
6. **Only `r` replaces an analysis.** Lenses, focus drills and a return to
   the view read one kept analysis, however long ago it arrived, so walking
   the call graph never starts a second one; the caption states the clock
   range it covers, which is the only sign that a result is old. `r` runs
   the analysis again, also for a closed window, and joins one still in
   flight. The four read last are kept. Two views share one exactly when
   they would send the same request.
7. **No preview pane, no app-only toggle.** A row has nothing more to show
   than its cells and the footnote, which spells the selected function out;
   the pane would only narrow the table. dtctl's app-only filter has no key.
8. **Seams added to `catalog.Spec`:** `EntityArg`, `ArgLabel`, `NoPreview`,
   `CancelOnLeave`, `Caption`, `Empty`, `Loading`, `Footnote`, `TabTarget`
   (the flame graph, ADR-0023), and a leading `__summary` record that a source may return and that is read only for an
   API spec that sets `Caption`, `Empty` or `Footnote` (`ReadsSummary`).
   `CanScope` takes a pin for a view without a query when its `Scopable`
   says so.
9. **The wait is counted per reading, the note per call.** A lens or a
   window drops the rows and starts the wait clock again, because the view
   cannot tell a lens that is another analysis (allocations) from one that
   is not (a thread state). Every call has a progress line of its own; a
   running analysis says its last note again to the call that joins it.
10. **An argument that names no process opens nothing.** The command bar,
    a deep link, a sent page and the command line run one check
    (`tui.CheckEntityArg`); the status line or the reply names the
    argument. The view never takes its argument as a row filter.

## Open questions for the first live run

- Every lens read rebuilds its rows from the raw result, the row caps come
  after the full projection, and four whole results are kept although 300
  rows of each are shown. Reviewers measured about 0.8 MB and 1.5 MB
  allocated per projection on the recording, and estimated 15 to 92 ms per
  projection and 105 MB for four results of 20,000 nodes; none of it has
  been measured on a result from a tenant.
- Whether real frame labels carry parameter lists, and how wide they get.
- `o` offers the Smartscape topology of the process only. A link into the
  profiling app needs that app's intent, which has not been verified.
- A deep link can carry facets or searches, which the source ignores while
  the breadcrumb shows them.
- The 404 wording says the process is gone and the 403 wording names two
  scopes and drops the server's message. What an environment without the
  endpoint, or a token that lacks one scope, really answers has not been
  observed; decide both wordings then.
- The recording holds Java frames only and stays under the row and focus
  caps. Go, script and signature labels and the caps are covered by cases
  built in the tests; replace or extend the recording with the first live
  one.
- The echoed `--focus` is not what the view asked: dtctl's flag falls back
  to a unique substring, the view matches the whole label. They differ when
  the state has no samples of the label and exactly one longer label
  contains it; the view then shows an empty state and the pasted command a
  result. Needs an exact-match flag in dtctl.

## Known gaps

- `h` works on a `:processes` row only. A process's own detail page does
  not offer it: the drill keys of a detail page jump to tabs of the same
  name, and a process page has no such tab.
- A timeframe change reaches every view of the stack, covered ones too
  (`app.setTimeframe`). Focus levels of one process share one analysis, but
  two processes on one stack start two, one of them for a view nobody
  sees. `[` on the first lens wraps to allocations and starts that
  analysis.
- A failure with a reason dtui has no words for shows the reason's name and
  the status only, not dtctl's message for it: that text is the SDK's and
  has carried a task token before.

Left out: a flame graph of allocations, profiling without an entity, and the
browser build, where the view says it is not available.
