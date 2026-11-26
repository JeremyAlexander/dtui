package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Dynatrace/dtctl/pkg/resources/profiling"
	"github.com/Dynatrace/dtctl/sdk/session"
	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// The one place dtui touches dtctl's profiling package: dtctlProfiler turns
// its analyses, views and failures into the dtui-side types of profiling.go.
// When that package's API moves, this file is what changes.

// dtctlProfiler is the profiler over dtctl's pkg/resources/profiling.
type dtctlProfiler struct {
	h *profiling.Handler
}

// newDtctlProfiler profiles the environment c talks to. It also makes c's
// transport report how an analysis in flight is going (analysisObserver).
func newDtctlProfiler(c *session.Client) dtctlProfiler {
	hc := c.HTTP()
	hc.SetTransport(analysisObserver{next: hc.GetClient().Transport})
	return dtctlProfiler{h: profiling.NewHandler(c)}
}

func (p dtctlProfiler) Subject(entityID string) (string, bool) {
	return profiling.APIEntityID(entityID), profiling.EligibleEntity(entityID)
}

func profilingWindow(q profileQuery) profiling.Window {
	return profiling.Window{From: q.From.UnixMilli(), To: q.To.UnixMilli()}
}

func (p dtctlProfiler) Hotspots(ctx context.Context, q profileQuery) (hotspotReading, error) {
	analysis, err := p.h.Hotspots(withAnalysisProgress(ctx), profiling.HotspotsRequest{
		Entity: q.Entity,
		Window: profilingWindow(q),
		// One analysis feeds every thread-state lens, so it has to include
		// the threads that are not running.
		ShowWaiting: true,
	})
	if err != nil {
		return nil, profilingFailure(err, q.Entity)
	}
	return dtctlHotspots{analysis}, nil
}

func (p dtctlProfiler) Allocations(ctx context.Context, q profileQuery) (allocationReading, error) {
	analysis, err := p.h.Allocations(withAnalysisProgress(ctx), profiling.AllocationsRequest{
		Entity: q.Entity,
		Window: profilingWindow(q),
	})
	if err != nil {
		return nil, profilingFailure(err, q.Entity)
	}
	return dtctlAllocations{analysis}, nil
}

