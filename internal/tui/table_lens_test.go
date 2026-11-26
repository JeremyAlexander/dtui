package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func lensTable() *tableView {
	spec := &catalog.Spec{
		Name:    "spans",
		Query:   func(catalog.Scope) string { return "fetch spans" },
		Columns: []catalog.Column{{Title: "NAME", Field: "name"}, {Title: "N", Field: "n", Width: 4, Right: true}},
		Lenses: []catalog.Lens{
			{Name: "all", Desc: "every span"},
			{Name: "roots", Desc: "entry spans"},
			{Name: "errors", Desc: "failed spans"},
		},
	}
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }, previewOff: true}
	return newTableView(ds, spec, catalog.Scope{Timeframe: catalog.DefaultTimeframe})
}

// collectMsgs runs cmd and every command a tea.Batch fans out to.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, collectMsgs(c)...)
	}
	return out
}

func lensStatus(t *testing.T, v *tableView, k string) string {
	t.Helper()
	cmd := v.Update(key(k))
	if cmd == nil {
		return ""
	}
	for _, m := range collectMsgs(cmd) {
		if s, ok := m.(statusMsg); ok {
			return s.text
		}
	}
	return ""
}

func TestTableLensCycling(t *testing.T) {
	v := lensTable()
	v.sortCol = 1
	v.cursor = 2
	steps := []struct {
		key  string
		lens int
		msg  string
	}{
		{"]", 1, "lens: roots — entry spans"},
		{"tab", 2, "lens: errors — failed spans"},
		{"]", 0, "lens: all — every span"},      // wraps forward
		{"[", 2, "lens: errors — failed spans"}, // wraps back
		{"shift+tab", 1, "lens: roots — entry spans"},
	}
	for _, s := range steps {
		if got := lensStatus(t, v, s.key); got != s.msg || v.scope.Lens != s.lens {
			t.Fatalf("%s: lens %d status %q, want lens %d %q", s.key, v.scope.Lens, got, s.lens, s.msg)
		}
	}
	if v.sortCol != -1 || v.cursor != 0 {
		t.Errorf("a lens switch resets sort and cursor: sort %d cursor %d", v.sortCol, v.cursor)
	}
	if v.setLens(1, false) != nil || v.setLens(3, false) != nil || v.setLens(-1, false) != nil {
		t.Error("the current or an out-of-range lens is a no-op")
	}
	if got := v.Crumb(); got != "spans·roots" {
		t.Errorf("crumb = %q", got)
	}
	v.setLens(0, false)
	if got := v.Crumb(); got != "spans" {
		t.Errorf("the default lens stays out of the crumb: %q", got)
	}

	plain := colsTable(catalog.Column{Title: "A"})
	for _, k := range []string{"]", "["} {
		if got := lensStatus(t, plain, k); got != "no lens strip on this view" {
			t.Errorf("%s without lenses: %q", k, got)
		}
	}
}

func TestTableRowCountValidity(t *testing.T) {
	v := lensTable()
	if _, ok := v.RowCount(); ok {
		t.Error("no fetch yet: count is not valid")
	}
	v.seq = 1
	v.all = []map[string]any{{"name": "a"}}
	if n, ok := v.RowCount(); n != 1 || !ok {
		t.Errorf("RowCount = %d %v", n, ok)
	}
	v.loading = true
	if _, ok := v.RowCount(); ok {
		t.Error("loading: count is not valid")
	}
}

