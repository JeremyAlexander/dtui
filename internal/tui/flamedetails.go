package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtui/internal/source"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// The details of the selected frame: what it is and why it is hot, read off
// the tree in memory. They follow the preview pane's preference (P) and sit
// under the graph, never beside it: a flame graph's width is its resolution,
// and a side pane would take two fifths of it.

// flameFunction is what the selected frame's function adds up to over the
// whole graph, kept for as long as it is asked about the same frame.
type flameFunction struct {
	graph *flame.Graph
	node  flame.NodeID
	lens  int
	flame.Function
}

func (v *flameView) function() flame.Function {
	g := v.graph
	if v.fn.graph != g || v.fn.node != g.Selected() || v.fn.lens != g.Lens() {
		v.fn = flameFunction{graph: g, node: g.Selected(), lens: g.Lens(), Function: g.Tree().Function(g.Selected(), g.Lens())}
	}
	return v.fn.Function
}

const (
	// flameDetailsWide is the narrowest body whose details are set in two
	// columns, as the preview pane goes beside its table from this width.
	flameDetailsWide = previewPaneMinWidth
	flameLabelWidth  = 10
	// flameTopShares is how many callers and callees are listed.
	flameTopShares = 3
)

// restDetails are the details of a remainder frame: what it stands for and
// how much, and nothing that is said of a function.
func (v *flameView) restDetails(w int) []string {
	tree, sel := v.graph.Tree(), v.graph.Selected()
	under := "the root"
	if p := tree.Parent(sel); p != flame.Root {
		under = catalog.ShortFrame(oneLine(tree.Label(p)), 48)
	}
	what := fmt.Sprintf("not a function: the samples of the frames under %s that the full graph did not keep (it keeps %s frames; a narrower timeframe or thread state leaves fewer)",
		under, catalog.GroupInt(int64(flameMaxFrames)))
	lines := flameField("remainder", what, w, " ")
	states := make([]string, 0, len(catalog.HotspotStates()))
	for i, state := range catalog.HotspotStates() {
		states = append(states, state+" "+catalog.GroupInt(tree.Total(sel, i)))
	}
	return append(lines, flameField("samples", strings.Join(states, " · "), w, " ")...)
}

// detailLines are the details of the selected frame for a body of w cells.
func (v *flameView) detailLines(w int) []string {
	g := v.graph
	tree, sel, lens := g.Tree(), g.Selected(), g.Lens()
	if sel == flame.Root {
		return v.rootDetails(w)
	}
	if tree.IsRest(sel) {
		return v.restDetails(w)
	}
	fn := v.function()
	all := tree.Total(flame.Root, lens)

	identity := func(w int) []string {
		var lines []string
		f := source.Parse(tree.Label(sel))
		for _, part := range []struct{ label, value string }{
			{"package", f.Package}, {"file", f.File}, {"type", f.Type}, {"function", f.Function},
		} {
			if part.value != "" {
				lines = append(lines, flameField(part.label, oneLine(part.value), w, "./$:")...)
			}
		}
		if f.Runtime != "" {
			lines = append(lines, flameField("part of", f.Runtime, w, " ")...)
		}
		states := make([]string, 0, len(catalog.HotspotStates()))
		for i, state := range catalog.HotspotStates() {
			states = append(states, fmt.Sprintf("%s %s/%s", state,
				catalog.GroupInt(tree.Self(sel, i)), catalog.GroupInt(tree.Total(sel, i))))
		}
		lines = append(lines, flameField("self/total", strings.Join(states, " · "), w, " ")...)
		where := fmt.Sprintf("level %d of %d", tree.Level(sel), tree.Levels()-1)
		lines = append(lines, flameField("depth", where, w, " ")...)
		paths := fmt.Sprintf("%d call paths reach this function: total %s (%s of all), self %s",
			fn.Paths, catalog.GroupInt(fn.Total), percent(fn.Total, all), catalog.GroupInt(fn.Self))
		if fn.Paths == 1 {
			paths = "this is the one call path that reaches this function"
		}
		return append(lines, flameField("paths", paths, w, " ")...)
	}
	around := func(w int) []string {
		lines := flameShares("callers", fn.Callers, fn.Total, w)
		if len(fn.Callees) == 0 {
			return append(lines, flameField("callees", "none: the samples are its own", w, " ")...)
		}
		return append(lines, flameShares("callees", fn.Callees, fn.Total, w)...)
	}

	var lines []string
	if w >= flameDetailsWide {
		left := w * 3 / 5
		lines = flameColumns(identity(left-2), around(w-left), left)
	} else {
		lines = append(identity(w), around(w)...)
	}
	path := tree.Path(sel)
	for i, frame := range path {
		path[i] = catalog.ShortFrame(oneLine(frame), 36)
	}
	return append(lines, flameField("stack", strings.Join(path, " › "), w, " ")...)
}

