package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
)

// flameKey is a key press by the name the view reads it under; key() knows
// the letters and a few others.
func flameKey(name string) tea.KeyMsg {
	types := map[string]tea.KeyType{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"backspace": tea.KeyBackspace, "home": tea.KeyHome, "end": tea.KeyEnd,
		"ctrl+f": tea.KeyCtrlF, "ctrl+b": tea.KeyCtrlB, "space": tea.KeySpace,
	}
	if typ, ok := types[name]; ok {
		if typ == tea.KeySpace {
			return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		return tea.KeyMsg{Type: typ}
	}
	return key(name)
}

// flameCalls counts the calls of the flame graph's sources by name.
type flameCalls map[string]int

func (c flameCalls) wire(a *app) {
	a.ds.sources = map[string]Source{}
	for _, name := range []string{catalog.HotspotStacksAPI, catalog.HotspotStacksFreshAPI} {
		a.ds.sources[name] = func(context.Context, catalog.Scope, string) ([]map[string]any, error) {
			c[name]++
			return nil, nil
		}
	}
}

// stackRecords is a stacks result: a summary of the window, and one record
// per "frame;frame running,lock" line.
func stackRecords(to string, lines ...string) []map[string]any {
	records := []map[string]any{{
		catalog.SummaryKey: true, catalog.ProfSumFrom: "2026-07-15T10:00:00Z", catalog.ProfSumTo: to,
		catalog.ProfSumRows: float64(len(lines)),
	}}
	if len(lines) == 0 {
		records[0][catalog.ProfSumEmpty] = catalog.ProfEmptyNoSamples
	}
	for _, line := range lines {
		stack, counts, _ := strings.Cut(line, " ")
		rec := map[string]any{catalog.ProfStack: stack}
		for i, count := range strings.Split(counts, ",") {
			var n float64
			fmt.Sscan(count, &n)
			if n > 0 {
				rec[catalog.HotspotStates()[i]] = n
			}
		}
		records = append(records, rec)
	}
	return records
}

// flameWalk is the graph the key tests move in: at 120 columns every frame
// is wide enough to draw. Only main;a;y and other;a have lock samples.
var flameWalk = []string{"main;a;x 40,0", "main;a;y 20,10", "main;b 60,0", "main;c 10,0", "other;a 70,30"}

// answerFlame delivers a stacks result to the flame graph that waits for it
// and lets its tree arrive.
func answerFlame(a *app, fv *flameView, records []map[string]any, err error) {
	deliver(a, fv.Update(dataMsg{owner: fv, seq: fv.seq, records: records, err: err}))
}

// openFlame opens the functions table of the recorded process, answers it,
// and presses tab on it. The flame graph is waiting for its stacks.
func openFlame(t *testing.T) (*app, *tableView, *flameView, flameCalls) {
	t.Helper()
	sc := loadProfilingScenario(t, "hotspots-running")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	calls := flameCalls{}
	calls.wire(a)
	screen(a)
	press(a, key("tab"))
	fv, ok := a.top().(*flameView)
	if !ok {
		t.Fatalf("tab on the functions opened %T (status %q)", a.top(), a.status)
	}
	return a, tv, fv, calls
}

// walkFlame is openFlame answered with the flameWalk stacks and no search.
func walkFlame(t *testing.T) (*app, *flameView) {
	t.Helper()
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	if fv.graph == nil {
		t.Fatal("the stacks did not become a graph")
	}
	fv.graph.ClearSearch() // the function the table handed over
	screen(a)
	return a, fv
}

// place says where the graph stands, as the model's tests do.
func place(fv *flameView) string {
	g := fv.graph
	path := func(n flame.NodeID) string {
		if n == flame.Root {
			return "all"
		}
		return strings.Join(g.Tree().Path(n), ";")
	}
	return fmt.Sprintf("sel=%s zoom=%s top=%d lens=%s search=%q", path(g.Selected()), path(g.Zoom()), g.Top(),
		catalog.HotspotState(fv.scope), g.Pattern())
}

// tab on the functions opens the flame graph of the same analysis, and tab
// there returns to the functions as they were. The graph is kept: opening it
// again finds it as it was left and reads nothing.
func TestFlameGraphOpensFromTheFunctionsAndBack(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	if calls[catalog.HotspotStacksAPI] != 1 || calls[catalog.HotspotStacksFreshAPI] != 0 {
		t.Fatalf("opening the graph read %v, want the held analysis once", calls)
	}
	if !fv.Busy() || !strings.Contains(screen(a), "analysing the hotspots of shop-backend") {
		t.Errorf("the graph does not say it waits:\n%s", screen(a))
	}
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	if got := screen(a); !strings.Contains(got, "hotspots (shop-backend) › flame·running") {
		t.Errorf("the breadcrumb does not lead from the functions to the graph:\n%s", got)
	}
	tv.cursor = 3
	fv.graph.ClearSearch()
	for _, k := range []string{"down", "down", "enter"} {
		press(a, flameKey(k))
	}
	before := place(fv)

	press(a, key("tab"))
	if a.top() != viewModel(tv) || tv.cursor != 3 || len(tv.rows) == 0 || tv.loading {
		t.Fatalf("tab on the graph: top %T, cursor %d, %d rows, loading %v", a.top(), tv.cursor, len(tv.rows), tv.loading)
	}
	press(a, key("shift+tab"))
	if a.top() != viewModel(fv) || place(fv) != before {
		t.Errorf("back on the graph: %T at %s, want it as it was left, at %s", a.top(), place(fv), before)
	}
	if calls[catalog.HotspotStacksAPI] != 1 || calls[catalog.HotspotStacksFreshAPI] != 0 {
		t.Errorf("returning to the graph read again: %v", calls)
	}
	// esc leaves it like tab does, once there is nothing of its own to undo.
	press(a, key("esc"))
	press(a, key("esc"))
	if a.top() != viewModel(tv) {
		t.Errorf("esc, esc on a zoomed graph left %T on top", a.top())
	}
}

func TestFlameGraphKeys(t *testing.T) {
	for _, tc := range []struct {
		name, keys, want, status string
	}{
		{"it opens on the root", "", "sel=all zoom=all top=0 lens=running search=\"\"", ""},
		{"down and j go to the widest callee", "down j", "sel=main;a zoom=all top=0 lens=running search=\"\"", ""},
		{"up and k go to the caller", "down down up k", "sel=all zoom=all top=0 lens=running search=\"\"", ""},
		{"right and left move along the level", "down down right right left", "sel=main;b zoom=all top=0 lens=running search=\"\"", ""},
		{"h and l are not movement here", "down l h", "sel=main zoom=all top=0 lens=running search=\"\"", "← and → move along a level"},
		{"f and b are not paging here", "f", "sel=all zoom=all top=0 lens=running search=\"\"", "pgdn and pgup page through the levels, as do ctrl+f and ctrl+b"},
		{"enter zooms, backspace zooms out one step", "down enter down enter backspace", "sel=main;a zoom=main top=0 lens=running search=\"\"", ""},
		{"backspace without a zoom says so", "backspace", "sel=all zoom=all top=0 lens=running search=\"\"", "not zoomed: enter zooms into the selected frame"},
		{"esc leaves every zoom at once", "down enter down enter esc", "sel=main;a zoom=all top=0 lens=running search=\"\"", ""},
		{"esc clears the search before the zoom", "down enter # esc", "sel=main zoom=main top=0 lens=running search=\"\"", ""},
		{"# searches for the selected function", "down down #", "sel=main;a zoom=all top=0 lens=running search=\"^a$\"", ""},
		{"# on the root says why not", "#", "sel=all zoom=all top=0 lens=running search=\"\"", "the root is every stack, not a function to search for"},
		{"n and N step through the matches", "down down # n", "sel=other;a zoom=all top=0 lens=running search=\"^a$\"", ""},
		{"n past the last match goes around and says so", "down down # n n", "sel=main;a zoom=all top=0 lens=running search=\"^a$\"", "back at the first match"},
		{"N before the first match goes around and says so", "down down # N", "sel=other;a zoom=all top=0 lens=running search=\"^a$\"", "on to the last match"},
		{"n without a search says how to search", "n", "sel=all zoom=all top=0 lens=running search=\"\"", "no search: / searches, # searches for the selected function"},
		{"] is the next thread state, with the place kept", "down right down ]", "sel=other;a zoom=all top=0 lens=lock search=\"\"", ""},
		{"[ from the first state is the last: never allocations", "[", "sel=all zoom=all top=0 lens=wait search=\"\"", ""},
		{"] from the last state is the first", "] ] ] ] ]", "sel=all zoom=all top=0 lens=running search=\"\"", ""},
		{"a state without the selected frame moves to its caller", "down down right ]", "sel=main zoom=all top=0 lens=lock search=\"\"", ""},
		{"G and g scroll to the ends", "G g", "sel=all zoom=all top=0 lens=running search=\"\"", ""},
		{"paging does not scroll a graph that fits", "pgdown ctrl+f space pgup ctrl+b", "sel=all zoom=all top=0 lens=running search=\"\"", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, fv := walkFlame(t)
			for _, k := range strings.Fields(tc.keys) {
				a.status = ""
				press(a, flameKey(k))
			}
			if got := place(fv); got != tc.want {
				t.Errorf("%s\nwant %s", got, tc.want)
			}
			if a.status != tc.status {
				t.Errorf("status %q, want %q", a.status, tc.status)
			}
			if a.top() != viewModel(fv) {
				t.Errorf("the keys left the graph for %T", a.top())
			}
		})
	}
}

