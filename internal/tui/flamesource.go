package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/source"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// O on a frame of the flame graph opens the file it was compiled from in the
// reader's editor (ADR-0024). The frame's name is text from the environment:
// it selects among the files found under the directory dtui was started in
// (internal/source) and is never run, joined to a path or put in a link.
// What is opened is a link in the editor's own scheme, handed to the system
// the way o hands it a Dynatrace link. No editor is started in dtui's
// terminal, and without a source directory (the browser build) the key says
// that it has nothing to look in.

// sourceLookup is the session's one index of the source directory, read on
// first need and kept.
type sourceLookup struct {
	root   string
	editor source.Editor
	// unknownEditor is what DTUI_EDITOR names when dtui knows no editor by
	// it: the reader is told, where the setting would have been used.
	unknownEditor string
	// limits bound the walk, replaced in tests.
	limits source.Limits

	mu    sync.Mutex
	index *source.Index
	err   error
	done  bool

	// The rest is read and written on the update loop only.

	// warming says a walk was started: one is enough for the session.
	warming bool
	// seq numbers the lookups and cancel ends the one in flight: one runs
	// at a time, and only the answer to the last one asked is acted on.
	seq    int
	cancel context.CancelFunc
	// asked is the last frame likely was asked about and what it said: the
	// footer asks on every draw.
	asked struct {
		label  string
		index  *source.Index
		done   bool
		likely bool
	}
}

func newSourceLookup(root, editor string) *sourceLookup {
	if root == "" {
		return nil
	}
	e, _ := source.EditorByID(editor)
	return &sourceLookup{root: root, editor: e, limits: source.DefaultLimits}
}

// withUnknownEditor notes a DTUI_EDITOR that names no editor.
func (s *sourceLookup) withUnknownEditor(setting string) *sourceLookup {
	if s != nil {
		s.unknownEditor = setting
	}
	return s
}

// unknownEditorNotice is what the reader is told at the start about a
// DTUI_EDITOR that names no editor: the setting turned the detection off.
func unknownEditorNotice(setting string) string {
	return "DTUI_EDITOR=" + oneLine(setting) + " is no editor dtui knows, so O on a flame graph copies a file's path: it names one of " +
		strings.Join(source.EditorIDs(), ", ")
}

// get is the index, walking the directory the first time it is asked for.
// It is called off the update loop. The walk is the session's and is not
// ended by a lookup that is: its own limits bound it.
func (s *sourceLookup) get() (*source.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.index, s.err = source.Scan(context.Background(), s.root, s.limits)
		s.done = true
	}
	return s.index, s.err
}

// walked is what the walk gave, without waiting for one in flight. A walk
// has three states: not ended (done is false), ended with an index, and
// failed for good.
func (s *sourceLookup) walked() (ix *source.Index, err error, done bool) {
	if s == nil || !s.mu.TryLock() {
		return nil, nil, false
	}
	defer s.mu.Unlock()
	return s.index, s.err, s.done
}

// likely reports whether O has a chance on a frame: there is a directory to
// look in, the frame is not a runtime's, and the walk, once it ended, gave
// an index with a file of the right name. A walk that failed gave none.
func (s *sourceLookup) likely(label string) bool {
	if s == nil {
		return false
	}
	ix, err, done := s.walked()
	if s.asked.label == label && s.asked.index == ix && s.asked.done == done && label != "" {
		return s.asked.likely
	}
	f := source.Parse(label)
	likely := f.Notation != source.Unknown && f.Runtime == "" && err == nil && (ix == nil || ix.Likely(f))
	s.asked.label, s.asked.index, s.asked.done, s.asked.likely = label, ix, done, likely
	return likely
}

// sourceAskMsg asks for the source of a frame of a view, and sourceFoundMsg
// answers. The answer is for the view that asked, while it is what the
// reader looks at and the frame is still the one selected.
type sourceAskMsg struct {
	view  viewModel
	label string
}

type sourceFoundMsg struct {
	seq   int
	view  viewModel
	label string
	index *source.Index
	found source.Found
	err   error
}

// sourceWarmMsg says the directory has been walked: the footer can now tell
// which frames have a file.
type sourceWarmMsg struct{}

// warm walks the source directory in the background, once: a walk that is
// under way, has ended or has failed is not started again.
func (s *sourceLookup) warm() tea.Cmd {
	if s == nil || s.warming {
		return nil
	}
	s.warming = true
	return func() tea.Msg {
		_, _ = s.get()
		return sourceWarmMsg{}
	}
}

// ask numbers a new lookup and ends the one before it.
func (s *sourceLookup) ask() (context.Context, int) {
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.seq, s.cancel = s.seq+1, cancel
	return ctx, s.seq
}

