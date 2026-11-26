# Profiling views (prototype)

`:hotspots` and `:allocations` show code-level profiling of one process: where
its threads spend samples, and where it allocates. They are a prototype on a
draft branch. This page says how to look at them and which decisions they
leave open; it becomes an ADR, or goes away, with the branch.

## What is there

| View | Opens with | Shows |
|---|---|---|
| `:hotspots PROCESS-…` (`:hot`, `:cpu`, `:profile`) | `h` on a `:processes` row | functions by self samples, one lens per thread state (running, lock, net_io, disk_io, wait) |
| the same, focused | `enter` on a function | the function's total and self, its callers, its callees; `enter` walks on, `esc` walks back |
| `:allocations PROCESS-…` (`:alloc`, `:memory`) | `h` on a hotspots row | allocation sites with their caller path, allocated and survived bytes |

Both read dtctl's `pkg/resources/profiling` through one adapter,
`profiling_dtctl.go`; nothing else in dtui imports that package. Every request
is a GET. `c` echoes the equivalent `dtctl exec profiling …` command with the
window that was analysed.

## Looking at it without the permission

An analysis needs `storage:profiles:read` and `storage:entities:read`, granted
by an IAM policy. To see the views without it, point the developer switch at a
recorded analysis:

```sh
DTUI_PROFILING_FIXTURE=testdata/profiling/java-webapp dtui hotspots PROCESS-000000000000A001
```

The value is a directory holding `hotspots.json` and `allocations.json`, or one
such file. Every view fed from it says `FIXTURE <name>` in its caption.
`DTUI_PROFILING_FIXTURE_DELAY=8s` makes the recording take that long, to look
at the loading state. `testdata/profiling/java-webapp` is synthetic.

A recording is what dtctl prints with `--raw`:

```sh
dtctl exec profiling hotspots PROCESS-… --show-waiting --raw -o json > hotspots.json
dtctl exec profiling allocations PROCESS-… --raw -o json > allocations.json
```

The switch, `fixtureProfiler` and `replayTransport` are prototype scaffolding
and are deleted before the views ship.

## Decisions this leaves to the maintainers

1. **A first result after up to a minute.** ADR-0013 budgets one query and
   first rows in about 2s. An analysis is submitted, polled, and throttled to a
   few at once per environment. The view says what it waits for, for how long,
   and what the environment answered last; it cannot be made fast.
2. **A new drill key, `h`.** vision.md freezes the key vocabulary. `h` was free
   on tables (ADR-0008) and serves the whole family: process → hotspots,
   hotspots ⇄ allocations. `b` is taken by fleet.
3. **An experimental API.** The endpoints live under `platform-reserved` and
   dtctl marks `exec profiling` experimental; their shape may change in any
   release.
4. **Calls that are cancelled.** A profiling view that leaves the breadcrumb
   trail cancels its analysis (`Spec.CancelOnLeave`), after a 2s grace so a
   lens switch does not restart it. Every other view still drops stale results.
5. **A 2h window.** A longer selection is analysed for its last 2h, and the
   caption says so ("last 2h of the selected 24h", ADR-0007).
6. **`r` within two minutes reuses the analysis.** Lenses and focus drills read
   one cached analysis; the caption states the clock range it covers.
7. **No preview pane, no app-only toggle.** The preview would cost an analysis
   per row. dtctl's app-only filter has no key yet; its empty state is worded.
8. **Seams added to `catalog.Spec`:** `EntityArg`, `ArgLabel`, `NoPreview`,
   `CancelOnLeave`, `Caption`, `Empty`, `Loading`, `Footnote`, and a leading
   `__summary` record a source may return.

Left out: flame graphs, the call tree, profiling without an entity, and the
web build, where the two sources are not wired.
