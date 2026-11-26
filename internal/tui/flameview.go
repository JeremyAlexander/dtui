package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// The flame graph of the hotspots analysis (ADR-0021): the second reading of
// the :hotspots table, which tab switches to and back from. This file is the
// screen: it fetches the stacks, maps keys onto the model and draws it. What
// the graph shows for a tree, a size and a key is internal/tui/flame's, which
// knows no terminal; the parity with flamelens is tracked in
// docs/design/profiling-flamegraph.md.

// flameHelp is the view's keys as the help overlay lists them. They are
// flamelens's where dtui has not given the key another meaning.
var flameHelp = []keyHint{
	{"↑/k ↓/j", "caller / widest callee"},
	{"← →", "previous / next frame on the level, whoever called it"},
	{"enter", "zoom into the selected frame"},
	{"backspace", "zoom out one step"},
	{"esc", "clear the search, then the zoom, then return to the functions"},
	{"/", "search by regular expression (enter applies, empty clears)"},
	{"#", "search for the selected function"},
	{"n / N", "next / previous match"},
	{"[ / ]", "thread state"},
	{"g/G pgup/dn", "first / last level, a screen of levels (ctrl+b/ctrl+f, space)"},
	{"y / c", "copy the selected function / the dtctl command for the stacks"},
}

// The smallest body a graph is drawn in. Below it the frames are too narrow
// to read or there is no room for a level under the lines around them.
const (
	flameMinWidth  = 40
	flameMinHeight = 6
	// flameChrome is the body lines that are not levels: the thread states,
	// the caption, and the two lines about the selected frame.
	flameChrome = 4
)

type flameView struct {
	ds *dataSource
	// spec is the hotspots view's: its wording of a wait, of an empty
	// analysis and of the window analysed is the graph's too.
	spec *catalog.Spec
	// scope is the process, the window, and the thread state as its lens.
	scope catalog.Scope

	// graph is nil until an analysis has been read.
	graph   *flame.Graph
	summary map[string]any
	// handed is what the functions table handed over last, and searchFor a
	// function it asked to be shown that no graph was there to search yet.
	handed    flameHandOver
	searchFor string

	loading  bool // waiting for the stacks
	building bool // the stacks are here, their tree is being built
	err      error
	// cancel ends the call in flight. Like the table it is opened from, the
	// view waits only while it is on the breadcrumb trail.
	cancel      context.CancelFunc
	parked      bool
	interrupted bool
	progress    *callProgress
	started     time.Time
	seq         int

	input  textinput.Model
	typing bool

	width, height int
}

// flameHandOver is what the functions table tells the graph each time tab
// leaves it for the graph.
type flameHandOver struct {
	scope catalog.Scope
	// window is the window of the analysis the table shows ("" while it
	// shows none): another one than the graph's means the graph is stale.
	window string
	// function is the function of the table's selected row.
	function string
}

// flameOpenMsg puts a table's flame graph on top of it.
type flameOpenMsg struct{ view *flameView }

// flameBuiltMsg is the tree of the stacks a view asked for with call seq.
type flameBuiltMsg struct {
	view    *flameView
	seq     int
	summary map[string]any
	tree    *flame.Tree
}

// openFlame switches from the functions table to the flame graph of the
// same analysis, or says why there is none to switch to.
func (v *tableView) openFlame() tea.Cmd {
	switch {
	case catalog.ProfileAllocations(v.scope):
		return status("the flame graph reads thread states: [ and ] switch to one")
	case v.width < flameMinWidth || v.height < flameMinHeight:
		return status(fmt.Sprintf("the flame graph needs %d columns and %d rows: this terminal is too small for it",
			flameMinWidth, flameMinHeight+4))
	}
	if v.flame == nil {
		v.flame = &flameView{ds: v.ds, spec: v.spec, input: newTextInput()}
		v.flame.input.Prompt = "/ "
	}
	hand := flameHandOver{scope: v.scope, window: profileWindowOf(v.summary)}
	if rec := v.selected(); rec != nil {
		hand.function = catalog.Str(rec, catalog.ProfFunction)
	}
	if v.scope.Arg != "" {
		hand.function = v.scope.Arg // a focused table is about one function
	}
	v.flame.take(hand)
	view := v.flame
	return func() tea.Msg { return flameOpenMsg{view: view} }
}

