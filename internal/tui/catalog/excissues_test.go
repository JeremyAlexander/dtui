package catalog

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// issueSpan is one projected record of the exceptions view: the fields its
// query keeps, with one OneAgent-style Java exception event.
func issueSpan(n int, start, service, typ, message, stack string) map[string]any {
	return map[string]any{
		"start_time":            start,
		"span.id":               fmt.Sprintf("%016x", n),
		"trace.id":              fmt.Sprintf("%032x", n),
		"span.name":             "POST /api/cart",
		"endpoint.name":         "/api/cart",
		"dt.service.name":       service,
		"service.name":          nil,
		"dt.smartscape.service": "SERVICE-0000000000000A01",
		"span.events": []any{map[string]any{
			"span_event.name":       "exception",
			"exception.type":        typ,
			"exception.message":     message,
			"exception.stack_trace": stack,
		}},
	}
}

const (
	cartLockedStack = "com.example.shop.cart.CartService.addItem (CartService.java:120)\n" +
		"com.example.shop.web.CartController.post (CartController.java:48)"
	// The same bug after a deploy moved both lines.
	cartLockedStackMoved = "com.example.shop.cart.CartService.addItem (CartService.java:131)\n" +
		"com.example.shop.web.CartController.post (CartController.java:52)"
	ordersStack = "com.example.shop.db.Orders.load (Orders.java:10)"
)

func issuesOf(t *testing.T, rows []map[string]any) []ExceptionIssue {
	t.Helper()
	out := make([]ExceptionIssue, len(rows))
	for i, row := range rows {
		issue, ok := IssueOf(row)
		if !ok {
			t.Fatalf("row %d is not an issue row: %v", i, row)
		}
		out[i] = issue
	}
	return out
}

func TestExceptionsQueryShape(t *testing.T) {
	tf := Timeframe{Label: "2h"}
	dql := exceptionsSpec.Query(Scope{Timeframe: tf})
	want := "fetch spans, from:now() - 2h\n" +
		"| filter iAny(span.events[][span_event.name] == \"exception\")\n" +
		"| sort start_time desc\n" +
		"| limit 1000\n" +
		"| fields start_time, span.id, trace.id, span.name, endpoint.name, dt.service.name, service.name, dt.smartscape.service, span.events"
	if dql != want {
		t.Errorf("unscoped query =\n%s\nwant\n%s", dql, want)
	}

	svc := Entity{ID: "SERVICE-0000000000000A01", Name: "cart", Type: "SERVICE"}
	scoped := exceptionsSpec.Query(Scope{Timeframe: tf, Entity: &svc})
	if !strings.Contains(scoped, "\n| filter dt.smartscape.service == toSmartscapeId(\"SERVICE-0000000000000A01\")\n| filter iAny(") {
		t.Errorf("a service scope must filter before the exception filter:\n%s", scoped)
	}
	if !exceptionsSpec.CanScope(tf, svc) {
		t.Error("a service pin must scope the view")
	}
	// Spans carry no scope field for a frontend: the pin must not be claimed.
	if exceptionsSpec.CanScope(tf, Entity{ID: "FRONTEND-1", Type: "FRONTEND"}) {
		t.Error("a frontend pin cannot scope spans")
	}
}

// The limit in the query is the sample's row cap: the header's "limit hit"
// compares the fetched count to it.
func TestExceptionsQueryLimitIsTheSampleCap(t *testing.T) {
	dql := exceptionsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h"}})
	if want := fmt.Sprintf("\n| limit %d\n", ExceptionSampleRows); !strings.Contains(dql, want) {
		t.Errorf("query does not carry %q:\n%s", want, dql)
	}
	if exceptionsSpec.Sample.Rows != 1000 || exceptionsSpec.Sample.Bytes != 8*1024*1024 {
		t.Errorf("sample caps = %d rows, %d bytes, want 1000 rows and 8 MB",
			exceptionsSpec.Sample.Rows, exceptionsSpec.Sample.Bytes)
	}
}

// A facet narrows what is sampled: it must land before the sort and the
// limit, on the whole span, not on the projected fields after them.
func TestExceptionsQueryNarrowsBeforeSampling(t *testing.T) {
	dql := ComposeQuery(exceptionsSpec.Query(Scope{Timeframe: Timeframe{Label: "2h"}}),
		[]string{"timeout"}, []Facet{{Field: "k8s.namespace.name", Value: "shop"}}, true)
	lines := strings.Split(dql, "\n")
	at := func(prefix string) int {
		for i, l := range lines {
			if strings.HasPrefix(l, prefix) {
				return i
			}
		}
		t.Fatalf("no line starts with %q in:\n%s", prefix, dql)
		return -1
	}
	facet, search := at("| filter k8s.namespace.name"), at("| search")
	sortAt, limit, fields := at("| sort"), at("| limit"), at("| fields ")
	if !(search < facet && facet < sortAt && sortAt < limit && limit < fields) || fields != len(lines)-1 {
		t.Errorf("stage order is wrong (search %d, facet %d, sort %d, limit %d, fields %d of %d):\n%s",
			search, facet, sortAt, limit, fields, len(lines), dql)
	}
}

