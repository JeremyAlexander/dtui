package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// profilingScenario is one call of a profiling source as the main package's
// TestProfilingScenarios recorded it from the synthetic Java web-app analysis
// (testdata/profiling/*.json): the rows, or the failure, the view was handed.
// This package cannot import the sources, so it renders their recorded output.
type profilingScenario struct {
	View   string `json:"view"`
	Entity *struct {
		ID, Name, Type string
	} `json:"entity"`
	Timeframe string           `json:"timeframe"`
	Lens      string           `json:"lens"`
	Arg       string           `json:"arg"`
	Error     string           `json:"error"`
	Records   []map[string]any `json:"records"`
}

func loadProfilingScenario(t *testing.T, name string) profilingScenario {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "profiling", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var sc profilingScenario
	if err := json.Unmarshal(data, &sc); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return sc
}

// openProfiling opens a scenario's view the way the command bar does
// (`:hotspots PROCESS-…`), in the scenario's timeframe and lens, still loading.
func openProfiling(t *testing.T, sc profilingScenario) (*app, *tableView) {
	t.Helper()
	a := testApp(t, "home")
	tf, ok := catalog.ParseTimeframe(sc.Timeframe)
	if !ok {
		t.Fatalf("bad timeframe %q", sc.Timeframe)
	}
	deliver(a, a.setTimeframe(tf))
	nav := NavigateMsg{Target: sc.View}
	if sc.Entity != nil {
		nav.Filter = sc.Entity.ID
	}
	_, cmd := a.Update(nav)
	deliver(a, cmd)
	tv := a.top().(*tableView)
	if sc.Entity != nil && tv.scope.Entity != nil {
		tv.scope.Entity.Name = sc.Entity.Name
	}
	for i, l := range tv.spec.Lenses {
		if l.Name == sc.Lens && i != tv.scope.Lens {
			deliver(a, tv.setLens(i, false))
		}
	}
	return a, tv
}

// answer delivers the scenario's result to the view that waits for it.
func (sc profilingScenario) answer(tv *tableView) {
	var err error
	if sc.Error != "" {
		err = errors.New(sc.Error)
	}
	tv.Update(dataMsg{owner: tv, seq: tv.seq, records: sc.Records, elapsed: 38 * time.Second, err: err})
}

// renderProfiling opens a scenario and answers it; a focus scenario is entered
// from the function's row of the table it was drilled from.
func renderProfiling(t *testing.T, name string) *app {
	t.Helper()
	sc := loadProfilingScenario(t, name)
	if sc.Arg == "" {
		a, tv := openProfiling(t, sc)
		sc.answer(tv)
		return a
	}
	parent := loadProfilingScenario(t, "hotspots-running")
	a, tv := openProfiling(t, parent)
	parent.answer(tv)
	for i, row := range tv.rows {
		if catalog.Str(row, catalog.ProfFunction) == sc.Arg {
			tv.cursor = i
		}
	}
	press(a, key("enter"))
	focus := a.top().(*tableView)
	if focus == tv || focus.scope.Arg != sc.Arg {
		t.Fatalf("enter on %s opened %q", sc.Arg, focus.scope.Arg)
	}
	sc.answer(focus)
	return a
}

// profilingSizes are the two widths every profiling state is pinned at: the
// 80-column minimum and the default desktop size, both from snapshotSizes.
var profilingSizes = []struct{ w, h int }{{80, 24}, {120, 40}}

func assertProfilingSnapshots(t *testing.T, prefix string, a *app) {
	t.Helper()
	for _, s := range profilingSizes {
		a.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
		assertSnapshot(t, fmt.Sprintf("%s-%dx%d", prefix, s.w, s.h), screen(a))
	}
}

func TestSnapshotProfiling(t *testing.T) {
	pinSnapshotClock(t)
	for _, name := range []string{
		"hotspots-running", "hotspots-lock", "hotspots-focus", "allocations",
		"no-data", "no-lock-samples", "forbidden", "not-found", "throttled", "service-refused", "no-process",
	} {
		t.Run(name, func(t *testing.T) {
			assertProfilingSnapshots(t, "profiling-"+name, renderProfiling(t, name))
		})
	}
}

