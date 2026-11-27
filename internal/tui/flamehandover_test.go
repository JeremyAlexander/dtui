package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// A tab that is read after the context was switched opens nothing: the
// graph it asked for is of the context before, and would stand under the new
// one's name.
func TestFlameGraphIsNotOpenedOverAnotherContext(t *testing.T) {
	a, fv := walkFlame(t)
	press(a, key("tab")) // on the functions, the graph kept
	_, open := a.Update(key("tab"))
	// What a finished context switch does to the trail.
	a.navigate(newHomeView(a.ds, a.tf), true)
	deliver(a, open)
	if _, home := a.top().(*homeView); !home || len(a.stack) != 1 {
		t.Fatalf("a tab read after the switch left %T on a trail of %d", a.top(), len(a.stack))
	}
	if fv.graph == nil {
		t.Fatal("the fixture is to keep a graph that could be shown")
	}
	// Any view but the table the graph belongs to is not covered either.
	a, _ = walkFlame(t)
	press(a, key("tab"))
	_, open = a.Update(key("tab"))
	other := newTableView(a.ds, catalog.Lookup("problems"), catalog.Scope{})
	a.navigate(other, false)
	deliver(a, open)
	if a.top() != viewModel(other) {
		t.Errorf("a tab read over another view left %T on top", a.top())
	}
}

// The segment pill is dim wherever the view's data is an analysis the
// segments do not reach (ADR-0007): on the functions, and on the graph of
// the same analysis.
func TestSegmentPillIsDimOnTheFlameGraph(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
	a, fv := walkFlame(t)
	a.segApplied = []SegmentOption{{UID: "seg-1", Name: "shop"}}
	pill := "◐ " + segmentSummary(a.segApplied)
	dim, bright := theme.Dim.Render(pill), theme.Segment.Render(pill)
	if dim == bright {
		t.Fatal("the two styles are to differ at 256 colours")
	}
	if a.top() != viewModel(fv) || !strings.Contains(a.View(), dim) || strings.Contains(a.View(), bright) {
		t.Errorf("the pill is not dim on the graph")
	}
	press(a, key("tab"))
	if _, table := a.top().(*tableView); !table || !strings.Contains(a.View(), dim) {
		t.Errorf("the pill is not dim on the functions")
	}
	a.navigate(newTableView(a.ds, catalog.Lookup("problems"), catalog.Scope{}), false)
	if !strings.Contains(a.View(), bright) {
		t.Errorf("the pill is not bright on a view the segments reach")
	}
}

// windowed are stacks of the window a summary names.
func windowed(summary map[string]any, lines ...string) []map[string]any {
	records := stackRecords("", lines...)
	records[0][catalog.ProfSumFrom] = summary[catalog.ProfSumFrom]
	records[0][catalog.ProfSumTo] = summary[catalog.ProfSumTo]
	return records
}

// The graph reads the analysis the functions hold. When that one is no
// longer held, the stacks are of a new analysis, of another window: the
// status line says that the two readings are now of two analyses. A window
// the reader asked the graph for is no surprise and is not announced.
func TestFlameGraphSaysWhenItIsOfAnotherAnalysis(t *testing.T) {
	const said = "the functions' analysis is no longer held"
	a, tv, fv, _ := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	if fv.graph == nil || strings.Contains(a.status, said) {
		t.Fatalf("the analysis the functions show: graph %v, status %q", fv.graph != nil, a.status)
	}

	a, _, fv, _ = openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:34:00Z", flameWalk...), nil)
	if fv.graph == nil || !strings.HasPrefix(a.status, said) || a.statusErr {
		t.Fatalf("another analysis: graph %v, status %q", fv.graph != nil, a.status)
	}

	// After r and after t on the graph, the window is the one asked for.
	for name, ask := range map[string]func(a *app, fv *flameView){
		"a new timeframe": func(a *app, fv *flameView) {
			deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: fv.scope.Timeframe.Dur / 2}))
		},
		"a refresh": func(_ *app, fv *flameView) { fv.Refresh() },
	} {
		a, tv, fv, _ := openFlame(t)
		answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
		ask(a, fv)
		a.status = ""
		answerFlame(a, fv, stackRecords("2026-07-15T12:40:00Z", flameWalk...), nil)
		if fv.graph == nil || strings.Contains(a.status, said) {
			t.Errorf("%s is announced as a surprise: graph %v, status %q", name, fv.graph != nil, a.status)
		}
	}
}

