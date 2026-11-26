package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// The profiling view (catalog "hotspots", with its allocations lens) in
// dtui's own terms. Everything here is independent of dtctl: the sources below turn a
// profiler's readings into the rows and the summary record the catalog specs
// render, word the failures, and share one analysis between the lenses and
// focus drills of a view. The profiler itself — dtctl's profiling package, or
// a recorded analysis — is in profiling_dtctl.go, the only file that knows
// dtctl's types.

// profiler runs profiling analyses of one process. Every request it sends is
// a GET (ADR-0011): an analysis reads samples the agent already collected.
type profiler interface {
	// Subject is the id the environment is asked about for entityID, the
	// same for every spelling of one process; ok is false for an entity no
	// analysis runs on.
	Subject(entityID string) (id string, ok bool)
	Hotspots(ctx context.Context, q profileQuery) (hotspotReading, error)
	Allocations(ctx context.Context, q profileQuery) (allocationReading, error)
}

// profileQuery is what one analysis is asked for.
type profileQuery struct {
	Entity   string
	From, To time.Time
}

// hotspotReading is one hotspots analysis, read per thread state. The states
// are the catalog.HotspotLenses names.
type hotspotReading interface {
	// Functions is one row per function, by self samples, descending; samples
	// is the number counted, the base of the percentages. A reading without
	// rows says why in empty (a catalog.ProfEmpty* reason).
	Functions(state string) (rows []functionRow, samples int, empty string, err error)
	// Focus is one function's total and self rows, then its callers, then
	// its callees. function is an exact frame label; one the state has no
	// samples of reads as empty, not as an error.
	Focus(state, function string) (rows []focusRow, empty string, err error)
}

// allocationReading is one allocations analysis.
type allocationReading interface {
	// Paths is one row per allocation site and call path, by allocated bytes,
	// descending, with the totals over all of them.
	Paths() (rows []allocationRow, allocBytes, survivedBytes int64, empty string)
}

type functionRow struct {
	Function          string
	Self, Total       int
	SelfPct, TotalPct float64
	Paths             int
}

type focusRow struct {
	Relation string // a catalog.ProfRel* value
	Function string
	Samples  int
	Share    float64
}

type allocationRow struct {
	Site    string
	Callers []string // innermost caller first
	// SurvivedBytes is summed per GC survived: not a size, and it can
	// exceed the allocated bytes.
	AllocBytes, SurvivedBytes int64
	AllocCount                int64
}

// profileReason names why an analysis could not be shown.
type profileReason string

const (
	profileNoProcess    profileReason = "no_process"    // the view has no entity to analyse
	profileNotAProcess  profileReason = "not_a_process" // the entity is not one an analysis runs on
	profileFutureStart  profileReason = "future_start"  // the window starts in the future
	profileFutureEnd    profileReason = "future_end"    // the window ends in the future
	profileEmptyWindow  profileReason = "empty_window"  // the window ends at or before its start
	profileInvalid      profileReason = "invalid"       // dtctl refused the request before sending it
	profileBadRequest   profileReason = "bad_request"   // 400
	profileForbidden    profileReason = "forbidden"     // 403
	profileNotFound     profileReason = "not_found"     // 404 on the request that starts the analysis
	profileTaskExpired  profileReason = "task_expired"  // 404 while waiting for the result
	profileThrottled    profileReason = "throttled"     // 429, after the retries
	profileServerError  profileReason = "server_error"  // 5xx
	profileNotCompleted profileReason = "not_completed" // still running after the last poll
	profileMalformed    profileReason = "malformed"     // the answer is not an analysis result
	profileNoRecording  profileReason = "no_recording"  // the fixture holds no analysis of this kind
)

// profileFailure is a failure the views word themselves, so that what the
// user reads is about dtui (which key to press, which view to open) and not
// about a CLI's flags.
type profileFailure struct {
	Reason profileReason
	Entity string
	// Status is the HTTP status the environment answered with, if it did.
	Status int
	// Message is the API's own message, when there is one.
	Message string
}

