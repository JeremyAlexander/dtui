package tui

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// words is the screen as one line of words: a line of it may wrap.
func words(a *app) string { return strings.Join(strings.Fields(screen(a)), " ") }

// fullFlame is the walk graph with room for three frames. The heaviest stack
// is main;a, which dtctl does not put first: the graph keeps the root, main
// and a, and folds the rest of five stacks.
func fullFlame(t *testing.T) (*app, *flameView) {
	t.Helper()
	flameMaxFrames = 3
	t.Cleanup(func() { flameMaxFrames = flame.MaxFrames })
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", append(flameWalk[:len(flameWalk):len(flameWalk)], "main;a 150,50")...), nil)
	if fv.graph == nil {
		t.Fatal("the stacks did not become a graph")
	}
	fv.graph.ClearSearch() // the function the table handed over
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	return a, fv
}

// A caller of the zoomed frame has no share of it, and the callees the zoom
// leaves out are not too narrow to draw: neither is said of it.
func TestFlameGraphSaysNoShareOfTheZoomForItsCaller(t *testing.T) {
	a, fv := walkFlame(t)
	for _, k := range []string{"down", "down", "enter"} {
		press(a, flameKey(k))
	}
	if out := words(a); !strings.Contains(out, "self 0 · total 60 · 30.0% of all · 100.0% of the zoomed frame") {
		t.Fatalf("on the zoomed frame:\n%s", screen(a))
	}
	press(a, flameKey("up"))
	out := words(a)
	if !strings.Contains(out, "self 0 · total 130 · 65.0% of all") || strings.Contains(out, "of the zoomed frame") || strings.Contains(out, "too narrow to draw") {
		t.Errorf("on the caller of the zoomed frame (zoom %v):\n%s", fv.graph.Tree().Path(fv.graph.Zoom()), screen(a))
	}
	// A search's share of the zoomed frame is still said, whatever is selected.
	press(a, key("/"))
	for _, r := range "^x$" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))
	if out := words(a); !strings.Contains(out, "40 samples · 20.0% of all · 66.7% of the zoomed frame") {
		t.Errorf("the search's share of the zoomed frame:\n%s", screen(a))
	}
}

// A remainder is not a function anywhere a function is acted on or said.
func TestFlameGraphTreatsARemainderAsNoFunction(t *testing.T) {
	a, fv := fullFlame(t)
	a.ds.source = newSourceLookup(t.TempDir(), "vscode") // not walked yet: O is offered on what may have a file
	press(a, flameKey("down"))
	if !strings.Contains(screen(a), "O open source") {
		t.Fatal("the fixture: O is to be offered on main before the walk")
	}
	out := words(a)
	if !strings.Contains(out, "paths the one call path kept reaches this function: the graph is full, and others may be among the frames not kept") {
		t.Errorf("the paths line of a function in a full graph:\n%s", screen(a))
	}
	if !strings.Contains(out, "25.0% in frames that were not kept") {
		t.Errorf("the callees of main do not say what was folded:\n%s", screen(a))
	}
	press(a, flameKey("down"))
	press(a, flameKey("right"))
	tree, sel := fv.graph.Tree(), fv.graph.Selected()
	if !tree.IsRest(sel) {
		t.Fatalf("the fixture: selected %v", tree.Path(sel))
	}
	out = words(a)
	if !strings.Contains(out, "folded 70 · total 70") || strings.Contains(out, "self 70") {
		t.Errorf("the numbers of a remainder:\n%s", screen(a))
	}
	if strings.Contains(out, "thread state leaves fewer") || !strings.Contains(out, "a narrower timeframe leaves fewer") {
		t.Errorf("the advice of a remainder (one graph is kept for every thread state):\n%s", screen(a))
	}
	if strings.Contains(screen(a), "O open source") {
		t.Errorf("the footer offers O on a remainder:\n%s", screen(a))
	}
	if text, _, ok := fv.YankText(); ok {
		t.Errorf("y copies %q as a function", text)
	}
	press(a, key("enter"))
	if fv.graph.Zoomed() || !strings.Contains(a.status, "not a frame to zoom into") {
		t.Errorf("enter on a remainder: zoomed %v, status %q", fv.graph.Zoomed(), a.status)
	}
}

// A remainder is drawn as what it is, not in a function's colour.
func TestFlameGraphPaintsARemainderLikeNoFunction(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
	_, fv := fullFlame(t)
	g := fv.graph
	p := newFlamePaint(g)
	var b strings.Builder
	p.row(&b, g.Layout().Rows[2], 2)
	row := b.String()
	if p.more.on == "" || !strings.Contains(row, p.more.on+flameEdge+"(frames not kept)") {
		t.Errorf("the remainder is not drawn in the marker's style: %q", row)
	}
	for _, ink := range p.ramp {
		if strings.Contains(row, ink.on+flameEdge+"(frames") {
			t.Errorf("the remainder is drawn in a colour of the ramp: %q", row)
		}
	}
}

