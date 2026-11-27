package flame

import "slices"

// Graph is a flame graph being looked at: a tree, the lens it is weighed in,
// the size of the screen, and where the reader is in it: the selected
// frame, the zoom, the first level in view and the search. Every method
// leaves the selection on a frame the screen shows, as long as the lens has
// any weight at all.
type Graph struct {
	tree *Tree
	lens int
	// width is the screen's width in cells and height the levels it shows.
	width, height int

	// zooms are the frames zoomed into, the latest last; none means the root.
	zooms  []NodeID
	sel    NodeID
	top    int
	search *search

	layout *Layout
	// share is what the search matches in the layout's lens and zoom, kept
	// until one of the three changes.
	share      MatchShare
	shareValid bool
}

// Direction is a way to move the selection.
type Direction int

const (
	// Up moves to the caller.
	Up Direction = iota
	// Down moves to the heaviest callee on screen, the leftmost of equals.
	Down
	// Left moves to the frame before this one on its level, whoever called it.
	Left
	// Right moves to the frame after this one on its level.
	Right
)

// New looks at a tree: the root selected, no zoom, the first lens, and no
// size until Resize gives one.
func New(t *Tree) *Graph {
	return &Graph{tree: t, sel: Root}
}

// Tree is the tree looked at.
func (g *Graph) Tree() *Tree { return g.tree }

// Lens is the lens the graph is weighed in.
func (g *Graph) Lens() int { return g.lens }

// Selected is the frame the cursor is on.
func (g *Graph) Selected() NodeID { return g.sel }

// Zoom is the frame drawn across the whole width: the root without a zoom.
func (g *Graph) Zoom() NodeID {
	if len(g.zooms) == 0 {
		return Root
	}
	return g.zooms[len(g.zooms)-1]
}

// Zoomed reports whether a frame other than the root fills the width.
func (g *Graph) Zoomed() bool { return len(g.zooms) > 0 }

// Top is the first level in view.
func (g *Graph) Top() int { return g.top }

// Height is the number of levels in view.
func (g *Graph) Height() int { return g.height }

// Layout is what the screen shows.
func (g *Graph) Layout() *Layout {
	if g.layout == nil {
		g.layout = g.tree.Layout(g.lens, g.Zoom(), g.width)
	}
	return g.layout
}

// Empty reports whether the lens has no weight at all, so that there is
// nothing to draw.
func (g *Graph) Empty() bool { return g.tree.Total(Root, g.lens) < 1 }

// Resize sets the screen: its width in cells and the levels it has room for.
func (g *Graph) Resize(width, height int) {
	width, height = max(width, 0), max(height, 0)
	if width != g.width {
		g.layout = nil
	}
	g.width, g.height = width, height
	g.settle()
}

// SetLens weighs the graph in another lens. The selection and the zoom stay
// where they are when they have weight there; a zoomed frame without any
// gives way to the last zoom that has some, and the selection to its nearest
// caller on screen. An unknown lens changes nothing.
func (g *Graph) SetLens(lens int) {
	if lens < 0 || lens >= g.tree.Lenses() || lens == g.lens {
		return
	}
	g.lens = lens
	g.invalidate()
	g.settle()
}

func (g *Graph) invalidate() {
	g.layout, g.shareValid = nil, false
}

// settle repairs what a change may have broken: a zoom without weight, a
// selection the screen no longer shows, and a view scrolled past its end or
// away from the selection.
func (g *Graph) settle() {
	for len(g.zooms) > 0 && g.tree.Total(g.Zoom(), g.lens) < 1 {
		g.zooms = g.zooms[:len(g.zooms)-1]
		g.invalidate()
	}
	l := g.Layout()
	for g.sel != Root {
		if _, ok := l.Find(g.tree, g.sel); ok {
			break
		}
		g.sel = g.tree.Parent(g.sel)
	}
	g.reveal()
}

// reveal scrolls as little as it takes to have the selection in view.
func (g *Graph) reveal() {
	level := g.tree.Level(g.sel)
	if level >= g.top+g.height {
		g.top = level - g.height + 1
	}
	g.top = max(min(g.top, level, g.lastTop()), 0)
}

// lastTop is the first level in view when the view is scrolled to its end.
// The screen never scrolls into levels that show nothing, and its first
// level always has a frame to select: the deepest level can hold markers
// only.
func (g *Graph) lastTop() int {
	rows := g.Layout().Rows
	frames := len(rows)
	if frames > 0 && !slices.ContainsFunc(rows[frames-1], func(c Cell) bool { return c.Hidden == 0 }) {
		frames--
	}
	return max(min(len(rows)-g.height, frames-1), 0)
}

// Move moves the selection one step and reports whether there was a frame
// to move to. The view scrolls along.
func (g *Graph) Move(d Direction) bool {
	l := g.Layout()
	to := None
	switch d {
	case Up:
		to = g.tree.Parent(g.sel)
	case Down:
		// By weight, not by width: rounding to cells can give the lighter
		// of two callees the wider one.
		heaviest := int64(0)
		for c := g.tree.FirstChild(g.sel); c != None; c = g.tree.NextSibling(c) {
			if _, ok := l.Find(g.tree, c); ok && g.tree.Total(c, g.lens) > heaviest {
				to, heaviest = c, g.tree.Total(c, g.lens)
			}
		}
	case Left, Right:
		to = g.beside(d)
	}
	if to == None {
		return false
	}
	g.sel = to
	g.reveal()
	return true
}