// An analysis takes up to a minute: the view says what it waits for, for how
// long, and what the source reported last.
func TestSnapshotProfilingLoading(t *testing.T) {
	pinSnapshotClock(t)
	a, tv := openProfiling(t, loadProfilingScenario(t, "hotspots-running"))
	if !tv.loading {
		t.Fatal("an opened view must be loading")
	}
	tv.started = catalog.Now().Add(-41 * time.Second)
	tv.progress.set("the environment answered 429: it is throttling profiling requests and asks to wait 10s")
	assertProfilingSnapshots(t, "profiling-loading", a)
}

// The window the caption states is the one analysed: a selection longer than
// the API's 2h reads as its last 2h, never as the selection.
func TestProfilingCaptionStatesTheAnalysedWindow(t *testing.T) {
	pinSnapshotClock(t)
	out := screen(renderProfiling(t, "hotspots-lock"))
	for _, want := range []string{"last 2h of the selected 24h (10:00–12:00 UTC)", "leaf: total", "11,353 lock samples", "FIXTURE java-webapp"} {
		if !strings.Contains(out, want) {
			t.Errorf("caption lacks %q:\n%s", want, out)
		}
	}
	out = screen(renderProfiling(t, "hotspots-running"))
	if !strings.Contains(out, "300 of 309 functions") {
		t.Errorf("a capped table must say how many functions it leaves out:\n%s", out)
	}
	if strings.Contains(out, "last 2h of") {
		t.Errorf("a 2h selection is analysed whole:\n%s", out)
	}
}

// enter walks callers and callees; esc goes back one function at a time.
func TestProfilingFocusWalksAndReturns(t *testing.T) {
	pinSnapshotClock(t)
	a := renderProfiling(t, "hotspots-focus")
	focus := a.top().(*tableView)
	depth := len(a.stack)

	// The total and self rows are the function itself: enter stays put.
	focus.cursor = 0
	press(a, key("enter"))
	if len(a.stack) != depth {
		t.Fatal("enter on the focused function's own row must not open it again")
	}
	var caller string
	for i, row := range focus.rows {
		if catalog.Str(row, catalog.ProfRelation) == catalog.ProfRelCaller {
			focus.cursor, caller = i, catalog.Str(row, catalog.ProfFunction)
			break
		}
	}
	press(a, key("enter"))
	next := a.top().(*tableView)
	if len(a.stack) != depth+1 || next.scope.Arg != caller {
		t.Fatalf("enter on caller %s: stack %d, arg %q", caller, len(a.stack), next.scope.Arg)
	}
	if next.scope.Lens != focus.scope.Lens || next.scope.Entity == nil || next.scope.Entity.ID != focus.scope.Entity.ID {
		t.Errorf("the walk lost its lens or process: %+v", next.scope)
	}
	press(a, key("esc"))
	if a.top() != focus {
		t.Fatal("esc must return to the function walked from")
	}
	press(a, key("esc"))
	if tv := a.top().(*tableView); tv.scope.Arg != "" || tv.spec.Name != "hotspots" {
		t.Fatalf("second esc should be back on the functions table, got %s %q", tv.spec.Name, tv.scope.Arg)
	}
}

// h is the profiling key of a process row. Inside the view it does nothing:
// allocations are its last lens, so switching to them keeps the view, its
// place on the trail and the way back to the thread state.
func TestProfilingDrillsFromAProcess(t *testing.T) {
	a := testApp(t, "processes")
	seedRows(t, a, []map[string]any{
		processRecord("PROCESS-000000000000A001", "shop-backend", []string{"JAVA"}, false, map[string]any{}, nil),
	})
	press(a, key("h"))
	hot := a.top().(*tableView)
	if hot.spec.Name != "hotspots" || hot.scope.Entity == nil || hot.scope.Entity.ID != "PROCESS-000000000000A001" {
		t.Fatalf("h on a process opened %s scoped to %+v", hot.spec.Name, hot.scope.Entity)
	}
	seedRows(t, a, loadProfilingScenario(t, "hotspots-running").Records)
	press(a, key("]")) // lock
	depth := len(a.stack)
	for range 6 {
		press(a, key("h"))
	}
	if len(a.stack) != depth || a.top() != viewModel(hot) || hot.scope.Lens != 1 {
		t.Fatalf("six h presses: stack %d deep (was %d), lens %d", len(a.stack), depth, hot.scope.Lens)
	}
	press(a, key("["))
	press(a, key("[")) // wraps from running to the last lens
	if len(a.stack) != depth || hot.spec.LensAt(hot.scope.Lens).Name != "allocations" {
		t.Fatalf("the lens before running is %s, stack %d deep", hot.spec.LensAt(hot.scope.Lens).Name, len(a.stack))
	}
	if got := hot.columns()[0].Title; got != "ALLOC" {
		t.Errorf("the allocations lens shows the column %s first", got)
	}
}