// On a terminal with fewer rows than the graph has levels, the paging keys
// scroll it, the strip says which levels are in view, and the selection is
// never left outside them.
func TestFlameGraphScrolls(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	deep := make([]string, 0, 30)
	for i := range 30 {
		deep = append(deep, strings.TrimSuffix(strings.Repeat("f;", i+1), ";")+" 5,0")
	}
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", deep...), nil)
	a.ds.previewOff = true // the details have a test of their own
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	out := screen(a)
	if !strings.Contains(out, "levels 1 to 15 of 31 ▼ pgdn") || !strings.Contains(out, "pgdn more levels") {
		t.Fatalf("the strip does not say what is in view:\n%s", out)
	}
	for _, step := range []struct {
		key  string
		top  int
		says string
	}{
		{"pgdown", 15, "levels 16 to 30 of 31 ▲ ▼"},
		{"pgup", 0, "levels 1 to 15 of 31 ▼ pgdn"},
		{"space", 15, ""}, {"ctrl+b", 0, ""}, {"ctrl+f", 15, ""}, {"home", 0, ""}, {"end", 16, ""}, {"g", 0, ""},
		{"G", 16, "levels 17 to 31 of 31 ▲ pgup"},
	} {
		press(a, flameKey(step.key))
		if fv.graph.Top() != step.top {
			t.Fatalf("%s: first level in view %d, want %d", step.key, fv.graph.Top(), step.top)
		}
		level := fv.graph.Tree().Level(fv.graph.Selected())
		if level < fv.graph.Top() || level >= fv.graph.Top()+fv.graph.Height() {
			t.Errorf("%s: the selection on level %d is out of view", step.key, level)
		}
		if out := screen(a); !strings.Contains(out, step.says) {
			t.Errorf("%s: the strip does not say %q:\n%s", step.key, step.says, out)
		}
	}
}

