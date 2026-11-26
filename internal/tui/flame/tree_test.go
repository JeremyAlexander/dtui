package flame

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// build makes a tree from folded lines, "a;b;c 3" or, for several lenses,
// "a;b;c 3,0,1".
func build(t testing.TB, lenses int, lines ...string) *Tree {
	t.Helper()
	b := NewBuilder(lenses)
	for _, line := range lines {
		stack, counts, _ := strings.Cut(line, " ")
		var weights []int64
		for _, c := range strings.Split(counts, ",") {
			w, err := strconv.ParseInt(c, 10, 64)
			if err != nil {
				t.Fatalf("line %q: %v", line, err)
			}
			weights = append(weights, w)
		}
		var frames []string
		if stack != "" {
			frames = strings.Split(stack, ";")
		}
		b.Add(frames, weights)
	}
	return b.Tree()
}

// dump writes a tree as one line per frame, indented by level:
// "label self/total" for every lens.
func dump(t *Tree) string {
	var b strings.Builder
	for n := NodeID(0); int(n) < t.Len(); n++ {
		b.WriteString(strings.Repeat("  ", t.Level(n)) + t.Label(n))
		for lens := range t.Lenses() {
			fmt.Fprintf(&b, " %d/%d", t.Self(n, lens), t.Total(n, lens))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestTreeFromStacks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lenses int
		lines  []string
		want   string
	}{
		{"no stacks: the root alone, without weight", 1, nil, "all 0/0\n"},
		{"one sample", 1, []string{"main 1"}, "all 0/1\n  main 1/1\n"},
		{
			"self is the weight of the stacks that end in a frame, total of those that pass through",
			1, []string{"main;a 2", "main;b 3", "main 5"},
			"all 0/10\n  main 5/10\n    a 2/2\n    b 3/3\n",
		},
		{
			"children are in label order, whatever order the stacks came in",
			1, []string{"z 1", "m 1", "a 1", "B 1"},
			"all 0/4\n  B 1/1\n  a 1/1\n  m 1/1\n  z 1/1\n",
		},
		{
			"equal stacks add up",
			1, []string{"main;a 2", "main;a 3"},
			"all 0/5\n  main 0/5\n    a 5/5\n",
		},
		{
			"a function reached through two paths is two frames",
			1, []string{"main;x;leaf 1", "main;y;leaf 2"},
			"all 0/3\n  main 0/3\n    x 0/1\n      leaf 1/1\n    y 0/2\n      leaf 2/2\n",
		},
		{
			"recursion is a chain of frames with one label",
			1, []string{"f;f;f 4", "f 1"},
			"all 0/5\n  f 1/5\n    f 0/4\n      f 4/4\n",
		},
		{
			"every lens weighs the same frames",
			3, []string{"main;a 2,0,7", "main;b 0,5,0"},
			"all 0/2 0/5 0/7\n  main 0/2 0/5 0/7\n    a 2/2 0/0 7/7\n    b 0/0 5/5 0/0\n",
		},
		{
			"a stack without weight adds no frame; a negative weight is none",
			2, []string{"gone 0,0", "gone;too -3,0", "kept -1,4"},
			"all 0/0 0/4\n  kept 0/0 4/4\n",
		},
		{
			"weights beyond the lenses are ignored, missing ones are zero",
			2, []string{"a 1,2,3", "b 4"},
			"all 0/5 0/2\n  a 1/1 2/2\n  b 4/4 0/0\n",
		},
		{
			"a frame without a label is skipped; a stack without frames weighs on the root",
			1, []string{"a;;b 1", " 2", "; 4"},
			"all 6/7\n  a 0/1\n    b 1/1\n",
		},
		{
			"a function spelled like the root is a frame of its own",
			1, []string{"all 1"},
			"all 0/1\n  all 1/1\n",
		},
		{"fewer than one lens is one", 0, []string{"a 3"}, "all 0/3\n  a 3/3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dump(build(t, tc.lenses, tc.lines...)); got != tc.want {
				t.Errorf("got:\n%swant:\n%s", got, tc.want)
			}
		})
	}
}