// entityIDRe is the shape of an entity id: TYPE-16HEX, upper case.
var entityIDRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9A-F]{16}$`)

// checkProcessArg refuses a launch or send argument the profiling view
// cannot analyse, where the caller still reads the reply: opened anyway it
// would land on an error screen, or as text in the row filter.
func checkProcessArg(view, arg string) error {
	switch {
	case arg == "":
		return fmt.Errorf("%s needs the process to analyse: dtui %s PROCESS-<16 hex digits>", view, view)
	case !entityIDRe.MatchString(arg):
		return fmt.Errorf("%s: %q is not a process id (PROCESS-<16 hex digits>, upper case)", view, arg)
	case !profiling.EligibleEntity(arg):
		return fmt.Errorf("%s: %s is not a process: an analysis runs on a PROCESS, a PROCESS_GROUP_INSTANCE or a PROCESS_GROUP", view, arg)
	}
	return nil
}

// profilingFailure turns the failures of an analysis into dtui's reasons.
// Anything that is not one (a failed connection, a cancelled call) passes
// through as it is.
func profilingFailure(err error, entity string) error {
	if errors.Is(err, profiling.ErrMalformedResult) {
		return &profileFailure{Reason: profileMalformed, Entity: entity,
			Message: strings.TrimPrefix(err.Error(), profiling.ErrMalformedResult.Error()+": ")}
	}
	f, ok := profiling.ClassifyError(err)
	if !ok {
		return err
	}
	reason, ok := map[profiling.FailureReason]profileReason{
		profiling.FailureInvalidInput: profileInvalid,
		profiling.FailureBadRequest:   profileBadRequest,
		profiling.FailureForbidden:    profileForbidden,
		profiling.FailureNotFound:     profileNotFound,
		profiling.FailureTaskExpired:  profileTaskExpired,
		profiling.FailureThrottled:    profileThrottled,
		profiling.FailureServer:       profileServerError,
		profiling.FailureTimeout:      profileNotCompleted,
	}[f.Reason]
	if !ok {
		return err // a reason dtctl added since: its own text is the best there is
	}
	return &profileFailure{Reason: reason, Entity: entity, Status: f.StatusCode, Message: f.Message}
}

// profilingEmpty maps dtctl's reason for an empty view onto the catalog's.
func profilingEmpty(r profiling.EmptyReason) string {
	switch r {
	case profiling.EmptyNoSamples:
		return catalog.ProfEmptyNoSamples
	case profiling.EmptyState:
		return catalog.ProfEmptyState
	}
	return string(r)
}

type dtctlHotspots struct{ a *profiling.Hotspots }

func (h dtctlHotspots) Functions(state string) ([]functionRow, int, string, error) {
	view, err := h.a.Functions(false, profiling.State(state), profiling.SortBySelf)
	if err != nil {
		return nil, 0, "", err
	}
	rows := make([]functionRow, len(view.Rows))
	for i, r := range view.Rows {
		rows[i] = functionRow{Function: r.Function, Self: r.Self, Total: r.Total,
			SelfPct: r.SelfPct, TotalPct: r.TotalPct, Paths: r.Paths}
	}
	return rows, view.Samples, profilingEmpty(view.Empty), nil
}

func (h dtctlHotspots) Focus(state, function string) ([]focusRow, string, error) {
	view, err := h.a.FocusExact(false, profiling.State(state), function)
	var noMatch *profiling.NoMatchError
	if errors.As(err, &noMatch) {
		// The function exists (the user entered it from a row); it just has
		// no samples in this thread state.
		return nil, catalog.ProfEmptyState, nil
	}
	if err != nil {
		return nil, "", err
	}
	var rows []focusRow
	for _, r := range view.Rows(0) {
		rows = append(rows, focusRow{Relation: r.Relation, Function: r.Function, Samples: r.Samples, Share: r.Share})
	}
	return rows, profilingEmpty(view.Empty), nil
}

type dtctlAllocations struct{ a *profiling.Allocations }

func (a dtctlAllocations) Paths() ([]allocationRow, int64, int64, string) {
	view := a.a.Stacks(false, false)
	rows := make([]allocationRow, len(view.Rows))
	for i, r := range view.Rows {
		// A stack reads allocation site first, then its callers outwards.
		frames := strings.Split(r.Stack, ";")
		rows[i] = allocationRow{
			Site: frames[0], Callers: frames[1:],
			AllocBytes: r.AllocBytes, SurvivedBytes: r.SurvivedBytes,
			AllocCount: r.AllocCount,
		}
	}
	return rows, view.AllocatedBytes, view.SurvivedBytes, profilingEmpty(view.Empty)
}

// --- progress of an analysis in flight ---------------------------------------

// analysisProgress follows what the environment answers while one analysis
// runs. dtctl's handler retries a throttled request and polls for the result
// without saying so, and a view that only shows a spinner for a minute reads
// as hung, so the transport watches the responses go by.
type analysisProgress struct {
	accepted atomic.Bool
}

type analysisProgressKey struct{}

func withAnalysisProgress(ctx context.Context) context.Context {
	return context.WithValue(ctx, analysisProgressKey{}, &analysisProgress{})
}

// analysisObserver is a pass-through RoundTripper that reports the throttled
// and still-running answers of a profiling analysis to the views waiting on
// it. It changes no request and no response.
type analysisObserver struct {
	next http.RoundTripper
}

func (o analysisObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	next := o.next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	if err == nil {
		if p, ok := req.Context().Value(analysisProgressKey{}).(*analysisProgress); ok {
			if note := p.note(resp.StatusCode, resp.Header.Get("Retry-After")); note != "" {
				profileNote(req.Context(), note)
			}
		}
	}
	return resp, err
}

// CloseIdleConnections passes http.Client.CloseIdleConnections on to the
// transport beneath, as the session client's own wrapper does.
func (o analysisObserver) CloseIdleConnections() {
	if c, ok := o.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// note words one response of an analysis in flight ("" = nothing to say). It
// says what the environment answered, not what happens next: the observer
// sits beneath the client's own retries and the SDK's, and cannot tell
// whether this request is sent again, or whether it started the analysis or
// asked for its result.
func (p *analysisProgress) note(status int, retryAfter string) string {
	switch status {
	case http.StatusTooManyRequests:
		note := "the environment answered 429: it is throttling profiling requests"
		// Retry-After is delay-seconds or an HTTP date (RFC 9110 10.2.3).
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
			note += fmt.Sprintf(" and asks to wait %ds", secs)
		} else if at, err := http.ParseTime(retryAfter); err == nil {
			note += " and asks to wait until " + at.UTC().Format("15:04:05 UTC")
		}
		return note
	case http.StatusAccepted:
		if p.accepted.CompareAndSwap(false, true) {
			return "the environment accepted the analysis — waiting for its result"
		}
		return "the analysis is still running on the server"
	}
	return ""
}

// profilingSourceFor builds the source of the profiling view over the
// environment c talks to.
func profilingSourceFor(c *session.Client) tui.Source {
	return profileSource(newDtctlProfiler(c), newProfileRuns())
}
