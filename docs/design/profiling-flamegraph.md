# Flame graph of the profiling view (prototype)

`tab` on the `:hotspots` table draws the analysis the table shows as a flame
graph, and `tab` there returns to the table. It is a prototype stacked on the
[profiling prototype](profiling-prototype.md) and argued in
[ADR-0023](../adr/0023-flame-graph-is-a-bespoke-screen.md) (proposed). The
interaction model is that of [flamelens](https://github.com/YS-L/flamelens)
(MIT), re-derived from its behaviour; no code of it is in this repository.
This page says what is built, how to look at it, and, row by row, where it
stands against flamelens.

## What is there

An abridged screen, zoomed and searched:

```
  running   lock   net_io   disk_io   wait                 zoomed · levels 1 to 16 of 27 ▼
 last 2h · 77,631 running samples (stacks seen, not time) · all threads, leaf: total
▏all
▏java.lang.Thread.run
▌org.apache.catalina.core.StandardWrapperValve.invoke
▏com.example.shop.web.TenantFilter.doFilter                             ▏…pw
▏…cableHandlerMethod.invokeForRequest▏▏…dyMethodProcessor.handleReturnValue
▏▏…dItem▏…roducts▏▏…▏▏▏▏▏▏…r.search▏·  █…2HttpMessageConverter.writeInternal
 ⌕ jackson\.  19,223 samples · 24.8% of all · 26.5% of the zoomed frame
 ▸ org.apache.catalina.core.StandardWrapperValve.invoke
   self 0 · total 72,546 · 93.4% of all · 100.0% of the zoomed frame
```

- The first line is the thread states, with the one drawn marked, and at its
  right which levels are in view. `▼` says there are more below.
- The second line is the table's caption: the window analysed and the samples
  of the thread state.
- One line per level, the root `all` first (an icicle; there is no bottom-up
  orientation). A frame is as wide as its share of the zoomed frame. Its
  callees start at its left edge in the order of their names, and what is
  left at the right is the frame's own samples.
- The first cell of a frame says what it is, with or without colour: `▏` a
  frame, `▌` the selected frame, `█` a search hit, `▍` another call of the
  selected function, `·` callees too narrow to draw.
- With colour a frame has one of eight warm colours, picked by its function's
  name, so a function has one colour wherever it is called. The selected
  frame, a search hit, another call of the selected function and the callers
  of a zoomed frame have a style of their own (`internal/tui/theme`).
- The last lines are the search, when there is one, and the selected frame:
  its function spelled out, its self and total samples, its share of all
  samples and of the zoomed frame, and how many callees are too narrow to
  draw.

The graph reads the analysis the table holds (`hotspot-stacks`), so opening it
asks the environment for nothing. `r` asks as the table's `r` does; an answer
that is the analysis already shown changes nothing and says so. `t` is
another window and so another analysis. One analysis carries every thread
state: `[` and `]` cost nothing.

The table keeps its graph. Returning to it finds the zoom, the selection and
the search as they were left. What the table hands over each time is its
thread state and the function of its selected row, as a search, and each
only when it changed since the last time.

## Keys

| Key | In the flame graph | flamelens |
|---|---|---|
| `↑` `k` | the caller | same |
| `↓` `j` | the widest callee on screen, the leftmost of equals | same |
| `←` `→` | the previous / next frame on the level, whoever called it | same, also `h` `l` |
| `h` `l` | not bound; a status line names the arrows | previous / next on the level |
| `pgup` `pgdn`, `ctrl+b` `ctrl+f`, `space` | a screen of levels | `b` `f` |
| `f` `b` | not bound; a status line names the paging keys | page down / up |
| `g` `G`, `home` `end` | the first / the last level | `g` `G` |
| `enter` | zoom into the selected frame; on the root, leave every zoom | same, one zoom at a time |
| `backspace` | zoom out one step | none |
| `esc` | clear the search, then the zoom, then return to the functions | clear the zoom |
| `/` | search by regular expression; `enter` applies, empty clears, `esc` cancels | same |
| `#` | search for the selected function | same |
| `n` `N` | next / previous match in reading order, around the ends with a note | same, without going around |
| `[` `]` | the thread state before / after | none |
| `tab` | the functions table | Flamegraph / Top |
| `r` | refresh: the analysis is asked for again | reset the view |
| `y` `c` `o` `t` `?` `q` | dtui's: copy the function, copy the command, open, timeframe, help, quit | `?` is a debug bar, `q` quits |

Where the two differ, a dtui key keeps its meaning: `h` and `l` are drill
letters, `f` is facets and `b` is fleet on every table, and `r` is refresh
everywhere. A flamelens habit must not start an analysis that runs for a
minute, so there is no reset key; `esc` undoes the search and the zoom. `?`
lists these keys first when it is opened on the graph.

## How to look at it

The binary draws a graph only from an analysis, which needs
`storage:profiles:read` and `storage:entities:read`; there is no
development-only entry point (`TestNoRecordedAnalysisInTheProduct`). With
the permission:

```sh
dtui hotspots PROCESS-…     # or h on a :processes row
tab                          # the flame graph; tab again for the functions
```

Without it, the tests draw the recorded analysis of the synthetic web
application through the same code:

```sh
go test ./internal/tui/flame                    # the model: tree, layout, movement, search
go test ./internal/tui -run Flame               # the view, driven by keys
go test . -run 'Stacks|FlameGraph'              # the sources: stacks reconcile with the table
ls internal/tui/testdata/profiling-flame-*.golden
```

## How it is built

| Part | Where | What it knows |
|---|---|---|
| Model | `internal/tui/flame` | the tree, the layout for a width, the selection, zoom, scroll and search. No terminal framework (`TestFlameIsFrameworkFree`) |
| View | `internal/tui/flameview.go` | fetching, keys, drawing, the states without a graph |
| Sources | `profiling.go`, `profiling_dtctl.go` | `hotspot-stacks` reads the analysis the table holds; `hotspot-stacks-fresh` is the table's refresh |
| Seam | `catalog.Spec.TabTarget`, `tabHandlers` | a view's second reading, opened by `tab` |

The tree is built from dtctl's folded stacks (`Hotspots.Stacks`), one weight
per thread state, with the children of a frame in the order of their names:
the same stacks give the same screen in any order, and a frame keeps its
place from one thread state to the next. Widths are integer arithmetic on
prefix sums, so siblings tile without gaps and a callee never leaves its
caller. A frame narrower than a cell is not drawn and cannot be selected
until a caller is zoomed; the last such callees of a frame share a marker.

Per thread state, the graph's root is the samples the table counts, and a
function's self and total samples are those of its frames in the graph
(`TestStacksReconcileWithTheFunctionsTable`).

Measured on a synthetic profile of 50,000 stacks, each 100 frames deep (about
half a million frames), on an Apple M5 Pro: building the tree once takes
about 155 ms and runs off the update loop; a layout for 250 columns 0.44 ms;
a search 1.8 ms; a cursor key with the screen drawn again in full colour
about 1.1 ms, a zoom or a thread state about 1.2 ms
(`BenchmarkBuild`, `BenchmarkLayout`, `BenchmarkSearch`,
`BenchmarkFlameGraphKey`, `BenchmarkFlameGraphZoom`).

## Parity with flamelens

Against flamelens 0.4.0. **done**: behaves the same, or better in the way
the note says. **partial**: there, but different in a way a flamelens user
would notice. **missing**: not built. **n/a**: does not apply to a view
inside dtui.

### Input and tree

| flamelens | Status | Note |
|---|---|---|
| Collapsed stacks from a file or stdin | n/a | the input is the analysis the table holds |
| Bad lines are skipped | n/a | a stack without samples and a frame without a name are skipped |
| A synthetic root `all`; totals on every frame of a stack, self on the last | done | |
| Child order: first seen, or by total with `--sorted` | partial | by name only; there is no order by total |
| Frames per level in screen order | done | |
| Per-function totals that count a stack once | done | the functions table; the two reconcile |

### Layout

| flamelens | Status | Note |
|---|---|---|
| Header naming both views, the active one marked | partial | the breadcrumb says `hotspots › flame·running`; there is no strip showing both readings |
| The graph takes every remaining row | done | |
| `Match` bar: pattern, samples, share of all and of the zoom | done | |
| `Selected` bar: name, samples, share of all and of the zoom | done | two lines, with self and the callees not drawn |
| `Search` input line | done | in the body, above the selected frame |
| `Info` bar for a transient message | done | dtui's status line |
| `Debug` bar with timings | missing | benchmarks instead |
| Help line of keys, `n/N` only while searching | done | dtui's footer; it is cut at the terminal's width |

### How a frame is drawn

| flamelens | Status | Note |
|---|---|---|
| Icicle only | done | |
| One line per level, coloured runs without borders | done | plus an edge cell per frame, which also works without colour |
| Label with a leading space; `.` for a one cell frame | done | the edge cell takes the place of both |
| Label clipped at the frame's width | done | shortened package first, with an ellipsis, counted in cells |
| Width from the share of the parent, truncated | done | integer prefix sums: no unpainted cells from rounding |
| A frame below one cell is not drawn, nor anything under it | done | plus a marker for the last such callees |
| Warm colour by a hash of the frame's path | partial | by a hash of the function's name: one colour per function |
| Dark or light text by the background's luma | done | one dark ink on every step of the ramp |
| Selected frame on a near white background | done | on the theme's text colour |
| Matched frames on a blue background | done | |
| Matched substring in red bold | missing | |
| Other calls of the selected function highlighted while moving | done | in a quieter style than a search hit |
| Callers of the zoomed frame dimmed | done | |

### Navigation

| flamelens | Status | Note |
|---|---|---|
| Starts on the root | done | |
| Down: the widest visible callee | done | |
| Up: the caller | done | |
| Left / right along the level, across callers, no wrap | done | arrows only |
| `h` / `l` for left / right | missing | drill letters in dtui |
| Only frames at least a cell wide can be reached | done | |
| Paging by a screen, top, bottom | partial | other keys than `f` / `b` |
| Never scrolls into empty levels | done | |
| The selection follows the view | done | |
| An indicator for more levels below | done | flamelens computes one and does not show it |

### Zoom

| flamelens | Status | Note |
|---|---|---|
| `enter` zooms; callers stay as full width lines | done | |
| The root stays `all`; shares of all and of the zoom | done | |
| One zoom at a time; `enter` on the root clears it | done | zooms stack, and `backspace` leaves one |
| `esc` clears the zoom, selection on the frame that was zoomed | done | after the search, if there is one |
| `r` resets selection, scroll, zoom and search | partial | no single key: `esc` undoes the search and the zoom, `g` scrolls back |
| The cursor can move onto the dimmed callers | done | |

### Search

| flamelens | Status | Note |
|---|---|---|
| `/` opens the input; `enter` applies, empty clears, `esc` cancels | done | |
| Unanchored, case-sensitive regular expression | done | Go's RE2, matched against the whole label, not the short name |
| An invalid pattern keeps the search and says so | done | |
| `#` searches for the selected name, anchored | done | |
| Share matched counts a stack once | done | |
| `n` / `N` step through visible matches | done | in reading order, and around the ends with a note |
| A search survives movement | done | |

### The Top table

| flamelens | Status | Note |
|---|---|---|
| `tab` switches between graph and table | done | the table is the existing `:hotspots` table |
| Columns total, own, name with shares | done | |
| Sorted by own; `1` / `2` pick the column | partial | `J` / `K`, dtui's sort keys |
| Row movement and paging | done | the table's keys |
| `/` filters rows by regular expression | partial | the table's filter is plain text |
| `enter` searches the row's function in the graph | partial | `tab` carries the selected row's function; `enter` stays the callers and callees drill |
| The `Match` bar stays visible in the table | missing | |

### Global

| flamelens | Status | Note |
|---|---|---|
| `q` and `ctrl+c` quit | done | |
| Live mode, freeze with `z` | n/a | an analysis is of a past window |
| Mouse | n/a | flamelens has none either |
| Resize: laid out again | done | a selection that got too narrow moves to its caller |
| Very narrow terminals | done | below 40 columns or 6 body rows the table refuses to open the graph, and an open one says it needs more room |
| `--sorted`, `--echo`, `--debug` | n/a | |

63 rows: 46 done, 8 partial, 4 missing, 5 n/a.

## Beyond flamelens, and not built

Built: the thread-state lens with the place kept, the marker for callees too
narrow to draw, a zoom history, the indicator for more levels, labels counted
in cells and safe to print, a floor for the terminal's size.

Not built, in the order they were asked about in the design brief:

- the split of the selected frame over the thread states, and its share of
  its caller;
- a flame graph of allocations;
- `offcpu` and `all` as thread states;
- the callers and callees drill from a frame;
- a details view of a frame (`d`), and copying a frame's whole stack;
- the zoom and the selection kept when a new analysis arrives;
- one page with a `functions | flame` strip in place of a pushed view;
- a differential, a timeline, a bottom-up orientation, a sandwich view: out
  of scope by ADR-0023.

The shortcuts taken are in [tech-debt.md](../dev/tech-debt.md).
