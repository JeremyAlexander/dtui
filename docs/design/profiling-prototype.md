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
6. **`r` within two minutes reuses the analysis.** Lenses and focus drills read
   one cached analysis, kept for two minutes after it arrived; the caption
   states the clock range it covers. Two views share one exactly when they
   would send the same request.
7. **No preview pane, no app-only toggle.** A row has nothing more to show
   than its cells and the footnote, which spells the selected function out;
   the pane would only narrow the table. dtctl's app-only filter has no key.
8. **Seams added to `catalog.Spec`:** `EntityArg`, `ArgLabel`, `NoPreview`,
   `CancelOnLeave`, `Caption`, `Empty`, `Loading`, `Footnote`, `TabTarget`
   (the flame graph, ADR-0021), and a leading
   `__summary` record that a source may return and that is read only for a
   spec that sets one of the last four. `CanScope` takes a pin for a view
   without a query when its `Scopable` says so.

## Open questions for the first live run

- Every lens read rebuilds its rows from the raw result, and an allocations
  result is kept whole although 300 rows of it are shown. Neither cost has
  been measured.
- Whether real frame labels carry parameter lists, and how wide they get.
- `o` offers the Smartscape topology of the process only. A link into the
  profiling app needs that app's intent, which has not been verified.
- A deep link can carry facets or searches, which the source ignores while
  the breadcrumb shows them.

Left out: a flame graph of allocations, profiling without an entity, and the
browser build, where the view says it is not available.
