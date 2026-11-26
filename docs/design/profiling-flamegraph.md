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

An abridged screen at 100 columns, with the cursor on a frame:

```
  running   lock   net_io   disk_io   wait                        levels 13 to 27 of 27 ▲ pgup
 last 2h (10:00 to 12:00 UTC) · 77,631 running samples (stacks seen, not time) · all threads, …
     ▏org.springframework.web.servlet.DispatcherServlet.doDispatch
     ▏o.s.w.m.s.InvocableHandlerMethod.invokeForRequest   ▏▏handleReturnValue
     ▏▏▏addItem   ▏listProduc…▏▏p…▏▏▏▏▏▏▏▏search       ▏▏▏ ▌c.f.jackson.databind.ObjectWriter.writeValue
      ▏                    ▏l…      ▏  ▏                   █c.f.j.d.ser.BeanPropertyWriter.serializeAsField
 search jackson\.  33,221 samples · 42.8% of all
 ▸ com.fasterxml.jackson.databind.ser.BeanPropertyWriter.serializeAsField
   self 1,769 · total 31,314 · 40.3% of all · 54 callees too narrow to draw (8,809 samples): enter zooms in
   package    com.fasterxml.jackson.databind.ser
   type       BeanPropertyWriter
   function   serializeAsField
   self/total running 1,769/31,314 · lock 0/0 · net_io 0/0 · disk_io 0/0 · wait 0/0
   depth      level 19 of 26
   paths      this is the one call path that reaches this function
   callers    100.0%  BeanSerializer.serialize
   callees     15.0%  ProductDto.getAttributes
   stack      java.lang.Thread.run › TaskThread$WrappingRunnable.run › …
```

- The first line is the thread states, with the one drawn marked, and at its
  right which levels are in view, with an arrow and the paging key for each
  side that has more. A narrow terminal gets the short form of both before it
  loses either.
- The caption is the table's: the window analysed and the samples of the
  thread state. It wraps onto a second line.
- One line per level, the root `all` first (an icicle; there is no bottom-up
  orientation). The root spans the whole width at every size, so there is
  nothing to pan to: a frame is as wide as its share of the zoomed frame, its
  callees start at its left edge in the order of their names, and what is
  left at the right is the frame's own samples. A frame too narrow to read is
  reached by zooming into its caller.
- The first cell of a frame says what it is, with or without colour: `▏` a
  frame, `█` the cursor, `▌` a search hit, `▍` another call of the function
  the cursor is on, `·` callees too narrow to draw.
- A cell shows as much of the name as fits, the most telling part first: the
  whole name, then its packages as initials, then type and function, then
  the function alone, then the start of the function. It never shows the
  tail of a type.
- With colour a frame has one of eight warm colours, picked by its function's
  name. **The cursor is black on white**, which nothing else on the screen
  is, and keeps that on a search hit; a hit is a quiet blue.
- Under the graph: the search, when there is one; the selected frame's name
  **in full, on as many lines as it takes**; its samples and shares; and its
  details.

No line is wider than the terminal and the screen is exactly as tall, in
every state (`TestFlameGraphFillsTheTerminalExactly`,
`TestFlameGraphFitsEveryWidth`). Lines are not filled with spaces to the last
column, so a terminal that counts one symbol wider than dtui does has room
for it.

The graph reads the analysis the table holds (`hotspot-stacks`), so opening it
asks the environment for nothing. `r` asks as the table's `r` does; an answer
that is the analysis already shown changes nothing and says so. `t` is
another window and so another analysis. One analysis carries every thread
state: `[` and `]` cost nothing.

The table keeps its graph. Returning to it finds the zoom, the selection and
the search as they were left. What the table hands over each time is its
thread state and the function of its selected row, each only when it changed
since the last time. The function is handed over as a search, **and the
cursor starts on its first frame on screen**: it is the frame that was asked
about.

## Details of the selected frame

The arrows select a frame and the lines under the graph follow: no key is
needed to see them. All of it is read off the tree in memory.