// The tree is walked by its links: parent, first child, next sibling, and
// the subtree a frame spans.
func TestTreeLinks(t *testing.T) {
	tree := build(t, 1, "main;a;x 1", "main;a;y 1", "main;b 1", "other 1")
	id := func(path ...string) NodeID {
		t.Helper()
		for n := NodeID(0); int(n) < tree.Len(); n++ {
			if slices.Equal(tree.Path(n), path) {
				return n
			}
		}
		t.Fatalf("no frame %v", path)
		return None
	}
	main, a, x, y, b, other := id("main"), id("main", "a"), id("main", "a", "x"), id("main", "a", "y"), id("main", "b"), id("other")

	if got := tree.Levels(); got != 4 {
		t.Errorf("Levels() = %d, want 4", got)
	}
	if got := tree.Len(); got != 7 {
		t.Errorf("Len() = %d, want 7", got)
	}
	for _, tc := range []struct {
		what      string
		got, want NodeID
	}{
		{"the root has no parent", tree.Parent(Root), None},
		{"parent of main", tree.Parent(main), Root},
		{"parent of x", tree.Parent(x), a},
		{"first child of the root", tree.FirstChild(Root), main},
		{"first child of main", tree.FirstChild(main), a},
		{"first child of a leaf", tree.FirstChild(x), None},
		{"first child of the last frame", tree.FirstChild(other), None},
		{"sibling of x", tree.NextSibling(x), y},
		{"sibling of the last child", tree.NextSibling(y), None},
		{"sibling of a", tree.NextSibling(a), b},
		{"sibling of b", tree.NextSibling(b), None},
		{"sibling of main", tree.NextSibling(main), other},
		{"sibling of the last frame", tree.NextSibling(other), None},
		{"the root has no sibling", tree.NextSibling(Root), None},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %d, want %d", tc.what, tc.got, tc.want)
		}
	}
	for _, tc := range []struct {
		frame, n NodeID
		want     bool
	}{
		{Root, other, true}, {main, main, true}, {main, y, true}, {main, b, true},
		{main, other, false}, {a, b, false}, {a, main, false}, {x, y, false}, {None, main, false},
	} {
		if got := tree.Within(tc.frame, tc.n); got != tc.want {
			t.Errorf("Within(%v, %v) = %v, want %v", tree.Path(tc.frame), tree.Path(tc.n), got, tc.want)
		}
	}
	if got := tree.Level(x); got != 3 {
		t.Errorf("Level(x) = %d, want 3", got)
	}
	if got := tree.Path(Root); len(got) != 0 {
		t.Errorf("the root's path is %v, want none", got)
	}
}

// What is asked about no frame, or about a lens the tree does not have, is
// answered with nothing rather than a panic.
func TestTreeOutOfRange(t *testing.T) {
	tree := build(t, 2, "a 1,2")
	for _, n := range []NodeID{None, NodeID(tree.Len()), -7} {
		if tree.Label(n) != "" || tree.Parent(n) != None || tree.Level(n) != 0 || tree.Path(n) != nil ||
			tree.FirstChild(n) != None || tree.NextSibling(n) != None ||
			tree.Self(n, 0) != 0 || tree.Total(n, 0) != 0 || tree.Within(n, Root) {
			t.Errorf("frame %d is not there, yet something is said about it", n)
		}
	}
	for _, lens := range []int{-1, 2} {
		if tree.Self(1, lens) != 0 || tree.Total(1, lens) != 0 {
			t.Errorf("lens %d is not there, yet it has weight", lens)
		}
	}
	if tree.Total(1, 1) != 2 || tree.Label(NodeID(tree.Len()-1)) != "a" {
		t.Error("the last frame and the last lens are there")
	}
}

// The root's total in a lens is the sum of every weight added in it.
func TestTreeTotalsReconcile(t *testing.T) {
	b := NewBuilder(5)
	var want [5]int64
	for i := range 2000 {
		frames := []string{"main", fmt.Sprint("f", i%7), fmt.Sprint("g", i%13), fmt.Sprint("h", i%3)}[:1+i%4]
		weights := []int64{int64(i % 5), int64(i % 3), 0, int64(i), int64((i * 7) % 11)}
		for lens, w := range weights {
			want[lens] += w
		}
		b.Add(frames, weights)
	}
	tree := b.Tree()
	for lens, sum := range want {
		if got := tree.Total(Root, lens); got != sum {
			t.Errorf("lens %d: the root totals %d, the stacks %d", lens, got, sum)
		}
		// And every frame's total is its own weight plus its callees'.
		for n := NodeID(0); int(n) < tree.Len(); n++ {
			sum := tree.Self(n, lens)
			for c := tree.FirstChild(n); c != None; c = tree.NextSibling(c) {
				sum += tree.Total(c, lens)
			}
			if got := tree.Total(n, lens); got != sum {
				t.Fatalf("lens %d, %v: total %d, self and callees %d", lens, tree.Path(n), got, sum)
			}
		}
	}
}

