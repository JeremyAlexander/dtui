package flame

import (
	"regexp"
)

// search is a pattern and the labels it matches. A label is matched once,
// however many frames carry it.
type search struct {
	pattern string
	// function is the label a search for one function was for.
	function string
	labels   []bool // by label id
}

// MatchShare is how much of the graph a search matches. A frame under a
// matching frame is part of it and is not counted again.
type MatchShare struct {
	// All is the matched weight in the whole graph and Zoom the part of it
	// inside the zoomed frame, both in the graph's lens.
	All, Zoom int64
}

// Search marks the frames whose label matches a regular expression (RE2
// syntax, unanchored, case-sensitive) and keeps them marked until
// ClearSearch. A pattern that does not compile is an error and leaves the
// search as it was. The selection does not move.
func (g *Graph) Search(pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	s := &search{pattern: pattern, labels: make([]bool, len(g.tree.labels))}
	for i, label := range g.tree.labels {
		s.labels[i] = re.MatchString(label)
	}
	// A remainder is no function, whatever its label is spelled like.
	s.labels[restLabelID] = false
	g.search, g.shareValid = s, false
	return nil
}

// SearchSelected searches for exactly the selected frame's label. The root
// is no function: on it nothing happens, and the result says so.
func (g *Graph) SearchSelected() bool {
	if g.sel == Root || g.tree.IsRest(g.sel) {
		return false
	}
	return g.SearchLabel(g.tree.Label(g.sel))
}

// SearchLabel searches for the frames with exactly this label and reports
// whether the tree has any.
func (g *Graph) SearchLabel(label string) bool {
	// The pattern is the label quoted and anchored, so it compiles.
	if err := g.Search("^" + regexp.QuoteMeta(label) + "$"); err != nil {
		return false
	}
	for i, hit := range g.search.labels {
		// The root's label names no function, whatever it is spelled like.
		if hit && i != 0 {
			g.search.function = label
			return true
		}
	}
	g.ClearSearch()
	return false
}

// ClearSearch forgets the search and reports whether there was one.
func (g *Graph) ClearSearch() bool {
	had := g.search != nil
	g.search, g.shareValid = nil, false
	return had
}

// SearchedFunction is the function a search by label is for (SearchLabel,
// SearchSelected); "" for a search by pattern and without a search.
func (g *Graph) SearchedFunction() string {
	if g.search == nil {
		return ""
	}
	return g.search.function
}

// Pattern is the regular expression searched for; "" without a search.
func (g *Graph) Pattern() string {
	if g.search == nil {
		return ""
	}
	return g.search.pattern
}

// Searching reports whether a search is marking frames.
func (g *Graph) Searching() bool { return g.search != nil }

// Matches reports whether the search marks the frame. The root is never
// marked: it stands for every stack, not for a function.
func (g *Graph) Matches(n NodeID) bool {
	return g.search != nil && n != Root && g.tree.has(n) && g.search.labels[g.tree.nodes[n].label]
}

// SameAsSelected reports whether the frame is another call of the selected
// function, the thing to highlight while no search says otherwise.
func (g *Graph) SameAsSelected(n NodeID) bool {
	return g.sel != Root && n != g.sel && n != Root && g.tree.has(n) &&
		g.tree.nodes[n].label == g.tree.nodes[g.sel].label
}

// Share is the weight the search matches, in the lens and zoom of the
// graph; zero without a search.
func (g *Graph) Share() MatchShare {
	if g.search == nil {
		return MatchShare{}
	}
	if !g.shareValid {
		zoom := g.Zoom()
		g.share = MatchShare{All: g.matched(Root), Zoom: g.matched(zoom)}
		// Under a matching caller the zoomed frame is matched as a whole.
		for n := g.tree.Parent(zoom); n != None; n = g.tree.Parent(n) {
			if g.Matches(n) {
				g.share.Zoom = g.tree.Total(zoom, g.lens)
				break
			}
		}
		g.shareValid = true
	}
	return g.share
}

// matched adds up the outermost matching frames at and under a frame.
func (g *Graph) matched(under NodeID) int64 {
	var sum int64
	end := g.tree.nodes[under].end
	for n := under; n < end; {
		if g.Matches(n) {
			sum = satAdd(sum, g.tree.Total(n, g.lens))
			n = g.tree.nodes[n].end
			continue
		}
		n++
	}
	return sum
}

// NextMatch moves the selection to the next frame the search marks among
// those on screen: to the right on the selection's level, then on the levels
// below, and from the last one around to the first. Backwards it goes the
// other way. found is false without a search or a marked frame on screen;
// wrapped says that the move went around.
func (g *Graph) NextMatch(forward bool) (found, wrapped bool) {
	if g.search == nil {
		return false, false
	}
	l := g.Layout()
	var hits []NodeID // in reading order: level by level, left to right
	at := -1          // the hits before the selection's place in that order
	selLevel := g.tree.Level(g.sel)
	for level, row := range l.Rows {
		for _, c := range row {
			if c.Hidden > 0 || !g.Matches(c.Node) {
				continue
			}
			if level < selLevel || (level == selLevel && c.Node < g.sel) {
				at = len(hits)
			}
			hits = append(hits, c.Node)
		}
	}
	if len(hits) == 0 {
		return false, false
	}
	var to int
	if forward {
		to = at + 1
		if to < len(hits) && hits[to] == g.sel {
			to++
		}
		if to >= len(hits) {
			to, wrapped = 0, true
		}
	} else {
		to = at
		if to < 0 {
			to, wrapped = len(hits)-1, true
		}
	}
	// One marked frame that is already selected is no move.
	if hits[to] == g.sel {
		return true, false
	}
	g.sel = hits[to]
	g.reveal()
	return true, wrapped
}
