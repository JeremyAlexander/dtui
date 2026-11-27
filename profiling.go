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
// focus drills of a view. The profiler itself, dtctl's profiling package, is in
// profiling_dtctl.go, the only file that knows dtctl's types.

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
	// Stacks is one row per unique call stack, with the samples that ended
	// in its last frame in every thread state at once. A reading without
	// rows says why in empty.
	Stacks() (rows []stackRow, empty string)
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

type stackRow struct {
	// Stack is the frames from the outermost caller to the one that was
	// running, joined by catalog.ProfStackSep.
	Stack                              string
	Running, Lock, NetIO, DiskIO, Wait int
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
	// What dtctl does not classify.
	profileUnexpectedStatus profileReason = "unexpected_status" // a failure status with no meaning of its own here (401)
	profileUnreachable      profileReason = "unreachable"       // no response at all
	profileOther            profileReason = "other"             // a reason dtctl has and dtui does not know
)

// profileFailure is a failure the views word themselves, so that what the
// user reads is about dtui (which key to press, which view to open) and not
// about a CLI's flags.
type profileFailure struct {
	Reason profileReason
	Entity string
	// Status is the HTTP status the environment answered with, if it did.
	Status int
	// Message is the API's own message, when there is one; for
	// profileUnreachable why no response came, and for profileOther dtctl's
	// name of the reason.
	Message string
	// Polling says the failure came while waiting for the result, not on
	// the request that starts the analysis.
	Polling bool
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
	case profileUnexpectedStatus:
		if f.Polling {
			return fmt.Sprintf("the environment answered with status %d while dtui waited for the analysis — press r to ask again", f.Status)
		}
		return detail(fmt.Sprintf("the environment answered the request for the analysis with status %d", f.Status)) + " — press r to ask again"
	case profileUnreachable:
		if f.Polling {
			return detail("the environment did not answer while dtui waited for the analysis") + " — press r to ask again"
		}
		return detail("the environment did not answer the request for the analysis") + " — press r to ask again"
	case profileOther:
		reason := f.Message
		if f.Status != 0 {
			reason += fmt.Sprintf(", status %d", f.Status)
		}
		return fmt.Sprintf("the analysis failed for a reason dtui has no words for (%s) — press r to ask again", reason)
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

// profileAbandonGrace is how long an analysis nobody waits for keeps running.
// A lens switch cancels the view's call and issues the next one a moment
// later, and that gap must not restart a minute-long analysis; for a view that
// was really left, dtui stops polling after the grace.
const profileAbandonGrace = 2 * time.Second

// profileCacheSize bounds the finished analyses kept: a hotspots result is
// megabytes.
const profileCacheSize = 4

// profileRuns shares analyses between the calls of the views. A call for an
// analysis that is kept gets it at once, however long ago it arrived: the
// lenses, the focus drills and a return to the view read one analysis, and
// the caption states the clock times it covers. Only a fresh read (r,
// tui.FreshRead) replaces it. A call for an analysis in flight joins it,
// a fresh one too. The analysis is cancelled shortly after the last call waiting for
// it was (the view was left): dtui then stops asking for its result. No
// request tells the environment so; what it does with an analysis nobody
// polls is not known.
type profileRuns struct {
	now   func() time.Time
	grace time.Duration
	// after runs f once d has passed; tests fire it by hand.
	after func(d time.Duration, f func())
	// report hands a note of a run to the view behind a waiting call.
	report func(ctx context.Context, text string)

	mu   sync.Mutex
	runs map[string]*profileRun
	uses int
}

type profileRun struct {
	key string
	// used orders the finished analyses by when each was last read, or
	// arrived: the one with the lowest is dropped first.
	used   int
	window catalog.ProfileWindow

	done    chan struct{}
	result  any
	err     error
	cancel  context.CancelFunc
	waiters map[context.Context]struct{}
	// note is what the analysis said last, for a call that joins it.
	note string
	// left counts the times the last waiter left, so that the grace timer
	// of an earlier leave can tell it was overtaken.
	left int
}

func newProfileRuns() *profileRuns {
	return &profileRuns{
		now: time.Now, grace: profileAbandonGrace, runs: map[string]*profileRun{},
		after:  func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		report: tui.ReportProgress,
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

// get returns the analysis for key, running fetch unless it is kept or in
// flight, and the window it covers. A fresh read drops the kept one first. A call that is already cancelled starts
// nothing.
func (r *profileRuns) get(ctx context.Context, key string, tf catalog.Timeframe,
	fetch func(ctx context.Context, w catalog.ProfileWindow) (any, error),
) (any, catalog.ProfileWindow, error) {
	if err := ctx.Err(); err != nil {
		return nil, catalog.ProfileWindow{}, err
	}
	r.mu.Lock()
	run := r.runs[key]
	if run != nil && run.finished() && tui.FreshRead(ctx) {
		delete(r.runs, key)
		run = nil
	}
	switch {
	case run == nil:
		run = r.start(key, tf, fetch)
	case run.finished():
		r.use(run)
	case run.note != "":
		// The view behind this call has heard nothing yet, and the next
		// response of the analysis can be a minute away.
		r.report(ctx, run.note)
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
	run := &profileRun{
		key:     key,
		window:  catalog.ProfileWindowOf(tf, r.now()),
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
		run.note = text
		for waiter := range run.waiters {
			r.report(waiter, text)
		}
	})
	r.runs[key] = run
	go func() {
		defer cancel()
		result, err := fetch(runCtx, run.window)
		r.mu.Lock()
		run.result, run.err = result, err
		r.use(run)
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

// use marks run as the analysis read last; the caller holds r.mu.
func (r *profileRuns) use(run *profileRun) {
	r.uses++
	run.used = r.uses
}

// evict drops the finished analyses read longest ago beyond the cache size;
// the caller holds r.mu. One still running neither counts nor is dropped: a result is
// megabytes only once it has arrived, and counting the runs in flight would
// drop the one result there is while four others run.
func (r *profileRuns) evict() {
	for {
		var oldest *profileRun
		finished := 0
		for _, run := range r.runs {
			if !run.finished() {
				continue
			}
			finished++
			if oldest == nil || run.used < oldest.used {
				oldest = run
			}
		}
		if finished <= profileCacheSize {
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

// profileSources are the sources of the profiling view over one profiler, by
// the names the catalog asks for them. They share the analyses: the flame
// graph's stacks are read from the analysis the functions table holds.
func profileSources(p profiler, runs *profileRuns) map[string]tui.Source {
	return map[string]tui.Source{
		"hotspots":                    profileSource(p, runs),
		catalog.HotspotStacksAPI:      stacksSource(p, runs, false),
		catalog.HotspotStacksFreshAPI: stacksSource(p, runs, true),
	}
}

// profileSource builds the source of the profiling view over one profiler:
// the allocations analysis for its allocations lens, the hotspots analysis
// for every other.
func profileSource(p profiler, runs *profileRuns) tui.Source {
	hotspots, allocations := hotspotsSource(p, runs), allocationsSource(p, runs)
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

// profileKey names the analysis a view of the timeframe reads at now, so that
// two views share one exactly when they would send the same request: the
// kind, the process as the environment knows it, and the window. A relative
// timeframe is its length, capped like the request ("last 24h" and "last 7d"
// both analyse the last 2h), and so is an open window that began longer ago
// than one analysis covers; any other absolute one is its bounds as they are
// sent.
func profileKey(kind, subject string, tf catalog.Timeframe, now time.Time) string {
	if !tf.Absolute() {
		return fmt.Sprintf("%s|%s|last %s", kind, subject, min(tf.Dur, catalog.ProfileMaxWindow))
	}
	if tf.To.IsZero() {
		if catalog.ProfileWindowOf(tf, now).Clamped {
			return fmt.Sprintf("%s|%s|last %s", kind, subject, catalog.ProfileMaxWindow)
		}
		return fmt.Sprintf("%s|%s|from %d", kind, subject, tf.From.UnixMilli())
	}
	w := catalog.ProfileWindowOf(tf, tf.To)
	return fmt.Sprintf("%s|%s|%d-%d", kind, subject, w.From.UnixMilli(), w.To.UnixMilli())
}

// profileSummary starts the record a profiling source leads its result with:
// the window the analysis covers, and whether that is less than the reader of
// tf selected. The second is said per reader, never taken from the analysis:
// views of different selections share one (profileKey), and each caption has
// to be true of its own selection (ADR-0007).
func profileSummary(tf catalog.Timeframe, w catalog.ProfileWindow) map[string]any {
	summary := map[string]any{
		catalog.SummaryKey:  true,
		catalog.ProfSumFrom: w.From.UTC().Format(time.RFC3339),
		catalog.ProfSumTo:   w.To.UTC().Format(time.RFC3339),
	}
	// A relative selection ends where the analysis does, so it is cut exactly
	// when it is longer than one analysis covers; an absolute one is cut when
	// it starts before the analysis does.
	clamped := tf.Dur > catalog.ProfileMaxWindow
	if tf.Absolute() {
		clamped = tf.From.Before(w.From)
	}
	if clamped {
		summary[catalog.ProfSumClamped] = true
	}
	return summary
}

// setShare stores part of whole under key as the 0..1 share a bar draws,
// unrounded, so that anything measured draws at least a sliver. A row with
// nothing of the whole, or a whole of nothing, gets no share and no bar.
func setShare(rec map[string]any, key string, part, whole float64) {
	if part > 0 && whole > 0 {
		rec[key] = part / whole
	}
}

// stampProcess names the analysed process on a row (catalog.ProfEntityID).
func stampProcess(rec map[string]any, e catalog.Entity) map[string]any {
	rec[catalog.ProfEntityID] = e.ID
	if e.Name != "" {
		rec[catalog.ProfEntityName] = e.Name
	}
	return rec
}

// readHotspots is the hotspots analysis of a scope and the window it covers,
// kept, shared and run as profileRuns.get says: the functions table and the
// flame graph read one analysis through it.
func readHotspots(ctx context.Context, p profiler, runs *profileRuns, scope catalog.Scope,
) (hotspotReading, catalog.ProfileWindow, catalog.Entity, error) {
	now := runs.now()
	entity, subject, err := profileTarget(p, scope, now)
	if err != nil {
		return nil, catalog.ProfileWindow{}, entity, err
	}
	key := profileKey("hotspots", subject, scope.Timeframe, now)
	result, window, err := runs.get(ctx, key, scope.Timeframe,
		func(ctx context.Context, w catalog.ProfileWindow) (any, error) {
			return p.Hotspots(ctx, profileQuery{Entity: entity.ID, From: w.From, To: w.To})
		})
	if err != nil {
		return nil, window, entity, err
	}
	reading, ok := result.(hotspotReading)
	if !ok || reading == nil {
		return nil, window, entity, errors.New("the hotspots analysis returned nothing to read")
	}
	return reading, window, entity, nil
}

// stacksSource serves the flame graph: every call stack of the hotspots
// analysis, with its samples per thread state (catalog.HotspotStates), under
// a summary that says which window they are from. It reads the analysis that
// is kept, which is the one the functions table shows, however long ago it
// arrived; fresh is the graph's own r, which replaces it as the table's r
// does (tui.WithFreshRead).
func stacksSource(p profiler, runs *profileRuns, fresh bool) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, _ string) ([]map[string]any, error) {
		if fresh {
			ctx = tui.WithFreshRead(ctx)
		}
		reading, window, _, err := readHotspots(ctx, p, runs, scope)
		if err != nil {
			return nil, err
		}
		rows, empty := reading.Stacks()
		summary := profileSummary(scope.Timeframe, window)
		summary[catalog.ProfSumRows] = float64(len(rows))
		if len(rows) == 0 {
			summary[catalog.ProfSumEmpty] = empty
		}
		records := make([]map[string]any, 0, len(rows)+1)
		records = append(records, summary)
		for _, row := range rows {
			rec := map[string]any{catalog.ProfStack: row.Stack}
			for _, state := range []struct {
				name    string
				samples int
			}{
				{"running", row.Running}, {"lock", row.Lock}, {"net_io", row.NetIO},
				{"disk_io", row.DiskIO}, {"wait", row.Wait},
			} {
				if state.samples > 0 {
					rec[state.name] = float64(state.samples)
				}
			}
			records = append(records, rec)
		}
		return records, nil
	}
}

// hotspotsSource serves the hotspots view: the functions table of the lens'
// thread state, or one function's callers and callees when the scope carries
// a function (Scope.Arg).
func hotspotsSource(p profiler, runs *profileRuns) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, _ string) ([]map[string]any, error) {
		reading, window, entity, err := readHotspots(ctx, p, runs, scope)
		if err != nil {
			return nil, err
		}
		summary := profileSummary(scope.Timeframe, window)
		state := catalog.HotspotState(scope)

		if scope.Arg != "" {
			rows, empty, err := reading.Focus(state, scope.Arg)
			if err != nil {
				return nil, err
			}
			records := []map[string]any{summary}
			total := 0
			for _, row := range rows {
				if row.Relation == catalog.ProfRelTotal {
					total = row.Samples
				}
			}
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
				rec := map[string]any{
					catalog.ProfRelation: row.Relation,
					catalog.ProfFunction: row.Function,
					catalog.ProfSamples:  float64(row.Samples),
					catalog.ProfShare:    row.Share,
				}
				setShare(rec, catalog.ProfFocusShare, float64(row.Samples), float64(total))
				records = append(records, stampProcess(rec, entity))
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
			rec := map[string]any{
				catalog.ProfFunction: row.Function,
				catalog.ProfSelf:     float64(row.Self),
				catalog.ProfSelfPct:  row.SelfPct,
				catalog.ProfTotal:    float64(row.Total),
				catalog.ProfTotalPct: row.TotalPct,
				catalog.ProfPaths:    float64(row.Paths),
			}
			setShare(rec, catalog.ProfSelfShare, float64(row.Self), float64(samples))
			setShare(rec, catalog.ProfTotalShare, float64(row.Total), float64(samples))
			records = append(records, stampProcess(rec, entity))
		}
		return records, nil
	}
}

// allocationsSource serves the allocations lens: one row per allocation site
// and call path.
func allocationsSource(p profiler, runs *profileRuns) tui.Source {
	return func(ctx context.Context, scope catalog.Scope, _ string) ([]map[string]any, error) {
		now := runs.now()
		entity, subject, err := profileTarget(p, scope, now)
		if err != nil {
			return nil, err
		}
		result, window, err := runs.get(ctx, profileKey("allocations", subject, scope.Timeframe, now), scope.Timeframe,
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
		summary := profileSummary(scope.Timeframe, window)
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
			rec := map[string]any{
				catalog.ProfSite:          row.Site,
				catalog.ProfCallers:       anySlice(row.Callers),
				catalog.ProfAllocBytes:    float64(row.AllocBytes),
				catalog.ProfSurvivedBytes: float64(row.SurvivedBytes),
				catalog.ProfAllocCount:    float64(row.AllocCount),
			}
			setShare(rec, catalog.ProfAllocShare, float64(row.AllocBytes), float64(allocBytes))
			records = append(records, stampProcess(rec, entity))
		}
		return records, nil
	}
}
