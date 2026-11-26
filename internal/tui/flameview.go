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
)

// The flame graph of the hotspots analysis (ADR-0023): the second reading of
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
	{"O", "open the selected frame's source in the editor, at its declaration (DTUI_EDITOR names the editor)"},
	{"P / d", "details of the selected frame on or off / given most of the screen"},
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
	// detailsFirst gives the details of the selected frame most of the
	// screen (d), where they otherwise have what the graph leaves.
	detailsFirst bool
	// fn is what the selected frame's function adds up to, kept while the
	// selection, the thread state and the analysis stay the same.
	fn flameFunction

	width, height int
	// lines is how the body's lines are shared out at this size.
	lines flameLines
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
	// The function was asked about: the cursor starts on its first frame
	// on screen, unless the reader already put it somewhere.
	if v.graph.SearchLabel(v.searchFor) && v.graph.Selected() == flame.Root {
		v.graph.NextMatch(true)
	}
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

// Hints are the view's keys, the ones a reader needs most first: the footer
// is one line, and what does not fit is dropped from the end (FitHints).
func (v *flameView) Hints() []keyHint {
	if v.typing {
		return []keyHint{{"type", "regular expression"}, {"enter", "search"}, {"esc", "cancel"}}
	}
	if !v.drawable() {
		return []keyHint{{"tab", "functions"}, {"[ ]", "thread state"}}
	}
	g := v.graph
	hints := []keyHint{{"↑↓←→", "move"}, {"enter", "zoom"}}
	// The key is offered where it has a chance: on a frame that may have a
	// file under the directory dtui was started in.
	if fn, ok := v.selectedFunction(); ok && v.ds.source.likely(fn) {
		hints = append(hints, keyHint{"O", "open source"})
	}
	hints = append(hints, keyHint{"tab", "functions"}, keyHint{"/", "search"})
	if g.Top()+g.Height() < len(g.Layout().Rows) {
		hints = append(hints, keyHint{"pgdn", "more levels"})
	} else if g.Top() > 0 {
		hints = append(hints, keyHint{"pgup", "levels above"})
	}
	if g.Zoomed() {
		hints = append(hints, keyHint{"backspace", "zoom out"})
	}
	if g.Searching() {
		hints = append(hints, keyHint{"n/N", "match"})
	}
	return append(hints, keyHint{"#", "search this"}, keyHint{"[ ]", "thread state"}, keyHint{"P", "details"})
}

// FitHints has the footer drop what does not fit, the least needed first,
// where it would otherwise cut the line at the terminal's edge.
func (v *flameView) FitHints() bool { return true }

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

// resize takes the body's size and shares its lines out again: how many
// levels there is room for depends on the lines the selected frame needs.
func (v *flameView) resize(width, height int) {
	v.width, v.height = width, height
	// The search line scrolls inside the body's width: a lead, the prompt
	// and the cursor's cell are not the pattern's.
	if w := max(width-lipgloss.Width(" "+v.input.Prompt)-2, 1); w != v.input.Width {
		v.input.Width = w
		v.input.SetCursor(v.input.Position()) // has the input scroll to its new width
	}
	if v.graph == nil {
		return
	}
	v.graph.Resize(v.graphWidth(), v.graph.Height())
	v.lines = v.share()
	v.graph.Resize(v.graphWidth(), v.lines.levels)
}

// graphWidth is the number of cells a level of the graph is drawn in: all
// of them. The root spans the screen, so there is nothing to pan to.
func (v *flameView) graphWidth() int { return v.width }

func (v *flameView) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "tab", "shift+tab":
		return func() tea.Msg { return popViewMsg{view: v} }
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
	case "O":
		fn, _ := v.selectedFunction()
		return func() tea.Msg { return sourceAskMsg{view: v, label: fn} }
	case "d":
		v.detailsFirst = !v.detailsFirst
		switch {
		case !v.ds.previewOn():
			return status("the details are off: P shows them")
		case v.detailsFirst:
			return status("the details have the room, the graph keeps a few levels: d gives it back")
		}
		return status("the graph has the room again")
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