// The search of one process's graph is not carried to another's.
func TestFlameGraphDropsTheSearchWithTheProcess(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	press(a, key("/"))
	press(a, key("a"))
	press(a, key("enter"))
	press(a, key("tab"))
	other := *tv.scope.Entity
	other.ID = "PROCESS_GROUP_INSTANCE-00000000000000FF"
	tv.scope.Entity = &other
	tv.cursor = len(tv.rows) // no row, so no function is handed over
	press(a, key("tab"))
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	if fv.graph == nil || fv.graph.Searching() {
		t.Errorf("the graph of another process is searched for %q", fv.graph.Pattern())
	}
}

// A second hand-over with another function moves the cursor to it: the
// lines about the selected frame, y and O are then about the function the
// table asked for, as the search is.
func TestFlameGraphMovesToTheFunctionHandedOver(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	first := catalog.Str(tv.rows[0], catalog.ProfFunction)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", "main;"+first+" 5,5", "main;other 5,5", "main;other;deep 1,0"), nil)
	if got := place(fv); !strings.HasPrefix(got, "sel=main;"+first+" ") {
		t.Fatalf("the cursor is not on the first function handed over: %s", got)
	}
	press(a, key("tab"))
	tv.rows[1][catalog.ProfFunction] = "other"
	tv.cursor = 1
	press(a, key("tab"))
	if got := place(fv); !strings.HasPrefix(got, "sel=main;other ") || fv.graph.Pattern() != "^other$" {
		t.Errorf("the cursor is not on the function handed over: %s", got)
	}
	if text, _, ok := fv.YankText(); !ok || text != "other" {
		t.Errorf("y copies %q", text)
	}
	// The same function again leaves the cursor where the reader put it.
	press(a, flameKey("down"))
	press(a, key("tab"))
	press(a, key("tab"))
	if got := place(fv); !strings.HasPrefix(got, "sel=main;other;deep ") {
		t.Errorf("an unchanged hand-over moved the cursor: %s", got)
	}
}

// A graph that is dropped is let go of: the details kept for its selected
// frame do not hold its tree for as long as the view lives.
func TestFlameGraphDropsWhatItKeptOfAGraph(t *testing.T) {
	kept := func(t *testing.T) (*app, *flameView) {
		a, fv := walkFlame(t)
		press(a, flameKey("down"))
		screen(a)
		if fv.fn.graph == nil {
			t.Fatal("the fixture is to keep the details of the selected frame")
		}
		return a, fv
	}
	a, fv := kept(t)
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: fv.scope.Timeframe.Dur / 2}))
	if fv.graph != nil || fv.fn.graph != nil {
		t.Errorf("after a new timeframe the view holds a graph: %v, details of one: %v", fv.graph != nil, fv.fn.graph != nil)
	}
	a, fv = kept(t)
	fv.Refresh()
	answerFlame(a, fv, nil, errors.New("forbidden"))
	if fv.graph != nil || fv.fn.graph != nil {
		t.Errorf("after an error the view holds a graph: %v, details of one: %v", fv.graph != nil, fv.fn.graph != nil)
	}
}

// A search is a pattern and not a place: it is searched again in the
// analysis of a new timeframe, as it is after a refresh, and a function
// searched for stays named as a function.
func TestFlameGraphKeepsTheSearchOverANewTimeframe(t *testing.T) {
	a, fv := walkFlame(t)
	press(a, key("/"))
	press(a, key("a"))
	press(a, key("enter"))
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: fv.scope.Timeframe.Dur / 2}))
	answerFlame(a, fv, stackRecords("2026-07-15T12:30:00Z", flameWalk...), nil)
	if fv.graph == nil || fv.graph.Pattern() != "a" || fv.graph.SearchedFunction() != "" {
		t.Fatalf("after a new timeframe the search is %q", fv.graph.Pattern())
	}
	press(a, key("esc"))
	press(a, flameKey("down"))
	press(a, key("#"))
	fn := fv.graph.SearchedFunction()
	if fn == "" {
		t.Fatal("# is to search for the selected function")
	}
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "15m", Dur: fv.scope.Timeframe.Dur / 2}))
	answerFlame(a, fv, stackRecords("2026-07-15T12:45:00Z", flameWalk...), nil)
	if fv.graph.SearchedFunction() != fn || fv.graph.Selected() != flame.Root {
		t.Errorf("after a new timeframe the function searched for is %q (pattern %q), want %q", fv.graph.SearchedFunction(), fv.graph.Pattern(), fn)
	}
	// A cleared search stays cleared.
	press(a, key("esc"))
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: fv.scope.Timeframe.Dur * 2}))
	answerFlame(a, fv, stackRecords("2026-07-15T12:50:00Z", flameWalk...), nil)
	if fv.graph.Searching() {
		t.Errorf("a cleared search came back as %q", fv.graph.Pattern())
	}
}

