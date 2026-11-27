package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/flame"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// flameTiny is an analysis as a quiet process gives one: a handful of
// stacks, eleven running samples, and names longer than most terminals,
// with the characters a terminal counts in its own way.
var flameTiny = func() []string {
	long := "org.springframework.cglib.proxy.MethodProxy$$EnhancerBySpringCGLIB$$" + strings.Repeat("a1b2c3d4", 12) + ".invokeSuper"
	noDots := strings.Repeat("lambda$handleRequestWithAVeryLongGeneratedName$", 4) + "0"
	wide := "com.example.注文.受注サービス." + strings.Repeat("処理", 40)
	odd := "app/src/été/👩‍👩‍👧‍👦/handler.js:résoudre" + strings.Repeat("́x", 30)
	return []string{
		"main;" + long + ";" + noDots + " 4,1",
		"main;" + long + ";" + wide + " 3",
		"main;" + odd + " 2,0,1",
		"main;" + long + " 1",
		noDots + ";" + wide + ";" + odd + " 1",
	}
}()

// No line of the screen is wider than the terminal, at any width, and the
// root of the graph spans all of it: a flame graph never needs panning.
//
// Every size starts at the root of the graph and every step is looked at on
// the graph: a key that left it (esc at the root, or any key on a screen too
// small to draw one) would have the rest of the sweep measure the table.
func TestFlameGraphFitsEveryWidth(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameTiny...), nil)
	if fv.graph == nil {
		t.Fatal("the stacks did not become a graph")
	}
	g := fv.graph
	steps := []struct {
		key   string
		holds func() bool // what the key is to have done
		what  string
	}{
		{"", func() bool { return g.Selected() == flame.Root && !g.Zoomed() && !g.Searching() }, "at the root"},
		{"down", func() bool { return g.Selected() != flame.Root }, "off the root"},
		{"down", nil, ""},
		{"down", nil, ""},
		{"enter", g.Zoomed, "zoomed"},
		{"right", g.Zoomed, "zoomed"},
		{"#", g.Searching, "searching"},
		{"]", func() bool { return g.Lens() == 1 }, "in the second thread state"},
		{"[", func() bool { return g.Lens() == 0 }, "in the first thread state"},
		{"esc", func() bool { return !g.Searching() && g.Zoomed() }, "zoomed without a search"},
		{"esc", func() bool { return !g.Zoomed() }, "not zoomed"},
		{"/", func() bool { return fv.typing }, "typing a search"},
	}
	drawn, roots := 0, 0
	for _, previewOff := range []bool{false, true} {
		a.ds.previewOff = previewOff
		for width := 20; width <= 300; width++ {
			heights := []int{24}
			if width%20 == 3 {
				heights = []int{12, 24, 45}
			}
			for _, height := range heights {
				a.Update(tea.WindowSizeMsg{Width: width, Height: height})
				g.Reset()
				for _, step := range steps {
					at := fmt.Sprintf("%dx%d after %q (preview off: %v)", width, height, step.key, previewOff)
					if step.key != "" {
						if !fv.drawable() {
							break // a screen too small for a graph claims no key
						}
						press(a, key(step.key))
					}
					if a.top() != viewModel(fv) || fv.graph != g {
						t.Fatalf("%s: the graph is no longer what is shown", at)
					}
					if step.holds != nil && fv.drawable() && !step.holds() {
						t.Fatalf("%s: the graph is not %s", at, step.what)
					}
					out := screen(a)
					lines := strings.Split(out, "\n")
					if len(lines) != height {
						t.Fatalf("%s: %d lines", at, len(lines))
					}
					for i, line := range lines {
						if w := lipgloss.Width(line); w > width {
							t.Fatalf("%s: line %d is %d cells wide:\n%s", at, i, w, out)
						}
					}
					if fv.drawable() {
						drawn++
						root := g.Layout().Rows[0]
						if len(root) != 1 || root[0].X != 0 || root[0].W != g.Layout().Width || root[0].W != fv.graphWidth() || root[0].W != width {
							t.Fatalf("%s: the root is %+v in a graph of %d cells", at, root, fv.graphWidth())
						}
						if g.Zoom() == flame.Root {
							roots++
						}
					}
				}
				if fv.typing {
					press(a, key("esc")) // leave the search line
				}
			}
		}
	}
	// 261 widths that draw a graph, 13 of them at three heights, twice.
	if sizes := 2 * (261 + 2*13); drawn != sizes*len(steps) || roots < sizes*4 {
		t.Errorf("the graph was looked at %d times (%d of them unzoomed), want %d", drawn, roots, sizes*len(steps))
	}
}

