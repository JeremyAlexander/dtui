package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// How the flame graph's screen is drawn. Two rules hold for every line of
// it: it is never wider than the body, and it ends where its text ends. A
// line is not filled with spaces to the last column, so that a terminal
// which counts one of its symbols wider than dtui does has room for it and
// does not wrap, which would push the header off the screen.

// flameLines is the body's lines shared out, top to bottom: the thread
// states (always one line), the caption, the levels of the graph, the search
// line, then the selected frame's name, numbers and details.
type flameLines struct {
	caption []string
	levels  int
	search  bool
	name    []string
	numbers []string
	details []string
}

const (
	// flameLevelsKept is the fewest levels the lines about the selected
	// frame leave the graph, and flameLevelsWithDetails what the details
	// leave it unless they were given the room (d).
	flameLevelsKept        = 3
	flameLevelsWithDetails = 8
	// flameDetailsMin is the fewest lines details are worth showing in.
	flameDetailsMin = 3
)

// share decides which lines the body shows for the graph as it stands. The
// graph's layout is of the body's width already.
func (v *flameView) share() flameLines {
	g := v.graph
	l := flameLines{search: v.typing || g.Searching()}
	if v.tooSmall() {
		return l
	}
	if g.Empty() {
		l.caption = v.captionLines()
		return l
	}
	room := v.height - 1
	if l.search {
		room--
	}
	caption := v.captionLines()
	name := v.nameLines()
	numbers := v.numberLines()

	// The lines around the graph are given out one at a time, the most
	// needed first, for as long as the graph keeps a few levels.
	budget := room - flameLevelsKept
	take := func(from []string, into *[]string) {
		if budget > 0 && len(*into) < len(from) {
			*into = from[:len(*into)+1]
			budget--
		}
	}
	take(name, &l.name)
	take(numbers, &l.numbers)
	take(caption, &l.caption)
	for range name {
		take(name, &l.name)
	}
	for range numbers {
		take(numbers, &l.numbers)
	}
	take(caption, &l.caption)
	if len(l.name) < len(name) && len(l.name) > 0 {
		// Never lose the name without saying so.
		last := &l.name[len(l.name)-1]
		*last = ansi.Truncate(*last, v.width-2, "") + "…"
	}

	have := room - len(l.caption) - len(l.name) - len(l.numbers)
	want := len(g.Layout().Rows)
	if v.ds.previewOn() {
		keep := flameLevelsWithDetails
		if v.detailsFirst {
			keep = flameLevelsKept
		}
		if free := have - min(want, keep); free >= flameDetailsMin {
			l.details = v.detailLines(v.width)
			if over := len(l.details) - free; over > 0 {
				note := fmt.Sprintf("   … %d more lines", over+1)
				if !v.detailsFirst {
					note += ": d gives the details the room"
				}
				l.details = append(l.details[:free-1:free-1], theme.Dim.Render(note))
			}
		}
	}
	l.levels = max(min(want, have-len(l.details)), 1)
	return l
}

func (v *flameView) View(width, height int) string {
	v.resize(width, height)
	var b strings.Builder
	b.WriteString(v.stateStrip())
	switch {
	case v.err != nil:
		// It may repeat tenant data: one safe line, wrapped under itself.
		b.WriteString("\n")
		for i, line := range flameWrap(oneLine(v.err.Error()), width-2, " ") {
			lead := "  "
			if i == 0 {
				lead = "✗ "
			}
			b.WriteString("\n" + ansi.Truncate(theme.Error.Render(lead+line), width, ""))
		}
	case v.graph == nil:
		b.WriteString("\n")
		for _, line := range v.waitLines() {
			b.WriteString("\n" + placeNote(line, width))
		}
	case v.tooSmall():
		b.WriteString("\n\n" + placeNote(fmt.Sprintf("the flame graph needs %d columns and %d rows: tab returns to the functions",
			flameMinWidth, flameMinHeight+4), width))
	default:
		v.renderGraph(&b, width)
	}
	return b.String()
}