// The root is nobody's caller: a function that starts its stacks says so.
func TestFlameGraphListsNoCallerNamedAfterTheRoot(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", "main;work 30", "work 15", "x;main 5"), nil)
	fv.graph.ClearSearch()
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	press(a, flameKey("down")) // main, the heaviest
	out := words(a)
	if strings.Contains(out, "% all") || !strings.Contains(out, "callers 14.3% x") || !strings.Contains(out, "85.7% no caller: it starts the stack") {
		t.Errorf("the callers of main:\n%s", screen(a))
	}
}

// The notice that the graph is full is its one disclosure: it leads the
// caption, where a narrow screen does not cut it.
func TestFlameGraphFullNoticeIsOnANarrowScreen(t *testing.T) {
	a, fv := fullFlame(t)
	for _, width := range []int{40, 60, 80, 100, 160} {
		a.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		if a.top() != viewModel(fv) || !fv.drawable() {
			t.Fatalf("at %d columns the graph is not drawn", width)
		}
		if out := words(a); !strings.Contains(out, "the graph is full") {
			t.Errorf("at %d columns the screen does not say the graph is full:\n%s", width, screen(a))
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if want := "the graph is full: it keeps 3 frames, the deeper frames of 5 stacks are folded into (frames not kept) · last 2h"; !strings.Contains(words(a), want) {
		t.Errorf("the caption does not start with %q:\n%s", want, screen(a))
	}
}

// A search that marks nothing says so; it is not told to zoom in.
func TestFlameGraphSaysWhenASearchMatchesNothing(t *testing.T) {
	a, fv := walkFlame(t)
	press(a, key("/"))
	for _, r := range "zzz" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))
	press(a, key("n"))
	if !fv.graph.Searching() || strings.Contains(a.status, "zoom in") || !strings.Contains(a.status, "nothing matches") {
		t.Errorf("n on a search without a match: %q", a.status)
	}
}

// An error is drawn inside the body, however long it is, and says that it
// was cut.
func TestFlameGraphErrorStaysInsideTheBody(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, nil, errors.New(strings.Repeat("the environment failed on the analysis; ", 34)))
	for _, size := range []struct{ w, h int }{{40, 8}, {40, 3}, {120, 4}, {120, 30}} {
		lines := strings.Split(fv.View(size.w, size.h), "\n")
		last := strings.TrimRight(ansi.Strip(lines[len(lines)-1]), " ")
		if len(lines) > size.h {
			t.Errorf("at %dx%d the error is %d lines", size.w, size.h, len(lines))
		}
		if cut := len(lines) == size.h && size.h < 30; cut && !strings.HasSuffix(last, "…") {
			t.Errorf("at %dx%d the error is cut without a mark: %q", size.w, size.h, last)
		}
		if size.h == 30 && !strings.HasSuffix(last, "analysis;") {
			t.Errorf("with room the error is not whole: %q", last)
		}
	}
}

