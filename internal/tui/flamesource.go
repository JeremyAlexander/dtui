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
// reader's editor (ADR-0023). The frame's name is text from the environment:
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
	// scan and limits are the walk, replaced in tests.
	limits source.Limits

	mu    sync.Mutex
	index *source.Index
	err   error
	done  bool

	// asked is the last frame likely was asked about and what it said: the
	// footer asks on every draw, and for a Go frame the answer is a pass
	// over every Go file of the index. Read and written on the update loop.
	asked struct {
		index  *source.Index
		label  string
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

// get is the index, walking the directory the first time it is asked for.
// It is called off the update loop.
func (s *sourceLookup) get(ctx context.Context) (*source.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.index, s.err = source.Scan(ctx, s.root, s.limits)
		// An interrupted walk is not the session's answer: the next one asks again.
		s.done = ctx.Err() == nil
	}
	return s.index, s.err
}

// ready is the index if the directory has been walked, without waiting for a
// walk in flight.
func (s *sourceLookup) ready() *source.Index {
	if s == nil || !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	return s.index
}

// likely reports whether O has a chance on a frame: there is a directory to
// look in, the frame is not a runtime's, and the index, once there is one,
// has a file of the right name.
func (s *sourceLookup) likely(label string) bool {
	if s == nil {
		return false
	}
	f := source.Parse(label)
	if f.Notation == source.Unknown || f.Runtime != "" {
		return false
	}
	ix := s.ready()
	if ix == nil {
		return true
	}
	if s.asked.index != ix || s.asked.label != label {
		s.asked.index, s.asked.label, s.asked.likely = ix, label, ix.Likely(f)
	}
	return s.asked.likely
}

// sourceAskMsg asks for the source of a frame of a view, and sourceFoundMsg
// answers. The answer is for the view that asked, while it is what the
// reader looks at.
type sourceAskMsg struct {
	view  viewModel
	label string
}

type sourceFoundMsg struct {
	view    viewModel
	label   string
	index   *source.Index
	matches []source.Match
	// unread is how many files that could hold the frame were not read:
	// beyond the bound, not reached in time, or no longer files to read.
	unread int
	// late says the lookup ran out of time before it ran out of files.
	late bool
	err  error
}

// sourceWarmMsg says the directory has been walked: the footer can now tell
// which frames have a file.
type sourceWarmMsg struct{}

// warm walks the source directory in the background, once.
func (s *sourceLookup) warm() tea.Cmd {
	if s == nil || s.ready() != nil {
		return nil
	}
	return func() tea.Msg {
		_, _ = s.get(context.Background())
		return sourceWarmMsg{}
	}
}

// findSource looks a frame's file up, off the update loop.
func (a *app) findSource(view viewModel, label string) tea.Cmd {
	s := a.ds.source
	short := catalog.ShortFrame(oneLine(label), 48)
	f := source.Parse(label)
	switch {
	case s == nil:
		return statusErr("O looks for a frame's file in the repository dtui was started in: start it inside the application's checkout")
	case label == "":
		return status("the root is every stack: move to a frame, then O opens its source")
	case f.Runtime != "":
		return status(short + " is part of " + f.Runtime + ": its source is not in this directory")
	case f.Notation == source.Unknown:
		return statusErr("no source file can be told from the name " + short)
	}
	return tea.Batch(status("looking for "+short+" under "+filepath.Base(s.root)), func() tea.Msg {
		ix, err := s.get(context.Background())
		if err != nil {
			return sourceFoundMsg{view: view, label: label, err: err}
		}
		matches, unread, late := ix.Resolve(context.Background(), f)
		return sourceFoundMsg{view: view, label: label, index: ix, matches: matches, unread: unread, late: late}
	})
}

// foundSource acts on a lookup: one file opens, several are offered, and
// none says so. A lookup that answers after the reader left the view that
// asked, or opened something over it, is dropped: an editor must not come up
// for a screen that is no longer there.
func (a *app) foundSource(msg sourceFoundMsg) tea.Cmd {
	if a.top() != msg.view || a.overlay != nil {
		return nil
	}
	short := catalog.ShortFrame(oneLine(msg.label), 48)
	switch {
	case msg.err != nil:
		return statusErr(oneLine(msg.err.Error()))
	case len(msg.matches) == 0:
		text := fmt.Sprintf("no source file for %s under %s: a library, or another repository (y copies the name)",
			short, filepath.Base(msg.index.Root()))
		if msg.index.Partial != "" {
			text += " · the directory was not read to its end: " + msg.index.Partial
		}
		if msg.unread > 0 {
			text += " · " + sourceUnread(msg.unread, msg.late)
		}
		return statusErr(text)
	case len(msg.matches) == 1:
		return a.openSource(msg.index, source.Parse(msg.label), msg.matches[0], msg.unread, msg.late)
	}
	a.overlay = &sourcePicker{app: a, index: msg.index, frame: source.Parse(msg.label), label: short, list: msg.matches}
	return nil
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
// the source directory. unread is how many files that could hold the frame
// were not read: the match is then the first found, and the reader is told.
func (a *app) openSource(ix *source.Index, f source.Frame, m source.Match, unread int, late bool) tea.Cmd {
	abs, err := ix.Abs(m.Path)
	if err != nil {
		return statusErr(oneLine(err.Error()))
	}
	how := sourceHow(f, m)
	if unread > 0 {
		how += "; the first found, " + sourceUnread(unread, late)
	}
	editor := a.ds.source.editor
	if editor.ID == "" {
		yank(sourceWhere(abs, m))
		return status(fmt.Sprintf("copied %s (%s) · no editor is known: DTUI_EDITOR names one (%s, …)",
			oneLine(sourceWhere(m.Path, m)), how, strings.Join(source.EditorIDs()[:4], ", ")))
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
	// The link is handed over and not waited for: whether an editor took it
	// is not known here, so the status says what was done and where the
	// file is, for when nothing opened.
	return status(fmt.Sprintf("asked the system to open %s in %s (%s) · if nothing opened, the file is %s",
		oneLine(sourceWhere(m.Path, m)), editor.Name, how, oneLine(abs)))
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
	app   *app
	index *source.Index
	frame source.Frame
	label string
	list  []source.Match
	sel   int
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
		return status("copied " + oneLine(sourceWhere(m.Path, m)))
	case "enter":
		a.overlay = nil
		return a.openSource(p.index, p.frame, p.list[p.sel], 0, false)
	}
	if moveSel(key, &p.sel, len(p.list)) {
		return nil
	}
	if idx, ok := digitIndex(key, len(p.list)); ok {
		a.overlay = nil
		return a.openSource(p.index, p.frame, p.list[idx], 0, false)
	}
	return nil
}

func (p *sourcePicker) View(width, height int) string {
	inner := max(min(width-theme.OverlayBox.GetHorizontalFrameSize(), 100), 20)
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render(pad("source of "+p.label, inner)) + "\n\n")
	for i, m := range p.list {
		// The end of a path is what tells two files apart.
		row := fmt.Sprintf("%d · %s", i+1, keepEnd(oneLine(sourceWhere(m.Path, m)), inner-5))
		if i == p.sel {
			b.WriteString(theme.Selected.Render(pad(" "+row, inner)))
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(row, inner-1)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + theme.Dim.Render(pad(sourceHow(p.frame, p.list[p.sel]), inner)))
	return centerOverlay(width, height, b.String())
}
