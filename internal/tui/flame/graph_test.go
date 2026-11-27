package flame

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// walkLines is the graph the movement tests walk. At 20 cells, in the first
// lens, every unit of weight is one cell:
//
//	aaaaaaaaaaaaaaaaaaaa   all
//	mmmmmmmmmmmmmooooooo   main, other
//	aaaaaabbbbbbcaaaaaaa   main;a, main;b, main;c, other;a
//	xxxxyy..............   main;a;x, main;a;y
//
// The second lens weighs main;a;y 1 and other;a 3.
var walkLines = []string{"main;a;x 4,0", "main;a;y 2,1", "main;b 6,0", "main;c 1,0", "other;a 7,3"}

// run applies space-separated steps to a graph and returns what each one
// answered: "+" for yes, "-" for no, "." for a step without an answer.
func run(t testing.TB, g *Graph, steps string) string {
	t.Helper()
	var out strings.Builder
	answer := func(ok bool) {
		if ok {
			out.WriteByte('+')
		} else {
			out.WriteByte('-')
		}
	}
	for _, step := range strings.Fields(steps) {
		switch name, arg, _ := strings.Cut(step, ":"); name {
		case "up":
			answer(g.Move(Up))
		case "down":
			answer(g.Move(Down))
		case "left":
			answer(g.Move(Left))
		case "right":
			answer(g.Move(Right))
		case "zoom":
			answer(g.ZoomIn())
		case "out":
			answer(g.ZoomOut())
		case "unzoom":
			answer(g.Unzoom())
		case "reset":
			g.Reset()
			out.WriteByte('.')
		case "scroll":
			n, _ := strconv.Atoi(arg)
			g.Scroll(n)
			out.WriteByte('.')
		case "top":
			g.ScrollTop()
			out.WriteByte('.')
		case "bottom":
			g.ScrollBottom()
			out.WriteByte('.')
		case "lens":
			n, _ := strconv.Atoi(arg)
			g.SetLens(n)
			out.WriteByte('.')
		case "size":
			w, h, _ := strings.Cut(arg, "x")
			wn, _ := strconv.Atoi(w)
			hn, _ := strconv.Atoi(h)
			g.Resize(wn, hn)
			out.WriteByte('.')
		case "search":
			answer(g.Search(arg) == nil)
		case "same":
			answer(g.SearchSelected())
		case "clear":
			answer(g.ClearSearch())
		case "first":
			answer(g.FirstMatch())
		case "next", "prev":
			found, wrapped := g.NextMatch(name == "next")
			switch {
			case wrapped:
				out.WriteByte('w')
			default:
				answer(found)
			}
		default:
			t.Fatalf("unknown step %q", step)
		}
	}
	return out.String()
}

// where says where a graph stands: the selected frame, the zoomed one, and
// the first level in view.
func where(g *Graph) string {
	path := func(n NodeID) string {
		if n == Root {
			return "all"
		}
		return strings.Join(g.Tree().Path(n), ";")
	}
	return fmt.Sprintf("sel=%s zoom=%s top=%d", path(g.Selected()), path(g.Zoom()), g.Top())
}