// A count beyond what an integer holds is the largest there is, and what is
// no count is none: neither turns negative on the way in.
func TestFlameTreeTakesNoCountOutOfRange(t *testing.T) {
	states := catalog.HotspotStates()
	rows := []map[string]any{
		{catalog.ProfStack: "a", states[0]: 1e300, states[1]: math.Inf(1)},
		{catalog.ProfStack: "b", states[0]: math.NaN(), states[1]: -5.0},
		{catalog.ProfStack: "c", states[0]: float64(math.MaxInt64), states[1]: 7.9},
		{catalog.ProfStack: "d", states[0]: "12", states[1]: "x"},
	}
	tree := flameTree(rows, len(states))
	got := map[string][2]int64{}
	for n := flame.NodeID(1); int(n) < tree.Len(); n++ {
		got[tree.Label(n)] = [2]int64{tree.Self(n, 0), tree.Self(n, 1)}
	}
	want := map[string][2]int64{"a": {math.MaxInt64, math.MaxInt64}, "c": {math.MaxInt64, 7}, "d": {12, 0}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if tree.Total(flame.Root, 0) != math.MaxInt64 || tree.Total(flame.Root, 1) != math.MaxInt64 {
		t.Errorf("the root: %d, %d", tree.Total(flame.Root, 0), tree.Total(flame.Root, 1))
	}
}

// The heaviest few of many are found in one pass: a profile whose stacks
// were cut at a depth has thousands of frames under the root, and the root's
// details are drawn on every paint. The bound is a count of comparisons, not
// a time.
func TestHeaviestSharesInOnePass(t *testing.T) {
	const n = 50_000
	compared := 0
	next := func(i int) flame.Share {
		return flame.Share{Label: fmt.Sprintf("f%05d", i), Weight: int64((i * 7919) % 1000)}
	}
	h := heaviest{keep: flameTopShares, heavier: func(a, b flame.Share) bool { compared++; return a.Weight > b.Weight }}
	for i := range n {
		h.add(next(i))
	}
	if compared > flameTopShares*n {
		t.Errorf("%d comparisons for %d shares: more than %d each", compared, n, flameTopShares)
	}
	// The heaviest first, and of equals the one that came first.
	want := []flame.Share{next(321), next(1321), next(2321)}
	if fmt.Sprint(h.top) != fmt.Sprint(want) || h.seen != n {
		t.Errorf("top %v of %d, want %v", h.top, h.seen, want)
	}
	for i := range 3 {
		if h.top[i].Weight != 999 {
			t.Fatalf("the fixture: %v", h.top)
		}
	}
}

// The root's details list what the stacks start in, the heaviest first and
// equals in the order of their names, and count the rest.
func TestFlameGraphRootDetails(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", "d 10", "b 30", "a 10", "c 30", "e 5", "f 0,4"), nil)
	fv.graph.ClearSearch()
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if out := words(a); !strings.Contains(out, "starts in 35.3% b 35.3% c 11.8% a and 2 more") {
		t.Errorf("the root's details:\n%s", screen(a))
	}
}

// A full graph keeps the stacks that weigh most over all thread states.
// dtctl orders them by running samples alone: kept in that order, the graph
// of the waiting threads would lose its heaviest stack to the running tail.
func TestFlameTreeKeepsTheHeaviestStacksOfEveryState(t *testing.T) {
	flameMaxFrames = 3
	t.Cleanup(func() { flameMaxFrames = flame.MaxFrames })
	rows := stackRecords("2026-07-15T12:00:00Z", "run;a 10,0", "run;b 9,0", "idle;park 0,100", "run;c 9,0")[1:]
	tree := flameTree(rows, len(catalog.HotspotStates()))
	var kept []string
	for n := flame.NodeID(1); int(n) < tree.Len(); n++ {
		if !tree.IsRest(n) {
			kept = append(kept, strings.Join(tree.Path(n), ";"))
		}
	}
	if got := strings.Join(kept, " "); got != "idle idle;park" {
		t.Errorf("kept %q, want the stack with 100 samples", got)
	}
	if tree.Total(flame.Root, 0) != 28 || tree.Total(flame.Root, 1) != 100 || tree.Folded() != 3 {
		t.Errorf("totals %d and %d, folded %d", tree.Total(flame.Root, 0), tree.Total(flame.Root, 1), tree.Folded())
	}
	// Equal stacks stay in the order they came in, and sums do not wrap.
	big := float64(math.MaxInt64)
	states := catalog.HotspotStates()
	rows = []map[string]any{
		{catalog.ProfStack: "small;x", states[0]: 5.0},
		{catalog.ProfStack: "huge;y", states[0]: big, states[1]: big},
	}
	tree = flameTree(rows, len(states))
	if got := tree.Path(1); len(got) != 1 || got[0] != "huge" {
		t.Errorf("a stack whose sum is beyond an integer is not the heaviest: first %v", got)
	}
}

// The marks of the graph are told apart by their colours at every colour
// depth and on both backgrounds: a search hit is not "the same function",
// and neither is the cursor or a caller of the zoom.
func TestFlameMarksDifferAtEveryColourDepth(t *testing.T) {
	oldProfile, oldDark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldProfile)
		lipgloss.SetHasDarkBackground(oldDark)
	})
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.ANSI256, termenv.TrueColor} {
		for _, dark := range []bool{true, false} {
			lipgloss.SetColorProfile(profile)
			lipgloss.SetHasDarkBackground(dark)
			marks := map[string]string{
				"the cursor": theme.FlameSelected.Render("x"), "a search hit": theme.FlameHit.Render("x"),
				"the same function": theme.FlameSame.Render("x"), "a caller of the zoom": theme.FlameCaller.Render("x"),
			}
			seen := map[string]string{}
			for name, drawn := range marks {
				if other, ok := seen[drawn]; ok {
					t.Errorf("profile %d, dark %v: %s and %s are drawn alike (%q)", profile, dark, name, other, drawn)
				}
				seen[drawn] = name
			}
		}
	}
}