// flameDeep is shaped like the analysis of a quiet service: nine running
// samples in a few stacks 78 frames deep, with names over 100 characters.
var flameDeep = func() []string {
	name := func(i int) string {
		return fmt.Sprintf("io.example.tracing.internal.reporters.%s.RemoteControlledSamplerWithAVeryLongGeneratedSuffix$Inner%d.context",
			strings.Repeat("deeplynested.", 4), i)
	}
	stack := func(depth int, leaf string) string {
		frames := make([]string, 0, depth)
		for i := range depth - 1 {
			frames = append(frames, name(i))
		}
		return strings.Join(append(frames, leaf), ";")
	}
	return []string{
		stack(77, "io.example.tracing.internal.Span.context") + " 3",
		stack(40, "io.example.export.Exporter.buildLogs") + " 2,1",
		stack(77, "io.example.tracing.internal.Span.finish") + " 2",
		stack(12, "java.lang.Thread.sleep") + " 2,0,0,0,7",
	}
}()

// The screen is exactly as tall as the terminal and no line is wider than
// it, in every state of the graph: a taller one scrolls the header away.
func TestFlameGraphFillsTheTerminalExactly(t *testing.T) {
	check := func(t *testing.T, a *app, width, height int, at string) {
		t.Helper()
		out := a.View()
		lines := strings.Split(out, "\n")
		if len(lines) != height {
			t.Fatalf("%dx%d %s: %d lines", width, height, at, len(lines))
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("%dx%d %s: line %d is %d cells wide", width, height, at, i, w)
			}
		}
	}
	for _, carried := range []bool{true, false} {
		a, tv, fv, _ := openFlame(t)
		answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameDeep...), nil)
		if fv.graph == nil {
			t.Fatal("the stacks did not become a graph")
		}
		if carried {
			// The search the table hands over for its selected row.
			press(a, key("tab"))
			tv.scope.Arg = "io.example.tracing.internal.Span.context"
			press(a, key("tab"))
			if !fv.graph.Searching() {
				t.Fatal("the function of the table was not carried over as a search")
			}
		} else {
			fv.graph.ClearSearch()
		}
		for _, width := range []int{40, 60, 80, 143, 200} {
			for height := 10; height <= 80; height++ {
				if width != 143 && height%7 != 3 {
					continue // every height at the width it was seen at
				}
				a.Update(tea.WindowSizeMsg{Width: width, Height: height})
				check(t, a, width, height, "as opened")
				for _, step := range []string{"down", "pgdown", "G", "enter", "#", "]", "[", "/", "esc", "g", "esc", "P", "P"} {
					press(a, key(step))
					check(t, a, width, height, fmt.Sprintf("after %q (carried search: %v)", step, carried))
				}
				for fv.graph.Zoomed() || fv.typing {
					press(a, key("esc"))
				}
			}
		}
	}
}

// The footer of the flame graph is never cut: it drops whole hints, the
// global ones first, and keeps the keys a reader cannot do without. Zooming
// is how a frame too narrow to read is reached, so its key is there at
// every width a graph is drawn in.
func TestFlameGraphFooterKeepsItsKeys(t *testing.T) {
	a, fv := walkFlame(t)
	for width := flameMinWidth; width <= 300; width++ {
		a.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		lines := strings.Split(screen(a), "\n")
		footer := lines[len(lines)-2]
		if strings.Contains(footer, "…") {
			t.Fatalf("at %d columns the footer is cut: %q", width, footer)
		}
		for _, want := range []string{"enter zoom", "? help"} {
			if !strings.Contains(footer, want) {
				t.Fatalf("at %d columns the footer lost %q: %q", width, want, footer)
			}
		}
		if width >= 60 {
			for _, want := range []string{"↑↓←→ move", "tab functions", "/ search"} {
				if !strings.Contains(footer, want) {
					t.Fatalf("at %d columns the footer lost %q: %q", width, want, footer)
				}
			}
		}
	}
	_ = fv
}