// stateStrip is the thread states with the one drawn marked, and at its
// right which levels are in view, with an arrow for each side that has more.
// A narrow screen gets the short form of each before it loses either.
func (v *flameView) stateStrip() string {
	lead := ""
	if v.Busy() {
		lead = " " + theme.Spinner.Render(theme.Spin())
	}
	states := catalog.HotspotStates()
	var full strings.Builder
	for i, state := range states {
		style := theme.TabInactive
		if i == v.scope.Lens {
			style = theme.TabActive
		}
		full.WriteString(" " + style.Render(state))
	}
	short := " " + theme.TabActive.Render(catalog.HotspotState(v.scope)) +
		theme.Dim.Render(fmt.Sprintf(" %d of %d", v.scope.Lens+1, len(states)))

	var where []string
	if v.drawable() {
		where = v.levelsInView()
	}
	strips := []string{lead + full.String(), lead + short}
	// Which levels are in view is said before every state is named: it is
	// the one sign that there is more of the graph than the screen shows.
	for _, strip := range strips {
		for _, text := range where {
			if gap := v.width - lipgloss.Width(strip) - lipgloss.Width(text) - 1; gap >= 2 {
				return strip + strings.Repeat(" ", gap) + theme.Dim.Render(text)
			}
		}
	}
	for _, strip := range strips {
		if lipgloss.Width(strip) <= v.width {
			return strip
		}
	}
	return ansi.Truncate(lead+short, v.width, "…")
}

// levelsInView says which levels of the graph the screen shows, in forms
// from the most telling to the shortest.
func (v *flameView) levelsInView() []string {
	g := v.graph
	levels, top := len(g.Layout().Rows), g.Top()
	last := min(top+g.Height(), levels)
	if top == 0 && last == levels {
		text := fmt.Sprintf("%d levels", levels)
		if g.Zoomed() {
			return []string{"zoomed · " + text, text}
		}
		return []string{text}
	}
	arrows, keys := "", ""
	if top > 0 {
		arrows, keys = " ▲", "pgup"
	}
	if last < levels {
		arrows += " ▼"
		keys = strings.TrimPrefix(keys+"/pgdn", "/")
	}
	text := fmt.Sprintf("levels %d to %d of %d", top+1, last, levels)
	forms := []string{text + arrows + " " + keys, text + arrows, fmt.Sprintf("%d to %d of %d", top+1, last, levels) + arrows}
	if g.Zoomed() {
		forms = append([]string{"zoomed · " + forms[0], "zoomed · " + forms[1]}, forms...)
	}
	return forms
}

// waitLines words the time without a graph: the wait for the analysis as
// the table words it, or the moment its tree takes.
func (v *flameView) waitLines() []string {
	switch {
	case v.building:
		return []string{"laying out the call stacks"}
	case !v.loading:
		return nil
	}
	text := "reading the call stacks"
	if v.spec.Loading != nil {
		if own := oneLine(v.spec.Loading(v.scope)); own != "" {
			text = own
		}
	}
	if !v.started.IsZero() {
		text += ", waited " + catalog.FormatDuration(catalog.Now().Sub(v.started).Truncate(time.Second))
	}
	lines := []string{text}
	if p := v.progress.get(); p != "" {
		lines = append(lines, oneLine(p))
	}
	return lines
}

// captionLines say which window and which samples the graph is of, in the
// table's words, with the samples of the thread state drawn: wrapped, and
// at most two lines.
func (v *flameView) captionLines() []string {
	text := ""
	switch {
	case v.loading:
		// A refresh over a graph: the wait is said where the window was.
		text = strings.Join(v.waitLines(), " · ")
	case v.spec.Caption != nil:
		summary := map[string]any{catalog.ProfSumSamples: float64(v.graph.Tree().Total(flame.Root, v.scope.Lens))}
		for _, key := range []string{catalog.ProfSumFrom, catalog.ProfSumTo, catalog.ProfSumClamped} {
			if val, ok := v.summary[key]; ok {
				summary[key] = val
			}
		}
		text = strings.Join(v.spec.Caption(v.scope, summary), " · ")
	}
	lines := flameWrap(oneLine(text), v.width-2, " ")
	if len(lines) > 2 {
		lines = append(lines[:1], ansi.Truncate(strings.Join(lines[1:], " "), v.width-2, "…"))
	}
	for i, line := range lines {
		lines[i] = " " + theme.Dim.Render(line)
	}
	return lines
}