func TestGroupExceptionIssuesGroupsByKey(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:59:58.000000000Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", cartLockedStack),
		issueSpan(2, "2026-07-15T11:59:50.000000000Z", "orders", "java.sql.SQLException", "connection refused", ordersStack),
		issueSpan(3, "2026-07-15T11:59:40.000000000Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", cartLockedStackMoved),
	}
	rows := GroupExceptionIssues(spans)
	issues := issuesOf(t, rows)
	if len(issues) != 2 {
		t.Fatalf("issues = %d, want 2: %+v", len(issues), issues)
	}

	locked := issues[0]
	if locked.Count != 2 || locked.Type != "java.lang.IllegalStateException" {
		t.Errorf("first issue = %+v, want the cart lock with 2 spans", locked)
	}
	if !strings.HasPrefix(locked.Key, "e1:") || len(locked.Key) != 15 {
		t.Errorf("key = %q, want e1: and 12 hex characters", locked.Key)
	}
	if locked.Rung != "app frames" {
		t.Errorf("rung = %q, want app frames", locked.Rung)
	}
	if want := []string{"com.example.shop.cart.CartService.addItem", "com.example.shop.web.CartController.post"}; !reflect.DeepEqual(locked.Frames, want) {
		t.Errorf("frames = %q, want %q", locked.Frames, want)
	}
	if locked.Message != "cart <n> is locked" {
		t.Errorf("message = %q, want the parameterised message", locked.Message)
	}
	if want := []string{"cart", "checkout"}; !reflect.DeepEqual(locked.Services, want) {
		t.Errorf("services = %q, want %q", locked.Services, want)
	}
	if locked.Newest != "2026-07-15T11:59:58.000000000Z" || locked.Oldest != "2026-07-15T11:59:40.000000000Z" {
		t.Errorf("newest/oldest = %q / %q", locked.Newest, locked.Oldest)
	}

	// The row stands for its newest span: enter opens that span's trace.
	if got := Str(rows[0], "trace.id"); got != fmt.Sprintf("%032x", 1) {
		t.Errorf("row trace.id = %q, want the newest member's", got)
	}
	if Str(rows[0], "span.id") != fmt.Sprintf("%016x", 1) || Str(rows[0], "start_time") != locked.Newest {
		t.Errorf("row span.id/start_time = %q / %q, want the newest member's",
			Str(rows[0], "span.id"), Str(rows[0], "start_time"))
	}
	if exceptionsSpec.Trace(rows[0]) != Str(rows[0], "trace.id") {
		t.Error("the spec's Trace must read the row's trace id")
	}

	if issues[1].Count != 1 || issues[1].Type != "java.sql.SQLException" || issues[1].Key == locked.Key {
		t.Errorf("second issue = %+v", issues[1])
	}
}

// Loudest first; among equally loud issues the one seen last; then the key,
// so two refreshes of one sample never swap rows.
func TestGroupExceptionIssuesOrder(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:01Z", "a", "TypeOld", "", ""),
		issueSpan(2, "2026-07-15T11:00:03Z", "a", "TypeNew", "", ""),
		issueSpan(3, "2026-07-15T11:00:02Z", "a", "TypeLoud", "", ""),
		issueSpan(4, "2026-07-15T11:00:00Z", "a", "TypeLoud", "", ""),
		issueSpan(5, "2026-07-15T11:00:03Z", "a", "TypeTie", "", ""),
	}
	var got []string
	keys := map[string]string{}
	for _, issue := range issuesOf(t, GroupExceptionIssues(spans)) {
		got = append(got, issue.Type)
		keys[issue.Type] = issue.Key
	}
	tied := []string{"TypeNew", "TypeTie"}
	if keys["TypeNew"] > keys["TypeTie"] {
		tied = []string{"TypeTie", "TypeNew"}
	}
	want := append(append([]string{"TypeLoud"}, tied...), "TypeOld")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// Grail trims trailing zeros of a timestamp's fraction, so the newest member
