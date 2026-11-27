# ADR-0024: Opening a frame's source is local, read-only and never runs anything

**Status:** Proposed · **Date:** 2026-11-27

## Context

The flame graph ([ADR-0023](0023-flame-graph-is-a-bespoke-screen.md)) shows
which function is hot. The next thing its reader wants is the code, and the
code is on their machine: dtui is usually started in the application's
checkout.

Getting there from a frame means acting on the machine dtui runs on, with a
name that comes from the environment. dtui has done that in two places only:
the clipboard (`y`) and handing the system a Dynatrace link (`o`). Two rules
bear on a third:

- [ADR-0011](0011-strictly-read-only.md): dtui changes nothing, and never
  takes the terminal away to run another program.
- A frame's label is text an attacker can choose. Whoever names a class or a
  script in a monitored process chooses what dtui is handed.

## Decision

`O` on a frame of the flame graph looks for the file the frame was compiled
from under the repository dtui was started in, and asks the system to open
it in the reader's editor. The limits are the decision.

- **It is local and read-only.** It reads files under one directory and asks
  the system to open one. Nothing is sent to or changed in Dynatrace.
- **A frame's name is hostile text.** It is parsed, compared with the names
  of files a walk of the directory found, and quoted before it is used as a
  pattern. It is never executed, never passed to a shell, never joined to a
  path and never put into a link. The path in a link is the walk's own. It
  is resolved again before it is handed out and refused unless it still
  resolves to itself (a file, or a directory on the way to it, that became a
  symbolic link since the walk is not what the walk saw), and it is
  percent-encoded.
- **The directory is the repository dtui was started in**: the nearest
  directory at or above the working directory with a `.git` entry that is
  there. Outside a repository there is none and nothing is walked. A
  repository that is the home directory, or the root of a volume, is refused
  although it holds `.git`: a walk of either reads a person's files and not
  an application's. A repository inside one is looked in.
- **The walk and the reading are bounded.** The walk does not follow
  symbolic links, is bounded in entries, files and time, and runs off the
  update loop, once per session. Reading the files for one frame is bounded
  in files, bytes and time, with the time looked at between the reads of one
  file as well as between files. One lookup runs at a time: a new one ends
  the one before it, and only the answer to the last one asked is acted on,
  and only while the cursor is still on the frame.
- **Only a regular file is opened.** A path the walk saw can be a named
  pipe, a device, a socket, a directory or a link by the time it is read. So
  the path is looked at without following a link, opened without waiting and
  without following a link in its last step where the system has flags for
  that, and the opened file is checked to be the regular file that was
  looked at. Anything else is counted as not read and is neither offered nor
  handed to an editor.
- **dtui starts no editor.** It hands the system a link in the editor's own
  scheme, as `o` hands it a Dynatrace link, and does not wait for it. A
  terminal editor in dtui's terminal would need the alternate-screen
  handling dtui chose never to have, so without a known editor the file's
  path is copied instead.
- **No configuration is needed and none is added to the project file.** The
  editor is taken from the terminal dtui runs in, or from `VISUAL`/`EDITOR`
  read as a name; `DTUI_EDITOR` overrides both, and a value of it that names
  no known editor is said at the start. An editor is a person's, and
  `.dtui.yaml` is committed and must not behave differently per machine, so
  the setting is an environment variable.
- **It says what it does not know.** The analysis has no line numbers: the
  line is where the function's name is declared, and the status line says
  whether that is exact, a guess among several, the type's line or the top
  of the file. It also says, first, when a match is not known to be the only
  one: files that were not read, matches that are not offered, a walk that
  stopped early.
- **The browser build refuses it.** It has no directory; the key says so.

It must never: run a program named by the environment or by a setting; open
a path the walk did not see; read outside the source directory; follow a
symbolic link; search for a runtime's frame; show a line it did not find in
the file; or take the terminal away from dtui.

Alternatives rejected:

- **Starting `$EDITOR`.** It runs a program a setting names, in dtui's
  terminal, which ADR-0011 rules out twice.
- **A setting in `.dtui.yaml` for the source directory or the editor.** A
  committed file would then decide what a colleague's machine reads and
  opens.
- **A deep link to the code in a hosting service.** It needs the commit the
  process was built from, which the analysis does not carry.

## Consequences

- dtui has a package that reads the reader's files, `internal/source`. It
  knows no terminal framework and nothing of Dynatrace, imports only what a
  list allows, and takes from `os` only the names that look at a path and
  open a file for reading (`TestSourceLookupRunsNothing`).
- **Known limits, accepted for now.** They are listed with their cures in
  [tech-debt.md](../dev/tech-debt.md).
  - The checks on a path and the open are separate steps. A directory on the
    way to a file can be swapped for a link between them, and the open then
    follows it. The cure is to open everything beneath one directory handle
    (`os.Root`).
  - On Windows the identity of the file that was looked at is read through
    its path only after the open, so a swap for another regular file is not
    seen there; a swap for a link is. The Windows build compiles and its
    tests are written, and none of it has run on Windows.
  - A hard link inside the directory is a regular file like any other, and
    leads wherever it was linked from. The walk cannot tell, and neither can
    a reader of the directory.
  - A read of a regular file on a file system that does not answer (a dead
    network mount) is not interrupted. Interrupting it would take a second
    thread of execution that could not be stopped either.
- **Removing `O` is cheap, and the graph does not need it.** It would take:
  deleting `internal/source` and `internal/tui/flamesource.go` with their
  tests; the `O` case and its footer hint in `flameview.go`; the three
  message cases and the `warm` call in `app.go`; `Options.SourceRoot`,
  `Editor`, `EditorUnknown` and `SourceRefused` with `sourceSettings` in
  `main.go`; the "file", "type" and "part of" lines of the frame's details,
  which read the same parser; the guard `TestSourceLookupRunsNothing`; and
  this record. Nothing else in the flame graph calls into it.