// Enter on an allocation path shows the whole record; there is no function
// to focus.
func TestEnterOnAnAllocationPathInspects(t *testing.T) {
	sc := loadProfilingScenario(t, "allocations")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	if got := hintFor(tv, "enter"); got != "inspect" {
		t.Errorf("enter is hinted as %q, want inspect", got)
	}
	press(a, key("enter"))
	if _, ok := a.top().(*tableView); ok {
		t.Fatalf("enter on an allocation path left the table on top (%T)", a.top())
	}
}

// hintFor is the label the footer gives a key on the view, "" if none.
func hintFor(v *tableView, k string) string {
	for _, h := range v.Hints() {
		if h.Key == k {
			return h.Desc
		}
	}
	return ""
}

// `:hotspots PROCESS-…` scopes the view; any other argument filters it.
func TestProfilingArgumentIsTheProcess(t *testing.T) {
	a := testApp(t, "home")
	_, cmd := a.Update(NavigateMsg{Target: "hotspots", Filter: "PROCESS-000000000000A001"})
	deliver(a, cmd)
	tv := a.top().(*tableView)
	if tv.scope.Entity == nil || tv.scope.Entity.ID != "PROCESS-000000000000A001" || tv.filter != "" {
		t.Fatalf("entity %+v, filter %q", tv.scope.Entity, tv.filter)
	}
	_, cmd = a.Update(NavigateMsg{Target: "hotspots", Filter: "jackson"})
	deliver(a, cmd)
	if tv := a.top().(*tableView); tv.scope.Entity != nil || tv.filter != "jackson" {
		t.Fatalf("a word is a filter: entity %+v, filter %q", tv.scope.Entity, tv.filter)
	}
	// Other views keep reading an entity id as filter text.
	_, cmd = a.Update(NavigateMsg{Target: "processes", Filter: "PROCESS-000000000000A001"})
	deliver(a, cmd)
	if tv := a.top().(*tableView); tv.filter != "PROCESS-000000000000A001" {
		t.Fatalf("processes filter = %q", tv.filter)
	}
}

// Leaving a profiling view ends the analysis it waits for; a view merely
// covered by a drill keeps waiting, and one returned to asks again.
func TestProfilingCancelsItsCallWhenLeft(t *testing.T) {
	a := testApp(t, "home")
	var calls []context.Context
	a.ds.sources = map[string]Source{
		"hotspots": func(ctx context.Context, _ catalog.Scope, _ string) ([]map[string]any, error) {
			calls = append(calls, ctx)
			return nil, nil
		},
	}
	// deliver runs the view's call and drops its result: the view stays
	// loading, as it does while an analysis runs.
	open := func() *tableView {
		_, cmd := a.Update(NavigateMsg{Target: "hotspots", Filter: "PROCESS-000000000000A001"})
		deliver(a, cmd)
		return a.top().(*tableView)
	}

	tv := open()
	if len(calls) != 1 || !tv.loading {
		t.Fatalf("opening issued %d calls, loading=%v", len(calls), tv.loading)
	}
	_, cmd := a.Update(NavigateMsg{Target: "problems"})
	deliver(a, cmd)
	if calls[0].Err() == nil {
		t.Fatal("jumping to another view must cancel the analysis in flight")
	}

	// '-' brings the view back: it asks again.
	_, cmd = a.Update(key("-"))
	deliver(a, cmd)
	if a.top() != tv || len(calls) != 2 || calls[1].Err() != nil {
		t.Fatalf("returning: top is the view=%v, %d calls", a.top() == tv, len(calls))
	}

	// Switching the lens replaces the call (the source keeps the analysis).
	deliver(a, tv.setLens(1, false))
	if len(calls) != 3 || calls[1].Err() == nil || calls[2].Err() != nil {
		t.Fatalf("lens switch: %d calls, old cancelled=%v", len(calls), calls[1].Err() != nil)
	}

	// A drill covers the view without leaving it: the analysis goes on. esc
	// from the drilled view is not a leave either.
	tv.Update(dataMsg{owner: tv, seq: tv.seq, records: loadProfilingScenario(t, "hotspots-lock").Records})
	_, cmd = a.Update(key("enter"))
	deliver(a, cmd)
	focus := a.top().(*tableView)
	if focus == tv || len(calls) != 4 {
		t.Fatalf("enter did not open the focus: %d calls", len(calls))
	}
	press(a, key("esc"))
	if calls[3].Err() == nil {
		t.Fatal("esc must cancel the popped view's analysis")
	}
	if a.top() != tv {
		t.Fatal("esc must return to the functions table")
	}
}