// profileWindowOf names the window a profiling summary is of.
func profileWindowOf(summary map[string]any) string {
	if summary == nil {
		return ""
	}
	return catalog.Str(summary, catalog.ProfSumFrom) + "/" + catalog.Str(summary, catalog.ProfSumTo)
}

// take adopts what the table hands over. What is the same as last time is
// left as the graph has it: a thread state or a search changed in the graph
// stays, until the table has something new to say.
func (v *flameView) take(hand flameHandOver) {
	last := v.handed
	v.handed = hand

	entity := func(s catalog.Scope) string {
		if s.Entity == nil {
			return ""
		}
		return s.Entity.ID
	}
	stale := hand.window != "" && v.summary != nil && hand.window != profileWindowOf(v.summary)
	if entity(hand.scope) != entity(v.scope) || !sameTimeframe(hand.scope.Timeframe, v.scope.Timeframe) || stale {
		lens := v.scope.Lens
		v.scope = catalog.Scope{Entity: hand.scope.Entity, Timeframe: hand.scope.Timeframe, Lens: lens}
		v.drop()
	}
	if hand.scope.Lens != last.scope.Lens {
		v.setLens(hand.scope.Lens)
	}
	if hand.function != "" && hand.function != last.function {
		v.searchFor = hand.function
		v.applySearchFor()
	}
}

func sameTimeframe(a, b catalog.Timeframe) bool {
	return a.Label == b.Label && a.Dur == b.Dur && a.From.Equal(b.From) && a.To.Equal(b.To)
}

// drop forgets the analysis shown, and with it the call for one.
func (v *flameView) drop() {
	v.graph, v.summary, v.err = nil, nil, nil
	v.building = false
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.loading = false
	v.seq++
}

// applySearchFor searches for the function the table asked to be shown, once
// there is a graph. For a function the graph does not have, nothing is
// searched.
func (v *flameView) applySearchFor() {
	if v.graph == nil || v.searchFor == "" {
		return
	}
	v.graph.SearchLabel(v.searchFor)
	v.searchFor = ""
}

func (v *flameView) setLens(lens int) {
	states := len(catalog.HotspotStates())
	v.scope.Lens = ((lens % states) + states) % states
	if v.graph != nil {
		v.graph.SetLens(v.scope.Lens)
	}
}

// --- the view contract -------------------------------------------------------

// Init reads the analysis when the view has none: the first time it opens,
// and after the table handed over another window or process. It reads what
// the table holds, so that opening the graph asks the environment for nothing.
func (v *flameView) Init() tea.Cmd {
	if v.graph != nil || v.loading || v.building {
		return nil
	}
	return v.fetch(catalog.HotspotStacksAPI)
}

// Refresh asks for the analysis as the table's refresh does: one that
// arrived a moment ago is reused, an older one is run again.
func (v *flameView) Refresh() tea.Cmd {
	if v.parked {
		v.interrupted = true
		return nil
	}
	return v.fetch(catalog.HotspotStacksFreshAPI)
}

