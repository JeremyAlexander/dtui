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

## Flame graph: built from folded stacks

The flame graph splits dtctl's `StackRow.Stack` at `;` to get the frames back
(`flameTree` in `internal/tui/flameview.go`), so a frame whose label contains
`;` becomes two frames (`TestAStackLabelWithTheSeparatorSplits` pins it), and
every stack is joined in dtctl and split again in dtui. While a tree is built
the stacks are also held several times over: dtctl's rows, the records made
of them, the split frames and the tree. Fix: have dtctl export the call
tree, or the frames of a stack as a list, and build from that; both halves
go with it.

## Flame graph: a new analysis resets the place in it

After `r` or `t` brings another analysis, the graph starts at its root; only
the thread state and the search (a pattern, or the function `#` named) are
kept (`flameView.built`). Fix:
find the zoomed and the selected frame again by their path in the new tree,
and fall back to the root when they are gone.

## Flame graph: not at parity with flamelens

The rows marked partial and missing in
[profiling-flamegraph.md](../design/profiling-flamegraph.md): the matched
part of a label is not highlighted, callees are ordered by name only, the
details take rows from the graph, other calls of the selected function are
not stepped through without `#`, callers of the zoom are told apart by
colour only, there is no single reset key and no `h` / `l`, the functions table filters by plain
text and does not show the graph's search, the two readings are a pushed
view, not one page with a strip, and there is no mouse (a decision for the
whole application: it changes how text is selected in every view). Fix: one at a time, each with the row in
that table changed.

## Flame graph: two measures of a label's width

The model counts cells with go-runewidth, as `catalog.ShortFrame` does; the
app counts with lipgloss. They disagree on a few sequences (a keycap is one
cell by the first and two by the second), and the cut the tables use
(`ansi.Truncate`) lets such a cluster through at its full width. So the view
fits a cell that is not ASCII a second time (`fitCells`) and cuts a line of
the body that is still too wide again (`flameWithin`). `catalog.ShortFrame`
has the same two measures and is the tables' too. Fix: one measure for both,
in the catalog, and a cut that counts by it.

## Flame graph: its recorded stacks are a large test file

`internal/tui/testdata/profiling/stacks.json` is 600 KB for 518 stacks,
because every stack repeats its callers. Fix: record the stacks once dtctl
exports a tree, or keep a smaller synthetic analysis for the graph alone.

## Flame graph: a frame's file is found by its name alone

`internal/source` maps a frame to a file by rules per notation (class and
package, import path, module, the path a script carries) and finds the line
by scanning for the function's name. It does not read build files, source
roots or source maps, so a class whose file is named differently, a
transpiled script and generated code miss or land on the type. The analysis
carries no line numbers, so the line is never the line sampled. Fix: take
file and line from the analysis once dtctl exports them per frame, and keep
the scan as the fallback.

A Go frame is matched by the last element of its import path and the
package clause, not by the module: `go.mod` is not read, so a library's
`example.org/lib/cart.Total` opens the application's `internal/cart` when
that declares a `Total` too. Fix: read the module path of each `go.mod` in
the index and require the import path to start with one.

## Flame graph: a read that does not answer is not interrupted

`internal/source` opens only a regular file and looks at the time between
the reads of one (`internal/source/regular.go`), so a named pipe or a large
file cannot hold `O`. One read of a regular file is still waited for: on a
file system that stops answering (a network mount whose server is gone) the
lookup waits as long as the system does, off the update loop, and the next
`O` starts another. Fix: none without a second thread that could not be
stopped either.

One call is on the update loop: `O` and the list's `y` make the path
absolute again before it is shown or handed over (`openSource`), which asks
the file system. A mount that dies between the answer and the key freezes
the keys there, quit included. Fix: carry the absolute path in the answer,
which is made off the loop.

## Flame graph: the checks on a file and the open are separate steps

`internal/source` looks at a path, opens it, and compares what it opened
with what it saw (`openSame`), and the editor resolves the path once more.
Nothing binds those steps to the directory that was validated: whoever can
write in the checkout can swap a directory on the way for a link between two
of them, and the open follows it. What a won race yields is the line of a
pattern in another file. On Windows the comparison reads the first identity
through the path after the open, so a swap for another regular file is not
seen there at all. A hard link inside the directory is a regular file and
leads wherever it was linked from; no check can tell. Fix: open everything
beneath one directory handle (`os.Root`), which replaces the checks in
`regular.go`, `open_unix.go` and `Index.Abs`.

## Flame graph: the walk reads a directory whole

`filepath.WalkDir` reads and sorts all entries of a directory before the
walk looks at its entry cap or its clock (`Scan`), so one directory of
millions of entries is read whole, in the background. Fix: read directories
in batches (`os.File.ReadDir(n)`) and look at the bounds between them.

## Flame graph: a Python package's own file is never a candidate

A frame `shop.reports.render` is looked for in `shop/reports.py`; a function
defined in `shop/reports/__init__.py` is not found. Fix: add the package's
`__init__.py` to the candidates of a dotted Python name.

## Flame graph: the table and the graph each hold the analysis

The functions table and its graph ask the source separately and each keep
what they got, so the two can come to show two analyses (the graph says so
when it happens), `r` inside the time an analysis is reused for builds every
stack row only to find it unchanged, and every table that opened a graph
keeps a tree of its own. Fix: one immutable result of an analysis that both
views hold, with the tree built once from it.

## Flame graph: a paint formats before it cuts

The selected frame's whole label and whole stack are formatted on every
paint and cut to the screen afterwards. It is linear and small (1.5 ms at
2,000 levels, 11 ms at 20,000, 25 ms with a label of 960 KB selected). Fix:
cut first, or keep the lines of a selection until it changes.

## Flame graph: the frame limit is a package variable

`flameMaxFrames` is a variable so that a test can make a small tree full,
and the caption and the details print it instead of asking the tree. No
test runs in parallel, so nothing races. Fix: have the tree say its limit
(`Tree.Limit`) and pass the limit to `flameTree`.

## Flame graph: not in the session history

A trail restored from the history ends at the functions table: the graph on
top of it is not recorded. Fix: a history entry for the graph (its thread
state, zoom path and search), restored once its analysis arrives.

## Profiling: defects of the functions view seen from the graph

Three belong to the profiling view, not the graph, and are for its owner:
`o` hands a process group's id to a link that knows no such type; the
echoed command does not quote the entity id; and some failures of dtctl
pass `profilingFailure` unworded. And one is older than both:
`openBrowser` (`links.go`) starts a process and never waits for it, which
`o` has always done and `O` now does too.

## Flame graph: the editor is named by the environment only

`O` picks the editor from `DTUI_EDITOR`, the terminal or `VISUAL`/`EDITOR`;
there is no personal settings file to keep the choice in, and a JetBrains
terminal is taken for IntelliJ IDEA whichever IDE it is. Fix: when dtui gets
a personal settings file, an `editor` key there; until then the variable.

## Flame graph: details that do not fit are cut, not scrolled

The details under the graph show what fits and count the rest; `d` gives
them most of the screen, and a stack of 70 frames still does not fit. Fix: a
scroll position for the details, or `d` as a page of its own.

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