// The echo is the dtctl command for what is on screen, window included.
func TestProfilingEcho(t *testing.T) {
	pinSnapshotClock(t)
	for name, want := range map[string]string{
		"hotspots-running": "dtctl exec profiling hotspots PROCESS-000000000000A001 --view functions --state running --show-waiting --limit 300 --default-timeframe-start 'now()-2h'",
		"hotspots-lock":    "dtctl exec profiling hotspots PROCESS-000000000000A001 --view functions --state lock --show-waiting --limit 300 --default-timeframe-start 'now()-2h'",
		"hotspots-focus":   "dtctl exec profiling hotspots PROCESS-000000000000A001 --focus 'com.fasterxml.jackson.core.json.UTF8JsonGenerator.writeString' --state running --show-waiting --limit 25 --default-timeframe-start 'now()-2h'",
		"allocations":      "dtctl exec profiling allocations PROCESS-000000000000A001 --limit 300 --default-timeframe-start 'now()-2h'",
		"no-process":       "dtctl exec profiling hotspots <PROCESS-id> --view functions --state running --show-waiting --limit 300 --default-timeframe-start 'now()-2h'",
		"service-refused":  "dtctl exec profiling hotspots <PROCESS-id> --view functions --state running --show-waiting --limit 300 --default-timeframe-start 'now()-2h'",
	} {
		if got := renderProfiling(t, name).top().(*tableView).Echo(); got != want {
			t.Errorf("%s echo:\n got %s\nwant %s", name, got, want)
		}
	}
}

// What is on screen is a reading of the scope in the breadcrumb. A lens or a
// timeframe switch asks for another reading, and until it arrives the view
// shows neither the old rows nor the old totals under the new label, and
// enter has nothing to act on.
func TestProfilingDropsAReadingItsScopeLeft(t *testing.T) {
	pinSnapshotClock(t)
	const hottest = "UTF8JsonGenerator.writeString"
	for name, leave := range map[string]func(a *app, tv *tableView){
		"lens": func(a *app, tv *tableView) { press(a, key("]")) },
		"timeframe": func(a *app, tv *tableView) {
			deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: 30 * time.Minute}))
		},
	} {
		sc := loadProfilingScenario(t, "hotspots-running")
		a, tv := openProfiling(t, sc)
		sc.answer(tv)
		if out := screen(a); !strings.Contains(out, "77,631 running samples") || !strings.Contains(out, hottest) {
			t.Fatalf("%s: the running reading is not on screen:\n%s", name, out)
		}
		depth := len(a.stack)
		leave(a, tv)
		out := screen(a)
		for _, stale := range []string{"77,631", hottest, "FIXTURE"} {
			if strings.Contains(out, stale) {
				t.Errorf("%s switch: %q of the old reading is still on screen:\n%s", name, stale, out)
			}
		}
		if !strings.Contains(out, "analysing the hotspots of shop-backend") {
			t.Errorf("%s switch: the wait is not worded:\n%s", name, out)
		}
		if len(tv.all) != 0 || len(tv.rows) != 0 || tv.summary != nil {
			t.Errorf("%s switch kept %d rows and summary %v", name, len(tv.all), tv.summary)
		}
		press(a, key("enter"))
		if len(a.stack) != depth {
			t.Errorf("%s switch: enter opened a view from a row that is not shown", name)
		}
	}
}