func (v *flameView) fetch(api string) tea.Cmd {
	v.interrupted = false
	v.seq++
	if v.cancel != nil {
		v.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	if !v.loading {
		v.started, v.progress = catalog.Now(), &callProgress{}
	}
	v.loading, v.building, v.err = true, false, nil
	return v.ds.call(withProgress(ctx, v.progress), v, v.seq, api, v.scope, "")
}

func (v *flameView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.scope.Timeframe = tf
	v.drop()
	if v.parked {
		v.interrupted = true
		return nil
	}
	return v.fetch(catalog.HotspotStacksAPI)
}

// Left ends the call in flight when the view drops off the trail. The
// analysis itself is the sources' to keep or abandon.
func (v *flameView) Left() {
	v.parked = true
	if !v.loading {
		return
	}
	v.cancel()
	v.cancel = nil
	v.seq++
	v.loading = false
	v.interrupted = true
}

// Returned makes up for the call Left interrupted.
func (v *flameView) Returned() tea.Cmd {
	v.parked = false
	if !v.interrupted {
		return nil
	}
	return v.fetch(catalog.HotspotStacksAPI)
}

// AutoRefreshes is false: a refresh can start an analysis that runs for a
// minute, which no timer should do.
func (v *flameView) AutoRefreshes() bool { return false }

func (v *flameView) Busy() bool { return v.loading || v.building }

func (v *flameView) InputActive() bool { return v.typing }

func (v *flameView) Crumb() string {
	return "flame·" + catalog.HotspotState(v.scope)
}

func (v *flameView) Echo() string { return catalog.HotspotStacksEcho(v.scope) }

// Selection is the selected frame's function and the process analysed, for
// the keys that act on an entity (o opens it, . pins it).
func (v *flameView) Selection() (map[string]any, *catalog.Entity) {
	if v.scope.Entity == nil {
		return nil, nil
	}
	rec := map[string]any{catalog.ProfEntityID: v.scope.Entity.ID}
	if fn, ok := v.selectedFunction(); ok {
		rec[catalog.ProfFunction] = fn
	}
	return rec, v.scope.Entity
}

// YankText copies the selected frame's function, spelled out.
func (v *flameView) YankText() (text, label string, ok bool) {
	fn, ok := v.selectedFunction()
	return fn, "function", ok
}

// selectedFunction is the label of the selected frame; the root is none.
func (v *flameView) selectedFunction() (string, bool) {
	if v.graph == nil || v.graph.Selected() == flame.Root {
		return "", false
	}
	return v.graph.Tree().Label(v.graph.Selected()), true
}

func (v *flameView) Hints() []keyHint {
	if v.typing {
		return []keyHint{{"type", "regular expression"}, {"enter", "search"}, {"esc", "cancel"}}
	}
	if !v.drawable() {
		return []keyHint{{"[ ]", "thread state"}, {"tab", "functions"}}
	}
	// The footer is one line: the keys a reader cannot guess come first.
	hints := []keyHint{{"↑↓←→", "move"}, {"enter", "zoom"}}
	if v.graph.Zoomed() {
		hints = append(hints, keyHint{"backspace", "zoom out"})
	}
	hints = append(hints, keyHint{"/", "search"})
	if v.graph.Searching() {
		hints = append(hints, keyHint{"n/N", "match"})
	}
	return append(hints, keyHint{"#", "search this"}, keyHint{"[ ]", "thread state"}, keyHint{"tab", "functions"})
}

// drawable reports whether there is a graph on screen to move in.
func (v *flameView) drawable() bool {
	return v.graph != nil && !v.graph.Empty() && v.err == nil && !v.tooSmall()
}

func (v *flameView) tooSmall() bool {
	return v.width < flameMinWidth || v.height < flameMinHeight
}

// --- updates -------------------------------------------------------------------

func (v *flameView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.resize(msg.width, msg.height)
	case dataMsg:
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		return v.arrived(msg)
	case flameBuiltMsg:
		if msg.seq != v.seq || !v.building {
			return nil
		}
		v.built(msg)
	case tea.KeyMsg:
		if v.typing {
			return v.typeKey(msg)
		}
		return v.handleKey(msg)
	}
	return nil
}

