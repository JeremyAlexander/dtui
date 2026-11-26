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

- **Read and rewrite in the main package.** `internal/tui` may import only
  dtctl's `pkg/exec` and `pkg/output` (`TestTUIHasNoDirectTenantAccess`), and
  that allowlist stays as it is. The main package reads the file with
  `pkg/reposcope`, resolved for the selected context's environment host and
  the launch directory, and builds the rewrite there: a function from a
  view's DQL to `InsertFilter`'s result for the resolved entry. It injects
  that function into `internal/tui` the way `sources.go` injects a `Source`.
  dtui does not re-implement the lookup, the renderer or the insertion.
- **Narrow logs and spans.** The entry narrows the catalog views over logs
  and spans (`:logs`, `:traces`) through dtctl's own `InsertFilter`.
- **Applied only where `InsertFilter` applies it.** `InsertFilter` leaves a
  query unchanged when one of its own `filter` or `filterOut` stages names a
  field the entry binds for that data object: `dt.entity.process_group` on
  logs, `dt.entity.service` on spans, and `service.name`,
  `k8s.namespace.name` and `k8s.workload.name` on both, each only when the
  entry has a binding of that kind. The user's own filter wins. dtui writes
  such stages itself:
  - a pin of a process group, a Kubernetes namespace or a workload on
    `:logs` (`SignalFilter` compares `dt.entity.process_group`,
    `k8s.namespace.name` and `k8s.workload.name`);
  - a facet on one of the bound fields, on either view.

  On those pages the scope is not applied and the page runs with the pin or
  facet alone. A pin on `:traces` compares `dt.smartscape.*` fields, which
  no entry binds, so the scope is ANDed with it; so is a lens, a `/` search
  and a facet on any other field. `InsertFilter` also refuses a query that
  fetches logs or spans again in a subquery, an entry that binds nothing
  the data object is filtered by, and text with a single quote outside
  every string literal and comment. The catalog's log and span queries
  quote with double quotes, so the last does not arise from a facet value.
- **Segments are independent.** `.dtui.yaml` segments travel beside the
  query, not in its text, so they are ANDed with whatever the query does,
  scoped or not. Neither narrowing takes precedence over the other.
- **Claim it only where it applied.** The header shows the scope only on a
  view whose query actually took it (ADR-0007). A view that refused it says
  why, from `InsertFilter`'s outcome, and that covers the pinned and faceted
  pages above.
- **On by default, never fatal.** When an entry resolves, the scope is on.
  `DTCTL_NO_REPO_SCOPE` or a `--no-repo-scope` launch flag turns it off for
  the session. A broken file, no covering entry or a tie between entries is
  a warning, never a failed launch, like a broken `.dtui.yaml`.
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
- The command echo (`c`) carries `--no-repo-scope` on a scoped view. The
  echoed DQL already contains the filter, so dtctl run in the same directory
  would not scope it twice: `InsertFilter` sees a filter on a bound field,
  leaves the query alone and warns that the scope was not applied. The flag
  avoids that warning and makes the echo run exactly what dtui ran, wherever
  it is pasted. The flag does not exist in dtctl before 0.47.0, so the echo
  of a scoped view fails on an older dtctl.
- Zero configuration still works
  ([ADR-0010](0010-curated-defaults-runtime-discovery.md)): no scope file
  means no scope, and every view opens as it does today.

## Open questions

These are maintainer decisions this ADR does not make.

- **Is `--no-repo-scope` part of the launch-flag contract?**
  [ADR-0018](0018-launch-flags-are-a-handoff-contract.md) makes the flags it
  lists stable: a rename or a change of meaning breaks every command already
  handed out. dtctl's repo scope, its flag and `DTCTL_NO_REPO_SCOPE` are
  experimental and may change in any dtctl release. Adding the flag to that
  contract would promise more than dtctl does; keeping it outside, as
  `--listen` is, leaves it free to follow dtctl until the feature is stable.
- **Is there a toggle while dtui runs, and on which key?** This ADR assigns
  none. Under [ADR-0008](0008-one-owner-per-key.md) a key has one owner, so
  a toggle needs a free key or a command-bar command. Until one is chosen,
  the scope is switched off at launch only.
- **What does `dtui send` do?**
  [ADR-0019](0019-agents-may-navigate-a-listening-tui.md) settles two
  things: a request is a page descriptor in the launch grammar, and it never
  changes what the user chose for the session, as with the context. It does
  not say whether the scope is session state, which a pushed `:logs` or
  `:traces` page would inherit from the listening TUI, or page state, which
  `send` could carry as a new field of the `NavigateMsg` wire format.