// A refresh in place keeps the rows in sight until the new ones arrive, and a
// failed one leaves nothing behind the error for enter or r to bring back.
func TestProfilingFailedRefreshLeavesNoRows(t *testing.T) {
	pinSnapshotClock(t)
	const hottest = "UTF8JsonGenerator.writeString"
	sc := loadProfilingScenario(t, "hotspots-running")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	depth := len(a.stack)

	press(a, key("r"))
	if out := screen(a); !tv.loading || len(tv.rows) != 300 || !strings.Contains(out, hottest) {
		t.Fatalf("r must keep the 300 rows while it asks again: loading=%v, %d rows", tv.loading, len(tv.rows))
	}
	tv.Update(dataMsg{owner: tv, seq: tv.seq, err: errors.New("the environment refused the analysis (403)")})
	if len(tv.all) != 0 || len(tv.rows) != 0 {
		t.Errorf("a failed refresh kept %d rows behind the error", len(tv.all))
	}
	press(a, key("enter"))
	if len(a.stack) != depth {
		t.Error("enter behind the error opened a view")
	}
	press(a, key("r"))
	if out := screen(a); strings.Contains(out, hottest) || strings.Contains(out, "300 rows") {
		t.Errorf("r after a failure repainted the old rows:\n%s", out)
	}
}

// The wait is counted from when the view started waiting, and what the source
// said last stays: a lens switch replaces the call, not the analysis. Another
// window is another analysis and starts the count again.
func TestProfilingWaitSurvivesALensSwitch(t *testing.T) {
	pinSnapshotClock(t)
	a, tv := openProfiling(t, loadProfilingScenario(t, "hotspots-running"))
	tv.started = catalog.Now().Add(-41 * time.Second)
	tv.progress.set("the environment accepted the analysis")
	press(a, key("]"))
	if got, want := strings.Join(tv.loadingText(), "|"),
		"analysing the hotspots of shop-backend, which can take minutes — 41s|the environment accepted the analysis"; got != want {
		t.Errorf("after a lens switch the view says\n %q, want\n %q", got, want)
	}
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: 30 * time.Minute}))
	if got, want := strings.Join(tv.loadingText(), "|"),
		"analysing the hotspots of shop-backend, which can take minutes — 0s"; got != want {
		t.Errorf("after a timeframe switch the view says\n %q, want\n %q", got, want)
	}
}

// Nothing of the wait is cut off at 80 columns: a line wider than the body
// wraps.
func TestProfilingLoadingLineWrapsAtEightyColumns(t *testing.T) {
	pinSnapshotClock(t)
	a, tv := openProfiling(t, loadProfilingScenario(t, "hotspots-running"))
	tv.scope.Entity.Name = "shop-backend-checkout-eu-west-1a"
	tv.started = catalog.Now().Add(-41 * time.Second)
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	var lines []string
	for _, line := range strings.Split(screen(a), "\n") {
		lines = append(lines, strings.TrimRight(line, " "))
	}
	want := "\n  analysing the hotspots of shop-backend-checkout-eu-west-1a, which can take\n  minutes — 41s\n"
	if out := strings.Join(lines, "\n"); !strings.Contains(out, want) {
		t.Errorf("the loading text is not whole, want %q in:\n%s", want, out)
	}
}

// profilingCalls opens :hotspots over a source that records every call and
// never answers, as an analysis that is still running.
func profilingCalls(t *testing.T) (a *app, tv *tableView, calls *[]context.Context, scopes *[]catalog.Scope) {
	t.Helper()
	a = testApp(t, "home")
	calls, scopes = &[]context.Context{}, &[]catalog.Scope{}
	a.ds.sources = map[string]Source{
		"hotspots": func(ctx context.Context, scope catalog.Scope, _ string) ([]map[string]any, error) {
			*calls = append(*calls, ctx)
			*scopes = append(*scopes, scope)
			return nil, nil
		},
	}
	_, cmd := a.Update(NavigateMsg{Target: "hotspots", Filter: "PROCESS-000000000000A001"})
	deliver(a, cmd)
	return a, a.top().(*tableView), calls, scopes
}