// / opens a line to type a regular expression into. While it is open the
// keys are text; enter searches, an empty enter clears, esc keeps what was.
func TestFlameGraphSearchLine(t *testing.T) {
	a, fv := walkFlame(t)
	typeText := func(text string) {
		for _, r := range text {
			press(a, key(string(r)))
		}
	}
	press(a, key("/"))
	if !fv.InputActive() {
		t.Fatal("/ did not open the search line")
	}
	typeText("q^[ab]$")
	if a.top() != viewModel(fv) || !strings.Contains(screen(a), "/ q^[ab]$") {
		t.Fatalf("typing did not go to the search line:\n%s", screen(a))
	}
	press(a, key("esc"))
	if fv.InputActive() || fv.graph.Searching() || a.top() != viewModel(fv) {
		t.Fatalf("esc on the search line: typing %v, searching %v, top %T", fv.InputActive(), fv.graph.Searching(), a.top())
	}

	press(a, key("/"))
	typeText("^[ab]$")
	press(a, key("enter"))
	if fv.InputActive() || fv.graph.Pattern() != "^[ab]$" {
		t.Fatalf("enter searched for %q", fv.graph.Pattern())
	}
	out := screen(a)
	for _, want := range []string{"search ^[ab]$", "190 samples · 95.0% of all", "n/N match"} {
		if !strings.Contains(out, want) {
			t.Errorf("the screen does not say %q:\n%s", want, out)
		}
	}

	press(a, key("/"))
	typeText("(")
	press(a, key("enter"))
	if fv.graph.Pattern() != "^[ab]$" || !a.statusErr || a.status != "not a regular expression: (" {
		t.Errorf("a broken pattern: searching %q, status %q (error %v)", fv.graph.Pattern(), a.status, a.statusErr)
	}

	press(a, key("/"))
	press(a, key("enter"))
	if fv.graph.Searching() {
		t.Error("an empty search line did not clear the search")
	}

	// A search that matches nothing in the thread state drawn says so.
	press(a, key("/"))
	typeText("^b$")
	press(a, key("enter"))
	press(a, key("]"))
	if out := screen(a); !strings.Contains(out, "search ^b$  no lock samples") {
		t.Errorf("a search without samples in this thread state:\n%s", out)
	}
	press(a, key("["))

	// Zoomed, the share of the zoomed frame is said too.
	for _, k := range []string{"down", "enter", "/"} {
		press(a, flameKey(k))
	}
	typeText("^a$")
	press(a, key("enter"))
	if out := screen(a); !strings.Contains(out, "130 samples · 65.0% of all · 46.2% of the zoomed frame") {
		t.Errorf("the zoomed share is not said:\n%s", out)
	}
}

