package flame

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

// fuzzTree reads stacks out of arbitrary bytes: a byte of depth, a byte per
// frame picking one of a few labels, two bytes of weight per lens. The
// labels include the kinds that go wrong: wide, combining, control
// characters, invalid UTF-8, and one spelled like the root.
func fuzzTree(data []byte) *Tree {
	labels := []string{"main", "a", "b", "com.example.shop.Cart.total", "日本語", "é", "x\x1b[31m\ny", "\xff", RootLabel, ""}
	b := NewBuilder(2)
	for len(data) > 0 {
		depth := int(data[0] % 12)
		data = data[1:]
		var frames []string
		for ; depth > 0 && len(data) > 0; depth-- {
			frames = append(frames, labels[int(data[0])%len(labels)])
			data = data[1:]
		}
		var weights [2]int64
		for lens := range weights {
			if len(data) >= 2 {
				// The second byte shifts the first far enough for sums to saturate.
				weights[lens] = int64(data[0]) << (data[1] % 60)
				data = data[2:]
			}
		}
		b.Add(frames, weights[:])
	}
	return b.Tree()
}

// FuzzGraph builds a tree from arbitrary stacks and walks it with arbitrary
// steps at an arbitrary size. Whatever comes in: nothing panics, every
// layout keeps the rules of TestLayoutAtEveryWidth, the root's total is the
// sum of its callees' and its own, and the selection stays on a frame the
// screen shows, inside the view.
func FuzzGraph(f *testing.F) {
	f.Add([]byte("\x02\x00\x01\x05\x00\x00\x00\x03\x00\x02\x03\x09\x01\x01\x01"), uint16(40), uint8(6), []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	f.Add([]byte{}, uint16(0), uint8(0), []byte{1, 4, 9})
	f.Add([]byte("\x0b\x04\x05\x06\x07\x08\x09\x00\x01\x02\x03\x04\xff\x3b\xff\x3b"), uint16(400), uint8(1), []byte{1, 1, 1, 1, 4, 11, 12, 8})
	f.Fuzz(func(t *testing.T, data []byte, width uint16, height uint8, steps []byte) {
		if len(data) > 4096 || len(steps) > 256 {
			t.Skip()
		}
		tree := fuzzTree(data)
		for lens := range tree.Lenses() {
			sum := tree.Self(Root, lens)
			for c := tree.FirstChild(Root); c != None; c = tree.NextSibling(c) {
				sum = satAdd(sum, tree.Total(c, lens))
			}
			if got := tree.Total(Root, lens); got != sum {
				t.Fatalf("lens %d: the root totals %d, itself and its callees %d", lens, got, sum)
			}
		}
		w := int(width % 500)
		g := New(tree)
		g.Resize(w, int(height))
		check := func(step string) {
			t.Helper()
			l := g.Layout()
			checkLayout(t, tree, g.Lens(), l)
			if g.Empty() || w == 0 {
				if len(l.Rows) != 0 || g.Selected() != Root {
					t.Fatalf("after %s: nothing to show, yet %d levels and %s", step, len(l.Rows), where(g))
				}
				return
			}
			if _, shown := l.Find(tree, g.Selected()); !shown {
				t.Fatalf("after %s: the selection is not on screen: %s", step, where(g))
			}
			level := tree.Level(g.Selected())
			if g.Top() < 0 || g.Top() > level || (height > 0 && level >= g.Top()+int(height)) {
				t.Fatalf("after %s: the selection's level %d is outside the view of %d levels from %d", step, level, height, g.Top())
			}
			if tree.Total(g.Zoom(), g.Lens()) < 1 {
				t.Fatalf("after %s: the zoomed frame has no weight: %s", step, where(g))
			}
		}
		check("the start")
		names := []string{"up", "down", "left", "right", "zoom", "out", "unzoom", "reset", "scroll:3", "scroll:-2",
			"top", "bottom", "lens:1", "lens:0", "search:a|語", "same", "next", "prev", "clear", "size:7x3", "size:120x40"}
		for _, s := range steps {
			step := names[int(s)%len(names)]
			run(t, g, step)
			if step == "size:7x3" || step == "size:120x40" {
				return // the size under test is gone; the checks above are for it
			}
			check(step)
		}
		for n := NodeID(0); int(n) < min(tree.Len(), 64); n++ {
			for _, cells := range []int{0, 1, 2, 5, w} {
				if got := runewidth.StringWidth(Label(tree.Label(n), cells)); got != cells {
					t.Fatalf("Label(%q, %d) is %d cells wide", tree.Label(n), cells, got)
				}
			}
		}
	})
}
