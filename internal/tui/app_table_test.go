package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func TestSortKeysCycleAndToggle(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{
		podRow("a", "ns1", "Running", 3),
		podRow("b", "ns1", "Running", 31),
		podRow("c", "ns1", "Running", 0),
	})
	tv := a.top().(*tableView)

	// J to the RST column (index 3): numeric column defaults descending.
	for range 4 {
		press(a, key("J"))
	}
	if tv.sortCol != 3 || !tv.sortDesc {
		t.Fatalf("sortCol = %d desc = %v, want 3/desc", tv.sortCol, tv.sortDesc)
	}
	if catalog.Str(tv.rows[0], "name") != "b" {
		t.Errorf("rows not sorted by restarts desc: %v", tv.rows)
	}

	// K toggles direction.
	press(a, key("K"))
	if tv.sortDesc || catalog.Str(tv.rows[0], "name") != "c" {
		t.Errorf("K should flip to ascending, first = %s", catalog.Str(tv.rows[0], "name"))
	}
}
func TestSpanLensSwitching(t *testing.T) {
	a := testApp(t, "traces")
	tv := a.top().(*tableView)
	if !strings.Contains(tv.dql, "| filter isNull(span.parent_id)") {
		t.Fatalf("traces should open on the roots lens:\n%s", tv.dql)
	}

	// ] cycles forward, [ back; the crumb names any non-default lens.
	press(a, key("]"))
	if a.top() != tv {
		t.Fatalf("] on a lensed table must switch lens, not views (top = %T)", a.top())
	}
	if !strings.Contains(tv.dql, `span.status_code == "error"`) {
		t.Errorf("lens errors not composed:\n%s", tv.dql)
	}
	if tv.Crumb() != "traces·errors" {
		t.Errorf("crumb = %q, want traces·errors", tv.Crumb())
	}
	if got := tv.columns()[2].Title; got != "ERROR" {
		t.Errorf("errors lens column[2] = %q, want ERROR (the minimal why)", got)
	}
	// The exceptions lens: a string-match filter (iterative expressions are
	// rejected inside DQL filter) and its own thrown-first columns.
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span_event.name`) {
		t.Errorf("] should advance to exceptions lens:\n%s", tv.dql)
	}
	if got := tv.columns()[1].Title; got != "EXCEPTION" {
		t.Errorf("exceptions lens column[1] = %q, want EXCEPTION", got)
	}
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span.kind == "server"`) {
		t.Errorf("] should advance to server lens:\n%s", tv.dql)
	}
	press(a, key("["))
	if !strings.Contains(tv.dql, `span_event.name`) {
		t.Errorf("[ should return to exceptions lens:\n%s", tv.dql)
	}

	// The db lens swaps in its curated statement columns.
	for range 3 { // exceptions → server → client → db
		press(a, key("]"))
	}
	if got := tv.columns()[1].Title; got != "STATEMENT" {
		t.Errorf("db lens column[1] = %q, want STATEMENT", got)
	}

	// The category lenses carry their own columns too.
	for range 2 { // db → rpc → messaging
		press(a, key("]"))
	}
	if got := tv.columns()[1].Title; got != "DESTINATION" {
		t.Errorf("messaging lens column[1] = %q, want DESTINATION", got)
	}

	// Digits never touch the lens strip: they stay global hotkeys.
	press(a, key("5"))
	if logs, ok := a.top().(*tableView); !ok || logs.spec.Name != "logs" {
		t.Fatalf("digit 5 should jump to logs, top = %v", a.top().Crumb())
	}
	press(a, key("0"))
	if _, ok := a.top().(*homeView); !ok {
		t.Fatalf("digit 0 should stay a hotkey jump to home, top = %T", a.top())
	}
}