// A view parked on the '-' stack is off the trail: nothing that happens while
// it is there may start an analysis nobody watches. It takes the new window
// along and asks once, when '-' brings it back.
func TestProfilingParkedViewAsksForNothing(t *testing.T) {
	a, tv, calls, scopes := profilingCalls(t)
	// Segments do not reach an analysis: applying one leaves the call alone.
	deliver(a, a.refreshAllViews())
	if len(*calls) != 1 || (*calls)[0].Err() != nil {
		t.Fatalf("a segment change on the view itself: %d calls, first cancelled=%v", len(*calls), (*calls)[0].Err() != nil)
	}
	_, cmd := a.Update(NavigateMsg{Target: "problems"})
	deliver(a, cmd)
	if len(*calls) != 1 || (*calls)[0].Err() == nil {
		t.Fatalf("leaving: %d calls, first cancelled=%v", len(*calls), (*calls)[0].Err() != nil)
	}

	half := catalog.Timeframe{Label: "30m", Dur: 30 * time.Minute}
	deliver(a, a.setTimeframe(half))
	if len(*calls) != 1 {
		t.Fatalf("a timeframe change started call %d on a parked view", len(*calls))
	}
	deliver(a, a.refreshAllViews())
	if len(*calls) != 1 {
		t.Fatalf("a segment change started call %d on a parked view", len(*calls))
	}
	deliver(a, tv.Refresh())
	if len(*calls) != 1 || tv.loading {
		t.Fatalf("a refresh of a parked view started call %d (loading=%v)", len(*calls), tv.loading)
	}

	_, cmd = a.Update(key("-"))
	deliver(a, cmd)
	if a.top() != tv || len(*calls) != 2 {
		t.Fatalf("'-' must ask exactly once: top is the view=%v, %d calls", a.top() == tv, len(*calls))
	}
	if (*calls)[1].Err() != nil || (*scopes)[1].Timeframe.Label != "30m" {
		t.Errorf("the call on return: cancelled=%v, window %q, want it live on 30m",
			(*calls)[1].Err() != nil, (*scopes)[1].Timeframe.Label)
	}

	// Parked again with its call cancelled, then back: one more call, and
	// the one before it is the only one that was cancelled for it.
	_, cmd = a.Update(key("-"))
	deliver(a, cmd)
	_, cmd = a.Update(key("-"))
	deliver(a, cmd)
	if len(*calls) != 3 || (*calls)[1].Err() == nil || (*calls)[2].Err() != nil {
		t.Errorf("second round trip: %d calls, want 3 with only the last one live", len(*calls))
	}
}

// A parked view that already had its result shows none of it on return when
// the window changed meanwhile.
func TestProfilingParkedViewReturnsOnTheNewWindow(t *testing.T) {
	pinSnapshotClock(t)
	sc := loadProfilingScenario(t, "hotspots-running")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	_, cmd := a.Update(NavigateMsg{Target: "problems"})
	deliver(a, cmd)
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: 30 * time.Minute}))
	if tv.loading {
		t.Fatal("a parked view must not fetch")
	}
	_, cmd = a.Update(key("-"))
	deliver(a, cmd)
	if !tv.loading || len(tv.rows) != 0 || tv.scope.Timeframe.Label != "30m" {
		t.Errorf("on return: loading=%v, %d rows, window %q; want a fresh wait on 30m",
			tv.loading, len(tv.rows), tv.scope.Timeframe.Label)
	}
}

// Auto refresh is for views a refetch costs a query. An analysis is asked for
// by hand: a tick neither replaces the call in flight nor, once the cached
// analysis expired, submits a new one every interval.
func TestAutoRefreshLeavesAnAnalysisAlone(t *testing.T) {
	a, tv, calls, _ := profilingCalls(t)
	press(a, key("R"))
	if a.status != "auto-refresh every 10s — not on this view, where r asks for a new analysis" {
		t.Errorf("R on a profiling view said %q", a.status)
	}
	a.Update(refreshTickMsg{gen: a.refreshGen})
	a.Update(refreshTickMsg{gen: a.refreshGen})
	if len(*calls) != 1 || (*calls)[0].Err() != nil || !tv.loading {
		t.Errorf("two ticks: %d calls, first cancelled=%v, loading=%v; want the one call untouched",
			len(*calls), (*calls)[0].Err() != nil, tv.loading)
	}

	// Every other view is refreshed by the tick, API-backed ones included.
	n := 0
	a.ds.sources["slos"] = func(context.Context, catalog.Scope, string) ([]map[string]any, error) {
		n++
		return nil, nil
	}
	_, cmd := a.Update(NavigateMsg{Target: "slos"})
	deliver(a, cmd)
	_, cmd = a.Update(refreshTickMsg{gen: a.refreshGen})
	collectMsgs(cmd)
	if n != 2 {
		t.Errorf("slos was fetched %d times after one tick, want 2 (open, tick)", n)
	}
	press(a, key("R"))
	if a.status != "auto-refresh every 30s" {
		t.Errorf("R on slos said %q", a.status)
	}
}

