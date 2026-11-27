# ADR-0023: The flame graph is a bespoke screen, and the last depth of profiling

**Status:** Proposed · **Date:** 2026-11-27

## Context

The profiling prototype reads one process as a table of functions and, one
function at a time, as its callers and callees. Finding where the samples go
that way means walking the call tree a row at a time. A flame graph shows the
whole tree at once, and one existing terminal viewer,
[flamelens](https://github.com/YS-L/flamelens), has an interaction model
people already know: move along callers and callees, zoom into a frame, search
by regular expression.

Four rules stand against drawing one in dtui:

- [ADR-0003](0003-declarative-view-catalog.md) and gate 4 of the vision: views
  are catalog `Spec`s. A flame graph is not a table.
- [ADR-0009](0009-navigate-dont-draw.md) and the anti-goal "no graph drawing".
- [ADR-0012](0012-navigate-analyze-mutate.md) and
  [ADR-0014](0014-breadth-not-depth.md): analysis belongs to the web UI, a noun
  is done at list, detail and drills, and rendering exists to pick the next
  keystroke.
- [ADR-0008](0008-one-owner-per-key.md) and the frozen key vocabulary.

## Decision

dtui draws a flame graph of the hotspots analysis as a bespoke `viewModel`,
written in Go in this repository. It is an exception to ADR-0003, argued
narrowly:

- **It is the navigation surface of a call tree, not a chart.** It has no
  axes, no legend and no export. A frame is a place to stand: the keys move to
  its caller, its heaviest callee and its neighbours, and `enter` makes it the
  whole width. It replaces walking callers and callees row by row, which is
  the depth the prototype already has. ADR-0009 bans laying out a graph of
  arbitrary edges; a call tree fills the screen by construction, like the
  trace waterfall does.
- **It is a second reading of the analysis the functions show, not a second
  analysis.** Opening it asks the profiling source for the stacks of that
  analysis, which the source keeps with the last few it ran: one read, of
  what the table already ran. When the analysis is no longer kept, the
  source runs a new
  one, of a later window; the graph's caption names its own window, and the
  status line says once that the two readings are now of two analyses. From
  then on every key but `r` and `t`, which ask for an analysis as they do on
  the table, is answered from memory ([ADR-0013](0013-query-budget.md)).
- **Its rules live in a package without a terminal framework**,
  `internal/tui/flame`, so that what the screen shows for a tree, a size and a
  key is tested as plain values (`TestFlameIsFrameworkFree`).
- **Its keys are flamelens's wherever dtui has not given the key away.** `tab`
  switches between the functions table and the graph, as it switches between
  "Top" and "Flamegraph" there; it no longer cycles the lens strip on
  `:hotspots`, where `[` and `]` do that. Where a flamelens key is a dtui
  key, dtui's meaning wins: `r` refreshes (reset is `esc`), `f` and `b` are
  `ctrl+f` and `ctrl+b`, and `h` and `l` are not bound, because they are
  drill letters everywhere else. The arrows and `j`/`k` carry the movement.
- **The details of the selected frame follow the preview preference** (`P`)
  but sit under the graph, never beside it: a graph's width is its
  resolution. They are read off the tree in memory.
- **This is the depth cap of profiling.** A differential between two
  windows, a timeline to pick a sub-window from, a bottom-up orientation and
  a per-function sandwich view are out of scope. Asking for one is asking to
  reverse this ADR, not to extend the view.

Alternatives rejected:

- **Running flamelens from dtui.** It needs a second program installed
  (gate 6, zero configuration), handing the terminal to another program and
  taking it back is the alternate-screen handling
  [ADR-0011](0011-strictly-read-only.md) chose never to have, the browser
  build has no processes, and while flamelens owns the terminal there is no
  thread-state lens, no timeframe and no `c` or `o`.
- **Linking flamelens.** It is a Rust program that owns the terminal; dtui is
  one static Go binary that also builds for the browser.
- **An external viewer** (a deep link into the web UI's profiler). `o` on
  the graph opens the process in the web UI, as it does on the table; a link
  to the profiler itself would be the better exit and is not there yet.
  Either way it leaves the terminal, which is the one thing the person this
  tool is for did not want to do.
- **Folded-stack output only**, for flamelens, speedscope or flamegraph.pl to
  read. That is dtctl's to offer and worth having, but it serves the people
  who already have such a viewer and gives the others nothing to look at.

## What is not in this decision

`O` on a frame opens the file it was compiled from in the reader's editor.
That is a decision of its own, with its own limits, and is recorded in
[ADR-0024](0024-open-a-frames-source-locally.md). The graph does not depend
on it: it can be accepted, and can ship, without the key.

## Consequences

- The list of bespoke screens in ADR-0003 grows by one, and so does the
  list of bespoke pages ADR-0014 enumerates; both name it. The flame graph
  is reached from `:hotspots` only and has no command-bar name: it is a
  second reading of that view, not a noun.
- `tab` on `:hotspots` opens the graph instead of the next lens. This is the
  one place a table's `tab` is not its lens strip; `Spec.TabTarget` names it.
- Colours come from a flame ramp added to `internal/tui/theme` with hand
  picked 256 and 16 colour fallbacks. Without colour the structure is still
  there: every frame starts with an edge glyph, and the selected one with a
  full block (`█`) where the others have a thin edge.
- Parity with flamelens is tracked row by row in
  [profiling-flamegraph.md](../design/profiling-flamegraph.md); what is not at
  parity is in [tech-debt.md](../dev/tech-debt.md).
- The view stands and falls with the profiling prototype
  ([profiling-prototype.md](../design/profiling-prototype.md)). What it adds
  to the catalog is `Spec.TabTarget`, the names of the stacks reading (its
  two source ids, its fields and its echoed command) and three text helpers
  the tables use too (`ShortFrame`, `OneLine`, `GroupInt`). What it adds to
  the calls dtui makes is that one read of the stacks, through dtctl.
- Limits. The tree is built from folded stacks, so a frame label that
  contains `;` splits into two frames until dtctl exports the tree itself.
  Allocations have no graph. The browser build has no profiling source, so
  no graph either.
