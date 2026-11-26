package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// helpOverlay is the '?' key reference. It is wrapped to the terminal and,
// where it is longer than the screen, scrolled with the movement keys; any
// other key dismisses it, and any key at all when everything is in view.
type helpOverlay struct {
	app *app
	// top is the first line in view, and more whether the last View had
	// lines it could not show.
	top  int
	more bool
	page int
}

func (a *app) openHelp() tea.Cmd {
	a.overlay = &helpOverlay{app: a}
	return nil
}

func (h *helpOverlay) Hints() []keyHint { return nil }

func (h *helpOverlay) HandleKey(msg tea.KeyMsg) tea.Cmd {
	if h.more || h.top > 0 {
		switch msg.String() {
		case "down", "j":
			h.top++
			return nil
		case "up", "k":
			h.top--
			return nil
		case "pgdown", " ", "ctrl+f":
			h.top += max(h.page, 1)
			return nil
		case "pgup", "ctrl+b":
			h.top -= max(h.page, 1)
			return nil
		case "g", "home":
			h.top = 0
			return nil
		case "G", "end":
			h.top = 1 << 30
			return nil
		}
	}
	h.app.overlay = nil
	return nil
}

// helpKeyWidth is the column the keys of the help are set in.
const helpKeyWidth = 12

func (h *helpOverlay) View(width, height int) string {
	// What the box's border and padding leave of the body.
	inner := max(width-theme.OverlayBox.GetHorizontalFrameSize(), 20)
	rows := max(height-theme.OverlayBox.GetVerticalFrameSize(), 3)
	entry := func(k keyHint) string {
		lines := wrapLines(k.Desc, max(inner-helpKeyWidth-3, 10))
		out := fmt.Sprintf("  %s %s", theme.KeyHint.Render(fmt.Sprintf("%-*s", helpKeyWidth, k.Key)), lines[0])
		for _, line := range lines[1:] {
			out += "\n" + strings.Repeat(" ", helpKeyWidth+3) + line
		}
		return out + "\n"
	}
	sections := []struct {
		title string
		keys  []keyHint
	}{
		{"Navigation", []keyHint{
			{":", "command bar — fuzzy view names, args filter (:pods checkout, :trace <id>, :query <dql>, :nav <type|id|name>, :ctx [name] — bare :ctx opens a picker)"},
			{"enter", "detail / drill into children / follow entity link / expand value / waterfall / session timeline"},
			{"0-9", "global bookmarks: 0 home · 1 problems · 2 services · 3 hosts · 4 pods · 5 logs · 6 traces · 7 workloads · 8 events · 9 aws — on an entered page 1-9 address the innermost numbered strip: its tabs, or the active tab's lens strip when it shows one (0 still jumps home, esc restores all bookmarks)"},
			{"esc / -", "back / toggle last two views"},
			{"tab", "cycle the view's primary strip: lens strip on tables · tabs on detail pages · panels on home · functions and flame graph on :hotspots"},
			{"[ / ]", "cycle the lens strip explicitly (the only way for a strip nested inside a detail tab)"},
			{"P", "preview pane on/off — the peek at the selected row is on by default and auto-hides on cramped terminals"},
			{"H", "history — restore a previous page (survives restarts)"},
			{"/", "filter table (live) — enter adds it as a server-side search, alt+enter replaces; a later jump back to this view restores the last server filter"},
			{"f / F", "facet manager: add attribute=value filters (fieldsSummary top values, * patterns), edit/remove each / clear all (F also forgets this view's last filter for later jumps)"},
			{"W", "save a named preset from any DQL table that has facets (empty facet-manager input or the table; a leading W in the attribute search saves instead of typing)"},
			{":preset", "preset list (name, view, personal/workspace, filters); enter or 1-9 opens, d deletes a personal save; :preset <name> skips the list"},
			{"J/K", "sort column/direction"},
			{"j/k ↑/↓ g/G", "move"},
			{"pgup/pgdn", "page jump (ctrl+d/u half page in inspectors)"},
		}},
		{"Drill-down (pre-scoped to selection; on detail pages the letters jump to the matching tab)", []keyHint{
			{"l", "logs"},
			{"s", "traces (spans) / jump to a log's or RUM event's trace"},
			{"m", "metrics — canned charts, or the metric explorer for other types"},
			{"p", "problems"},
			{"v", "events"},
			{"a", "log patterns — Davis clustering of the current logs (enter: records with the pattern's fields parsed out)"},
			{"h", "hotspots of a process (code-level profiling: thread states and allocations as lenses) — on a :processes row"},
			{"u", "sessions of a frontend / session timeline of a RUM event"},
			{"e", "user events of a frontend / of a session"},
			{"x / X", "smartscape navigator — walk the topology from the selection (:nav); one-hop relations live on the detail page's related tab"},
			{"d", "describe / details"},
		}},
		{"Scope & actions", []keyHint{
			{".", "pin selection as global scope (ctrl+x unpins)"},
			{"t", "timeframe picker — presets or a custom relative window (45m, 12h, 3d)"},
			{"S", "segments — up to 10 filter segments applied to every DQL view (:segments); v picks variable values; a .dtui.yaml in the project pre-selects them"},
			{"alt+s", "segments on/off — suspend the applied set for the unfiltered picture, restore it with bindings intact"},
			{"ctrl+q", "reveal query — this view's DQL in the editor"},
			{"o", "open in the Dynatrace UI — a picker appears when several targets apply"},
			{"y / c", "yank id / copy CLI command"},
		}},
		{"Global", []keyHint{
			{"r / R", "refresh / cycle auto-refresh"},
			{"?", "help"},
			{"q", "quit"},
		}},
	}
	section := func(title string) string {
		lines := wrapLines(title, max(inner-4, 10))
		out := theme.Section(lines[0])
		for _, line := range lines[1:] {
			out += "\n    " + theme.HeaderVal.Render(line)
		}
		return out
	}
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("dtctl tui — keys") + "\n")
	if _, onFlame := h.app.top().(*flameView); onFlame {
		// The overlay is longer than most terminals: a view with keys of
		// its own has them where they are sure to be read.
		b.WriteString("\n" + section("Flame graph (tab returns to the functions)") + "\n")
		for _, k := range flameHelp {
			b.WriteString(entry(k))
		}
	}
	for _, s := range sections {
		b.WriteString("\n" + section(s.title) + "\n")
		for _, k := range s.keys {
			b.WriteString(entry(k))
		}
	}
	b.WriteString("\n" + theme.Dim.Render(wrap("views: home · query · nav · "+strings.Join(catalog.Names(), " · "), min(76, inner))))

	lines := strings.Split(b.String(), "\n")
	h.page = rows - 1
	h.top = max(min(h.top, len(lines)-h.page), 0)
	h.more = len(lines) > rows
	if h.more {
		// One line of the box says where the reader is and how to move.
		note := fmt.Sprintf("lines %d to %d of %d · j/k pgdn/pgup scroll · any other key closes",
			h.top+1, min(h.top+h.page, len(lines)), len(lines))
		lines = append(lines[h.top:min(h.top+h.page, len(lines)):len(lines)], theme.Dim.Render(ansi.Truncate(note, inner, "…")))
	}
	return centerOverlay(width, height, strings.Join(lines, "\n"))
}