// arrived takes the answer to the view's call: a failure, the analysis the
// view already shows, or stacks to build a tree from.
func (v *flameView) arrived(msg dataMsg) tea.Cmd {
	v.loading = false
	if msg.err != nil {
		v.graph, v.summary, v.err = nil, nil, msg.err
		return nil
	}
	summary, rows := catalog.SplitSummary(msg.records)
	if v.graph != nil && profileWindowOf(summary) == profileWindowOf(v.summary) {
		// A refresh inside the time an analysis is reused for: nothing new
		// to draw, and the reader's place in the graph is kept.
		return status("this is still the analysis shown: one is reused for a while after it arrives (esc resets the view)")
	}
	// Building the tree is the one step whose cost grows with the analysis,
	// so it runs off the update loop and comes back as a message.
	v.building = true
	view, seq, lenses := v, v.seq, len(catalog.HotspotStates())
	return func() tea.Msg {
		return flameBuiltMsg{view: view, seq: seq, summary: summary, tree: flameTree(rows, lenses)}
	}
}

// flameTree builds the call tree of a stacks result: one stack per row,
// split into frames, weighed once per thread state.
func flameTree(rows []map[string]any, lenses int) *flame.Tree {
	states := catalog.HotspotStates()
	b := flame.NewBuilder(lenses)
	weights := make([]int64, lenses)
	for _, row := range rows {
		for i := range weights {
			n, _ := catalog.FloatValue(row[states[i]])
			weights[i] = int64(n)
		}
		b.Add(strings.Split(catalog.Str(row, catalog.ProfStack), catalog.ProfStackSep), weights)
	}
	return b.Tree()
}

// built shows a new analysis. The graph starts at its root; a search that
// was on is searched again, since it is a pattern and not a place.
func (v *flameView) built(msg flameBuiltMsg) {
	v.building = false
	pattern := ""
	if v.graph != nil {
		pattern = v.graph.Pattern()
	}
	v.graph, v.summary = flame.New(msg.tree), msg.summary
	v.graph.SetLens(v.scope.Lens)
	v.resize(v.width, v.height)
	if pattern != "" {
		_ = v.graph.Search(pattern) // it compiled before
	}
	v.applySearchFor()
}

func (v *flameView) resize(width, height int) {
	v.width, v.height = width, height
	if v.graph != nil {
		v.graph.Resize(width, v.levelsShown())
	}
}

// levelsShown is the number of body lines left for levels of the graph.
func (v *flameView) levelsShown() int {
	n := v.height - flameChrome
	if v.typing || (v.graph != nil && v.graph.Searching()) {
		n-- // the search line
	}
	return max(n, 0)
}

func (v *flameView) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "tab", "shift+tab":
		return func() tea.Msg { return popViewMsg{} }
	case "[", "]":
		step := 1
		if key == "[" {
			step = -1
		}
		v.setLens(v.scope.Lens + step)
		return nil
	}
	if !v.drawable() {
		return nil // esc falls through to the app, which leaves the view
	}
	g := v.graph
	switch key {
	case "up", "k":
		g.Move(flame.Up)
	case "down", "j":
		g.Move(flame.Down)
	case "left":
		g.Move(flame.Left)
	case "right":
		g.Move(flame.Right)
	case "h", "l":
		// The flamelens keys for this; in dtui they are drill letters.
		return status("← and → move along a level")
	case "pgdown", "ctrl+f", " ":
		g.Scroll(max(g.Height(), 1))
	case "pgup", "ctrl+b":
		g.Scroll(-max(g.Height(), 1))
	case "f", "b":
		return status("pgdn and pgup page through the levels, as do ctrl+f and ctrl+b")
	case "g", "home":
		g.ScrollTop()
	case "G", "end":
		g.ScrollBottom()
	case "enter":
		g.ZoomIn()
	case "backspace":
		if !g.ZoomOut() {
			return status("not zoomed: enter zooms into the selected frame")
		}
	case "esc":
		// The view's own steps back come first: the search, then the zoom.
		// With neither, the app leaves the view.
		switch {
		case g.ClearSearch():
			v.resize(v.width, v.height)
		case g.Unzoom():
		default:
			return nil
		}
		return claimKey
	case "/":
		v.typing = true
		v.input.SetValue("")
		v.input.Focus()
		v.resize(v.width, v.height)
		return textinput.Blink
	case "#":
		if !g.SearchSelected() {
			return status("the root is every stack, not a function to search for")
		}
		v.resize(v.width, v.height)
	case "n", "N":
		return v.stepMatch(key == "n")
	}
	return nil
}