// What the table hands over: its thread state and the function of its
// selected row, each only when it changed since the last time, so that what
// was set in the graph stays until the table says something new.
func TestFlameGraphTakesWhatTheTableHandsOver(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	first := catalog.Str(tv.rows[0], catalog.ProfFunction)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", "main;"+first+" 5,5", "main;other 5,5"), nil)
	if got, want := fv.graph.Pattern(), "^"+strings.NewReplacer(".", `\.`).Replace(first)+"$"; got != want {
		t.Fatalf("the selected function is searched as %q, want %q", got, want)
	}
	// The function was asked about: the cursor is on its frame, and the
	// lines about the selected frame are about it.
	if got := place(fv); !strings.HasPrefix(got, "sel=main;"+first+" ") {
		t.Errorf("the cursor is not on the function handed over: %s", got)
	}
	if out := screen(a); !strings.Contains(out, "▸ "+first) || !strings.Contains(out, "self 5 · total 5 · 50.0% of all") {
		t.Errorf("the lines about the selected frame are not about the function handed over:\n%s", out)
	}
	// A function searched for is named, not spelled as its pattern.
	if out := screen(a); !strings.Contains(out, "search "+first+"  5 samples · 50.0% of all") {
		t.Errorf("the search line does not name the function and its share:\n%s", out)
	}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	if out := screen(a); !strings.Contains(out, "search …onGenerator.writeString  5 samples · 50.0% of all") {
		t.Errorf("a narrow search line does not shorten the function and keep the numbers:\n%s", out)
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Nothing new from the table: the graph keeps its own search and state.
	press(a, key("esc"))
	press(a, key("]"))
	press(a, key("tab"))
	press(a, key("tab"))
	if fv.graph.Searching() || catalog.HotspotState(fv.scope) != "lock" {
		t.Errorf("an unchanged hand-over reset the graph: %s", place(fv))
	}

	// Another row, another search. A function the graph does not have is
	// not searched for: nothing is.
	press(a, key("tab"))
	tv.rows[1][catalog.ProfFunction] = "other"
	tv.cursor = 1
	press(a, key("tab"))
	if fv.graph.Pattern() != "^other$" {
		t.Errorf("the newly selected function is searched as %q", fv.graph.Pattern())
	}
	press(a, key("tab"))
	tv.cursor = 2
	press(a, key("tab"))
	if fv.graph.Searching() {
		t.Errorf("a function the graph lacks is searched as %q", fv.graph.Pattern())
	}
}

// The graph is of the analysis the table shows. When the table has moved on
// to another window, the graph reads again; a thread state the table changed
// is the graph's too.
func TestFlameGraphFollowsTheTablesAnalysis(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	press(a, key("tab"))

	tv.summary[catalog.ProfSumTo] = "2026-07-15T12:05:00Z"
	tv.scope.Lens = 1
	press(a, key("tab"))
	if fv.graph != nil || !fv.loading || calls[catalog.HotspotStacksAPI] != 2 {
		t.Fatalf("the table shows another analysis: graph kept %v, loading %v, calls %v", fv.graph != nil, fv.loading, calls)
	}
	if catalog.HotspotState(fv.scope) != "lock" {
		t.Errorf("the table's thread state is not the graph's: %s", catalog.HotspotState(fv.scope))
	}
}

// tab is the lens strip's key on every other table, and the brackets still
// cycle the lenses of the functions.
func TestTabStillCyclesLensesElsewhere(t *testing.T) {
	a := testApp(t, "home")
	openView(a, "traces")
	tv := a.top().(*tableView)
	if len(tv.spec.Lenses) < 2 || tv.spec.TabTarget != "" {
		t.Fatalf("traces has %d lenses and tab target %q", len(tv.spec.Lenses), tv.spec.TabTarget)
	}
	press(a, key("tab"))
	if a.top() != viewModel(tv) || tv.scope.Lens != 1 {
		t.Errorf("tab: top %T, lens %d, want the next lens", a.top(), tv.scope.Lens)
	}
	press(a, key("shift+tab"))
	if tv.scope.Lens != 0 {
		t.Errorf("shift+tab: lens %d, want the one before", tv.scope.Lens)
	}
	hints := fmt.Sprint(tv.Hints())
	if !strings.Contains(hints, "{tab lens}") || strings.Contains(hints, "flame") {
		t.Errorf("hints %s", hints)
	}

	sc := loadProfilingScenario(t, "hotspots-running")
	a, tv = openProfiling(t, sc)
	sc.answer(tv)
	if hints := fmt.Sprint(tv.Hints()); !strings.Contains(hints, "{[ ] lens}") || !strings.Contains(hints, "{tab flame graph}") {
		t.Errorf("the functions' hints %s", hints)
	}
	press(a, key("]"))
	if a.top() != viewModel(tv) || tv.scope.Lens != 1 {
		t.Errorf("]: top %T, lens %d", a.top(), tv.scope.Lens)
	}
}

// Where there is no graph to open, tab says why and stays on the table.
func TestFlameGraphRefusesToOpen(t *testing.T) {
	sc := loadProfilingScenario(t, "allocations")
	a, tv := openProfiling(t, sc)
	sc.answer(tv)
	screen(a)
	press(a, key("tab"))
	if a.top() != viewModel(tv) || a.status != "the flame graph reads thread states: [ and ] switch to one" {
		t.Errorf("on allocations: top %T, status %q", a.top(), a.status)
	}

	for _, size := range []struct{ w, h int }{{39, 30}, {100, 9}} {
		sc = loadProfilingScenario(t, "hotspots-running")
		a, tv = openProfiling(t, sc)
		sc.answer(tv)
		a.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		screen(a)
		press(a, key("tab"))
		if a.top() != viewModel(tv) || a.status != "the flame graph needs 40 columns and 10 rows: this terminal is too small for it" {
			t.Errorf("at %dx%d: top %T, status %q", size.w, size.h, a.top(), a.status)
		}
	}
	// A terminal that shrinks under an open graph says so in its place.
	a, fv := walkFlame(t)
	a.Update(tea.WindowSizeMsg{Width: 39, Height: 30})
	if out := screen(a); !strings.Contains(strings.Join(strings.Fields(out), " "), "the flame graph needs 40 columns and 10 rows: tab returns to the functions") {
		t.Errorf("too small:\n%s", out)
	}
	press(a, flameKey("down"))
	if fv.graph.Selected() != flame.Root {
		t.Error("a key moved in a graph that is not drawn")
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if out := screen(a); !strings.Contains(out, "█all") {
		t.Errorf("the graph did not come back:\n%s", out)
	}
}

// Every state without a graph says what happened, in the table's words.
func TestFlameGraphStatesSayWhatHappened(t *testing.T) {
	for name, want := range map[string][]string{
		"stacks-no-data":   {"∅ no samples in this window"},
		"stacks-forbidden": {"✗ the environment refused the analysis (403)", "storage:profiles:read"},
	} {
		a, _, fv, _ := openFlame(t)
		sc := loadProfilingScenario(t, name)
		var err error
		if sc.Error != "" {
			err = errors.New(sc.Error)
		}
		answerFlame(a, fv, sc.Records, err)
		for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}} {
			a.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			out := screen(a)
			folded := strings.Join(strings.Fields(out), " ")
			for _, text := range want {
				if !strings.Contains(folded, text) {
					t.Errorf("%s at %dx%d does not say %q:\n%s", name, size.w, size.h, text, out)
				}
			}
			if lines := strings.Count(out, "\n") + 1; lines != size.h {
				t.Errorf("%s at %dx%d is %d lines", name, size.w, size.h, lines)
			}
		}
		if fv.Busy() {
			t.Errorf("%s: still busy", name)
		}
	}

	// A thread state nothing was sampled in.
	a, fv := walkFlame(t)
	press(a, key("]"))
	press(a, key("]"))
	if out := screen(a); !strings.Contains(out, "∅ no net_io samples in this window") || !strings.Contains(out, "0 net_io samples") {
		t.Errorf("an empty thread state:\n%s", out)
	}
	if got := fmt.Sprint(fv.Hints()); got != "[{tab functions} {[ ] thread state}]" {
		t.Errorf("hints without a graph: %s", got)
	}
}