func (f *profileFailure) Error() string {
	// detail appends the API's message where there is one, so that a
	// sentence never ends in a colon.
	detail := func(text string) string {
		if f.Message == "" {
			return text
		}
		return text + ": " + f.Message
	}
	switch f.Reason {
	case profileNoProcess:
		return "profiling analyses one process — press h on a row of :processes, or open :hotspots PROCESS-…"
	case profileNotAProcess:
		return fmt.Sprintf("%s cannot be profiled: an analysis runs on a process (PROCESS-…, PROCESS_GROUP_INSTANCE-… or PROCESS_GROUP-…) — press h on a row of :processes", f.Entity)
	case profileFutureStart:
		return "the selected window starts in the future: there is nothing to analyse yet — t picks another window"
	case profileFutureEnd:
		return "the selected window ends in the future: an analysis covers past time only — t picks another window"
	case profileEmptyWindow:
		return "the selected window is empty — t picks another window"
	case profileInvalid:
		return detail("this analysis cannot be asked for")
	case profileBadRequest:
		return detail("the environment rejected the analysis (400)")
	case profileForbidden:
		return "the environment refused the analysis (403): profiling needs storage:profiles:read and storage:entities:read, as scopes of the token and granted by an IAM policy — the scopes on the token alone are not enough"
	case profileNotFound:
		return fmt.Sprintf("the environment knows no process %s (404): process instances are short-lived, and ids belong to one environment — pick a current one from :processes", f.Entity)
	case profileTaskExpired:
		return "the environment no longer knows this analysis (404 while waiting for its result) — press r to run it again"
	case profileThrottled:
		return "the environment is throttling profiling requests (429) and kept refusing this one — wait a moment, then press r"
	case profileServerError:
		return detail(fmt.Sprintf("the environment failed on the analysis (%d)", f.Status)) + " — press r to ask again"
	case profileNotCompleted:
		return "the analysis was still running when dtui stopped asking for its result — press r to ask again"
	case profileMalformed:
		return detail("the environment's answer is not an analysis result (this API is experimental and may have changed)")
	}
	return f.Message
}

// profileWindowCheck refuses a window no analysis can cover before anything
// is asked for: one that has not happened yet, or an empty one. The
// environment would answer the first with an empty result, which would then
// be cached as if the process had been idle.
func profileWindowCheck(tf catalog.Timeframe, now time.Time) error {
	if !tf.Absolute() {
		return nil
	}
	switch {
	case !tf.From.Before(now):
		return &profileFailure{Reason: profileFutureStart}
	case tf.To.IsZero():
		return nil
	case !tf.To.After(tf.From):
		return &profileFailure{Reason: profileEmptyWindow}
	case tf.To.After(now):
		return &profileFailure{Reason: profileFutureEnd}
	}
	return nil
}

// profileCacheTTL is how long an analysis of a moving window ("last 2h") is
// reused, counted from when it arrived. Switching the thread-state lens and
// walking callers and callees read the same analysis, so within the TTL they
// cost no request; after it, the next read (or r) analyses the window as it
// then is.
const profileCacheTTL = 2 * time.Minute

// profileAbandonGrace is how long an analysis nobody waits for keeps running.
// A lens switch cancels the view's call and issues the next one a moment
// later, and that gap must not restart a minute-long analysis; for a view that
// was really left, dtui stops polling after the grace.
const profileAbandonGrace = 2 * time.Second

// profileCacheSize bounds the finished analyses kept: a hotspots result is
// megabytes.
const profileCacheSize = 4

// profileRuns shares analyses between the calls of the views. A call for an
// analysis that is cached gets it at once; one for an analysis in flight
// joins it. The analysis is cancelled shortly after the last call waiting for
// it was (the view was left): dtui then stops asking for its result. No
// request tells the environment so; what it does with an analysis nobody
// polls is not known.
type profileRuns struct {
	now   func() time.Time
	grace time.Duration
	// after runs f once d has passed; tests fire it by hand.
	after func(d time.Duration, f func())

	mu   sync.Mutex
	runs map[string]*profileRun
	seq  int
}

type profileRun struct {
	key    string
	seq    int
	window catalog.ProfileWindow
	pinned bool // an absolute, closed window: its analysis never goes stale

	done    chan struct{}
	at      time.Time // when the analysis arrived
	result  any
	err     error
	cancel  context.CancelFunc
	waiters map[context.Context]struct{}
	// left counts the times the last waiter left, so that the grace timer
	// of an earlier leave can tell it was overtaken.
	left int
}

