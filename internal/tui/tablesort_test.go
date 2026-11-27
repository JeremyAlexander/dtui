package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// "sort: fetch order" is the order the rows arrived in. Sorting works on a
// copy, so a cycle of J through every column ends where it began; a ranked
// table (hotspots by self samples) is read in that order.
func TestSortCycleRestoresFetchOrder(t *testing.T) {
	a := testApp(t, "logs")
	seedRows(t, a, []map[string]any{{"content": "c"}, {"content": "a"}, {"content": "b"}})
	tv := a.top().(*tableView)
	order := func(rows []map[string]any) string {
		var out []string
		for _, r := range rows {
			out = append(out, catalog.Str(r, "content"))
		}
		return strings.Join(out, "")
	}
	sorted := false
	for range len(tv.columns()) + 1 {
		press(a, key("J"))
		sorted = sorted || order(tv.rows) == "abc"
		if got := order(tv.all); got != "cab" {
			t.Fatalf("sorting by column %d reordered the fetched rows to %s", tv.sortCol, got)
		}
	}
	if !sorted {
		t.Fatal("no column sorted the rows by their content")
	}
	if tv.sortCol != -1 || a.status != "sort: fetch order" || order(tv.rows) != "cab" {
		t.Errorf("after a full cycle: column %d, status %q, rows %s; want fetch order cab", tv.sortCol, a.status, order(tv.rows))
	}
}

// A sort reads each row's key once. A column whose key is built from the
// whole row (a joined call path) would otherwise build it twice per
// comparison, on the UI goroutine.
func TestSortReadsEachKeyOnce(t *testing.T) {
	calls := 0
	spec := &catalog.Spec{Name: "paths", API: "paths", Columns: []catalog.Column{{
		Title: "PATH", Field: "n",
		Sort: func(rec map[string]any) any {
			calls++
			return rec["n"]
		},
	}}}
	a := testApp(t, "home")
	tv := newTableView(a.ds, spec, catalog.Scope{})
	for i := range 300 {
		tv.all = append(tv.all, map[string]any{"n": fmt.Sprintf("row %03d", (i*7919)%300)})
	}
	tv.sortCol, tv.sortDesc = 0, true
	tv.applyFilter()
	if calls != 300 {
		t.Errorf("sorting 300 rows read the sort key %d times, want 300", calls)
	}
	for i, want := range []string{"row 299", "row 298", "row 297"} {
		if got := catalog.Str(tv.rows[i], "n"); got != want {
			t.Errorf("row %d is %s, want %s", i, got, want)
		}
	}
	// Rows without a key sink to the end in either direction.
	tv.all = append([]map[string]any{{"n": ""}}, tv.all...)
	for _, desc := range []bool{true, false} {
		tv.sortDesc = desc
		tv.applyFilter()
		if got := catalog.Str(tv.rows[len(tv.rows)-1], "n"); got != "" {
			t.Errorf("descending=%v: the row without a key is not last (%q is)", desc, got)
		}
	}
}