// stepMatch moves to the next or the previous match and says when there was
// none to move to, or when the step went around.
func (v *flameView) stepMatch(forward bool) tea.Cmd {
	if !v.graph.Searching() {
		return status("no search: / searches, # searches for the selected function")
	}
	found, wrapped := v.graph.NextMatch(forward)
	switch {
	case !found:
		return status("no match is wide enough to draw here: zoom in, or widen the terminal")
	case wrapped && forward:
		return status("back at the first match")
	case wrapped:
		return status("on to the last match")
	}
	return nil
}

// typeKey edits the search line. enter searches for what it holds, and for
// nothing clears the search; esc leaves the search as it was.
func (v *flameView) typeKey(msg tea.KeyMsg) tea.Cmd {
	if v.graph == nil {
		v.stopTyping() // the graph that was searched is gone
		return nil
	}
	switch msg.String() {
	case "esc":
		v.stopTyping()
		return claimKey
	case "enter":
		pattern := v.input.Value()
		v.stopTyping()
		if pattern == "" {
			v.graph.ClearSearch()
		} else if err := v.graph.Search(pattern); err != nil {
			v.resize(v.width, v.height)
			return statusErr("not a regular expression: " + oneLine(pattern))
		}
		v.resize(v.width, v.height)
		return nil
	}
	var cmd tea.Cmd
	v.input, cmd = v.input.Update(msg)
	return cmd
}

func (v *flameView) stopTyping() {
	v.typing = false
	v.input.Blur()
	v.resize(v.width, v.height)
}

// --- rendering -----------------------------------------------------------------

func (v *flameView) View(width, height int) string {
	v.resize(width, height)
	var b strings.Builder
	b.WriteString(pad(v.stateStrip(), width))
	b.WriteString("\n")
	switch {
	case v.err != nil:
		// It may repeat tenant data: one safe line, wrapped under itself.
		text := strings.ReplaceAll(wrap(oneLine(v.err.Error()), width-2), "\n", "\n  ")
		b.WriteString("\n" + theme.Error.Render("✗ "+text))
	case v.graph == nil:
		for _, line := range v.waitLines() {
			b.WriteString("\n" + placeNote(line, width))
		}
	case v.tooSmall():
		b.WriteString("\n" + placeNote(fmt.Sprintf("the flame graph needs %d columns and %d rows: tab returns to the functions",
			flameMinWidth, flameMinHeight+4), width))
	default:
		v.renderGraph(&b, width)
	}
	return b.String()
}