// flameWrap breaks text into lines of at most width cells: between words,
// and inside a word after one of the breakpoints or, failing that, anywhere.
// Nothing of the text is lost.
func flameWrap(text string, width int, breakpoints string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(ansi.Wrap(text, max(width, 1), breakpoints), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
}

func (v *flameView) renderGraph(b *strings.Builder, width int) {
	g, l := v.graph, v.lines
	for _, line := range l.caption {
		b.WriteString("\n" + line)
	}
	if g.Empty() {
		empty := "no " + catalog.HotspotState(v.scope) + " samples"
		if v.spec.Empty != nil {
			reason := catalog.ProfEmptyState
			if g.Tree().Len() == 1 {
				reason = catalog.Str(v.summary, catalog.ProfSumEmpty)
			}
			if own := v.spec.Empty(v.scope, map[string]any{catalog.ProfSumEmpty: reason}); own != "" {
				empty = own
			}
		}
		b.WriteString("\n\n" + placeNote("∅ "+catalog.SafeText(empty), width))
		return
	}

	paint := newFlamePaint(g)
	rows := g.Layout().Rows
	for level := g.Top(); level < g.Top()+l.levels; level++ {
		b.WriteString("\n")
		if level < len(rows) {
			paint.row(b, rows[level], level)
		}
	}
	if v.typing {
		b.WriteString("\n " + v.input.View())
	} else if l.search {
		b.WriteString("\n " + v.matchLine())
	}
	for _, block := range [][]string{l.name, l.numbers, l.details} {
		for _, line := range block {
			b.WriteString("\n" + ansi.Truncate(line, width, "…"))
		}
	}
}

// matchLine says what is searched for, a pattern or a function, and how much
// of the graph it matches, counting each stack once.
func (v *flameView) matchLine() string {
	g := v.graph
	share, whole := g.Share(), g.Info(flame.Root)
	text := fmt.Sprintf("%s samples · %s of all", catalog.GroupInt(share.All), percent(share.All, whole.All))
	if g.Zoomed() {
		text += fmt.Sprintf(" · %s of the zoomed frame", percent(share.Zoom, whole.Zoom))
	}
	if share.All == 0 {
		text = "no " + catalog.HotspotState(v.scope) + " samples"
	}
	// The numbers are what the line is for: what was searched for gets the
	// room they leave, a function shortened like a frame's label.
	room := max(v.width-lipgloss.Width(text)-len(" search   ")-2, 12)
	what := ansi.Truncate(oneLine(g.Pattern()), room, "…")
	if fn := g.SearchedFunction(); fn != "" {
		what = catalog.ShortFrame(oneLine(fn), room)
	}
	return ansi.Truncate(theme.Hit.Render("search "+what)+theme.Dim.Render("  "+text), v.width-2, "…")
}

// nameLines are the selected frame's function spelled out in full, on as
// many lines as it takes: what a narrow cell could not show is read here.
func (v *flameView) nameLines() []string {
	g := v.graph
	text := "every " + catalog.HotspotState(v.scope) + " stack of the process"
	if sel := g.Selected(); sel != flame.Root {
		text = oneLine(g.Tree().Label(sel))
	}
	lines := flameWrap(text, v.width-4, "./$:")
	for i, line := range lines {
		lead := "   "
		if i == 0 {
			lead = " " + theme.Gutter.Render("▸") + " "
		}
		lines[i] = lead + theme.HeaderVal.Render(line)
	}
	return lines
}

// numberLines are the selected frame's samples and shares.
func (v *flameView) numberLines() []string {
	g := v.graph
	info := g.Info(g.Selected())
	parts := []string{
		"self " + catalog.GroupInt(info.Self),
		"total " + catalog.GroupInt(info.Total),
		percent(info.Total, info.All) + " of all",
	}
	if g.Zoomed() {
		parts = append(parts, percent(info.Total, info.Zoom)+" of the zoomed frame")
	}
	if info.Hidden > 0 {
		callees := "1 callee"
		if info.Hidden > 1 {
			callees = fmt.Sprintf("%d callees", info.Hidden)
		}
		parts = append(parts, fmt.Sprintf("%s too narrow to draw (%s samples): enter zooms in",
			callees, catalog.GroupInt(info.HiddenWeight)))
	}
	lines := flameWrap(strings.Join(parts, " · "), v.width-4, " ")
	for i, line := range lines {
		lines[i] = "   " + theme.Dim.Render(line)
	}
	return lines
}

// percent is a share of a whole to one decimal; of nothing it is nothing.
func percent(part, whole int64) string {
	if whole <= 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(whole)*100)
}