// Only a view that asks for it has its footer fitted: every other view's
// footer is the line it was, cut at the terminal's edge.
func TestOtherFootersAreAsTheyWere(t *testing.T) {
	a := testApp(t, "services")
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	lines := strings.Split(screen(a), "\n")
	footer := strings.TrimRight(lines[len(lines)-2], " ")
	if !strings.HasSuffix(footer, "…") || lipgloss.Width(footer) != 60 {
		t.Errorf("a table's footer at 60 columns: %q", footer)
	}
	own, global := []keyHint{{"a", "one"}, {"b", "two"}}, []keyHint{{":", "views"}, {"S", "segments"}, {"?", "help"}, {"q", "quit"}}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{200, "a one  b two  : views  S segments  ? help  q quit"},
		{40, "a one  b two  : views  ? help  q quit"},
		{32, "a one  b two  : views  ? help"},
		{22, "a one  b two  ? help"},
		{14, "a one  ? help"},
		{6, "? help"},
		{2, "? help"},
	} {
		o, g := fitHints(own, global, tc.width)
		if got := ansi.Strip(hintLine(append(o, g...))); got != tc.want {
			t.Errorf("fitHints at %d: %q, want %q", tc.width, got, tc.want)
		}
	}
	if len(own) != 2 || len(global) != 4 {
		t.Error("fitHints changed what it was given")
	}
}

// The arrow keys as a terminal sends them, through the whole app from the
// functions table: the cursor starts on the function the table's row is
// about, each key moves it, and the lines under the graph follow it. The
// cursor's mark is on one frame only, a search hit or not.
func TestArrowKeysMoveTheCursorFromTheTable(t *testing.T) {
	a, tv, fv, _ := openFlame(t)
	carried := "io.example.tracing.internal.Span.context"
	answerFlame(a, fv, stackRecords("2026-07-15T12:00:00Z", flameDeep...), nil)
	press(a, tea.KeyMsg{Type: tea.KeyTab})
	if a.top() != viewModel(tv) {
		t.Fatalf("tab on the graph left %T on top", a.top())
	}
	tv.scope.Arg = carried
	a.Update(tea.WindowSizeMsg{Width: 143, Height: 39})
	press(a, tea.KeyMsg{Type: tea.KeyTab})

	g := fv.graph
	if got := g.Tree().Label(g.Selected()); got != carried {
		t.Fatalf("the cursor starts on %q, want the function the table carried over", got)
	}
	seen := map[flame.NodeID]bool{g.Selected(): true}
	for _, k := range []tea.KeyType{tea.KeyUp, tea.KeyDown, tea.KeyRight, tea.KeyLeft, tea.KeyUp, tea.KeyUp, tea.KeyDown} {
		before := g.Selected()
		press(a, tea.KeyMsg{Type: k})
		if g.Selected() == before {
			t.Fatalf("%v did not move the cursor from %v", k, g.Tree().Path(before)[g.Tree().Level(before)-1])
		}
		seen[g.Selected()] = true
		out := screen(a)
		if n := strings.Count(out, flameEdgeSel); n != 1 {
			t.Fatalf("after %v the cursor's mark is on %d frames:\n%s", k, n, out)
		}
		name := g.Tree().Label(g.Selected())
		if !strings.Contains(strings.Join(strings.Fields(out), ""), "▸"+name) {
			t.Fatalf("after %v the lines under the graph are not about %s:\n%s", k, name, out)
		}
	}
	if len(seen) < 4 {
		t.Errorf("the keys visited %d frames", len(seen))
	}
}