// A graph the reader refreshed is of a newer analysis than the functions
// show. It is still the graph: tab and tab again finds it as it was left,
// and reads nothing, until the functions themselves show another analysis.
func TestFlameGraphRefreshedIsKeptOverTabs(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	fv.graph.ClearSearch()
	deliver(a, fv.Refresh())
	answerFlame(a, fv, stackRecords("2026-07-15T12:34:00Z", flameWalk...), nil)
	before := calls[catalog.HotspotStacksAPI] + calls[catalog.HotspotStacksFreshAPI]
	for round := range 3 {
		press(a, flameKey("down"))
		press(a, key("enter"))
		at := place(fv)
		press(a, key("tab"))
		press(a, key("tab"))
		if fv.graph == nil || place(fv) != at || calls[catalog.HotspotStacksAPI]+calls[catalog.HotspotStacksFreshAPI] != before {
			t.Fatalf("round %d: the refreshed graph was dropped (calls %v)", round, calls)
		}
		press(a, key("esc"))
		press(a, flameKey("up"))
	}
	// The functions read another analysis: now the graph is stale.
	press(a, key("tab"))
	tv.summary[catalog.ProfSumTo] = "2026-07-15T13:00:00Z"
	press(a, key("tab"))
	if fv.graph != nil || !fv.loading {
		t.Errorf("the functions show another analysis and the graph was kept")
	}
}

// - returns to the trail that was left. A graph on it that the functions
// would no longer open (they now show allocations, another thread state, a
// failure) is not put back over them.
func TestDashDoesNotPutAStaleFlameGraphBack(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	press(a, key("tab")) // on the functions; - remembers the trail with the graph
	press(a, key("-"))
	if a.top() != viewModel(fv) {
		t.Fatalf("- is to return to the graph while nothing changed: %T", a.top())
	}
	press(a, key("-"))
	for _, change := range []func(){
		func() { tv.scope.Lens = len(catalog.HotspotStates()) }, // allocations
		func() { tv.scope.Lens = 1 },
		func() { tv.err = errors.New("forbidden") },
		func() { tv.summary = map[string]any{catalog.ProfSumFrom: "a", catalog.ProfSumTo: "b"} },
	} {
		a, tv, fv, _ = openFlame(t)
		answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
		press(a, key("tab"))
		change()
		press(a, key("-"))
		if a.top() != viewModel(tv) || len(a.stack) == 0 || !strings.Contains(a.status, "tab opens") {
			t.Errorf("- put %T over the changed functions (status %q)", a.top(), a.status)
		}
	}
	if !catalog.ProfileAllocations(catalog.Scope{Lens: len(catalog.HotspotStates())}) {
		t.Fatal("the fixture is to switch the functions to allocations")
	}
}

// tab on functions that failed to load opens nothing and asks for nothing,
// and the footer does not offer it.
func TestFlameGraphIsNotOpenedFromFailedFunctions(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	press(a, key("tab"))
	tv.err = errors.New("forbidden")
	before := calls[catalog.HotspotStacksAPI]
	press(a, key("tab"))
	if a.top() != viewModel(tv) || calls[catalog.HotspotStacksAPI] != before || !strings.Contains(a.status, "failed") {
		t.Errorf("tab on failed functions: top %T, calls %v, status %q", a.top(), calls, a.status)
	}
	if strings.Contains(screen(a), "tab flame graph") {
		t.Errorf("the footer offers the graph of functions that failed:\n%s", screen(a))
	}
}

// A graph that goes away while its search line is open takes the line with
// it: the keys are the app's again.
func TestFlameGraphStopsTypingWhenItsGraphGoes(t *testing.T) {
	a, fv := walkFlame(t)
	fv.Refresh()
	press(a, key("/"))
	if !fv.InputActive() {
		t.Fatal("/ is to open the search line")
	}
	answerFlame(a, fv, nil, errors.New("forbidden"))
	if fv.InputActive() || strings.Contains(screen(a), "regular expression") {
		t.Errorf("the view still types after its graph failed:\n%s", screen(a))
	}
	a, fv = walkFlame(t)
	press(a, key("/"))
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: fv.scope.Timeframe.Dur / 2}))
	if fv.InputActive() {
		t.Error("the view still types after its graph was dropped")
	}
}

