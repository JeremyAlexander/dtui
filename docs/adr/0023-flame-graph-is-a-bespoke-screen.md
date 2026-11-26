# ADR-0023: The flame graph is a bespoke screen, and the last depth of profiling

**Status:** Proposed · **Date:** 2026-11-26

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
  its caller, its widest callee and its neighbours, and `enter` makes it the
  whole width. It replaces walking callers and callees row by row, which is
  the depth the prototype already has. ADR-0009 bans laying out a graph of
  arbitrary edges; a call tree fills the screen by construction, like the
  trace waterfall does.
- **It costs no request.** It reads the analysis the functions table already
  holds. Every key but `r` and `t`, which ask for an analysis as they do on
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
- **An external viewer** (a deep link into the web UI's profiler). It is the
  `o` exit and stays one, but it leaves the terminal, which is the one thing
  the person this tool is for did not want to do.
- **Folded-stack output only**, for flamelens, speedscope or flamegraph.pl to
  read. That is dtctl's to offer and worth having, but it serves the people
  who already have such a viewer and gives the others nothing to look at.

## Opening a frame's source

`O` on a frame opens the file it was compiled from in the reader's editor.
It is the one place dtui acts on the machine it runs on beyond the clipboard
and the browser, so its limits are part of this decision.

- **It is local and read-only.** It reads files under one directory and asks
  the system to open one. Nothing is sent to or changed in Dynatrace
  ([ADR-0011](0011-strictly-read-only.md)).
- **A frame's name is hostile text.** It comes from the environment, and
  whoever names a class or a script there chooses it. It is parsed, compared
  with the names of files a walk of the directory found, and quoted before it
  is used as a pattern. It is never executed, never passed to a shell, never
  joined to a path and never put into a link. The path in a link is the
  walk's own, resolved again and refused if it is no longer inside the
  directory, and percent-encoded.
- **The directory is the repository dtui was started in**, and nothing
  outside it is read or opened. The walk does not follow symbolic links, is
  bounded in entries, files and time, and runs off the update loop. Reading
  the files for one frame is bounded the same way, in files, bytes and time,
  and the time is looked at between the reads of one file as well as between
  files.
- **Only a regular file is opened.** A path the walk saw can be a named
  pipe, a device, a socket or a directory by the time it is read, and
  opening one of those can wait forever. So the path is looked at without
  following a link, opened without waiting where the system has a flag for
  that, and the opened file is checked to be the regular file that was
  looked at. Anything else is counted as not read and is neither offered
  nor handed to an editor. One wait is left: a read of a regular file on a
  file system that does not answer (a dead network mount) is not
  interrupted, because that would take a second thread of execution that
  could not be stopped either.
- **dtui starts no editor.** It hands the system a link in the editor's
  scheme, as `o` hands it a Dynatrace link. A terminal editor in dtui's
  terminal would need the alternate-screen handling dtui chose never to
  have, so a terminal editor gets the place copied instead.
- **No configuration is needed and none is added to the project file.** The
  editor is taken from the terminal dtui runs in, or from `VISUAL`/`EDITOR`
  read as a name; `DTUI_EDITOR` overrides both. An editor is a person's, and
  `.dtui.yaml` is committed and must not behave differently per machine, so
  the setting is an environment variable and not a key of that file. There
  is no personal settings file to put it in, and this is not the feature to
  add one for.
- **It is honest about the line.** The analysis has no line numbers. The
  line is where the function's name is declared, and the status line says
  whether that is exact, a guess among several, the type's line or the top
  of the file.
- **The browser build refuses it.** It has no directory; the key says so.

It must never: run a program named by the environment or by a setting; open a
path the walk did not see; read outside the source directory; follow a
symbolic link out of it; search for a runtime's frame; show a line it did not
find in the file; or take the terminal away from dtui.

## Consequences

- The list of bespoke screens in ADR-0003 grows by one. The flame graph is
  reached from `:hotspots` only and has no command-bar name: it is a second
  reading of that view, not a noun.
- `tab` on `:hotspots` opens the graph instead of the next lens. This is the
  one place a table's `tab` is not its lens strip; `Spec.TabTarget` names it.
- Colours come from a flame ramp added to `internal/tui/theme` with hand
  picked 256 and 16 colour fallbacks. Without colour the structure is still
  there: every frame starts with an edge glyph, and the selected one with the
  gutter bar the tables use.
- Parity with flamelens is tracked row by row in
  [profiling-flamegraph.md](../design/profiling-flamegraph.md); what is not at
  parity is in [tech-debt.md](../dev/tech-debt.md).
- The view stands and falls with the profiling prototype
  ([profiling-prototype.md](../design/profiling-prototype.md)): it adds no
  seam to the catalog beyond `TabTarget` and no call to the environment.
- Limits. The tree is built from folded stacks, so a frame label that
  contains `;` splits into two frames until dtctl exports the tree itself.
  Allocations have no graph. The browser build has no profiling source, so
  no graph either.
