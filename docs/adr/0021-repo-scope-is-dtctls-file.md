# ADR-0021: Repo scope is dtctl's file; dtui applies it and never writes it

**Status:** Proposed · **Date:** 2026-11-26

## Context

dtctl is adding an experimental repo scope: a committed
`.dtctl-repo-scope.yaml` at the git root, never looked up past `.git`, keyed
by environment host. An entry binds a directory of the repository to the
services, process groups, Kubernetes namespace and workload, and service
names that run its code. The file holds bindings only, never DQL: dtctl
renders the filter itself, with every literal validated and quoted, so a
cloned repository can narrow a query but cannot widen it or inject text into
it.

A user who launches dtui inside such a repository expects the same narrowing
dtctl gives them there. dtui already has one committed narrowing, the
`segments` of `.dtui.yaml`, and one rule for claiming any of them
([ADR-0007](0007-scope-honesty.md)). dtctl exports the pieces: `pkg/reposcope`
(`Locate`, `Load`, `Resolve`, `Filters`) and `pkg/dql` (`InsertFilter`, whose
outcome says whether the filter was applied or why not). They only read,
apart from `Save`.

## Decision

The scope file is dtctl's. dtui applies the entry dtctl resolves and owns
nothing about the format.

- **Read through dtctl only.** The main package reads the file with
  `pkg/reposcope`, resolved for the selected context's environment host and
  the launch directory, and hands the result to `internal/tui` as data; the
  dtctl import allowlist of `internal/tui` stays as it is. dtui does not
  re-implement the lookup or the renderer.
- **Narrow logs and spans, alongside segments.** The entry narrows the
  catalog views over logs and spans (`:logs`, `:traces`) through dtctl's own
  `InsertFilter`. It is ANDed with `.dtui.yaml` segments: two independent
  narrowings, neither takes precedence, each with its own toggle.
- **Claim it only where it applied.** The header shows the scope only on a
  view whose query actually took it (ADR-0007). A view that refused it says
  why, from `InsertFilter`'s outcome.
- **On by default, never fatal.** When an entry resolves, the scope is on.
  `DTCTL_NO_REPO_SCOPE`, a `--no-repo-scope` launch flag or the toggle turn
  it off. A broken file, no covering entry or a tie between entries is a
  warning, never a failed launch, like a broken `.dtui.yaml`.
- **It never selects the environment.** `.dtui.yaml`'s `environment` and
  `--context` keep that job; the scope file is only consulted for the host
  they selected.
- **Read-only.** dtui never calls `Save`, discovery or the Live Debugger
  rendering ([ADR-0011](0011-strictly-read-only.md)). Creating and editing
  entries stays in dtctl.

For example, a repository whose file has an entry `checkout` for
`abc12345.apps.example.invalid` covering `services/checkout`: dtui launched
in that directory on that environment opens `:logs` and `:traces` narrowed
to `checkout`'s bindings, and every other view with no scope in its header.

## Rejected alternatives

- **A dtui-internal parser of the scope file.** The renderer is
  security-relevant, and a second copy would drift from dtctl's.
- **Rendering the entry as facets.** An entry is an OR across fields; facets
  are single-field and ANDed.
- **Shelling out to `dtctl repo-scope current`.** dtui links dtctl's
  packages and never invokes the binary
  ([ADR-0005](0005-dtctl-owns-configuration.md)).
- **Opt-in only.** dtctl applies the scope by default in the same directory,
  so the two tools would disagree about the same query until the user found
  a flag.
- **Merging the entries into `.dtui.yaml`.** That file walks to the
  filesystem root and ignores unknown keys; the scope file stops at `.git`
  and refuses to rewrite keys it does not know.
- **dtctl reading `.dtui.yaml` on its own.** A committed file would then
  choose the environment a mutating CLI talks to.

## Consequences

- dtui needs a dtctl release that contains `pkg/reposcope` (first planned
  for 0.47.0) and the move to the `github.com/Dynatrace/dtctl` module path.
- dtui couples to an experimental dtctl package: its API may change in any
  dtctl release, and a dtctl bump can break the build.
- The command echo (`c`) must carry `--no-repo-scope` on a scoped view. The
  echoed DQL already contains the filter, and dtctl would scope it again.
- Zero configuration still works
  ([ADR-0010](0010-curated-defaults-runtime-discovery.md)): no scope file
  means no scope, and every view opens as it does today.