| Line | What it says |
|---|---|
| name | the function as the stacks spell it, wrapped, never shortened |
| numbers | self and total samples in the thread state drawn, share of all and of the zoomed frame, callees too narrow to draw |
| `package` `type` `function` | the name taken apart (`file` and `function` for a script's frame); `part of` names the runtime a frame belongs to |
| `self/total` | the frame's samples in every thread state, not only the one drawn |
| `depth` | its level and the deepest level of the graph |
| `paths` | how many call paths reach the function in the whole graph, and its total and self over all of them, each stack counted once: the number the functions table calls PATHS |
| `callers` `callees` | the three heaviest of each over all those paths, with their share of the function's total, and how many more there are |
| `stack` | the callers from the outermost down to the frame, shortened and wrapped |

On the root the details are the samples per thread state, the depth and the
frames the stacks start in.

**Placement.** The repository's preview pane goes beside a table from 110
columns and under it on a narrow, tall screen. The flame graph's details
follow the same preference and key (`P`, on by default) but always sit under
the graph: a graph's width is its resolution, and a side pane would take two
fifths of it. From 110 columns the details are set in two columns instead.
They take what the graph does not need and leave it at least eight levels;
`d` gives them the room and leaves the graph three. Where there are fewer
than three lines for them (a terminal of about 20 rows on a deep graph) they
are left out and the name and numbers stay. What does not fit is counted in
a last line.

## Opening the source of a frame

`O` on a frame opens the file it was compiled from in the reader's editor:
one key, when one file matches and the editor is known. The design, with what
it must never do, is in ADR-0023; this is how it behaves.

1. **The directory.** Files are looked up under the repository dtui was
   started in: the nearest directory at or above the working directory with a
   `.git` entry, or the working directory. Nothing outside it is ever read or
   opened. The browser build has no directory: there the key says so and the
   footer does not offer it.
2. **The index.** The directory is walked once per session, off the update
   loop, when the graph first opens. The walk skips symbolic links, hidden
   directories and `node_modules`, `vendor`, `target`, `build`, `dist`,
   `out`, `bin`, `obj`, virtual environments; it stops at 300,000 entries,
   60,000 source files or 3 seconds and says so when a lookup then misses.
3. **The file.** A frame's name selects among the files the walk saw; it is
   never joined to a path.

   | Frame | File |
   |---|---|
   | JVM `com.example.shop.CartService$Line.total` | `CartService.java`, `.kt`, `.scala` or `.groovy` in a directory ending in `com/example/shop`; nested, anonymous and lambda classes map to the outer class, `FooKt` to `Foo.kt`; a Kotlin or Scala file anywhere that declares the package |
   | Go `example.com/shop/internal/cart.(*Service).AddItem` | the Go files of the directory the import path ends in that declare the function (with its receiver); `main.main` is looked for in every Go file |
   | Python `shop.reports.daily.render` | `shop/reports/daily.py`, also for `…daily.Report.render` |
   | Script `/app/src/server/handler.ts:resolve` | the file whose path shares the longest tail with the frame's |
   | .NET `Shop.Services.CartService.AddItem` | `CartService.cs`, `.fs` or `.vb` that declares the namespace |
   | `java.`, `javax.`, `jdk.`, `sun.`, `kotlin.`, `scala.`, `System.`, `Microsoft.`, `node:`, the Go standard library | not searched: the status line names the runtime |

4. **The line.** The analysis carries no file and no line for a class-based
   frame (a label is `package.Type.method`), and no line for a script's
   frame. So a line is never the line that was sampled: it is where the
   function's name is declared, found by a scan of the file per language.
   The status line always says which it was: `the declaration of total`;
   `approximate: the first of several declarations` (overloads, or a nested
   class with a method of the same name); `approximate: the type's
   declaration` when the function is not found by name (a generated method,
   a static initializer); `the top of the file` when nothing could be told.
   No line is ever invented.
5. **The editor.** `DTUI_EDITOR` names it (`vscode`, `vscode-insiders`,
   `vscodium`, `cursor`, `windsurf`, `zed`, `idea`, `pycharm`, `goland`,
   `webstorm`, `rider`, `phpstorm`, `rubymine`, `clion`, `sublime`,
   `textmate`). Without it, the terminal dtui runs in says which editor it
   belongs to (VS Code, Cursor, Zed, a JetBrains IDE), and failing that
   `VISUAL` or `EDITOR` is read as the name of a program (`code`, `subl`, …),
   never run. The file opens through a link in the editor's scheme
   (`vscode://file/<path>:<line>`, `idea://open?file=<path>&line=<line>`)
   handed to the system the way `o` hands it a Dynatrace link, with the path
   percent-encoded. With no editor known (a terminal editor, nothing set) the
   place is copied as `path:line` and the status line says how to name one.
6. **The answer.** One file opens. Several (the same class in two modules)
   are offered in a short list: `enter` or a digit opens, `y` copies. None
   says so, and that the frame is a library's or another repository's.

The footer shows `O open source` only on a frame that may have a file: not on
the root, not on a runtime's frame, and, once the directory has been walked,
only where a file of the right name exists.

## Mouse

Not built. dtui does not turn on mouse reporting anywhere, and turning it on
changes how text is selected in every view of every terminal, so it is a
decision for the whole application and not for one screen. If it is taken:
click selects a frame, double click zooms, the wheel scrolls levels.

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
| `O` | open the selected frame's source in the editor | none |
| `P` | the details of the selected frame on or off (the preview preference) | none |
| `d` | give the details the room, or give it back | none |
| `r` | refresh: the analysis is asked for again | reset the view |
| `y` `c` `o` `t` `?` `q` | dtui's: copy the function, copy the command, open, timeframe, help, quit | `?` is a debug bar, `q` quits |

Where the two differ, a dtui key keeps its meaning: `h` and `l` are drill
letters, `f` is facets and `b` is fleet on every table, and `r` is refresh
everywhere. A flamelens habit must not start an analysis that runs for a
minute, so there is no reset key; `esc` undoes the search and the zoom. `O`
is the capital of `o`: `o` opens the selection in Dynatrace, `O` in the
editor. `d` is "describe / details" on every table.

The footer is one line and is never cut: it drops whole hints, the global
ones first, and keeps the movement keys, `enter zoom`, `tab`, `/` and `?`
(`TestFlameGraphFooterKeepsItsKeys`). `?` lists these keys first when it is
opened on the graph; the help wraps to the terminal and scrolls.

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
go test ./internal/tui -run 'Flame|Source|Arrow' # the view, driven by keys
go test ./internal/source                       # a frame's file, and the editor's link
go test . -run 'Stacks|FlameGraph'              # the sources: stacks reconcile with the table
ls internal/tui/testdata/profiling-flame-*.golden
```

## How it is built

| Part | Where | What it knows |
|---|---|---|
| Model | `internal/tui/flame` | the tree, the layout for a width, the selection, zoom, scroll and search. No terminal framework (`TestFlameIsFrameworkFree`) |
| View | `internal/tui/flameview.go`, `flamedraw.go`, `flamedetails.go` | fetching and keys; how the body's lines are shared out and drawn; the details |
| Source | `internal/source`, `internal/tui/flamesource.go` | a frame's name taken apart, the file it points at under one directory, an editor's link; the `O` key. No terminal framework, nothing of Dynatrace |
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
| `Selected` bar: name, samples, share of all and of the zoom | done | the name in full on as many lines as it takes, with self, the callees not drawn, and the details |
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
| Label clipped at the frame's width | done | packages as initials, then type and function, then the function, then its start; counted in cells |
| Width from the share of the parent, truncated | done | integer prefix sums: no unpainted cells from rounding |
| A frame below one cell is not drawn, nor anything under it | done | plus a marker for the last such callees |
| Warm colour by a hash of the frame's path | partial | by a hash of the function's name: one colour per function |
| Dark or light text by the background's luma | done | one dark ink on every step of the ramp |
| Selected frame on a near white background | done | black on white, and the same on a search hit |
| Matched frames on a blue background | done | a quiet blue, so that the cursor is the loudest thing on screen |
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
| An indicator for more levels below | done | arrows for both sides, the levels in view and the paging keys; flamelens computes one and does not show it |

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
| `enter` searches the row's function in the graph | partial | `tab` carries the selected row's function and puts the cursor on it; `enter` stays the callers and callees drill |
| The `Match` bar stays visible in the table | missing | |

### Global

| flamelens | Status | Note |
|---|---|---|
| `q` and `ctrl+c` quit | done | |
| Live mode, freeze with `z` | n/a | an analysis is of a past window |
| Mouse | n/a | flamelens has none either; see Mouse above |
| Resize: laid out again | done | a selection that got too narrow moves to its caller |
| Very narrow terminals | done | below 40 columns or 6 body rows the table refuses to open the graph, and an open one says it needs more room |
| `--sorted`, `--echo`, `--debug` | n/a | |

63 rows: 46 done, 8 partial, 4 missing, 5 n/a.

## Beyond flamelens, and not built

Built: the thread-state lens with the place kept, the details of the selected
frame (its samples in every thread state, the call paths of its function,
callers and callees, its stack), its source opened in the editor, the marker
for callees too narrow to draw, a zoom history, the indicator for more
levels, labels counted in cells and safe to print, a floor for the terminal's
size.

Not built:

- a flame graph of allocations;
- `offcpu` and `all` as thread states;
- the callers and callees drill from a frame (the details list the three
  heaviest of each);
- copying a frame's whole stack;
- the zoom and the selection kept when a new analysis arrives;
- one page with a `functions | flame` strip in place of a pushed view;
- the mouse;
- a line number that is the line sampled: the analysis carries none;
- a differential, a timeline, a bottom-up orientation, a sandwich view: out
  of scope by ADR-0023.

The shortcuts taken are in [tech-debt.md](../dev/tech-debt.md).
