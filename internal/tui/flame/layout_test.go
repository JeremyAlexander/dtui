package flame

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// draw renders a layout as one line per level: a frame as the first letter
// of its label over its cells, a marker as the digit of the frames it stands
// for, an empty cell as a dot.
func draw(t *Tree, l *Layout) string {
	var b strings.Builder
	for _, row := range l.Rows {
		line := []byte(strings.Repeat(".", l.Width))
		for _, c := range row {
			glyph := t.Label(c.Node)[0]
			if c.Hidden > 0 {
				glyph = byte('0' + min(c.Hidden, 9))
			}
			for x := c.X; x < c.X+c.W; x++ {
				line[x] = glyph
			}
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func find(t testing.TB, tree *Tree, path ...string) NodeID {
	t.Helper()
	for n := NodeID(0); int(n) < tree.Len(); n++ {
		if strings.Join(tree.Path(n), ";") == strings.Join(path, ";") {
			return n
		}
	}
	t.Fatalf("no frame %v", path)
	return None
}

func TestLayout(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		lens  int
		zoom  []string
		width int
		want  string
	}{
		{
			"a frame is as wide as its share; its own weight is at the right of its callees",
			[]string{"main;a 2", "main;b 3", "main 5"}, 0, nil, 10,
			"aaaaaaaaaa\nmmmmmmmmmm\naabbb.....\n",
		},
		{
			"siblings tile without a gap: every edge is rounded down, none twice",
			[]string{"a 1", "b 1", "c 1"}, 0, nil, 10,
			"aaaaaaaaaa\naaabbbcccc\n",
		},
		{
			"callees start at their caller's left edge",
			[]string{"a 5", "b;x 2", "b;y 2", "b 1"}, 0, nil, 10,
			"aaaaaaaaaa\naaaaabbbbb\n.....xxyy.\n",
		},
		{
			"a frame narrower than a cell is not drawn, and neither is anything under it",
			[]string{"m;big 98", "m;small;deep;deeper 1", "m 1"}, 0, nil, 10,
			"aaaaaaaaaa\nmmmmmmmmmm\nbbbbbbbbb1\n",
		},
		{
			"narrow frames before a frame lose their cell to it",
			[]string{"m;a 55", "m;b 2", "m;c 2", "m;d 41"}, 0, nil, 10,
			"aaaaaaaaaa\nmmmmmmmmmm\naaaaaddddd\n",
		},
		{
			"narrow frames after the last frame share one marker",
			[]string{"m;a 55", "m;b 2", "m;c 2", "m 41"}, 0, nil, 10,
			"aaaaaaaaaa\nmmmmmmmmmm\naaaaa2....\n",
		},
		{
			"a marker that would fall outside its caller is not drawn",
			// m ends in the middle of cell 5; x and y lie in that cell.
			[]string{"m;a 50", "m;x 3", "m;y 3", "n 44"}, 0, nil, 10,
			"aaaaaaaaaa\nmmmmmnnnnn\naaaaa.....\n",
		},
		{
			"a frame without weight in the lens is not drawn",
			[]string{"a;x 4,0", "a;y 0,4", "b 4,4"}, 1, nil, 8,
			"aaaaaaaa\naaaabbbb\nyyyy....\n",
		},
		{
			"zoomed: the frame fills the width, its callers above it too, and nothing beside them",
			[]string{"m;a;x 1", "m;a;y 3", "m;b 4", "n 8"}, 0, []string{"m", "a"}, 8,
			"aaaaaaaa\nmmmmmmmm\naaaaaaaa\nxxyyyyyy\n",
		},
		{
			"zoomed into a frame too narrow to show before",
			[]string{"big 999", "small;x 1"}, 0, []string{"small"}, 6,
			"aaaaaa\nssssss\nxxxxxx\n",
		},
		{"one cell: the last frame to end in it has it", []string{"a;b 1", "c 1"}, 0, nil, 1, "a\nc\n"},
		{"one cell that no frame fills", []string{"a;b 1", " 1"}, 0, nil, 1, "a\n1\n"},
		{"no width", []string{"a 1"}, 0, nil, 0, ""},
		{"no weight in the lens", []string{"a 1,0"}, 1, nil, 10, ""},
		{"an unknown lens", []string{"a 1"}, 3, nil, 10, ""},
		{"no stacks", nil, 0, nil, 10, ""},
		{"one sample", []string{"main;f 1"}, 0, nil, 4, "aaaa\nmmmm\nffff\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lenses := 1
			if len(tc.lines) > 0 {
				lenses = strings.Count(tc.lines[0], ",") + 1
			}
			tree := build(t, lenses, tc.lines...)
			zoom := Root
			if tc.zoom != nil {
				zoom = find(t, tree, tc.zoom...)
			}
			l := tree.Layout(tc.lens, zoom, tc.width)
			if got := draw(tree, l); got != tc.want {
				t.Errorf("got:\n%swant:\n%s", got, tc.want)
			}
			if l.Zoom != zoom || l.Width != tc.width {
				t.Errorf("the layout is of zoom %d at width %d, want %d at %d", l.Zoom, l.Width, zoom, tc.width)
			}
		})
	}
}

// A marker says how many frames it stands for and what they weigh.
func TestLayoutMarker(t *testing.T) {
	tree := build(t, 1, "m;a 55", "m;b 2", "m;c 1", "m 42")
	l := tree.Layout(0, Root, 10)
	want := Cell{Node: find(t, tree, "m", "b"), X: 5, W: 1, Hidden: 2, Weight: 3}
	if got := l.Rows[2][1]; got != want {
		t.Errorf("marker %+v, want %+v", got, want)
	}
}

// A callee without weight in the lens is no part of a marker.
func TestLayoutMarkerCountsWhatIsThere(t *testing.T) {
	tree := build(t, 2, "m;a 55,55", "m;b 2,2", "m;c 1,0", "m;d 1,1", "m 41,42")
	if got, want := tree.Layout(0, Root, 10).Rows[2][1], (Cell{Node: find(t, tree, "m", "b"), X: 5, W: 1, Hidden: 3, Weight: 4}); got != want {
		t.Errorf("first lens: marker %+v, want %+v", got, want)
	}
	if got, want := tree.Layout(1, Root, 10).Rows[2][1], (Cell{Node: find(t, tree, "m", "b"), X: 5, W: 1, Hidden: 2, Weight: 3}); got != want {
		t.Errorf("second lens: marker %+v, want %+v", got, want)
	}
}

func TestCellAt(t *testing.T) {
	for _, tc := range []struct {
		offset, whole int64
		width, want   int
	}{
		{0, 10, 5, 0}, {1, 10, 5, 0}, {2, 10, 5, 1}, {9, 10, 5, 4}, {10, 10, 5, 5},
		{11, 10, 5, 5},            // beyond the whole: the right edge
		{-3, 10, 5, 0},            // before it: the left edge
		{math.MaxInt64, 10, 5, 5}, // far beyond
		{math.MaxInt64 - 1, math.MaxInt64, 400, 399}, // no overflow on the way
		{math.MaxInt64, math.MaxInt64, 400, 400},
		{1, 1, 1, 1},
	} {
		if got := cellAt(tc.offset, tc.whole, tc.width); got != tc.want {
			t.Errorf("cellAt(%d, %d, %d) = %d, want %d", tc.offset, tc.whole, tc.width, got, tc.want)
		}
	}
}

func TestLayoutFind(t *testing.T) {
	tree := build(t, 1, "m;a 55", "m;b 2", "m;c 3", "m 40", "n;deep 1", " 3")
	l := tree.Layout(0, Root, 10)
	for _, tc := range []struct {
		path []string
		want Cell
		ok   bool
	}{
		{nil, Cell{Node: Root, X: 0, W: 10}, true},
		{[]string{"m"}, Cell{Node: 1, X: 0, W: 9}, true},
		{[]string{"m", "a"}, Cell{Node: 2, X: 0, W: 5}, true},
		{[]string{"m", "b"}, Cell{}, false}, // the marker's first frame is not drawn itself
		{[]string{"m", "c"}, Cell{}, false},
		{[]string{"n"}, Cell{}, false},
		{[]string{"n", "deep"}, Cell{}, false}, // a level the layout has, under a frame it has not
	} {
		got, ok := l.Find(tree, find(t, tree, tc.path...))
		if got != tc.want || ok != tc.ok {
			t.Errorf("Find(%v) = %+v, %v, want %+v, %v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := l.Find(tree, None); ok {
		t.Error("no frame was found on screen")
	}
	if _, ok := l.Find(tree, NodeID(tree.Len())); ok {
		t.Error("a frame beyond the tree was found on screen")
	}
	// Zoomed, the frames beside the zoomed one are not on screen.
	zoomed := tree.Layout(0, find(t, tree, "m", "a"), 10)
	if _, ok := zoomed.Find(tree, find(t, tree, "n")); ok {
		t.Error("a frame beside the zoom was found on screen")
	}
	if got, ok := zoomed.Find(tree, find(t, tree, "m")); !ok || got.W != 10 {
		t.Errorf("the zoomed frame's caller: %+v, %v, want the whole width", got, ok)
	}
}

// checkLayout holds a layout to the rules that make it a flame graph,
// whatever the tree: the caller is the layout's own description.
func checkLayout(t testing.TB, tree *Tree, lens int, l *Layout) {
	t.Helper()
	for level, row := range l.Rows {
		if len(row) == 0 {
			t.Fatalf("level %d is empty", level)
		}
		x, last := 0, None
		for _, c := range row {
			if c.W < 1 || c.X < x || c.X+c.W > l.Width {
				t.Fatalf("level %d: cell %+v overlaps the one before (ends at %d) or leaves the %d cells", level, c, x, l.Width)
			}
			if c.Node <= last || tree.Level(c.Node) != level || tree.Total(c.Node, lens) < 1 {
				t.Fatalf("level %d: cell %+v is out of order, on the wrong level, or weightless", level, c)
			}
			x, last = c.X+c.W, c.Node
			if c.Hidden > 0 && c.W != 1 {
				t.Fatalf("level %d: marker %+v is not one cell", level, c)
			}
			parent := tree.Parent(c.Node)
			if parent == None {
				continue
			}
			pc, ok := l.Find(tree, parent)
			if !ok || c.X < pc.X || c.X+c.W > pc.X+pc.W {
				t.Fatalf("level %d: cell %+v is not inside its caller's %+v (shown: %v)", level, c, pc, ok)
			}
		}
	}
}

// Every width from one cell to four hundred gives a layout that keeps the
// rules, and the root level is always the whole width.
func TestLayoutAtEveryWidth(t *testing.T) {
	b := NewBuilder(2)
	for i := range 600 {
		frames := []string{"main", fmt.Sprint("f", i%5), fmt.Sprint("g", i%17), fmt.Sprint("h", i%29), fmt.Sprint("i", i)}
		b.Add(frames[:1+i%5], []int64{int64(1 + i*i%97), int64(i % 3)})
	}
	tree := b.Tree()
	deep := find(t, tree, "main", "f3", "g3")
	for width := 1; width <= 400; width++ {
		for lens := range 2 {
			for _, zoom := range []NodeID{Root, deep} {
				l := tree.Layout(lens, zoom, width)
				checkLayout(t, tree, lens, l)
				if len(l.Rows) == 0 || l.Rows[0][0] != (Cell{Node: Root, W: width}) {
					t.Fatalf("width %d, lens %d, zoom %d: the root is not the whole width", width, lens, zoom)
				}
				if got := l.Rows[tree.Level(zoom)]; len(got) != 1 || got[0].W != width {
					t.Fatalf("width %d, lens %d: the zoomed frame's level is %+v", width, lens, got)
				}
			}
		}
	}
}

// The same tree, lens, zoom and width give the same layout every time.
func TestLayoutIsDeterministic(t *testing.T) {
	lines := []string{"main;a;x 3", "main;a;y 1", "main;b 9", "other;a 5", "main 2"}
	want := draw(build(t, 1, lines...), build(t, 1, lines...).Layout(0, Root, 37))
	for range 20 {
		tree := build(t, 1, lines...)
		if got := draw(tree, tree.Layout(0, Root, 37)); got != want {
			t.Fatalf("got:\n%swant:\n%s", got, want)
		}
	}
}

// Weights as large as a number holds are laid out like any other: the
// arithmetic neither overflows nor leaves the screen.
func TestLayoutWithHugeWeights(t *testing.T) {
	huge := fmt.Sprint(int64(math.MaxInt64 / 2))
	tree := build(t, 1, "a "+huge, "b "+huge, "c;x "+huge, "c;y "+huge)
	l := tree.Layout(0, Root, 12)
	checkLayout(t, tree, 0, l)
	// The root's total is saturated: c starts one short of it and ends at
	// it, and of its callees only x starts before the edge.
	if got, want := draw(tree, l), "aaaaaaaaaaaa\naaaaabbbbbbc\n...........x\n"; got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
	half := build(t, 1, "a "+fmt.Sprint(int64(math.MaxInt64/4)), "b "+fmt.Sprint(int64(math.MaxInt64/4)))
	if got, want := draw(half, half.Layout(0, Root, 12)), "aaaaaaaaaaaa\naaaaaabbbbbb\n"; got != want {
		t.Errorf("two quarters of the largest number:\n%swant:\n%s", got, want)
	}
}

func TestLabel(t *testing.T) {
	for _, tc := range []struct {
		name, label string
		width       int
		want        string
	}{
		{"padded to the width", "main", 8, "main    "},
		{"exactly fitting", "main", 4, "main"},
		{"no room", "main", 0, ""},
		{"negative room", "main", -3, ""},
		{"one cell", "main", 1, "…"},
		{"a package gives way before the method does", "com.example.shop.Cart.total", 16, "c.e.s.Cart.total"},
		{"the method alone before a fragment of its type", "com.example.shop.Cart.total", 8, "total   "},
		{"the method alone, exactly fitting", "com.example.shop.Cart.total", 5, "total"},
		{"a method that does not fit keeps its start", "com.example.shop.Cart.total", 4, "tot…"},
		{"a method without its parameters", "com.example.Cart.total(int, long)", 6, "total "},
		{"a script's function before a fragment of its path", "src/server/handler.js:resolve", 9, "resolve  "},
		{"a name without a dot keeps its start", "lambda$handleRequest$0", 8, "lambda$…"},
		{"a wide name keeps its start", "com.example.注文.処理する", 5, "処理…"},
		{"a control sequence is shown, not obeyed", "a\x1b[31mb", 10, "a␛[31mb   "},
		{"a line break becomes a space", "first\nsecond\tthird", 20, "first second third  "},
		{"a carriage return is dropped", "a\rb", 4, "ab  "},
		{"invalid UTF-8 is replaced", "a\xffb", 4, "a�b "},
		{"wide characters count two cells", "日本語", 8, "日本語  "},
		{"a wide character that does not fit leaves its cell empty", "日本語", 4, "日… "},
		{"a combining mark takes no cell", "éa", 4, "éa  "},
		{"only combining marks", "́̂", 3, "́̂   "},
		{"empty", "", 3, "   "},
		{"a joined emoji is one glyph of two cells", "a👨\u200d👩\u200d👧b", 5, "a👨\u200d👩\u200d👧b "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Label(tc.label, tc.width)
			if got != tc.want {
				t.Errorf("Label(%q, %d) = %q, want %q", tc.label, tc.width, got, tc.want)
			}
			if w := runewidth.StringWidth(got); w != max(tc.width, 0) {
				t.Errorf("Label(%q, %d) = %q is %d cells wide", tc.label, tc.width, got, w)
			}
		})
	}
}
