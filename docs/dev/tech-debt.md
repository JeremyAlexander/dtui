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

## Exception keys: what the fingerprint does not read

`internal/tui/catalog/exc*.go` groups exceptions by a key. Known gaps, each
of which splits or merges issues it should not:

- A JavaScript `[cause]:` chain in one stack text is not followed; all
  frames of the text are taken as one exception's (`jsRootCause`).
- A route inside a URL is replaced with the URL (`<url>`), so two failing
  endpoints of one message merge; a bare route (`GET /cart/7/items`) is kept.
- A Go dependency counts as application code when its trace has no file
  lines and its host is not in `goVendorPrefixes`; an application package
  named like a standard-library one counts as the runtime's.
- A Go main module, or a sibling module under `replace`, that the trace
  prints as `module@version` (a `-trimpath` build, an install by version)
  is taken for a dependency. The cure is to learn the main module from the
  file line of `main.main`.
- A `pkg/errors` style Go trace (a bare `main.run` over a file line, no
  arguments) yields no frames and falls to the message rung.
- A segment of a Windows path with more than three words ends the path at
  its first space, and so does the last segment (`C:\data\my cart.json`).
- The pass-through lists (`javaPassThrough` and its siblings) match by
  method name alone, so an application method named `proceed` or `next`
  never counts toward a key next to other application frames unless it is
  the throw site.
- A cause chain is found by `exception.is_caused_by_root` alone.
  `exception.caused_by_id` is not read, since no live span seen so far
  carried it, and `exception.escaped` has no role: instrumentation that
  links its events without flagging the root is keyed on its last event.
- A snake-case code (`error_code_404`) loses its number to the id rule,
  which runs before the code rule: `error_code_404` and `error_code_500`
  merge.
- Messages: a unit of more than seven letters (`250milliseconds`), an IPv6 zone id and `host:addr`, punctuation
  directly after a URL, a status inside a JSON body (`{"status":404}`), and
  a message that crosses the 4096-byte input cap are not read as what they
  are.
- JavaScript: V8's `bound` and symbol frames, Safari's `global code` and a
  `fn@url` frame with a line but no column are dropped.
- Java: nested lambdas (`lambda$lambda$addItem$0$1`), Scala 3 anonymous
  functions (`$anonfun$1`) and a JPMS module version with `+` keep an
  ordinal or drop the frame. Needs real traces before a rule.
- Modules: a base64 bundle hash (`Cart-B4xK9pQ2.js`), a dotted Windows
  Python directory (`C:\Python3.11\Lib`), a `#fragment`, a repeated `/./`
  and a lower-case Windows root (`/users/deploy`) are kept as written.
- Only the Java reader is tuned: the .NET, Go and JavaScript readers run
  their expressions on every line without a cheaper test before them
  (`BenchmarkParseStackFrames`).
- The fuzz targets check that a key is well-formed and repeatable, not
  that it depends on its input: a fingerprint that returned one constant
  key would pass them.

Fix: each needs a corpus case first. Until a release has shipped `e1` keys
the cure is free; after that each one moves keys and so costs a key version
(`ExceptionKeyVersion`).