// is found by instant, not by comparing the text.
func TestGroupExceptionIssuesComparesInstants(t *testing.T) {
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "", ""),
		issueSpan(2, "2026-07-15T11:00:00.5Z", "a", "T", "", ""),
		issueSpan(3, "2026-07-15T10:59:59.25Z", "a", "T", "", ""),
	}
	issue := issuesOf(t, GroupExceptionIssues(spans))[0]
	if issue.Newest != "2026-07-15T11:00:00.5Z" || issue.Oldest != "2026-07-15T10:59:59.25Z" {
		t.Errorf("newest/oldest = %q / %q", issue.Newest, issue.Oldest)
	}
}

// Every fetched span is counted in exactly one row: the header's number and
// the sum of the counts are the same number.
func TestGroupExceptionIssuesCountsEverySpan(t *testing.T) {
	silent := issueSpan(3, "2026-07-15T11:00:02Z", "a", "", "", "")
	noEvents := map[string]any{"start_time": "2026-07-15T11:00:05Z", "span.id": "s", "trace.id": "t"}
	spans := []map[string]any{
		issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "", ""),
		issueSpan(2, "2026-07-15T11:00:01Z", "a", "T", "", ""),
		silent, noEvents,
	}
	issues := issuesOf(t, GroupExceptionIssues(spans))
	total := 0
	for _, issue := range issues {
		total += issue.Count
	}
	if total != len(spans) {
		t.Errorf("counts add up to %d, want %d", total, len(spans))
	}
	last := issues[len(issues)-1]
	if last.Key != "" || last.Count != 2 || last.Rung != "" {
		t.Errorf("spans whose events say nothing = %+v, want one keyless row of 2", last)
	}
}

func TestGroupExceptionIssuesEmpty(t *testing.T) {
	if rows := GroupExceptionIssues(nil); len(rows) != 0 {
		t.Errorf("no spans grouped into %d rows", len(rows))
	}
}

// Grouping runs off the update loop on records nothing else holds yet, and
// its rows must not keep them alive: it reads, and writes nothing back.
func TestGroupExceptionIssuesLeavesTheSpansAlone(t *testing.T) {
	span := issueSpan(1, "2026-07-15T11:00:00Z", "a", "T", "m", cartLockedStack)
	keys := len(span)
	rows := GroupExceptionIssues([]map[string]any{span})
	if len(span) != keys {
		t.Errorf("grouping added keys to the span: %v", span)
	}
	if _, ok := rows[0]["span.events"]; ok {
		t.Error("an issue row carries the span's events")
	}
}

func TestIssueOfOtherRecords(t *testing.T) {
	if _, ok := IssueOf(exceptionSpan()); ok {
		t.Error("a span is not an issue row")
	}
	if _, ok := IssueOf(nil); ok {
		t.Error("nil is not an issue row")
	}
}

func TestExceptionIssueColumns(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", cartLockedStack),
		issueSpan(2, "2026-07-15T11:59:40Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", cartLockedStackMoved),
		issueSpan(3, "2026-07-15T11:59:30Z", "checkout", "", "", ""),
	})
	cell := func(row map[string]any, title string) string {
		for _, c := range exceptionsSpec.Columns {
			if c.Title == title {
				return c.Text(row)
			}
		}
		t.Fatalf("no column %q", title)
		return ""
	}
	for title, want := range map[string]string{
		"COUNT":     "2",
		"EXCEPTION": "lang.IllegalStateException",
		"MESSAGE":   "cart <n> is locked",
		"SERVICE":   "cart +1",
		"BY":        "app",
	} {
		if got := cell(rows[0], title); got != want {
			t.Errorf("%s = %q, want %q", title, got, want)
		}
	}
	if got := cell(rows[1], "EXCEPTION"); got != "(nothing recorded)" {
		t.Errorf("keyless EXCEPTION = %q", got)
	}
	// Neither a message nor a frame: the cell is empty.
	if got := cell(rows[1], "MESSAGE"); got != "" {
		t.Errorf("keyless MESSAGE = %q", got)
	}
	// COUNT sorts as a number, LAST as the instant.
	for _, c := range exceptionsSpec.Columns {
		switch c.Title {
		case "COUNT":
			if c.Sort(rows[0]) != 2 {
				t.Errorf("COUNT sort key = %v, want 2", c.Sort(rows[0]))
			}
		case "LAST":
			if c.Sort(rows[0]) != "2026-07-15T11:59:58Z" {
				t.Errorf("LAST sort key = %v", c.Sort(rows[0]))
			}
		}
	}
}

// A message with no frames to show falls back to the throw site, so a row
// never has an empty middle.
func TestExceptionIssueMessageColumnFallsBackToTheThrowSite(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "", cartLockedStack),
	})
	for _, c := range exceptionsSpec.Columns {
		if c.Title == "MESSAGE" {
			if got := c.Text(rows[0]); got != "at com.example.shop.cart.CartService.addItem" {
				t.Errorf("MESSAGE = %q, want the throw site", got)
			}
		}
	}
}