// P on the graph says what happened to the graph's details, not what the
// preview pane of a table would do at this size.
func TestPreviewKeyOnTheFlameGraphSpeaksOfItsDetails(t *testing.T) {
	a, fv := walkFlame(t)
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	press(a, flameKey("down"))
	if len(fv.lines.details) == 0 {
		t.Fatal("the fixture is to show details at 80x24")
	}
	press(a, key("P"))
	if a.status != "details off (P restores them)" {
		t.Errorf("P off: %q", a.status)
	}
	press(a, key("P"))
	if a.status != "details on" {
		t.Errorf("P on, with details drawn: %q", a.status)
	}
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	press(a, key("P"))
	press(a, key("P"))
	if len(fv.lines.details) != 0 || !strings.HasPrefix(a.status, "details on: there is no room for them") {
		t.Errorf("P on, without room: %q (%d lines of details)", a.status, len(fv.lines.details))
	}
	// d says the same where giving the details the room shows none.
	press(a, key("d"))
	if !strings.Contains(a.status, "no room") {
		t.Errorf("d without room: %q", a.status)
	}
}

// The functions' footer keeps its help key when the tab hint is added to it.
func TestFunctionsFooterKeepsTheHelpKey(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	press(a, key("tab"))
	for _, width := range []int{80, 100, 120, 160} {
		a.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		footer := ""
		for _, line := range strings.Split(screen(a), "\n") {
			if strings.Contains(line, "tab flame graph") {
				footer = line
			}
		}
		if !strings.Contains(footer, "? help") {
			t.Errorf("at %d columns the footer is %q", width, footer)
		}
	}
}

// A new timeframe re-times the functions and the graph alike. The functions
// then show a window the graph already asked for: the next tab finds the
// graph as it was left.
func TestFlameGraphIsKeptAfterBothWereRetimed(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: fv.scope.Timeframe.Dur / 2}))
	answerFlame(a, fv, stackRecords("2026-07-15T12:40:00Z", flameWalk...), nil)
	tv.summary = map[string]any{catalog.ProfSumFrom: "2026-07-15T10:00:00Z", catalog.ProfSumTo: "2026-07-15T12:40:00Z"}
	before := calls[catalog.HotspotStacksAPI]
	press(a, key("tab"))
	press(a, key("tab"))
	if fv.graph == nil || calls[catalog.HotspotStacksAPI] != before {
		t.Errorf("the graph of the new timeframe was dropped at the next tab (calls %v)", calls)
	}
}

// shift+tab switches between the functions and the graph as tab does (there
// are two readings, so backwards is the other one), and the help says so.
func TestShiftTabSwitchesBetweenFunctionsAndGraph(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	answerFlame(a, fv, windowed(tv.summary, flameWalk...), nil)
	press(a, key("shift+tab"))
	if a.top() != viewModel(tv) {
		t.Fatalf("shift+tab on the graph: %T", a.top())
	}
	press(a, key("shift+tab"))
	if a.top() != viewModel(fv) {
		t.Fatalf("shift+tab on the functions: %T", a.top())
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 200})
	press(a, key("?"))
	if out := strings.Join(strings.Fields(screen(a)), " "); !strings.Contains(out, "(shift+tab backwards)") {
		t.Errorf("the help does not name shift+tab:\n%s", out)
	}
}

// The help closes on any key when all of it is in view, also after it was
// scrolled on a smaller terminal that then grew to fit it exactly.
func TestHelpClosesWhenItCameToFitExactly(t *testing.T) {
	a, _ := walkFlame(t)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	press(a, key("?"))
	screen(a)
	h, ok := a.overlay.(*helpOverlay)
	if !ok || !h.more {
		t.Fatal("the help is to need scrolling at 20 lines")
	}
	press(a, key("j"))
	screen(a)
	total := 0
	for height := 21; height < 400 && h.more; height++ {
		a.Update(tea.WindowSizeMsg{Width: 120, Height: height})
		screen(a)
		total = height
	}
	if h.more || h.top != 0 {
		t.Fatalf("at %d lines the help fits and is still scrolled (top %d, more %v)", total, h.top, h.more)
	}
	press(a, key("j"))
	if a.overlay != nil {
		t.Error("j did not close a help that is all in view")
	}
}

// A call that was replaced may go on reporting; its notes must not reach the
// call that replaced it.
func TestFlameGraphProgressIsPerCall(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	first := fv.progress
	first.set("polling the first call")
	press(a, key("r"))
	if fv.progress == first || fv.progress.get() != "" {
		t.Fatalf("the second call reads the first call's progress: %q", fv.progress.get())
	}
	first.set("still polling the first call")
	if got := fv.progress.get(); got != "" {
		t.Errorf("a note of the replaced call reached the call in flight: %q", got)
	}
}
