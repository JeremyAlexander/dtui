# Tech debt

Known shortcuts, gaps and duplication, so they are fixed on purpose rather
than rediscovered. Add an entry when you leave one behind; delete it in the
PR that fixes it. One entry: what, where, why it matters, the fix.

## Near-duplicate code below the lint threshold

CI runs `dupl` at 150 tokens. At 100 tokens it still finds two pairs:
`internal/tui/catalog/specs.go` (two eight-line column blocks) and
`internal/tui/history.go` (two fifteen-line blocks). Small, but agents copy
what they find. Fix: extract the shared part, then lower the threshold.

## Live Debugger snapshot fields are unvalidated

`docs/dev/learnings.md` marks the `application.snapshots` facts as taken
from dtctl's implementation and not yet validated live by dtui; the host,
process and trace association fields are read tolerantly in
`internal/tui/catalog/livedebugger.go`. Fix: validate on a tenant with
Live Debugger data and update the learnings entry.

## Preset save does not store the table lens

`W` writes view + facets + searches. A table opened on a non-default lens
(`[` / `]`) replays on the spec's default lens. Storing `scope.Lens` would
need a new YAML field and a restore path in `applyNamedPreset`. Fix: add
`lens` to the preset schema if a real preset needs it.

## learnings.md is one long file

`docs/dev/learnings.md` is over 1,700 lines, so an agent looking for one fact
loads all of it or greps. Fix: keep the section numbering stable and, if it
keeps growing, split section 1 (DQL facts) by data object.

## Profiling analyses outlive a host that keeps running

A profiling view cancels its analysis when it leaves the breadcrumb trail,
but quitting or switching the context does not (`internal/tui/app.go`,
`handleCtxSwitched` and the quit path). The native binary exits, so nothing
is left running. The browser host does not exit; it answers the view with
"not available" today (`cmd/web/sources.go`). Fix: cancel every
`CancelOnLeave` view when the stacks are dropped, in the change that wires
the profiling source into `cmd/web`.

## A deferred navigation can land after a context switch

`pushViewMsg` and the other drill messages are built by a command and applied
when it returns. One issued just before `:ctx` switches can push a view with
the old environment's entity onto the new one's stack. `dataSource.call` now
reads its source when the call is issued; the message route is older and
applies to every drill. Fix: stamp navigation messages with the context
sequence and drop the stale ones in `app.Update`.

## Wrapped error text is laid out per view

A table view makes its error one safe line and continues it under its own
text (`renderTable` in `internal/tui/table.go`). Seven other views wrap the
raw text with the continuation under the mark: `metrics.go`, `inspector.go`,
`reader.go`, `navrender.go`, `timeline.go`, `relations.go`, `waterfall.go`.
Fix: one `errorBlock(err, width)` helper, used by all eight.

## Profiling view: unmeasured per-key work

`tableView.pageSize` builds the caption on every key, and `ScopeColumns`
allocates its columns per call (`internal/tui/table.go`,
`internal/tui/catalog/profiling.go`). Nothing has been measured. Fix: a
benchmark of one key press on a 300 row table first, then cache the caption
per result.

## Footer hints offer keys that do nothing without rows

While a table loads, is empty or has failed, its footer still lists
`/ filter` and `J/K sort` (every table view), and at 160 columns the
`:processes` footer no longer fits `q quit` since `h hotspots` joined it.
Fix: derive the hint list from whether the view has rows, and drop the
lowest-value hint first when the line overflows.

## third_party/ lists seven modules at versions the build no longer uses

`third_party/licenses.csv` and `third_party/THIRD_PARTY_LICENSES.txt` were
generated before `go.mod` moved to what the local dtctl checkout requires.
They name `github.com/Dynatrace/dtctl/sdk` v0.46.0 (built: v0.47.0),
`andybalholm/brotli` v1.2.8 (v1.3.0), `klauspost/compress` v1.20.2
(v1.20.4), `parquet-go/bitpack` v1.0.0 (v1.0.3), `parquet-go/jsonlite`
v1.0.0 (v1.5.5), `parquet-go/parquet-go` v0.32.0 (v0.32.1) and
`pierrec/lz4/v4` v4.1.21 (v4.1.33); the other 68 of the 75 rows match.
The set of modules is the same, so no licence is missing, but the CI job
`third-party-notices` fails on the difference. `make third-party-licenses`
cannot repair it while the two `replace` lines are in `go.mod`: it takes
the sdk's licence text from the module cache, and the replaced sdk is a
directory of the checkout with no licence file of its own, so the script
stops with "no license file" and leaves both files truncated. Fix: when
the `replace` lines go and `go.mod` requires a dtctl release that has the
profiling package, run `make third-party-licenses` (it downloads the
released modules) and commit the result. Do not edit the files by hand.