// findSource looks a frame's file up, off the update loop.
func (a *app) findSource(view viewModel, label string) tea.Cmd {
	s := a.ds.source
	short := catalog.ShortFrame(oneLine(label), 48)
	f := source.Parse(label)
	if s == nil {
		if a.opts.SourceRefused != "" {
			return statusErr("O looks for a frame's file in the repository dtui was started in: " + oneLine(a.opts.SourceRefused))
		}
		return statusErr("O looks for a frame's file in the repository dtui was started in: start it inside the application's checkout")
	}
	// Whatever this key press says, a lookup still in flight is for a frame
	// the reader has moved on from.
	ctx, seq := s.ask()
	switch {
	case label == "":
		return status("the root is every stack: move to a frame, then O opens its source")
	case f.Runtime != "":
		return status(short + " is part of " + f.Runtime + ": its source is not in this directory")
	case f.Notation == source.Unknown:
		return statusErr("no source file can be told from the name " + short)
	}
	return tea.Batch(status("looking for "+short+" under "+oneLine(filepath.Base(s.root))), func() tea.Msg {
		ix, err := s.get()
		if err != nil {
			return sourceFoundMsg{seq: seq, view: view, label: label, err: err}
		}
		return sourceFoundMsg{seq: seq, view: view, label: label, index: ix, found: ix.Resolve(ctx, f)}
	})
}

// foundSource acts on a lookup: one file opens, several are offered, and
// none says so. Only the answer to the last lookup asked is acted on, and
// only while the reader is still where they asked: on the view, with
// nothing over it and the cursor on the frame. An editor must not come up
// for a screen that is no longer there, or for another frame than the one
// selected. An answer that is dropped while the view is still up says so.
func (a *app) foundSource(msg sourceFoundMsg) tea.Cmd {
	s := a.ds.source
	if s == nil || msg.seq != s.seq || a.top() != msg.view {
		return nil // a newer lookup says its own; a view that was left says nothing
	}
	short := catalog.ShortFrame(oneLine(msg.label), 48)
	if a.overlay != nil {
		return status("the source of " + short + " was found while something else was open: O looks again")
	}
	if fv, ok := msg.view.(*flameView); ok {
		if fn, _ := fv.selectedFunction(); fn != msg.label {
			return status("the source of " + short + " was found after the cursor moved on: O looks for the frame it is on")
		}
	}
	switch {
	case msg.err != nil:
		return statusErr("the source directory could not be read: " + oneLine(msg.err.Error()))
	case len(msg.found.Matches) == 0:
		text := fmt.Sprintf("no source file for %s under %s: a library, or another repository (y copies the name)",
			short, oneLine(filepath.Base(msg.index.Root())))
		if caveats := sourceCaveats(msg.index, msg.found); len(caveats) > 0 {
			text = strings.Join(caveats, " · ") + " · " + text
		}
		return statusErr(text)
	case len(msg.found.Matches) == 1:
		return a.openSource(msg.index, source.Parse(msg.label), msg.found.Matches[0], sourceCaveats(msg.index, msg.found))
	}
	a.overlay = &sourcePicker{app: a, index: msg.index, frame: source.Parse(msg.label), label: short,
		list: msg.found.Matches, caveats: sourceCaveats(msg.index, msg.found)}
	return nil
}

// sourceCaveats is what a reader must know before they take a lookup's
// answer for the whole truth: matches that are not offered, files that were
// not read, and a directory that was not walked to its end.
func sourceCaveats(ix *source.Index, found source.Found) []string {
	var caveats []string
	if found.More > 0 {
		caveats = append(caveats, catalog.GroupInt(int64(found.More))+" more matches are not offered")
	}
	if found.Unread > 0 {
		caveats = append(caveats, sourceUnread(found.Unread, found.Late))
	}
	if ix != nil && ix.Partial != "" {
		caveats = append(caveats, "the directory was not read to its end ("+oneLine(ix.Partial)+")")
	}
	return caveats
}

// sourceWhere words a match as path:line, and sourceHow how its line was
// arrived at: a profile has no line numbers, so the reader is always told.
func sourceWhere(path string, m source.Match) string {
	if m.Line > 0 {
		return fmt.Sprintf("%s:%d", path, m.Line)
	}
	return path
}

func sourceHow(f source.Frame, m source.Match) string {
	name := oneLine(f.Symbol())
	switch m.How {
	case source.Declaration:
		return "the declaration of " + name
	case source.FirstOfSeveral:
		return "approximate: the first of several declarations of " + name
	case source.TypeOnly:
		return "approximate: the type's declaration, the function was not found by name"
	}
	return "the top of the file: no line could be told"
}

// sourceUnread says how many files that could hold a frame a lookup left
// unread, and that it was the time that ran out when it was.
func sourceUnread(files int, late bool) string {
	text := catalog.GroupInt(int64(files)) + " files that could hold it were not read"
	if files == 1 {
		text = "1 file that could hold it was not read"
	}
	if late {
		text = fmt.Sprintf("the search was cut after %d seconds: %s", int(source.ResolveTime.Seconds()), text)
	}
	return text
}