// stateStrip is the thread states with the one drawn marked, and at its
// right which levels are in view when not all of them are.
func (v *flameView) stateStrip() string {
	var b strings.Builder
	if v.Busy() {
		b.WriteString(" " + theme.Spinner.Render(theme.Spin()))
	}
	for i, state := range catalog.HotspotStates() {
		style := theme.TabInactive
		if i == v.scope.Lens {
			style = theme.TabActive
		}
		b.WriteString(" " + style.Render(state))
	}
	if !v.drawable() {
		return b.String()
	}
	levels, top, shown := len(v.graph.Layout().Rows), v.graph.Top(), v.graph.Height()
	last := min(top+shown, levels)
	var where string
	switch {
	case top == 0 && last == levels:
		where = fmt.Sprintf("%d levels", levels)
	case last < levels:
		where = fmt.Sprintf("levels %d to %d of %d ▼", top+1, last, levels)
	default:
		where = fmt.Sprintf("levels %d to %d of %d", top+1, last, levels)
	}
	if v.graph.Zoomed() {
		where = "zoomed · " + where
	}
	gap := v.width - lipgloss.Width(b.String()) - lipgloss.Width(where) - 1
	if gap < 2 {
		return b.String()
	}
	return b.String() + strings.Repeat(" ", gap) + theme.Dim.Render(where)
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

// caption says which window and which samples the graph is of, in the
// table's words, with the samples of the thread state drawn.
func (v *flameView) caption() string {
	if v.loading {
		// A refresh over a graph: the wait is said where the window was.
		return strings.Join(v.waitLines(), " · ")
	}
	if v.spec.Caption == nil {
		return ""
	}
	summary := map[string]any{catalog.ProfSumSamples: float64(v.graph.Tree().Total(flame.Root, v.scope.Lens))}
	for _, key := range []string{catalog.ProfSumFrom, catalog.ProfSumTo, catalog.ProfSumClamped} {
		if val, ok := v.summary[key]; ok {
			summary[key] = val
		}
	}
	return strings.Join(v.spec.Caption(v.scope, summary), " · ")
}

func (v *flameView) renderGraph(b *strings.Builder, width int) {
	b.WriteString(" " + theme.Dim.Render(pad(oneLine(v.caption()), width-1)) + "\n")
	g := v.graph
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
		b.WriteString("\n" + placeNote("∅ "+catalog.SafeText(empty), width))
		return
	}

	paint := newFlamePaint(g)
	rows := g.Layout().Rows
	for level := g.Top(); level < g.Top()+g.Height(); level++ {
		if level < len(rows) {
			paint.row(b, rows[level], level)
		}
		b.WriteString("\n")
	}
	if v.typing {
		b.WriteString(pad(" "+v.input.View(), width) + "\n")
	} else if g.Searching() {
		b.WriteString(pad(" "+v.matchLine(), width) + "\n")
	}
	title, numbers := v.selectedLines(width)
	b.WriteString(pad(title, width) + "\n" + pad(numbers, width))
}

// matchLine says what is searched for and how much of the graph it matches,
// counting each stack once.
func (v *flameView) matchLine() string {
	g := v.graph
	share, whole := g.Share(), g.Info(flame.Root)
	text := fmt.Sprintf("%s samples · %s of all", catalog.GroupInt(share.All), percent(share.All, whole.All))
	if g.Zoomed() {
		text += fmt.Sprintf(" · %s of the zoomed frame", percent(share.Zoom, whole.Zoom))
	}
	if share.All == 0 {
		text = "matches no frame"
	}
	return theme.Hit.Render("⌕ "+oneLine(g.Pattern())) + theme.Dim.Render("  "+text)
}

// selectedLines are the two lines about the selected frame: its function
// spelled out, and its numbers.
func (v *flameView) selectedLines(width int) (title, numbers string) {
	g := v.graph
	sel := g.Selected()
	info := g.Info(sel)
	state := catalog.HotspotState(v.scope)
	if sel == flame.Root {
		title = "every " + state + " stack of the process"
	} else {
		title = catalog.ShortFrame(oneLine(g.Tree().Label(sel)), max(width-3, 1))
	}
	parts := []string{
		"self " + catalog.GroupInt(info.Self),
		"total " + catalog.GroupInt(info.Total),
		percent(info.Total, info.All) + " of all",
	}
	if g.Zoomed() {
		parts = append(parts, percent(info.Total, info.Zoom)+" of the zoomed frame")
	}
	if info.Hidden > 0 {
		parts = append(parts, fmt.Sprintf("%d callees too narrow to draw (%s samples): enter zooms in",
			info.Hidden, catalog.GroupInt(info.HiddenWeight)))
	}
	return " " + theme.Gutter.Render("▸") + " " + title, "   " + theme.Dim.Render(strings.Join(parts, " · "))
}

// percent is a share of a whole to one decimal; of nothing it is nothing.
func percent(part, whole int64) string {
	if whole <= 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(whole)*100)
}

// The first cell of a frame says what it is even without colour: a frame, the
// selected frame, a search hit, another call of the selected function, and a
// marker for callees too narrow to draw.
const (
	flameEdge     = "▏"
	flameEdgeSel  = "▌"
	flameEdgeHit  = "█"
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

// row writes one level: its cells at their columns, with spaces between.
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