// r asks as the table's refresh does. An answer that is the analysis shown
// changes nothing and says so; a new one is drawn from its root, with the
// search searched again.
func TestFlameGraphRefresh(t *testing.T) {
	a, _, fv, calls := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	for _, k := range []string{"down", "down", "#", "enter"} {
		press(a, flameKey(k))
	}
	before := place(fv)

	press(a, key("r"))
	if calls[catalog.HotspotStacksFreshAPI] != 1 || !fv.Busy() {
		t.Fatalf("r: calls %v, busy %v", calls, fv.Busy())
	}
	if out := screen(a); !strings.Contains(out, "analysing the hotspots of shop-backend") || !strings.Contains(out, "█a") {
		t.Errorf("a refresh over a graph does not show both the wait and the graph:\n%s", out)
	}
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	if place(fv) != before || fv.Busy() || !strings.Contains(a.status, "this is still the analysis shown") {
		t.Errorf("the same analysis: %s (was %s), status %q", place(fv), before, a.status)
	}

	press(a, key("r"))
	answerFlame(a, fv, stackRecords("2026-07-15T12:03:00Z", flameWalk...), nil)
	if got, want := place(fv), `sel=all zoom=all top=0 lens=running search="^a$"`; got != want {
		t.Errorf("a new analysis: %s, want %s", got, want)
	}
	if out := screen(a); !strings.Contains(out, "12:03 UTC") {
		t.Errorf("the caption is not of the new window:\n%s", out)
	}

	// A failed refresh is not drawn over: the graph it could not replace
	// is gone, and the failure is said.
	press(a, key("r"))
	answerFlame(a, fv, nil, errors.New("the environment failed on the analysis (500)"))
	if out := screen(a); fv.graph != nil || !strings.Contains(out, "✗ the environment failed on the analysis (500)") {
		t.Errorf("a failed refresh:\n%s", out)
	}
	// And r asks again.
	press(a, key("r"))
	if calls[catalog.HotspotStacksFreshAPI] != 4 || !fv.loading {
		t.Errorf("r after a failure: calls %v, loading %v", calls, fv.loading)
	}
}