// beside is the frame next to the selection on its level; markers are
// passed over.
func (g *Graph) beside(d Direction) NodeID {
	level := g.tree.Level(g.sel)
	l := g.Layout()
	if level >= len(l.Rows) {
		return None
	}
	row := l.Rows[level]
	step := 1
	if d == Left {
		step = -1
	}
	at := -1
	for i, c := range row {
		if c.Node == g.sel && c.Hidden == 0 {
			at = i
		}
	}
	if at < 0 {
		return None
	}
	for i := at + step; i >= 0 && i < len(row); i += step {
		if row[i].Hidden == 0 {
			return row[i].Node
		}
	}
	return None
}

// Scroll moves the view by a number of levels, down for a positive one, and
// stops at either end. A selection that leaves the view moves to the first
// frame of the first level in view.
func (g *Graph) Scroll(levels int) {
	g.top = max(min(g.top+levels, g.lastTop()), 0)
	g.follow()
}

// ScrollTop shows the root's level first.
func (g *Graph) ScrollTop() { g.Scroll(-g.top) }

// ScrollBottom shows the deepest level that has a frame last.
func (g *Graph) ScrollBottom() { g.Scroll(g.lastTop() - g.top) }

// follow keeps the selection inside the view after a scroll.
func (g *Graph) follow() {
	level := g.tree.Level(g.sel)
	if level >= g.top && level < g.top+g.height {
		return
	}
	l := g.Layout()
	if g.top >= len(l.Rows) {
		return
	}
	for _, c := range l.Rows[g.top] {
		if c.Hidden == 0 {
			g.sel = c.Node
			return
		}
	}
}

// ZoomIn draws the selected frame across the whole width and remembers the
// zooms around it, for ZoomOut: a zoom that is not a caller of the new one
// (the reader went up from it) is forgotten, so that zooming out never
// zooms in. Zooming into the root is no zoom at all and forgets every zoom,
// and a remainder is no frame to zoom into. It reports whether the screen
// changed.
func (g *Graph) ZoomIn() bool {
	switch {
	case g.sel == g.Zoom() || g.tree.IsRest(g.sel):
		return false
	case g.sel == Root:
		g.zooms = nil
	default:
		around := g.zooms[:0]
		for _, z := range g.zooms {
			if z != g.sel && g.tree.Within(z, g.sel) {
				around = append(around, z)
			}
		}
		g.zooms = append(around, g.sel)
	}
	g.invalidate()
	g.settle()
	return true
}

// ZoomOut returns to the zoom before the last ZoomIn, with the selection on
// the frame that was zoomed. It reports whether there was a zoom to leave.
func (g *Graph) ZoomOut() bool {
	if len(g.zooms) == 0 {
		return false
	}
	g.sel = g.Zoom()
	g.zooms = g.zooms[:len(g.zooms)-1]
	g.invalidate()
	g.settle()
	return true
}

// Unzoom leaves every zoom at once, with the selection on the frame that was
// zoomed. It reports whether there was a zoom to leave.
func (g *Graph) Unzoom() bool {
	if len(g.zooms) == 0 {
		return false
	}
	g.sel = g.Zoom()
	g.zooms = nil
	g.invalidate()
	g.settle()
	return true
}

// Reset is the graph as it was first shown in this lens: the root selected,
// no zoom, no search, the root's level first.
func (g *Graph) Reset() {
	g.zooms, g.search, g.sel, g.top = nil, nil, Root, 0
	g.invalidate()
	g.settle()
}

// Info is what there is to say about one frame.
type Info struct {
	// Self and Total are the frame's weights in the graph's lens.
	Self, Total int64
	// All is the root's total and Zoom the zoomed frame's: the two wholes a
	// frame's share is read against. Zoom is 0 for a frame that is not
	// inside the zoomed frame: a caller of it has no share of it.
	All, Zoom int64
	// Hidden counts the frames it called that are narrower than a cell, for
	// a frame on screen inside the zoom, and HiddenWeight is what they
	// weigh together. Zooming in shows them. The callees of a caller of the
	// zoomed frame that the zoom leaves out are not among them: they are
	// not too narrow, they are not asked for.
	Hidden       int
	HiddenWeight int64
}

// Info describes a frame as the screen shows it.
func (g *Graph) Info(n NodeID) Info {
	info := Info{
		Self:  g.tree.Self(n, g.lens),
		Total: g.tree.Total(n, g.lens),
		All:   g.tree.Total(Root, g.lens),
	}
	if !g.tree.Within(g.Zoom(), n) {
		return info
	}
	info.Zoom = g.tree.Total(g.Zoom(), g.lens)
	l := g.Layout()
	if _, shown := l.Find(g.tree, n); !shown {
		return info
	}
	for c := g.tree.FirstChild(n); c != None; c = g.tree.NextSibling(c) {
		w := g.tree.Total(c, g.lens)
		if _, shown := l.Find(g.tree, c); !shown && w > 0 {
			info.Hidden++
			info.HiddenWeight = satAdd(info.HiddenWeight, w)
		}
	}
	return info
}