// The same stacks give the same tree in any order, so the screen does not
// depend on the order an analysis happened to list them in.
func TestTreeDoesNotDependOnInputOrder(t *testing.T) {
	lines := []string{"main;a;x 1,4", "main;b 2,0", "main;a 3,1", "other;a 5,5", "main;a;x 7,0", "é;ü 1,1"}
	want := dump(build(t, 2, lines...))
	for i := range lines {
		rotated := append(slices.Clone(lines[i:]), lines[:i]...)
		if got := dump(build(t, 2, rotated...)); got != want {
			t.Fatalf("rotated by %d:\n%swant:\n%s", i, got, want)
		}
	}
	slices.Reverse(lines)
	if got := dump(build(t, 2, lines...)); got != want {
		t.Fatalf("reversed:\n%swant:\n%s", got, want)
	}
}

// A stack as deep as stacks get is built and walked without recursion.
func TestTreeVeryDeepStack(t *testing.T) {
	const depth = 200_000
	frames := make([]string, depth)
	for i := range frames {
		frames[i] = "f" + strconv.Itoa(i%10)
	}
	b := NewBuilder(1)
	b.Add(frames, []int64{3})
	b.Add(frames[:depth/2], []int64{2})
	tree := b.Tree()
	if tree.Len() != depth+1 || tree.Levels() != depth+1 {
		t.Fatalf("%d frames on %d levels, want %d on %d", tree.Len(), tree.Levels(), depth+1, depth+1)
	}
	leaf := NodeID(depth)
	if tree.Level(leaf) != depth || tree.Total(leaf, 0) != 3 || tree.Total(1, 0) != 5 || len(tree.Path(leaf)) != depth {
		t.Errorf("the deepest frame: level %d, total %d; the first: total %d", tree.Level(leaf), tree.Total(leaf, 0), tree.Total(1, 0))
	}
	if got := tree.Self(NodeID(depth/2), 0); got != 2 {
		t.Errorf("the frame halfway down has self %d, want 2", got)
	}
}

// Weights that add up beyond what a number holds stay at the largest number
// instead of turning negative.
func TestTreeWeightsSaturate(t *testing.T) {
	tree := build(t, 1,
		"a;x "+strconv.FormatInt(math.MaxInt64, 10), "a;x 5",
		"a;y "+strconv.FormatInt(math.MaxInt64-1, 10), "a;y 1", "b 7")
	for _, n := range []NodeID{Root, 1, 2, 3} {
		if got := tree.Total(n, 0); got != math.MaxInt64 {
			t.Errorf("%v totals %d, want the largest number", tree.Path(n), got)
		}
	}
	if got := tree.Self(3, 0); got != math.MaxInt64 {
		t.Errorf("a sum that just fits is %d, want the largest number exactly", got)
	}
}

// A tree that is full keeps counting: what no longer gets a frame of its own
// weighs on the deepest frame it reached.
func TestTreeIsBounded(t *testing.T) {
	b := NewBuilder(1)
	b.maxNodes = 3 // the root and two frames
	b.Add([]string{"a", "b", "c", "d"}, []int64{1})
	b.Add([]string{"a", "x"}, []int64{2})
	b.Add([]string{"y"}, []int64{4})
	b.Add([]string{"a", "b"}, []int64{8})
	want := "all 4/15\n  a 2/11\n    b 9/9\n"
	if got := dump(b.Tree()); got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// A builder can go on after a tree was taken from it; the tree taken does
// not change.
func TestBuilderOutlivesItsTree(t *testing.T) {
	b := NewBuilder(1)
	b.Add([]string{"a"}, []int64{1})
	first := b.Tree()
	b.Add([]string{"b", "c"}, []int64{2})
	if got := dump(first); got != "all 0/1\n  a 1/1\n" {
		t.Errorf("the first tree changed:\n%s", got)
	}
	if got := dump(b.Tree()); got != "all 0/3\n  a 1/1\n  b 0/2\n    c 2/2\n" {
		t.Errorf("the second tree:\n%s", got)
	}
}