// rootDetails are the details of the root: the process as a whole.
func (v *flameView) rootDetails(w int) []string {
	g := v.graph
	tree, lens := g.Tree(), g.Lens()
	states := make([]string, 0, len(catalog.HotspotStates()))
	for i, state := range catalog.HotspotStates() {
		states = append(states, state+" "+catalog.GroupInt(tree.Total(flame.Root, i)))
	}
	lines := flameField("samples", strings.Join(states, " · "), w, " ")
	lines = append(lines, flameField("depth", fmt.Sprintf("%d levels", tree.Levels()-1), w, " ")...)
	var top []flame.Share
	for c := tree.FirstChild(flame.Root); c != flame.None; c = tree.NextSibling(c) {
		if total := tree.Total(c, lens); total > 0 {
			top = append(top, flame.Share{Label: tree.Label(c), Weight: total})
		}
	}
	// The heaviest first, equals in the order of their names, as they stand.
	for i := 1; i < len(top); i++ {
		for j := i; j > 0 && top[j].Weight > top[j-1].Weight; j-- {
			top[j], top[j-1] = top[j-1], top[j]
		}
	}
	return append(lines, flameShares("starts in", top, tree.Total(flame.Root, lens), w)...)
}

// flameField is one labelled value, wrapped under itself.
func flameField(label, value string, w int, breakpoints string) []string {
	lines := flameWrap(value, w-flameLabelWidth-4, breakpoints)
	for i, line := range lines {
		lead := strings.Repeat(" ", flameLabelWidth)
		if i == 0 {
			lead = fmt.Sprintf("%-*s", flameLabelWidth, label)
		}
		lines[i] = "   " + theme.Dim.Render(lead) + " " + line
	}
	return lines
}

// flameShares lists the heaviest of some functions with their share of a
// whole, and counts the rest.
func flameShares(label string, shares []flame.Share, whole int64, w int) []string {
	var lines []string
	for i, s := range shares {
		if i == flameTopShares {
			lines = append(lines, flameField("", fmt.Sprintf("and %d more", len(shares)-i), w, " ")...)
			break
		}
		lead := ""
		if i == 0 {
			lead = label
		}
		share := fmt.Sprintf("%6s  ", percent(s.Weight, whole))
		name := catalog.ShortFrame(oneLine(s.Label), max(w-flameLabelWidth-4-len(share), 8))
		lines = append(lines, "   "+theme.Dim.Render(fmt.Sprintf("%-*s", flameLabelWidth, lead))+" "+share+name)
	}
	return lines
}

// flameColumns sets two blocks of lines side by side, the left one in left
// cells.
func flameColumns(a, b []string, left int) []string {
	lines := make([]string, max(len(a), len(b)))
	for i := range lines {
		if i < len(a) {
			lines[i] = a[i]
		}
		if i < len(b) {
			lines[i] += strings.Repeat(" ", max(left-lipgloss.Width(lines[i]), 0)) + b[i]
		}
	}
	return lines
}