func TestGraphWalk(t *testing.T) {
	for _, tc := range []struct {
		name, steps, answers, want string
	}{
		{"it starts on the root", "", "", "sel=all zoom=all top=0"},
		{"down goes to the widest callee", "down", "+", "sel=main zoom=all top=0"},
		{"of equally wide callees, to the leftmost", "down down", "++", "sel=main;a zoom=all top=0"},
		{"down from a leaf goes nowhere", "down down down down", "+++-", "sel=main;a;x zoom=all top=0"},
		{"up goes to the caller", "down down up", "+++", "sel=main zoom=all top=0"},
		{"up from the root goes nowhere", "up", "-", "sel=all zoom=all top=0"},
		{"right goes to the next frame on the level", "down down right", "+++", "sel=main;b zoom=all top=0"},
		{"right crosses into another caller's frames", "down down right right right", "+++++", "sel=other;a zoom=all top=0"},
		{"right from the last frame goes nowhere", "down right right", "++-", "sel=other zoom=all top=0"},
		{"left goes to the frame before", "down right left", "+++", "sel=main zoom=all top=0"},
		{"left from the first frame goes nowhere", "down left", "+-", "sel=main zoom=all top=0"},
		{"the root has nothing beside it", "left right", "--", "sel=all zoom=all top=0"},

		{"zoom fills the width with the selection", "down down zoom", "+++", "sel=main;a zoom=main;a top=0"},
		{"zoomed, the frames beside the zoom are gone", "down down zoom right left", "+++--", "sel=main;a zoom=main;a top=0"},
		{"zoomed, down goes to the callee that is now widest", "down down zoom down right", "+++++", "sel=main;a;y zoom=main;a top=0"},
		{"zoomed, up reaches the callers", "down down zoom up up", "+++++", "sel=all zoom=main;a top=0"},
		{"zooming into the zoomed frame changes nothing", "down zoom zoom", "++-", "sel=main zoom=main top=0"},
		{"zooming into the root without a zoom changes nothing", "zoom", "-", "sel=all zoom=all top=0"},
		{"zooming into the root leaves every zoom", "down zoom down zoom up up zoom out", "+++++++-", "sel=all zoom=all top=0"},
		{"zoom out returns to the zoom before", "down zoom down zoom out", "+++++", "sel=main;a zoom=main top=0"},
		{"zoom out of the first zoom is no zoom", "down zoom down zoom out out out", "++++++-", "sel=main zoom=all top=0"},
		{"unzoom leaves every zoom, the selection on the last one", "down zoom down zoom down unzoom unzoom", "++++++-", "sel=main;a zoom=all top=0"},
		{"zooming into a caller of the zoom is a zoom like any other", "down down zoom up zoom out", "++++++", "sel=main zoom=main;a top=0"},
		{"reset is the graph as first shown", "down down zoom search:a reset clear", "++++.-", "sel=all zoom=all top=0"},

		{"a lens moves a selection without weight to its nearest caller on screen", "down down right lens:1", "+++.", "sel=main zoom=all top=0"},
		{"a lens keeps a selection that has weight", "down right down lens:1", "+++.", "sel=other;a zoom=all top=0"},
		{"a lens leaves a zoom without weight for the one before", "down zoom down right zoom lens:1", "+++++.", "sel=main zoom=main top=0"},
		{"a lens leaves every zoom without weight", "down down right zoom lens:1", "++++.", "sel=main zoom=all top=0"},
		{"an unknown lens changes nothing", "down lens:2 lens:-1", "+..", "sel=main zoom=all top=0"},

		{"a narrower screen moves a selection that no longer shows to its caller", "down down right right size:4x10", "++++.", "sel=main zoom=all top=0"},
		{"no width at all leaves the selection on the root", "down down size:0x10 size:20x10", "++..", "sel=all zoom=all top=0"},

		{"down scrolls when the selection leaves the view", "size:20x2 down down", ".++", "sel=main;a zoom=all top=1"},
		{"up scrolls back", "size:20x2 down down down up up up", ".++++++", "sel=all zoom=all top=0"},
		{"scrolling takes the selection along once it leaves the view", "size:20x2 scroll:1", "..", "sel=main zoom=all top=1"},
		{"scrolling inside the view leaves the selection", "size:20x3 down down scroll:1", ".++.", "sel=main;a zoom=all top=1"},
		{"scrolling stops at the last level with a frame", "size:20x2 scroll:9", "..", "sel=main;a zoom=all top=2"},
		{"scrolling stops at the root", "size:20x2 down down down scroll:-9", ".+++.", "sel=all zoom=all top=0"},
		{"bottom and top", "size:20x2 bottom top", "...", "sel=all zoom=all top=0"},
		{"bottom", "size:20x2 bottom", "..", "sel=main;a zoom=all top=2"},
		{"a view as tall as the graph does not scroll", "bottom scroll:3", "..", "sel=all zoom=all top=0"},
		{"a taller screen scrolls back what it now has room for", "size:20x2 bottom size:20x3", "...", "sel=main;a zoom=all top=1"},
		{"a shorter screen keeps the selection in view", "down down down size:20x1", "+++.", "sel=main;a;x zoom=all top=3"},
		{"no height at all does not break", "down size:20x0 down up", "+.++", "sel=main zoom=all top=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := New(build(t, 2, walkLines...))
			g.Resize(20, 10)
			if got := run(t, g, tc.steps); got != tc.answers {
				t.Errorf("answers %q, want %q", got, tc.answers)
			}
			if got := where(g); got != tc.want {
				t.Errorf("%s, want %s", got, tc.want)
			}
			if _, shown := g.Layout().Find(g.Tree(), g.Selected()); !shown && g.Selected() != Root {
				t.Errorf("the selection %s is not on screen", where(g))
			}
		})
	}
}

// The deepest level can hold nothing but a marker. The view scrolls far
// enough to show it, but never so far that its first level has no frame to
// select.
func TestGraphScrollsToAMarkerOnlyLevel(t *testing.T) {
	// main fills the width; its one callee is narrower than a cell.
	g := New(build(t, 1, "main 99", "main;tiny 1", "main 100"))
	for _, tc := range []struct {
		height int
		want   string
	}{
		{2, "sel=main zoom=all top=1"},
		{1, "sel=main zoom=all top=1"},
		{3, "sel=all zoom=all top=0"},
	} {
		g.Reset()
		g.Resize(20, tc.height)
		g.ScrollBottom()
		if got := where(g); got != tc.want {
			t.Errorf("height %d: %s, want %s", tc.height, got, tc.want)
		}
	}
	if rows := g.Layout().Rows; len(rows) != 3 || rows[2][0].Hidden != 1 {
		t.Fatalf("the fixture does not end in a marker-only level: %+v", rows)
	}
}