// A dataSource-level change still reaches an ordinary view parked on '-'.
func TestRefreshAllViewsReachesParkedOrdinaryViews(t *testing.T) {
	a := testApp(t, "logs")
	logs := a.top().(*tableView)
	_, cmd := a.Update(NavigateMsg{Target: "problems"})
	deliver(a, cmd)
	seq := logs.seq
	deliver(a, a.refreshAllViews())
	if logs.seq != seq+1 || !logs.loading {
		t.Errorf("parked logs view: seq %d → %d, loading=%v; want one refetch", seq, logs.seq, logs.loading)
	}
}

// The allocations path column is found by the filter and ordered by the
// sort, although its cell is drawn to the width it gets.
func TestAllocationPathsCanBeFilteredAndSorted(t *testing.T) {
	sc := loadProfilingScenario(t, "allocations")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	all := len(tv.rows)
	press(a, key("/"))
	typeText(a, "ProductCache")
	press(a, key("enter"))
	if len(tv.rows) == 0 || len(tv.rows) == all {
		t.Fatalf("filter ProductCache left %d of %d rows", len(tv.rows), all)
	}
	for _, row := range tv.rows {
		if site := catalog.Str(row, catalog.ProfSite); !strings.Contains(fmt.Sprint(site, row[catalog.ProfCallers]), "ProductCache") {
			t.Errorf("the filter kept %s", site)
		}
	}
	tv.filterInput.SetValue("")
	tv.applyFilter()
	tv.sortCol, tv.sortDesc = len(tv.columns())-1, false
	tv.sortRows()
	for i := 1; i < len(tv.rows); i++ {
		if a, b := catalog.Str(tv.rows[i-1], catalog.ProfSite), catalog.Str(tv.rows[i], catalog.ProfSite); strings.ToLower(a) > strings.ToLower(b) {
			t.Fatalf("sorted by path, %s comes before %s", a, b)
		}
	}
}

// A pinned process is the one :hotspots analyses; an explicit argument wins
// over the pin; and the notice that a pin cannot scope the view appears only
// when that is what happened.
func TestProfilingTakesThePinnedProcess(t *testing.T) {
	process := &catalog.Entity{ID: "PROCESS-000000000000A001", Name: "shop-backend", Type: "PROCESS"}
	service := &catalog.Entity{ID: "SERVICE-000000000000B001", Name: "shop-frontend", Type: "SERVICE"}
	for _, tc := range []struct {
		name       string
		pin        *catalog.Entity
		arg        string
		wantEntity string
		wantStatus string
	}{
		{"a pinned process", process, "", "PROCESS-000000000000A001", ""},
		{"an argument over a pinned process", process, "PROCESS-000000000000A002", "PROCESS-000000000000A002", ""},
		{"an argument over a pinned service", service, "PROCESS-000000000000A002", "PROCESS-000000000000A002", ""},
		{"a pinned service", service, "", "", "hotspots can't scope to SERVICE (ctrl+x unpins)"},
	} {
		a := testApp(t, "home")
		a.pin = tc.pin
		a.status = ""
		deliver(a, a.jumpTo("hotspots", tc.arg))
		tv := a.top().(*tableView)
		got := ""
		if tv.scope.Entity != nil {
			got = tv.scope.Entity.ID
		}
		if got != tc.wantEntity || a.status != tc.wantStatus {
			t.Errorf("%s: analyses %q with status %q, want %q with %q", tc.name, got, a.status, tc.wantEntity, tc.wantStatus)
		}
	}
	// Another view keeps the wording that fits a list.
	a := testApp(t, "home")
	a.pin = &catalog.Entity{ID: "FRONTEND-AAAABBBBCCCCDDDD", Name: "astroshop", Type: "FRONTEND"}
	deliver(a, a.jumpTo("traces", ""))
	if want := "traces can't scope to FRONTEND — showing all (ctrl+x unpins)"; a.status != want {
		t.Errorf("traces under a frontend pin: status %q, want %q", a.status, want)
	}
}

// A call reads from the source that was wired when it was issued: one made
// before a context switch must not reach the next environment's source with
// this one's process id.
func TestCallKeepsTheSourceItWasIssuedWith(t *testing.T) {
	source := func(name string) Source {
		return func(context.Context, catalog.Scope, string) ([]map[string]any, error) {
			return []map[string]any{{"from": name}}, nil
		}
	}
	ds := &dataSource{sources: map[string]Source{"hotspots": source("before")}}
	cmd := ds.call(context.Background(), nil, 1, "hotspots", catalog.Scope{}, "")
	ds.sources = map[string]Source{"hotspots": source("after")}
	msg := cmd().(dataMsg)
	if msg.err != nil || len(msg.records) != 1 || msg.records[0]["from"] != "before" {
		t.Errorf("the call answered from %v (err %v), want the source it was issued with", msg.records, msg.err)
	}
}