// Another timeframe is another analysis: the graph is dropped and read
// again, and an answer for the old window is not drawn.
func TestFlameGraphTimeframe(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	stale := fv.seq
	tf, _ := catalog.ParseTimeframe("30m")
	deliver(a, a.setTimeframe(tf))
	if calls[catalog.HotspotStacksAPI] != 2 || fv.scope.Timeframe.Label != "30m" || tv.scope.Timeframe.Label != "30m" {
		t.Fatalf("calls %v, graph window %s, table window %s", calls, fv.scope.Timeframe.Label, tv.scope.Timeframe.Label)
	}
	deliver(a, fv.Update(dataMsg{owner: fv, seq: stale, records: stackRecords("2026-07-15T12:00:00Z", flameWalk...)}))
	if fv.graph != nil || fv.building {
		t.Error("the answer for the window before was drawn")
	}
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameWalk...), nil)
	if fv.graph == nil {
		t.Error("the answer for the new window was not drawn")
	}
}

// The view waits only while it is on the trail: leaving ends its call, and
// coming back asks again. The tree of an answer is its view's wherever that
// view is by then.
func TestFlameGraphLeavesAndReturns(t *testing.T) {
	a, tv, fv, calls := openFlame(t)
	cancelled := false
	inner := fv.cancel
	fv.cancel = func() { cancelled = true; inner() }
	openView(a, "problems") // a command-bar jump: the trail is replaced
	if !cancelled || fv.loading || !fv.parked {
		t.Fatalf("leaving: cancelled %v, loading %v, parked %v", cancelled, fv.loading, fv.parked)
	}
	deliver(a, a.setTimeframe(a.tf)) // reaches the parked view, which must not fetch
	if calls[catalog.HotspotStacksAPI] != 1 {
		t.Fatalf("a parked graph fetched: %v", calls)
	}
	press(a, key("-"))
	if a.top() != viewModel(fv) || !fv.loading || fv.parked || calls[catalog.HotspotStacksAPI] != 2 {
		t.Fatalf("returning: top %T, loading %v, calls %v", a.top(), fv.loading, calls)
	}

	// The answer arrives; before its tree does, the reader is back on the
	// functions. The tree still reaches the graph.
	build := fv.Update(dataMsg{owner: fv, seq: fv.seq, records: stackRecords("2026-07-15T12:00:00Z", flameWalk...)})
	if !fv.building || !fv.Busy() {
		t.Fatal("the tree is not being built")
	}
	press(a, key("tab"))
	if a.top() != viewModel(tv) {
		t.Fatalf("tab left %T on top", a.top())
	}
	deliver(a, build)
	if fv.graph == nil || fv.Busy() {
		t.Error("the tree did not reach the covered graph")
	}
}