// The first cell of a frame says what it is even without colour: a frame, the
// selected frame (the heaviest mark there is), a search hit, another call of
// the selected function, and a marker for callees too narrow to draw.
const (
	flameEdge     = "▏"
	flameEdgeSel  = "█"
	flameEdgeHit  = "▌"
	flameEdgeSame = "▍"
	flameMarker   = "·"
)

// flameInk is a style as the two strings around the text it colours, so that
// a screen of frames is written without rendering a style per frame.
type flameInk struct{ on, off string }

func inkOf(s lipgloss.Style) flameInk {
	const mark = "\x00"
	r := s.Render(mark)
	i := strings.Index(r, mark)
	if i < 0 {
		return flameInk{}
	}
	return flameInk{on: r[:i], off: r[i+len(mark):]}
}

// flamePaint draws the cells of one graph in the colours of the terminal as
// it is now.
type flamePaint struct {
	g                                 *flame.Graph
	zoomLevel                         int
	ramp                              []flameInk
	selected, hit, same, caller, more flameInk
}

func newFlamePaint(g *flame.Graph) *flamePaint {
	p := &flamePaint{
		g: g, zoomLevel: g.Tree().Level(g.Zoom()),
		selected: inkOf(theme.FlameSelected), hit: inkOf(theme.FlameHit), same: inkOf(theme.FlameSame),
		caller: inkOf(theme.FlameCaller), more: inkOf(theme.FlameMore),
	}
	for _, style := range theme.Flame {
		p.ramp = append(p.ramp, inkOf(style))
	}
	return p
}

// row writes one level: its cells at their columns, with spaces between and
// none after the last. The cursor's style wins over every other, so that
// the selected frame is the same thing on a search hit as anywhere else.
func (p *flamePaint) row(b *strings.Builder, cells []flame.Cell, level int) {
	tree, x := p.g.Tree(), 0
	for _, c := range cells {
		b.WriteString(strings.Repeat(" ", c.X-x))
		x = c.X + c.W
		if c.Hidden > 0 {
			b.WriteString(p.more.on + flameMarker + p.more.off)
			continue
		}
		label := tree.Label(c.Node)
		ink, edge := p.ramp[theme.FlameStep(label)], flameEdge
		switch {
		case c.Node == p.g.Selected():
			ink, edge = p.selected, flameEdgeSel
		case p.g.Matches(c.Node):
			ink, edge = p.hit, flameEdgeHit
		case !p.g.Searching() && p.g.SameAsSelected(c.Node):
			ink, edge = p.same, flameEdgeSame
		case level < p.zoomLevel:
			ink = p.caller
		}
		text := flame.Label(label, c.W-1)
		if !isASCII(text) {
			// The model counts cells as the tables do; a terminal can count
			// a rare cluster wider. Never let a frame push its level over.
			text = pad(text, c.W-1)
		}
		b.WriteString(ink.on + edge + text + ink.off)
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