func TestExceptionIssueYank(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "T", "", ""),
		issueSpan(2, "2026-07-15T11:59:58Z", "cart", "", "", ""),
	})
	text, label := exceptionsSpec.Yank(rows[0])
	if !strings.HasPrefix(text, "e1:") || label != "issue key "+text {
		t.Errorf("yank = %q, %q", text, label)
	}
	if text, _ := exceptionsSpec.Yank(rows[1]); text != "" {
		t.Errorf("a keyless row yanked %q", text)
	}
}

func TestPreviewFactsExceptionIssue(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", cartLockedStack),
		issueSpan(2, "2026-07-15T11:59:40Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", cartLockedStackMoved),
	})
	if got := PreviewTitle(rows[0]); got != "java.lang.IllegalStateException" {
		t.Errorf("title = %q", got)
	}
	facts := PreviewFacts(rows[0])
	issue, _ := IssueOf(rows[0])
	want := []PreviewFact{
		{Label: "issue", Value: issue.Key},
		{Label: "in sample", Value: "2 spans"},
		{Label: "grouped by", Value: "app frames"},
		{Label: "message", Value: "cart <n> is locked", Wrap: true},
		{Label: "thrown at", Value: "com.example.shop.cart.CartService.addItem"},
		{Label: "then", Value: "com.example.shop.web.CartController.post"},
		{Label: "services", Value: "cart, checkout"},
		{Label: "last seen", Value: FormatTime("2026-07-15T11:59:58Z")},
		{Label: "first seen", Value: FormatTime("2026-07-15T11:59:40Z")},
	}
	if !reflect.DeepEqual(facts, want) {
		t.Errorf("facts =\n%+v\nwant\n%+v", facts, want)
	}
}

// One span was seen once: no first-seen line repeats the last. And where
// the span's event recorded an outer exception of the one in the key, the
// pane names both.
func TestPreviewFactsExceptionIssueOfOneWrappedSpan(t *testing.T) {
	wrapped := "java.lang.RuntimeException: request failed\n" +
		"\tat com.example.shop.web.CartController.post(CartController.java:48)\n" +
		"Caused by: java.lang.IllegalStateException: cart 4411 is locked\n" +
		"\tat com.example.shop.cart.CartService.addItem(CartService.java:120)\n" +
		"\t... 1 more"
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "java.lang.RuntimeException", "request failed", wrapped),
	})
	if got := PreviewTitle(rows[0]); got != "java.lang.IllegalStateException" {
		t.Errorf("title = %q, want the root cause the key is built on", got)
	}
	facts := PreviewFacts(rows[0])
	if got := factByLabel(facts, "recorded as").Value; got != "java.lang.RuntimeException" {
		t.Errorf("recorded as = %q", got)
	}
	if got := factByLabel(facts, "in sample").Value; got != "1 span" {
		t.Errorf("in sample = %q", got)
	}
	if got := factByLabel(facts, "first seen"); got.Label != "" {
		t.Errorf("a single span has a first-seen line: %+v", got)
	}
	if got := factByLabel(facts, "last seen").Value; got == "" {
		t.Error("no last-seen line")
	}
}

// The command bar opens its first match, and equal matches rank in registry
// order. The exceptions view must not take the prefixes people already type
// for the events view.
func TestExceptionsViewLeavesEventsItsPrefixes(t *testing.T) {
	for input, want := range map[string]string{
		"e": "events", "ev": "events",
		"ex": "exceptions", "exc": "exceptions", "exceptions": "exceptions",
		"exe": "executions", "exec": "executions",
	} {
		if got := Match(input); len(got) == 0 || got[0].Name != want {
			t.Errorf(":%s opens %v, want %s", input, got[0].Name, want)
		}
	}
}

// The preview pane names a row the way its EXCEPTION cell does: an event
// with a message and no type is an "exception", and the row of spans whose
// events recorded nothing says so instead of posing as an exception type.
func TestPreviewTitleOfRowsWithoutAType(t *testing.T) {
	rows := GroupExceptionIssues([]map[string]any{
		issueSpan(1, "2026-07-15T11:59:58Z", "cart", "", "disk full", ""),
		issueSpan(2, "2026-07-15T11:59:57Z", "cart", "", "", ""),
	})
	for i, want := range []string{"exception", "(nothing recorded)"} {
		if got := PreviewTitle(rows[i]); got != want {
			t.Errorf("row %d: title = %q, want %q", i, got, want)
		}
		if got := exceptionsSpec.Columns[2].Text(rows[i]); got != want {
			t.Errorf("row %d: EXCEPTION cell = %q, want %q", i, got, want)
		}
	}
}