// y copies the selected function, c the command that prints the stacks, and
// the process is the entity the graph is about.
func TestFlameGraphExits(t *testing.T) {
	a, fv := walkFlame(t)
	if _, _, ok := fv.YankText(); ok {
		t.Error("the root has a function to copy")
	}
	press(a, flameKey("down"))
	if text, label, ok := fv.YankText(); !ok || text != "main" || label != "function" {
		t.Errorf("y copies %q (%s, %v)", text, label, ok)
	}
	want := "dtctl exec profiling hotspots PROCESS-000000000000A001 --view stacks --show-waiting --limit 0 --default-timeframe-start 'now()-2h'"
	if got := fv.Echo(); got != want {
		t.Errorf("c echoes %s", got)
	}
	rec, entity := fv.Selection()
	if entity == nil || entity.ID != "PROCESS-000000000000A001" || rec[catalog.ProfFunction] != "main" {
		t.Errorf("selection %v of %v", rec, entity)
	}
	if fv.AutoRefreshes() {
		t.Error("the graph would start an analysis on a timer")
	}
}

// At every width and height, in every state, a rendered line fits the
// terminal and the screen has as many lines as the terminal.
func TestFlameGraphFitsEveryTerminal(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, loadProfilingScenario(t, "stacks").Records, nil)
	odd := stackRecords("2026-07-15T12:00:00Z", "日本語のメソッド;e\u0301te\u0301;a\x1b[31mb\nc 40,0", "日本語のメソッド;👨\u200d👩\u200d👧.run 30,0", "ab\u2764\ufe0f;x 30,0")
	check := func(what string) {
		t.Helper()
		for _, size := range []struct{ w, h int }{{40, 10}, {41, 11}, {57, 13}, {80, 24}, {120, 40}, {200, 60}, {400, 100}, {20, 5}} {
			a.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			out := a.View()
			lines := strings.Split(out, "\n")
			if len(lines) != size.h {
				t.Errorf("%s at %dx%d: %d lines", what, size.w, size.h, len(lines))
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > size.w {
					t.Errorf("%s at %dx%d: line %d is %d cells wide: %q", what, size.w, size.h, i, w, line)
				}
			}
		}
	}
	check("the recorded analysis")
	for _, k := range []string{"esc", "down", "down", "down", "enter", "#"} {
		press(a, flameKey(k))
	}
	check("zoomed and searched")
	press(a, key("/"))
	check("typing a search")
	press(a, key("esc"))
	press(a, key("r"))
	check("refreshing")
	answerFlame(a, fv, odd, nil)
	check("odd labels")
	if out := screen(a); strings.Contains(out, "\x1b") {
		t.Error("a control sequence of a label reached the terminal")
	}
}

// Without colour the first cell of a frame says what it is; with colour the
// frames are painted too, and no style is left open at the end of a level.
func TestFlameGraphMarksFrames(t *testing.T) {
	a, fv := walkFlame(t)
	for _, k := range []string{"down", "down"} {
		press(a, flameKey(k))
	}
	level := func(out string, n int) string { return strings.Split(out, "\n")[4+n] }
	out := screen(a)
	if got := level(out, 2); !strings.HasPrefix(got, "█a") || !strings.Contains(got, "▏b") || !strings.Contains(got, "▍a") {
		t.Errorf("the selected frame, a frame and another call of the selected function:\n%s", got)
	}
	press(a, key("#"))
	if got := level(screen(a), 2); !strings.HasPrefix(got, "█a") || !strings.Contains(got, "▌a") || strings.Contains(got, "▍") {
		t.Errorf("a search hit:\n%s", got)
	}
	// Callees narrower than a cell share a marker, and their caller says
	// how many they are.
	press(a, key("r"))
	answerFlame(a, fv, stackRecords("2026-07-15T12:01:00Z", "main;a 500,0", "main;tiny 1,0", "main;small 2,0", "main 497,0"), nil)
	press(a, flameKey("down"))
	out = screen(a)
	if !strings.Contains(level(out, 2), "·") || !strings.Contains(out, "2 callees too narrow to draw (3 samples): enter zooms in") {
		t.Errorf("callees too narrow to draw are not marked:\n%s", out)
	}

	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for _, line := range strings.Split(a.View(), "\n")[4:7] {
		if !strings.Contains(line, "\x1b[") || !strings.HasSuffix(strings.TrimRight(line, " "), "\x1b[0m") {
			t.Errorf("a level is not painted, or leaves its style open: %q", line)
		}
	}
}