// openSource opens one match in the editor, or copies its place when no
// editor is known. The path is the index's own, checked again to be inside
// the source directory. caveats are what the lookup did not see.
//
// The status line is one line and is cut at the right: what the reader must
// not miss comes first (an approximate line, the caveats), and the path is
// last and cut from its left, where two files differ least.
func (a *app) openSource(ix *source.Index, f source.Frame, m source.Match, caveats []string) tea.Cmd {
	abs, err := ix.Abs(m.Path)
	if err != nil {
		return statusErr(oneLine(err.Error()))
	}
	what := "it"
	if m.How == source.Declaration {
		what = sourceHow(f, m)
	} else {
		caveats = append([]string{sourceHow(f, m)}, caveats...)
	}
	lead := ""
	if len(caveats) > 0 {
		lead = strings.Join(caveats, " · ") + " · "
	}
	where := oneLine(sourceWhere(abs, m))
	said := func(text string) tea.Cmd {
		return status(text + keepEnd(where, max(a.width-4-lipgloss.Width(text), 24)))
	}
	editor := a.ds.source.editor
	if editor.ID == "" {
		yank(sourceWhere(abs, m))
		ids := strings.Join(source.EditorIDs()[:4], ", ")
		why := "no editor is known (DTUI_EDITOR names one: " + ids + ", …)"
		if unknown := a.ds.source.unknownEditor; unknown != "" {
			why = "DTUI_EDITOR=" + oneLine(unknown) + " is no editor dtui knows (it names one of " + ids + ", …)"
		}
		return said(fmt.Sprintf("%s%s · copied the path of %s: ", lead, why, what))
	}
	link, err := editor.URL(abs, m.Line)
	if err != nil {
		return statusErr(oneLine(err.Error()))
	}
	open := openBrowser
	if a.opts.OpenURL != nil {
		open = a.opts.OpenURL
	}
	if err := open(link); err != nil {
		return statusErr(editor.Name + ": " + oneLine(err.Error()))
	}
	// The link is handed over and not waited for: whether the editor took
	// it is not known here, so the status says what was asked and where the
	// file is, for when nothing opened.
	return said(fmt.Sprintf("%sasked %s to open %s: ", lead, editor.Name, what))
}

// keepEnd cuts a line from the left to at most width cells.
func keepEnd(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return "…" + ansi.TruncateLeft(s, lipgloss.Width(s)-width+1, "")
}

// sourcePicker offers the files a frame may be in when there are several.
type sourcePicker struct {
	app     *app
	index   *source.Index
	frame   source.Frame
	label   string
	list    []source.Match
	caveats []string
	sel     int
	// top is the first match drawn and rows how many are: a number opens
	// only a match the reader can see.
	top, rows int
}

func (p *sourcePicker) Hints() []keyHint {
	return []keyHint{{"enter/1-9", "open"}, {"y", "copy path"}, {"j/k", "move"}, {"esc", "cancel"}}
}

func (p *sourcePicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	key := msg.String()
	switch key {
	case "esc", "O":
		a.overlay = nil
		return nil
	case "y":
		a.overlay = nil
		m := p.list[p.sel]
		abs, err := p.index.Abs(m.Path)
		if err != nil {
			return statusErr(oneLine(err.Error()))
		}
		yank(sourceWhere(abs, m))
		const said = "copied the path "
		return status(said + keepEnd(oneLine(sourceWhere(abs, m)), max(a.width-4-len(said), 24)))
	case "enter":
		a.overlay = nil
		return a.openSource(p.index, p.frame, p.list[p.sel], p.caveats)
	}
	if moveSel(key, &p.sel, len(p.list)) {
		return nil
	}
	if idx, ok := digitIndex(key, len(p.list)); ok && idx >= p.top && idx < p.top+p.rows {
		a.overlay = nil
		return a.openSource(p.index, p.frame, p.list[idx], p.caveats)
	}
	return nil
}

func (p *sourcePicker) View(width, height int) string {
	inner := max(min(width-theme.OverlayBox.GetHorizontalFrameSize(), 100), 20)
	// Under the matches: how the selected one's line was arrived at, and
	// what the lookup did not see, which holds for every match.
	notes := append([]string{sourceHow(p.frame, p.list[p.sel])}, p.caveats...)
	// The box, its title and a blank line above the matches, a blank line
	// and the notes below: the matches scroll in what is left.
	p.rows = min(max(height-theme.OverlayBox.GetVerticalFrameSize()-3-len(notes), 1), len(p.list))
	p.top = max(max(min(min(p.top, p.sel), len(p.list)-p.rows), p.sel-p.rows+1), 0)
	title := "source of " + p.label
	if p.rows < len(p.list) {
		title = fmt.Sprintf("%d to %d of %d · %s", p.top+1, p.top+p.rows, len(p.list), title)
	}
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render(pad(ansi.Truncate(title, inner, "…"), inner)) + "\n\n")
	for i := p.top; i < p.top+p.rows; i++ {
		m := p.list[i]
		// The end of a path is what tells two files apart.
		row := fmt.Sprintf("%d · %s", i+1, keepEnd(oneLine(sourceWhere(m.Path, m)), inner-5))
		if i == p.sel {
			b.WriteString(theme.Selected.Render(pad(" "+row, inner)))
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(row, inner-1)))
		}
		b.WriteString("\n")
	}
	for _, note := range notes {
		b.WriteString("\n" + theme.Dim.Render(pad(ansi.Truncate(note, inner, "…"), inner)))
	}
	return centerOverlay(width, height, b.String())
}