func newProfileRuns() *profileRuns {
	return &profileRuns{
		now: time.Now, grace: profileAbandonGrace, runs: map[string]*profileRun{},
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// profileNoteKey carries the run's progress reporter on the context of the
// requests it makes; the transport observer in profiling_dtctl.go calls it.
type profileNoteKey struct{}

// profileNote tells the views waiting on the run that owns ctx how it is
// going. A context of no run is ignored.
func profileNote(ctx context.Context, text string) {
	if note, ok := ctx.Value(profileNoteKey{}).(func(string)); ok {
		note(text)
	}
}

// get returns the analysis for key, running fetch unless it is cached or in
// flight, and the window it covers. A call that is already cancelled starts
// nothing.
func (r *profileRuns) get(ctx context.Context, key string, tf catalog.Timeframe,
	fetch func(ctx context.Context, w catalog.ProfileWindow) (any, error),
) (any, catalog.ProfileWindow, error) {
	if err := ctx.Err(); err != nil {
		return nil, catalog.ProfileWindow{}, err
	}
	r.mu.Lock()
	run := r.runs[key]
	if run != nil && run.finished() && !run.pinned && r.now().Sub(run.at) > profileCacheTTL {
		delete(r.runs, key)
		run = nil
	}
	if run == nil {
		run = r.start(key, tf, fetch)
	}
	run.waiters[ctx] = struct{}{}
	r.mu.Unlock()

	select {
	case <-run.done:
		r.mu.Lock()
		delete(run.waiters, ctx)
		r.mu.Unlock()
		return run.result, run.window, run.err
	case <-ctx.Done():
		r.mu.Lock()
		delete(run.waiters, ctx)
		if len(run.waiters) == 0 && !run.finished() {
			run.left++
			left := run.left
			r.after(r.grace, func() { r.abandon(run, left) })
		}
		r.mu.Unlock()
		return nil, run.window, ctx.Err()
	}
}

// abandon cancels a run that nobody has waited for since the leave numbered
// left: a later leave has a grace, and a timer, of its own.
func (r *profileRuns) abandon(run *profileRun, left int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run.left != left || len(run.waiters) > 0 || run.finished() {
		return
	}
	run.cancel()
	if r.runs[run.key] == run {
		delete(r.runs, run.key)
	}
}

// start launches an analysis; the caller holds r.mu.
func (r *profileRuns) start(key string, tf catalog.Timeframe,
	fetch func(ctx context.Context, w catalog.ProfileWindow) (any, error),
) *profileRun {
	r.seq++
	run := &profileRun{
		key: key, seq: r.seq,
		window:  catalog.ProfileWindowOf(tf, r.now()),
		pinned:  tf.Absolute() && !tf.To.IsZero(),
		done:    make(chan struct{}),
		waiters: map[context.Context]struct{}{},
	}
	// The run outlives any one call (a lens switch replaces the call, not
	// the analysis), so it gets a context of its own.
	runCtx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	runCtx = context.WithValue(runCtx, profileNoteKey{}, func(text string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		for waiter := range run.waiters {
			tui.ReportProgress(waiter, text)
		}
	})
	r.runs[key] = run
	go func() {
		defer cancel()
		result, err := fetch(runCtx, run.window)
		r.mu.Lock()
		run.result, run.err, run.at = result, err, r.now()
		close(run.done)
		// A failure is not kept: the next call asks again. The calls
		// waiting for this one hold the run and read it from there.
		if err != nil && r.runs[key] == run {
			delete(r.runs, key)
		}
		r.evict()
		r.mu.Unlock()
	}()
	return run
}

// evict drops the oldest finished analyses beyond the cache size; the caller
// holds r.mu. One still running is never dropped, so more than the cache size
// can be held while that many run at once.
func (r *profileRuns) evict() {
	for len(r.runs) > profileCacheSize {
		var oldest *profileRun
		for _, run := range r.runs {
			if run.finished() && (oldest == nil || run.seq < oldest.seq) {
				oldest = run
			}
		}
		if oldest == nil {
			return
		}
		delete(r.runs, oldest.key)
	}
}

func (run *profileRun) finished() bool {
	select {
	case <-run.done:
		return true
	default:
		return false
	}
}

// profileSource builds the source of the profiling view over one profiler:
// the allocations analysis for its allocations lens, the hotspots analysis
// for every other. fixture names the recorded analysis it serves ("" =
// live); it goes into every summary so the view can say so.
func profileSource(p profiler, runs *profileRuns, fixture string) tui.Source {
	hotspots, allocations := hotspotsSource(p, runs, fixture), allocationsSource(p, runs, fixture)
	return func(ctx context.Context, scope catalog.Scope, query string) ([]map[string]any, error) {
		switch {
		case !catalog.ProfileAllocations(scope):
			return hotspots(ctx, scope, query)
		case scope.Arg != "":
			// The allocations analysis is of the process; it has no
			// reading for one function. Nothing is asked for.
			return []map[string]any{{catalog.SummaryKey: true, catalog.ProfSumEmpty: catalog.ProfEmptyFocus}}, nil
		}
		return allocations(ctx, scope, query)
	}
}

// profileTarget is the process a profiling scope analyses and the id the
// environment is asked about for it, or the refusal that says why nothing is
// analysed: no process, not a process, or a window that cannot be. It sends
// no request.
func profileTarget(p profiler, scope catalog.Scope, now time.Time) (entity catalog.Entity, subject string, err error) {
	if scope.Entity == nil || scope.Entity.ID == "" {
		return catalog.Entity{}, "", &profileFailure{Reason: profileNoProcess}
	}
	subject, ok := p.Subject(scope.Entity.ID)
	if !ok {
		return catalog.Entity{}, "", &profileFailure{Reason: profileNotAProcess, Entity: scope.Entity.ID}
	}
	if err := profileWindowCheck(scope.Timeframe, now); err != nil {
		return catalog.Entity{}, "", err
	}
	return *scope.Entity, subject, nil
}

// profileKey names the analysis a view of the timeframe reads, so that two
// views share one exactly when they would send the same request: the kind,
// the process as the environment knows it, and the window. A relative
// timeframe is its length, capped like the request ("last 24h" and "last 7d"
// both analyse the last 2h); an absolute one is its bounds as they are sent.
func profileKey(kind, subject string, tf catalog.Timeframe) string {
	if !tf.Absolute() {
		return fmt.Sprintf("%s|%s|last %s", kind, subject, min(tf.Dur, catalog.ProfileMaxWindow))
	}
	if tf.To.IsZero() {
		return fmt.Sprintf("%s|%s|from %d", kind, subject, tf.From.UnixMilli())
	}
	w := catalog.ProfileWindowOf(tf, tf.To)
	return fmt.Sprintf("%s|%s|%d-%d", kind, subject, w.From.UnixMilli(), w.To.UnixMilli())
}

// profileSummary starts the record a profiling source leads its result with.
func profileSummary(w catalog.ProfileWindow, fixture string) map[string]any {
	summary := map[string]any{
		catalog.SummaryKey:  true,
		catalog.ProfSumFrom: w.From.UTC().Format(time.RFC3339),
		catalog.ProfSumTo:   w.To.UTC().Format(time.RFC3339),
	}
	if w.Clamped {
		summary[catalog.ProfSumClamped] = true
	}
	if fixture != "" {
		summary[catalog.ProfSumFixture] = fixture
	}
	return summary
}

// stampProcess names the analysed process on a row (catalog.ProfEntityID).
func stampProcess(rec map[string]any, e catalog.Entity) map[string]any {
	rec[catalog.ProfEntityID] = e.ID
	if e.Name != "" {
		rec[catalog.ProfEntityName] = e.Name
	}
	return rec
}

// hotspotsSource serves the hotspots view: the functions table of the lens'
// thread state, or one function's callers and callees when the scope carries
// a function (Scope.Arg).
func hotspotsSource(p profiler, runs *profileRuns, fixture string) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, _ string) ([]map[string]any, error) {
		entity, subject, err := profileTarget(p, scope, runs.now())
		if err != nil {
			return nil, err
		}
		result, window, err := runs.get(ctx, profileKey("hotspots", subject, scope.Timeframe), scope.Timeframe,
			func(ctx context.Context, w catalog.ProfileWindow) (any, error) {
				return p.Hotspots(ctx, profileQuery{Entity: entity.ID, From: w.From, To: w.To})
			})
		if err != nil {
			return nil, err
		}
		reading, ok := result.(hotspotReading)
		if !ok || reading == nil {
			return nil, errors.New("the hotspots analysis returned nothing to read")
		}
		summary := profileSummary(window, fixture)
		state := catalog.HotspotState(scope)

		if scope.Arg != "" {
			rows, empty, err := reading.Focus(state, scope.Arg)
			if err != nil {
				return nil, err
			}
			records := []map[string]any{summary}
			// Callers come first and a function can have hundreds, so each
			// direction keeps its first rows, which are its largest.
			callers, callees := 0, 0
			for _, row := range rows {
				switch row.Relation {
				case catalog.ProfRelTotal:
					summary[catalog.ProfSumSamples] = float64(row.Samples)
				case catalog.ProfRelCaller:
					if callers++; callers > catalog.ProfileFocusCap {
						continue
					}
				case catalog.ProfRelCallee:
					if callees++; callees > catalog.ProfileFocusCap {
						continue
					}
				}
				records = append(records, stampProcess(map[string]any{
					catalog.ProfRelation: row.Relation,
					catalog.ProfFunction: row.Function,
					catalog.ProfSamples:  float64(row.Samples),
					catalog.ProfShare:    row.Share,
				}, entity))
			}
			summary[catalog.ProfSumCallers], summary[catalog.ProfSumCallees] = float64(callers), float64(callees)
			if len(rows) == 0 {
				summary[catalog.ProfSumEmpty] = empty
			}
			return records, nil
		}

		rows, samples, empty, err := reading.Functions(state)
		if err != nil {
			return nil, err
		}
		summary[catalog.ProfSumSamples] = float64(samples)
		summary[catalog.ProfSumRows] = float64(len(rows))
		if len(rows) == 0 {
			summary[catalog.ProfSumEmpty] = empty
		}
		if len(rows) > catalog.ProfileRowCap {
			rows = rows[:catalog.ProfileRowCap]
		}
		records := make([]map[string]any, 0, len(rows)+1)
		records = append(records, summary)
		for _, row := range rows {
			records = append(records, stampProcess(map[string]any{
				catalog.ProfFunction: row.Function,
				catalog.ProfSelf:     float64(row.Self),
				catalog.ProfSelfPct:  row.SelfPct,
				catalog.ProfTotal:    float64(row.Total),
				catalog.ProfTotalPct: row.TotalPct,
				catalog.ProfPaths:    float64(row.Paths),
			}, entity))
		}
		return records, nil
	}
}