// A marker is not a place to stand: moving along a level passes over it.
func TestGraphMovesPastMarkers(t *testing.T) {
	// Level 2 at 20 cells: a, the marker for b and c, then other's callee z.
	tree := build(t, 1, "m;a 55", "m;b 2", "m;c 2", "m 41", "other;z 100")
	g := New(tree)
	g.Resize(20, 10)
	if row := g.Layout().Rows[2]; len(row) != 3 || row[1].Hidden != 2 {
		t.Fatalf("the fixture has no marker between two frames: %+v", row)
	}
	if got := run(t, g, "down down right"); got != "+++" || g.Selected() != find(t, tree, "other", "z") {
		t.Errorf("right from a: answers %s, on %v, want other;z", got, tree.Path(g.Selected()))
	}
	if got := run(t, g, "left left"); got != "+-" || g.Selected() != find(t, tree, "m", "a") {
		t.Errorf("left from other;z: answers %s, on %v, want m;a", got, tree.Path(g.Selected()))
	}
	// Nor does a search step onto one, though its first frame matches.
	if got := run(t, g, "search:^b$ next prev"); got != "+--" || g.Selected() != find(t, tree, "m", "a") {
		t.Errorf("stepping to a match that is only a marker: answers %s, on %v", got, tree.Path(g.Selected()))
	}
}

func TestGraphInfo(t *testing.T) {
	tree := build(t, 2, "m;a 55,1", "m;b 2,0", "m;c 2,0", "m 41,0", "n 100,9")
	g := New(tree)
	g.Resize(20, 10)
	m, a, b := find(t, tree, "m"), find(t, tree, "m", "a"), find(t, tree, "m", "b")
	for _, tc := range []struct {
		name  string
		steps string
		node  NodeID
		want  Info
	}{
		{"self, total, and the two wholes", "", a, Info{Self: 55, Total: 55, All: 200, Zoom: 200}},
		{"callees narrower than a cell are counted", "", m, Info{Self: 41, Total: 100, All: 200, Zoom: 200, Hidden: 2, HiddenWeight: 4}},
		{"zoomed, the zoom is the second whole", "down zoom", m, Info{Self: 41, Total: 100, All: 200, Zoom: 100, Hidden: 2, HiddenWeight: 4}},
		{"with room for them, no callee is hidden", "size:100x10 down zoom", m, Info{Self: 41, Total: 100, All: 200, Zoom: 100}},
		{"a frame that is not on screen has no hidden callees to speak of", "down right zoom", m, Info{Self: 41, Total: 100, All: 200, Zoom: 100}},
		{"another lens", "lens:1", a, Info{Self: 1, Total: 1, All: 10, Zoom: 10}},
		{"a callee without weight in the lens is not hidden, it is not there", "lens:1", m, Info{Total: 1, All: 10, Zoom: 10}},
		{"a frame narrower than a cell", "", b, Info{Self: 2, Total: 2, All: 200, Zoom: 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g.SetLens(0)
			g.Resize(20, 10)
			g.Reset()
			run(t, g, tc.steps)
			if got := g.Info(tc.node); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGraphAccessors(t *testing.T) {
	tree := build(t, 2, walkLines...)
	g := New(tree)
	if g.Tree() != tree || g.Lens() != 0 || g.Zoomed() || g.Empty() || g.Height() != 0 {
		t.Errorf("a new graph: lens %d, zoomed %v, empty %v, height %d", g.Lens(), g.Zoomed(), g.Empty(), g.Height())
	}
	if rows := g.Layout().Rows; rows != nil {
		t.Errorf("a graph without a size shows %v", rows)
	}
	g.Resize(20, 7)
	run(t, g, "down zoom lens:1")
	if g.Lens() != 1 || !g.Zoomed() || g.Height() != 7 || g.Layout().Width != 20 || g.Layout().Zoom != g.Zoom() {
		t.Errorf("lens %d, zoomed %v, height %d, layout %d wide of zoom %d", g.Lens(), g.Zoomed(), g.Height(), g.Layout().Width, g.Layout().Zoom)
	}
	g.Resize(-5, -5)
	if g.Height() != 0 || g.Layout().Width != 0 {
		t.Errorf("a negative size is %dx%d, want none", g.Layout().Width, g.Height())
	}
	if empty := New(build(t, 2, "a 1,0")); empty.Empty() {
		t.Error("a lens with weight is empty")
	} else if empty.SetLens(1); !empty.Empty() {
		t.Error("a lens without weight is not empty")
	}
}

// Changing the height alone lays nothing out again; changing the lens, the
// zoom or the width does.
func TestGraphKeepsItsLayout(t *testing.T) {
	g := New(build(t, 2, walkLines...))
	g.Resize(20, 10)
	first := g.Layout()
	g.Resize(20, 4)
	run(t, g, "down right scroll:1 search:a next")
	if g.Layout() != first {
		t.Error("moving, scrolling, searching or a new height laid the graph out again")
	}
	for _, step := range []string{"size:21x4", "lens:1", "zoom", "out"} {
		before := g.Layout()
		run(t, g, step)
		if g.Layout() == before {
			t.Errorf("%s kept the old layout", step)
		}
	}
}