// The main states of the graph at the canonical sizes, drawn from the
// recorded analysis of the synthetic web application.
func TestSnapshotFlameGraph(t *testing.T) {
	pinSnapshotClock(t)
	open := func(t *testing.T) *app {
		a, _, fv, _ := openFlame(t)
		answerFlame(a, fv, loadProfilingScenario(t, "stacks").Records, nil)
		press(a, key("esc")) // the function the table handed over
		return a
	}
	t.Run("root", func(t *testing.T) {
		assertSnapshots(t, "profiling-flame-root", open(t))
	})
	t.Run("zoomed", func(t *testing.T) {
		a := open(t)
		for _, k := range []string{"down", "down", "down", "down", "down", "down", "enter", "down"} {
			press(a, flameKey(k))
		}
		assertSnapshots(t, "profiling-flame-zoomed", a)
	})
	t.Run("searched", func(t *testing.T) {
		a := open(t)
		press(a, key("/"))
		for _, r := range `jackson\.` {
			press(a, key(string(r)))
		}
		press(a, key("enter"))
		press(a, key("n"))
		assertSnapshots(t, "profiling-flame-searched", a)
	})
}

// Every key the help overlay lists for the graph is one the view acts on,
// and the overlay lists them.
func TestFlameGraphHelpListsItsKeys(t *testing.T) {
	a, fv := walkFlame(t)
	press(a, key("?"))
	out := strings.Join(strings.Fields(screen(a)), " ")
	for _, h := range flameHelp {
		if !strings.Contains(out, h.Desc) {
			t.Errorf("the help overlay does not say %q", h.Desc)
		}
	}
	press(a, key("esc"))
	// The keys of the view itself: each changes the graph or says something.
	for _, k := range []string{"down", "j", "right", "left", "enter", "backspace", "#", "n", "N", "]", "[", "k", "up", "G", "g", "pgdown", "pgup", "ctrl+f", "ctrl+b", "space"} {
		fv.graph.Reset()
		press(a, flameKey("down"))
		press(a, flameKey("down"))
		before := place(fv)
		a.status = ""
		press(a, flameKey(k))
		acted := place(fv) != before || a.status != ""
		paging := strings.HasPrefix(k, "pg") || strings.HasPrefix(k, "ctrl") || k == "space" || k == "g" || k == "G" || k == "left"
		if !acted && !paging {
			t.Errorf("%s did nothing", k)
		}
		if a.top() != viewModel(fv) {
			t.Fatalf("%s left the graph", k)
		}
	}
}

// BenchmarkFlameGraphKey is what one key press costs on a profile larger
// than any seen (50,000 stacks, each 100 frames deep, about half a million
// frames) at 250 columns in full colour: the move, and the screen drawn again.
// BenchmarkFlameGraphZoom is the same for a key that lays the graph out anew.
func benchFlameView(b *testing.B) *flameView {
	b.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	b.Cleanup(func() { lipgloss.SetColorProfile(old) })
	builder := flame.NewBuilder(len(catalog.HotspotStates()))
	frames := make([]string, 100)
	for s := range 50_000 {
		for d := range frames {
			switch {
			case d < 60:
				frames[d] = fmt.Sprintf("com.example.app.trunk%d.Handler.step%d", s%64, d)
			case d < 90:
				frames[d] = fmt.Sprintf("com.example.app.branch%d.Handler.step%d", s%8, d)
			default:
				frames[d] = fmt.Sprintf("com.example.app.leaf%d.Handler.step%d", s%997, d+s%7)
			}
		}
		builder.Add(frames, []int64{int64(1 + s%13), int64(s % 3), 0, int64(s % 2), 1})
	}
	spec := catalog.Lookup("hotspots")
	fv := &flameView{spec: spec, input: newTextInput(), width: 250, height: 56, building: true}
	fv.built(flameBuiltMsg{view: fv, tree: builder.Tree(), summary: map[string]any{}})
	if fv.graph.Tree().Len() < 400_000 {
		b.Fatalf("the profile has %d frames", fv.graph.Tree().Len())
	}
	return fv
}

func BenchmarkFlameGraphKey(b *testing.B) {
	fv := benchFlameView(b)
	keys := []tea.KeyMsg{flameKey("down"), flameKey("right"), flameKey("down"), flameKey("up"), flameKey("left"), flameKey("up")}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		fv.handleKey(keys[i%len(keys)])
		i++
		if out := fv.View(250, 56); len(out) < 1000 {
			b.Fatal("nothing was drawn")
		}
	}
}

func BenchmarkFlameGraphZoom(b *testing.B) {
	fv := benchFlameView(b)
	for range 30 {
		fv.handleKey(flameKey("down"))
	}
	keys := []tea.KeyMsg{flameKey("enter"), flameKey("backspace"), flameKey("]"), flameKey("[")}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		fv.handleKey(keys[i%len(keys)])
		i++
		if out := fv.View(250, 56); len(out) < 1000 {
			b.Fatal("nothing was drawn")
		}
	}
}
