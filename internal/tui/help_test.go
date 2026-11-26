package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The help fits the terminal at every size: no line is wider than it, the
// screen is exactly as tall, and what is not in view is reached by scrolling.
func TestHelpFitsTheTerminal(t *testing.T) {
	a := testApp(t, "services")
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {120, 40}, {200, 60}, {300, 120}} {
		width, height := size[0], size[1]
		a.Update(tea.WindowSizeMsg{Width: width, Height: height})
		press(a, key("?"))
		h := overlayAs[*helpOverlay](t, a)
		seen := ""
		for range 400 {
			out := screen(a)
			lines := strings.Split(out, "\n")
			if len(lines) != height {
				t.Fatalf("%dx%d: the help is %d lines tall", width, height, len(lines))
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > width {
					t.Fatalf("%dx%d: line %d of the help is %d cells wide:\n%s", width, height, i, w, out)
				}
			}
			seen += out
			if !h.more || strings.Contains(out, fmt.Sprintf("to %d of %d", h.top+h.page, h.top+h.page)) {
				break
			}
			press(a, key("j"))
		}
		// The first and the last entry, and one from the middle whose
		// description is longer than any terminal here is wide.
		for _, want := range []string{"command bar", "global bookmarks", "restores all bookmarks", "quit", "views: home"} {
			if !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(seen, "│", " ")), " "), want) {
				t.Errorf("%dx%d: scrolling through the help never showed %q", width, height, want)
			}
		}
		if h.more {
			press(a, key("G"))
			press(a, key("g"))
			if h.top != 0 || a.overlay == nil {
				t.Errorf("%dx%d: G and g scroll the help: top %d", width, height, h.top)
			}
		}
		press(a, key("x"))
		if a.overlay != nil {
			t.Fatalf("%dx%d: a key that does not scroll did not close the help", width, height)
		}
	}
}

// Where all of the help is in view, any key closes it, the scroll keys
// included: as it always was.
func TestHelpClosesOnAnyKeyWhenItFits(t *testing.T) {
	a := testApp(t, "services")
	a.Update(tea.WindowSizeMsg{Width: 300, Height: 120})
	for _, k := range []string{"j", "k", "pgdown", "g", "G", "?", "q", "esc"} {
		press(a, key("?"))
		h := overlayAs[*helpOverlay](t, a)
		if screen(a); h.more {
			t.Fatal("the help does not fit 300x120")
		}
		press(a, key(k))
		if a.overlay != nil {
			t.Errorf("%s did not close a help that is all in view", k)
		}
	}
}