// TestTabCyclesLensOnPlainTable: tab drives the view's primary strip — the
// lens strip on a top-level table — while digits stay global bookmarks there
// (no numbers on screen means no local claim).
func TestTabCyclesLensOnPlainTable(t *testing.T) {
	a := testApp(t, "traces")
	tv := a.top().(*tableView)
	start := tv.scope.Lens
	press(a, key("tab"))
	if tv.scope.Lens == start {
		t.Fatal("tab should advance the lens strip on a plain table")
	}
	press(a, key("shift+tab"))
	if tv.scope.Lens != start {
		t.Fatal("shift+tab should cycle the lens strip back")
	}
	press(a, key("1"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "problems" {
		t.Fatalf("digit on a top-level table must stay a global bookmark, top = %T", a.top())
	}
}
func TestAPIViewRoutesThroughSource(t *testing.T) {
	a := testApp(t, "home")
	var gotScope catalog.Scope
	a.ds.sources = map[string]Source{
		"slos": func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error) {
			gotScope = scope
			return []map[string]any{{"name": "checkout availability", "status": "SUCCESS", "sli": 99.98, "target": 99.9}}, nil
		},
	}
	tv := newTableView(a.ds, catalog.Lookup("slos"), catalog.Scope{Timeframe: a.tf})
	cmd := tv.Refresh()
	if cmd == nil {
		t.Fatal("API view Refresh returned no command")
	}
	msg := cmd()
	dm, ok := msg.(dataMsg)
	if !ok {
		t.Fatalf("source result = %T", msg)
	}
	tv.Update(dm)
	if len(tv.rows) != 1 || catalog.Str(tv.rows[0], "name") != "checkout availability" {
		t.Fatalf("rows = %v", tv.rows)
	}
	if gotScope.Timeframe.Label != a.tf.Label {
		t.Errorf("source received scope %+v", gotScope)
	}
	if echo := tv.Echo(); echo != "dtctl get slos" {
		t.Errorf("echo = %q", echo)
	}
	// Facets need fieldsSummary — refused on API views with a status message.
	if cmd := tv.handleKey(key("f")); cmd == nil {
		t.Error("facets on an API view must answer with a status")
	} else if sm, ok := cmd().(statusMsg); !ok || !sm.isErr {
		t.Errorf("facets on an API view = %v", cmd())
	}
}
func TestAPIViewMissingSourceErrors(t *testing.T) {
	a := testApp(t, "home")
	a.ds.sources = nil
	tv := newTableView(a.ds, catalog.Lookup("slos"), catalog.Scope{Timeframe: a.tf})
	msg := tv.Refresh()()
	dm, ok := msg.(dataMsg)
	if !ok || dm.err == nil || !strings.Contains(dm.err.Error(), "not wired") {
		t.Fatalf("missing source must surface an error, got %#v", msg)
	}
}

// A result is rows from its first record on. A summary record is something a
// source sends only to a view whose spec reads one (Caption, Empty, Footnote),
// so a field that happens to be called __summary costs no other view a row.
func TestResultKeepsARowWithASummaryField(t *testing.T) {
	for _, marker := range []any{true, false, "weekly"} {
		a := testApp(t, "logs")
		seedRows(t, a, []map[string]any{
			{"content": "first", catalog.SummaryKey: marker},
			{"content": "second"},
		})
		tv := a.top().(*tableView)
		if len(tv.all) != 2 || len(tv.rows) != 2 || tv.summary != nil {
			t.Errorf("logs, __summary=%v: %d fetched, %d shown, summary %v; want 2, 2, none", marker, len(tv.all), len(tv.rows), tv.summary)
		}

		// An API view that reads no summary keeps the record too.
		a = testApp(t, "slos")
		seedRows(t, a, []map[string]any{
			{"name": "checkout availability", catalog.SummaryKey: marker},
			{"name": "search latency"},
		})
		tv = a.top().(*tableView)
		if len(tv.all) != 2 || tv.summary != nil {
			t.Errorf("slos, __summary=%v: %d fetched, summary %v; want 2, none", marker, len(tv.all), tv.summary)
		}
	}

	// The query escape hatch derives its columns from the first record.
	a := testApp(t, "home")
	_, cmd := a.Update(queryMsg{dql: "data record(__summary = true, n = 1), record(n = 2)"})
	deliver(a, cmd)
	qv := a.top().(*queryView)
	qv.results.Update(dataMsg{owner: qv.results, seq: qv.results.seq, records: []map[string]any{
		{catalog.SummaryKey: true, "n": 1.0}, {"n": 2.0},
	}})
	if len(qv.results.rows) != 2 {
		t.Errorf(":query showed %d of 2 records", len(qv.results.rows))
	}
}

// Only the marker a source sets makes a leading record the summary.
func TestSummaryIsReadOnlyWhereASpecAsksForIt(t *testing.T) {
	spec := &catalog.Spec{Name: "t", API: "t", Columns: []catalog.Column{{Title: "A", Field: "a"}},
		Caption: func(_ catalog.Scope, summary map[string]any) []string {
			return []string{"total " + catalog.Str(summary, "total")}
		}}
	v := newTableView(&dataSource{previewOff: true}, spec, catalog.Scope{})
	answer := func(records ...map[string]any) {
		v.seq++
		v.Update(dataMsg{owner: v, seq: v.seq, records: records})
	}
	answer(map[string]any{catalog.SummaryKey: true, "total": "7"}, map[string]any{"a": "x"})
	if len(v.all) != 1 || catalog.Str(v.summary, "total") != "7" {
		t.Fatalf("a marked record is the summary: %d rows, summary %v", len(v.all), v.summary)
	}
	if out := ansi.Strip(v.View(60, 10)); !strings.Contains(out, " total 7\n") {
		t.Errorf("the caption reads the summary:\n%s", out)
	}
	answer(map[string]any{catalog.SummaryKey: "no", "a": "x"}, map[string]any{"a": "y"})
	if len(v.all) != 2 || v.summary != nil {
		t.Errorf("a field of that name is not the marker: %d rows, summary %v", len(v.all), v.summary)
	}
}

// A view without a worded wait keeps what it shows while it asks again, in
// every case a slow view drops it: a lens switch, a timeframe switch, a
// refresh that fails.
func TestOrdinaryViewKeepsItsRowsWhileItRefetches(t *testing.T) {
	a := testApp(t, "traces")
	seedRows(t, a, []map[string]any{{"span.name": "GET /cart"}, {"span.name": "GET /checkout"}})
	tv := a.top().(*tableView)

	press(a, key("]"))
	if !tv.loading || len(tv.rows) != 2 {
		t.Errorf("lens switch: loading=%v, %d rows; want the 2 rows kept", tv.loading, len(tv.rows))
	}
	deliver(a, a.setTimeframe(catalog.Timeframe{Label: "30m", Dur: 30 * time.Minute}))
	if !tv.loading || len(tv.rows) != 2 {
		t.Errorf("timeframe switch: loading=%v, %d rows; want the 2 rows kept", tv.loading, len(tv.rows))
	}
	tv.Update(dataMsg{owner: tv, seq: tv.seq, err: errors.New("boom")})
	if len(tv.all) != 2 {
		t.Errorf("a failed refresh dropped the rows of an ordinary view: %d left", len(tv.all))
	}
}
