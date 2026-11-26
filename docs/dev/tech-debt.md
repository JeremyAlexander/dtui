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
- A Windows path with a space in a segment is cut at the space.
- The pass-through lists (`javaPassThrough` and its siblings) match by
  method name alone, so an application method named `invoke` or `next`
  never counts toward a key unless it is the throw site.
- `rootException` follows `exception.caused_by_id` without that field ever
  having been seen on a live span.

Fix: each needs a corpus case first, and raises `ExceptionKeyVersion` once
keys are stored or shared anywhere.
