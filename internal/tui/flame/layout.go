package flame

import (
	"math/bits"
	"slices"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// Cell is one run of screen cells on one level of a Layout.
type Cell struct {
	// Node is the frame drawn. On a marker (Hidden > 0) it is the first of
	// the frames the marker stands for.
	Node NodeID
	// X is the first cell and W the number of cells; W is never zero.
	X, W int
	// Hidden is zero for a frame. A marker stands for that many frames with
	// one caller, each narrower than a cell, in a cell no frame takes: the
	// last callees of a frame, where its own weight begins.
	Hidden int
	// Weight is the weight the marker's frames have together.
	Weight int64
}

// Layout is what a width shows of a tree in one lens: per level, the frames
// at least one cell wide, left to right. A frame's width is its share of the
// zoomed frame's total. Its callees start at its left edge, in label order,
// and what is left at the right is its own weight.
type Layout struct {
	Width int
	// Zoom is the frame drawn across the whole width. The frames above it
	// are its callers, each as wide as the screen.
	Zoom NodeID
	// Rows holds the cells of each level that shows any, the root's first.
	Rows [][]Cell
}

// Layout lays the tree out in width cells, with zoom across all of them. A
// zoomed frame without weight in the lens, or a width below one, shows
// nothing.
func (t *Tree) Layout(lens int, zoom NodeID, width int) *Layout {
	l := &Layout{Width: width, Zoom: zoom}
	whole := t.Total(zoom, lens)
	if width < 1 || whole < 1 {
		return l
	}
	l.Rows = make([][]Cell, t.Level(zoom)+1)
	for n := zoom; n != None; n = t.Parent(n) {
		l.Rows[t.Level(n)] = []Cell{{Node: n, W: width}}
	}

	cellOf := func(offset int64) int { return cellAt(offset, whole, width) }
	type span struct {
		node  NodeID
		start int64 // weight offset of the frame's left edge
		x1    int   // one past its last cell
	}
	put := func(level int, c Cell) {
		for len(l.Rows) <= level {
			l.Rows = append(l.Rows, nil)
		}
		l.Rows[level] = append(l.Rows[level], c)
	}
	todo := []span{{node: zoom, x1: width}}
	var shown []span
	for len(todo) > 0 {
		parent := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		level := t.Level(parent.node) + 1
		shown = shown[:0]
		// A callee narrower than a cell starts and ends in the cell the next
		// one starts in. A frame there takes that cell; callees that are
		// still without one after the last frame get a marker in it.
		marker := Cell{W: 1}
		start := parent.start
		for c := t.FirstChild(parent.node); c != None; c = t.NextSibling(c) {
			w := t.Total(c, lens)
			if w < 1 {
				continue
			}
			// Totals are sums, so a callee ends where its caller does or
			// before, and so do the cells.
			end := satAdd(start, w)
			x0, x1 := cellOf(start), cellOf(end)
			if x1 > x0 {
				marker = Cell{W: 1}
				put(level, Cell{Node: c, X: x0, W: x1 - x0})
				shown = append(shown, span{node: c, start: start, x1: x1})
			} else {
				if marker.Hidden == 0 {
					marker.Node, marker.X = c, x0
				}
				marker.Hidden++
				marker.Weight = satAdd(marker.Weight, w)
			}
			start = end
		}
		if marker.Hidden > 0 && marker.X < parent.x1 {
			put(level, marker)
		}
		// The leftmost callee is laid out next, so that every level fills
		// from left to right.
		for i := len(shown) - 1; i >= 0; i-- {
			todo = append(todo, shown[i])
		}
	}
	return l
}

// cellAt is the cell a weight offset from the left edge falls into when
// whole weighs width cells: offset × width ÷ whole, rounded down, in 128 bits
// so that no weight overflows it. An offset beyond the whole is the right
// edge. whole and width are at least one.
func cellAt(offset, whole int64, width int) int {
	hi, lo := bits.Mul64(uint64(min(max(offset, 0), whole)), uint64(width))
	cell, _ := bits.Div64(hi, lo, uint64(whole))
	return int(cell)
}

// Find is the cell a frame is drawn in; false for a frame too narrow to
// show, for one outside the zoom, and for a marker.
func (l *Layout) Find(t *Tree, n NodeID) (Cell, bool) {
	level := t.Level(n)
	if !t.has(n) || level >= len(l.Rows) {
		return Cell{}, false
	}
	// A level's frames are in the order of their ids.
	row := l.Rows[level]
	i, found := slices.BinarySearchFunc(row, n, func(c Cell, n NodeID) int { return int(c.Node) - int(n) })
	if !found || row[i].Hidden > 0 {
		return Cell{}, false
	}
	return row[i], true
}

// Label fits a frame's label into exactly width cells: made safe to print
// and one line (catalog.OneLine), shortened the way the functions table
// shortens a frame (catalog.ShortFrame), then padded with spaces. Where the
// table would cut the label from the left, a cell shows the function alone,
// and no more of it than its start when even that is too long: a reader
// tells two narrow frames apart by how their functions begin, not by the
// tail of a type. Cells are counted as the catalog counts them, with
// go-runewidth.
func Label(label string, width int) string {
	if width < 1 {
		return ""
	}
	label = catalog.OneLine(label)
	short := catalog.ShortFrame(label, width)
	if strings.HasPrefix(short, "…") && !strings.HasPrefix(label, "…") {
		short = runewidth.Truncate(functionOf(label), width, "…")
	}
	return short + strings.Repeat(" ", max(width-runewidth.StringWidth(short), 0))
}

// functionOf is the function at the end of a label: what follows the last
// dot of a package.Type.function, or the colon of a path/file.js:function,
// without a parameter list. A parameter list is the parentheses that close
// the label. Go spells a method's receiver in parentheses in the middle of
// it (pkg.(*Type).Method), and those are part of the name.
func functionOf(label string) string {
	if open := paramsAt(label); open > 0 {
		label = label[:open]
	}
	if i := strings.LastIndexAny(label, ".:"); i >= 0 && i < len(label)-1 {
		return label[i+1:]
	}
	return label
}

// paramsAt is where the parameter list that closes a label opens; -1 for a
// label that does not end in one.
func paramsAt(label string) int {
	if !strings.HasSuffix(label, ")") {
		return -1
	}
	depth := 0
	for i := len(label) - 1; i >= 0; i-- {
		switch label[i] {
		case ')':
			depth++
		case '(':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}