// P is an app-wide preference; on a view that has no preview pane it says
// so, and the view does not offer the key.
func TestPreviewToggleOnAViewWithoutAPane(t *testing.T) {
	sc := loadProfilingScenario(t, "hotspots-running")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	press(a, key("P"))
	if want := "preview pane off (P restores it)"; a.status != want {
		t.Errorf("P: status %q, want %q", a.status, want)
	}
	if got := hintFor(tv, "P"); got != "" {
		t.Errorf("the view offers P %s, which does nothing on it", got)
	}
	press(a, key("P"))
	if want := "preview pane on — this view has none"; a.status != want {
		t.Errorf("P again: status %q, want %q", a.status, want)
	}
	// A view with a pane keeps its wording.
	b := testApp(t, "hosts")
	b.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	press(b, key("P"))
	press(b, key("P"))
	if want := "preview pane on"; b.status != want {
		t.Errorf("hosts: status %q, want %q", b.status, want)
	}
}

// Enter on a row that cannot be focused says which kind of row it is.
func TestEnterOnARowThatIsNoFunction(t *testing.T) {
	a := renderProfiling(t, "hotspots-focus")
	tv := a.top().(*tableView)
	tv.cursor = 0 // the total row of the function in focus
	press(a, key("enter"))
	if want := "this row is the function in focus — enter a caller or callee to walk on"; a.status != want {
		t.Errorf("enter on the focused function: %q, want %q", a.status, want)
	}
	tv.rows = append(tv.rows, map[string]any{catalog.ProfRelation: catalog.ProfRelCaller, catalog.ProfFunction: "(root)"})
	tv.cursor = len(tv.rows) - 1
	press(a, key("enter"))
	if want := "(root) is where the stacks start, not a function"; a.status != want {
		t.Errorf("enter on the root: %q, want %q", a.status, want)
	}
	if a.top() != viewModel(tv) {
		t.Error("enter on a row that is no function opened a view")
	}
}

// A function name is the environment's text. Wherever the view repeats it —
// the breadcrumb, the loading line, the footnote, the echo in the footer —
// it can neither style the terminal nor break a line; what c copies is the
// command as it is.
func TestHostileFunctionNameStaysInItsCell(t *testing.T) {
	pinSnapshotClock(t)
	const hostile = "com.example.\x1b[31mRED\x1b[0m\nsecond.line\tClass.method"
	sc := loadProfilingScenario(t, "hotspots-running")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	tv.rows[0][catalog.ProfFunction] = hostile
	tv.cursor = 0

	check := func(state string) {
		t.Helper()
		frame := a.View()
		if strings.Contains(frame, "\x1b[31mRED") || strings.Contains(frame, "\x1b[0m\nsecond") {
			t.Errorf("%s: the frame carries the name's own escape sequence", state)
		}
		if n := strings.Count(frame, "\n") + 1; n != 40 {
			t.Errorf("%s: the frame is %d lines on a 40 line screen", state, n)
		}
		if !strings.Contains(ansi.Strip(frame), "␛[31mRED") {
			t.Errorf("%s: the name is not shown with its escape made visible", state)
		}
	}
	check("selected in the table") // the footnote spells the selected name out
	press(a, key("enter"))
	focus := a.top().(*tableView)
	if focus == tv || focus.scope.Arg != hostile {
		t.Fatalf("enter opened %q", focus.scope.Arg)
	}
	check("loading the focus") // breadcrumb and echo
	focus.Update(dataMsg{owner: focus, seq: focus.seq, elapsed: time.Second, records: []map[string]any{
		{catalog.SummaryKey: true, catalog.ProfSumSamples: 10.0},
		{catalog.ProfRelation: "total", catalog.ProfFunction: hostile, catalog.ProfSamples: 10.0, catalog.ProfShare: 100.0},
	}})
	check("focused")
	if echo := focus.Echo(); !strings.Contains(echo, hostile) {
		t.Errorf("the copied command lost the name as it is: %q", echo)
	}
}