// allocationsSource serves the allocations lens: one row per allocation site
// and call path.
func allocationsSource(p profiler, runs *profileRuns, fixture string) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, _ string) ([]map[string]any, error) {
		entity, subject, err := profileTarget(p, scope, runs.now())
		if err != nil {
			return nil, err
		}
		result, window, err := runs.get(ctx, profileKey("allocations", subject, scope.Timeframe), scope.Timeframe,
			func(ctx context.Context, w catalog.ProfileWindow) (any, error) {
				return p.Allocations(ctx, profileQuery{Entity: entity.ID, From: w.From, To: w.To})
			})
		if err != nil {
			return nil, err
		}
		reading, ok := result.(allocationReading)
		if !ok || reading == nil {
			return nil, errors.New("the allocations analysis returned nothing to read")
		}
		rows, allocBytes, survivedBytes, empty := reading.Paths()
		summary := profileSummary(window, fixture)
		summary[catalog.ProfSumAlloc] = float64(allocBytes)
		summary[catalog.ProfSumSurvived] = float64(survivedBytes)
		summary[catalog.ProfSumRows] = float64(len(rows))
		if len(rows) == 0 {
			summary[catalog.ProfSumEmpty] = empty
		}
		if len(rows) > catalog.ProfileRowCap {
			rows = rows[:catalog.ProfileRowCap]
		}
		records := make([]map[string]any, 0, len(rows)+1)
		records = append(records, summary)
		for _, row := range rows {
			records = append(records, stampProcess(map[string]any{
				catalog.ProfSite:          row.Site,
				catalog.ProfCallers:       anySlice(row.Callers),
				catalog.ProfAllocBytes:    float64(row.AllocBytes),
				catalog.ProfSurvivedBytes: float64(row.SurvivedBytes),
				catalog.ProfAllocCount:    float64(row.AllocCount),
			}, entity))
		}
		return records, nil
	}
}