func TestTableEmptyStates(t *testing.T) {
	v := lensTable()
	body := func() string { return ansi.Strip(v.View(100, 12)) }

	if b := body(); !strings.Contains(b, "∅ no data in timeframe (last 2h) — [/] switches lens") {
		t.Errorf("the all lens reads as data:\n%s", b)
	}
	v.scope.Lens = 2
	if b := body(); !strings.Contains(b, "∅ no errors in timeframe (last 2h) — [/] switches lens") {
		t.Errorf("a named lens names what is missing:\n%s", b)
	}
	v.all = []map[string]any{{"name": "a"}, {"name": "b"}}
	v.setFilter("zzz")
	if b := body(); !strings.Contains(b, `no rows match "zzz" (2 fetched — esc clears the filter)`) {
		t.Errorf("filter miss:\n%s", b)
	}
	v.loading = true
	if strings.Contains(body(), "no rows match") {
		t.Error("no empty state while loading")
	}

	plain := colsTable(catalog.Column{Title: "A"})
	plain.scope.Timeframe = catalog.DefaultTimeframe
	if b := ansi.Strip(plain.View(80, 10)); !strings.Contains(b, "∅ no data in timeframe (last 2h)") || strings.Contains(b, "switches lens") {
		t.Errorf("plain empty state:\n%s", b)
	}
}

func TestTableHeaderAndRows(t *testing.T) {
	v := lensTable()
	v.lensDigits = true
	v.all = []map[string]any{{"name": "alpha", "n": 3.0}, {"name": "beta", "n": 12.0}, {"name": "gamma", "n": 1.0}}
	v.applyFilter()
	v.sortCol, v.sortDesc = 1, true
	v.applyFilter()
	v.cursor = 1
	assertSnapshot(t, "table-lens-60x8", ansi.Strip(v.View(60, 8)))
	// Two visible rows: the third is cut, and no trailing newline follows
	// the last one.
	out := ansi.Strip(v.View(60, 5))
	if strings.Contains(out, "gamma") || strings.HasSuffix(out, "\n") {
		t.Errorf("visible rows must stop at the height:\n%q", out)
	}
}

// emptyLine is the line of a rendered table that carries its empty message.
func emptyLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "∅") {
			return strings.TrimRight(line, " ")
		}
	}
	t.Fatalf("no empty message in:\n%s", out)
	return ""
}

// The empty message of a table sits in the middle of the body, in every view:
// a DQL view, one with lenses, an API view.
func TestEmptyMessageIsCentred(t *testing.T) {
	for _, tc := range []struct {
		view  string
		width int
		want  string
	}{
		{"hosts", 100, strings.Repeat(" ", 34) + "∅ no data in timeframe (last 2h)"},
		{"hosts", 81, strings.Repeat(" ", 24) + "∅ no data in timeframe (last 2h)"},
		{"slos", 100, strings.Repeat(" ", 34) + "∅ no data in timeframe (last 2h)"},
		{"traces", 100, strings.Repeat(" ", 23) + "∅ no roots in timeframe (last 2h) — [/] switches lens"},
	} {
		v := newTableView(&dataSource{previewOff: true}, catalog.Lookup(tc.view), catalog.Scope{Timeframe: catalog.DefaultTimeframe})
		if got := emptyLine(t, ansi.Strip(v.View(tc.width, 12))); got != tc.want {
			t.Errorf("%s at %d columns:\n got %q\nwant %q", tc.view, tc.width, got, tc.want)
		}
	}
}

// A message wider than the body wraps inside it instead of being cut.
func TestLongEmptyMessageWraps(t *testing.T) {
	spec := &catalog.Spec{Name: "t", Columns: []catalog.Column{{Title: "A", Field: "a"}},
		Empty: func(catalog.Scope, map[string]any) string {
			return "nothing was sampled in this window because the process was idle"
		}}
	v := newTableView(&dataSource{previewOff: true}, spec, catalog.Scope{})
	lines := strings.Split(strings.TrimRight(ansi.Strip(v.View(40, 12)), "\n "), "\n")
	got := []string{strings.TrimRight(lines[len(lines)-2], " "), strings.TrimRight(lines[len(lines)-1], " ")}
	want := []string{"  ∅ nothing was sampled in this window", "  because the process was idle"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("wrapped empty message:\n got %q\nwant %q", got, want)
	}
	if got := emptyLine(t, ansi.Strip(v.View(100, 12))); got != strings.Repeat(" ", 17)+"∅ nothing was sampled in this window because the process was idle" {
		t.Errorf("the same message where it fits is centred, got %q", got)
	}
}