// The cursor is drawn in a style no other frame has, a search hit included,
// and keeps it on a hit.
func TestTheCursorIsUnlikeASearchHit(t *testing.T) {
	sel, hit := theme.FlameSelected, theme.FlameHit
	if sel.GetBackground() == hit.GetBackground() || !sel.GetBold() || hit.GetBold() {
		t.Error("the cursor and a search hit are styled alike")
	}
	for _, step := range theme.Flame {
		if step.GetBackground() == sel.GetBackground() || step.GetBackground() == hit.GetBackground() {
			t.Error("a colour of the ramp is the cursor's or a hit's")
		}
	}
	a, fv := walkFlame(t)
	for _, k := range []string{"/", "^", "a", "$", "enter", "n"} {
		press(a, key(k))
	}
	if !fv.graph.Matches(fv.graph.Selected()) {
		t.Fatal("n did not move to a match")
	}
	if out := screen(a); !strings.Contains(out, flameEdgeSel+"a") || !strings.Contains(out, flameEdgeHit+"a") {
		t.Errorf("a selected hit and another hit:\n%s", out)
	}
}

// A trail of crumbs longer than the terminal keeps its end, where the reader
// is; one that fits is as it was.
func TestBreadcrumbKeepsItsEnd(t *testing.T) {
	a, tv, _, _ := openFlame(t)
	crumbs := func() string { return strings.TrimRight(strings.Split(screen(a), "\n")[1], " ─") }
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := crumbs(); got != " hotspots (shop-backend) › flame·running" {
		t.Errorf("a trail that fits: %q", got)
	}
	tv.scope.Entity.Name = "SpringBoot com.example.shop.backend.ShopBackendApplication shop-backend-7d9f8b6c5d-x2k4q (shop-backend)"
	if got := crumbs(); got != " … › flame·running" {
		t.Errorf("a trail too long for 80 columns: %q", got)
	}
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	if got := crumbs(); !strings.HasPrefix(got, " hotspots (SpringBoot ") || !strings.HasSuffix(got, "› flame·running") {
		t.Errorf("the same trail where it fits: %q", got)
	}
}

// A label with a keycap, a joined emoji or a lone combining mark is measured
// by the terminal's rules in one place and by the cell in another: whatever
// the two make of it, no level is wider than the screen.
func TestFlameGraphFitsLabelsTheTerminalCountsItsOwnWay(t *testing.T) {
	a, _, fv, _ := openFlame(t)
	answerFlame(a, fv, stackRecords("2026-07-15T12:05:00Z",
		"keycap1️⃣.run;a 40", "keycap1️⃣.run;b 30", "👨‍👩‍👧.Foo.bar;x́́ 20,5", "#️⃣*️⃣9️⃣;🇦🇹🇦🇹🇦🇹.go 7",
		strings.Repeat("7️⃣", 70)+".pkg."+strings.Repeat("8️⃣", 70)+";leaf 60"), nil)
	if fv.graph == nil {
		t.Fatal("the stacks did not become a graph")
	}
	fv.graph.ClearSearch()
	for width := flameMinWidth; width <= 130; width++ {
		a.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		fv.graph.Reset()
		// A level is as wide as its cells say, so that every frame starts
		// in its own column: a cell the screen counts wider is cut, not
		// left to push the frames after it along.
		for level, cells := range fv.graph.Layout().Rows {
			var b strings.Builder
			newFlamePaint(fv.graph).row(&b, cells, level)
			if last := cells[len(cells)-1]; lipgloss.Width(b.String()) != last.X+last.W {
				t.Fatalf("at %d columns level %d is %d cells wide, its cells end at %d: %q", width, level, lipgloss.Width(b.String()), last.X+last.W, b.String())
			}
		}
		for _, step := range []string{"", "down", "#", "esc", "down", "enter", "right", "#", "esc", "esc", "down", "right", "right", "down", "d", "d"} {
			if step != "" {
				press(a, key(step))
			}
			if a.top() != viewModel(fv) || !fv.drawable() {
				t.Fatalf("at %d columns after %q the graph is not drawn", width, step)
			}
			for i, line := range strings.Split(a.View(), "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Fatalf("at %d columns after %q line %d is %d cells wide: %q", width, step, i, w, line)
				}
			}
		}
	}
}
