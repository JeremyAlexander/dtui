package tui

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// issueSpanRecord is one record of the exceptions view's query: the fields
// it projects, with one OneAgent-style Java exception event.
func issueSpanRecord(n int, start, service, typ, message, stack string) map[string]any {
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
	issueCartStack = "com.example.shop.cart.CartService.addItem (CartService.java:120)\n" +
		"com.example.shop.web.CartController.post (CartController.java:48)"
	issueOrdersStack = "com.example.shop.db.Orders.load (Orders.java:10)"
)

// issueSpans is a small sample: the cart lock twice, from two services, and
// one database error between them.
func issueSpans() []map[string]any {
	return []map[string]any{
		issueSpanRecord(1, "2026-07-15T11:59:58.000000000Z", "cart", "java.lang.IllegalStateException", "cart 4411 is locked", issueCartStack),
		issueSpanRecord(2, "2026-07-15T11:59:50.000000000Z", "orders", "java.sql.SQLException", "connection refused", issueOrdersStack),
		issueSpanRecord(3, "2026-07-15T11:59:40.000000000Z", "checkout", "java.lang.IllegalStateException", "cart 9020 is locked", issueCartStack),
	}
}

// manySpans is a sample of n spans of one issue, one second apart, newest
// first, ending at 11:59:59.
func manySpans(n int) []map[string]any {
	newest := time.Date(2026, 7, 15, 11, 59, 59, 0, time.UTC)
	spans := make([]map[string]any, n)
	for i := range spans {
		at := newest.Add(-time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		spans[i] = issueSpanRecord(i+1, at, "cart", "java.lang.IllegalStateException", "cart is locked", issueCartStack)
	}
	return spans
}

func warning(notificationType, message string) []exec.QueryNotification {
	return []exec.QueryNotification{{Severity: "WARNING", NotificationType: notificationType, Message: message}}
}

// seedSample lands spans in the top view the way its fetch command would:
// grouped, with the facts of the sample they were grouped from.
func seedSample(t *testing.T, a *app, spans []map[string]any, notices []exec.QueryNotification) *tableView {
	t.Helper()
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Sample == nil {
		t.Fatalf("top view is %T, want a sampled table", a.top())
	}
	tv.seq++
	tv.loading = true
	rows, facts := takeSample(tv.spec.Sample, sampleWindow(tv.scope.Timeframe), spans, notices)
	a.Update(dataMsg{owner: tv, seq: tv.seq, dql: tv.DQL(), records: rows, sample: facts,
		notices: notices, elapsed: time.Second})
	return tv
}

// The fetch command groups: what reaches the update loop is the issue rows
// and the facts of the sample, never the spans.
func TestExceptionsViewGroupsInTheFetchCommand(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	a.ds.runFn = func(string) ([]map[string]any, error) { return issueSpans(), nil }

	msg, ok := tv.Refresh()().(dataMsg)
	if !ok {
		t.Fatal("Refresh did not issue the list query")
	}
	if msg.sample == nil || msg.sample.fetched != 3 {
		t.Fatalf("sample facts = %+v, want 3 fetched", msg.sample)
	}
	if len(msg.records) != 2 {
		t.Fatalf("the command delivered %d records, want 2 issue rows", len(msg.records))
	}
	for _, rec := range msg.records {
		if _, isIssue := catalog.IssueOf(rec); !isIssue {
			t.Fatalf("delivered record is not an issue row: %v", rec)
		}
	}

	a.Update(msg)
	if len(tv.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tv.rows))
	}
	out := screen(a)
	for _, want := range []string{"all 3 exception spans · last 2h", "lang.IllegalStateException", "cart <n> is locked", "cart +1", "grouped by: app frames"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
}

func TestExceptionsViewAsksForBothCaps(t *testing.T) {
	d := &dataSource{segments: []exec.FilterSegmentRef{{ID: "uid-a"}}}
	opts := d.sampleOpts(catalog.Lookup("exceptions").Sample)
	if opts.MaxResultRecords != catalog.ExceptionSampleRows || opts.MaxResultBytes != catalog.ExceptionSampleBytes {
		t.Errorf("caps = %d records, %d bytes", opts.MaxResultRecords, opts.MaxResultBytes)
	}
	if len(opts.Segments) != 1 || opts.ClientContext != clientContext {
		t.Errorf("a sample query must run like any other: %+v", opts)
	}
}

// The header says what the counts are counts of: how many spans arrived, how
// far back they reach and what cut them short.
func TestSampleLine(t *testing.T) {
	pinSnapshotClock(t)
	spec := catalog.Lookup("exceptions").Sample
	sizeLimit := warning("BYTE_LIMIT_ADDED", "Your result has been limited because the maximum bytes (8388608) were reached.")
	scanLimit := warning("SCAN_LIMIT_GBYTES", "Your execution was stopped after 500 gigabytes of data were scanned.")
	for _, tc := range []struct {
		name    string
		spans   []map[string]any
		notices []exec.QueryNotification
		want    string
		warn    bool
	}{
		{"whole", issueSpans(), nil, "all 3 exception spans · last 2h", false},
		{"one", issueSpans()[:1], nil, "1 exception span · last 2h", false},
		{"row limit", manySpans(catalog.ExceptionSampleRows), nil,
			"limit hit: newest 1000 exception spans, back to 11:43:20; older ones are not counted · last 2h", true},
		{"one short of the limit", manySpans(catalog.ExceptionSampleRows - 1), nil,
			"all 999 exception spans · last 2h", false},
		{"size limit", manySpans(412), sizeLimit,
			"size limit hit: newest 412 exception spans, back to 11:53:08; older ones are not counted · last 2h", true},
		{"scan limit", manySpans(5), scanLimit,
			"scan limit hit: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"timeout", manySpans(5), warning("FETCH_TIMEOUT", ""),
			"fetch timed out: 5 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"scan limit, nothing found", nil, scanLimit,
			"scan limit hit: the scan gave up before it found exception spans; narrow the timeframe (t) · last 2h", true},
		{"timeout, nothing found", nil, warning("FETCH_TIMEOUT", ""),
			"fetch timed out: the scan gave up before it found exception spans; narrow the timeframe (t) · last 2h", true},
		{"size limit, nothing fits", nil, sizeLimit,
			"size limit hit: no exception span fit · last 2h", true},
		// A scan that stopped early outranks the row limit: the rows that
		// arrived are then not the newest of the timeframe.
		{"scan limit on a full sample", manySpans(catalog.ExceptionSampleRows), scanLimit,
			"scan limit hit: 1000 exception spans of what was scanned; narrow the timeframe (t) · last 2h", true},
		{"info is not a cut", issueSpans(), []exec.QueryNotification{{Severity: "INFO", NotificationType: "SCAN_LIMIT_GBYTES"}},
			"all 3 exception spans · last 2h", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, facts := takeSample(spec, sampleWindow(catalog.Timeframe{Label: "2h"}), tc.spans, tc.notices)
			text, warn := facts.line()
			if text != tc.want || warn != tc.warn {
				t.Errorf("line = %q (warn %v)\nwant   %q (warn %v)", text, warn, tc.want, tc.warn)
			}
		})
	}
}

// Nothing fetched and nothing cut is an ordinary empty view: no header line
// claims a sample.
func TestSampleLineEmptyAndWhole(t *testing.T) {
	_, facts := takeSample(catalog.Lookup("exceptions").Sample, "last 2h", nil, nil)
	if text, _ := facts.line(); text != "" {
		t.Errorf("an empty, uncut sample wrote %q", text)
	}
	a := testApp(t, "exceptions")
	seedSample(t, a, nil, nil)
	if out := screen(a); !strings.Contains(out, "no data in timeframe (last 2h)") || strings.Contains(out, "exception spans") {
		t.Errorf("empty view:\n%s", out)
	}
}

func TestSampleWindowWording(t *testing.T) {
	if got := sampleWindow(catalog.Timeframe{Label: "24h"}); got != "last 24h" {
		t.Errorf("relative window = %q", got)
	}
	from := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	abs := catalog.AbsoluteTimeframe(from, from.Add(time.Hour), from.Add(2*time.Hour))
	if got := sampleWindow(abs); got != "2026-07-15 10:00→11:00 UTC" {
		t.Errorf("absolute window = %q", got)
	}
}

// The header describes the rows on screen. While a refetch for another
// timeframe is in flight those are still the old window's.
func TestSampleLineKeepsTheWindowOfItsRows(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)

	tv.SetTimeframe(catalog.Timeframe{Label: "24h", Dur: 24 * time.Hour}) // the fetch is not run
	if out := screen(a); !strings.Contains(out, "all 3 exception spans · last 2h") {
		t.Errorf("header changed before the new sample arrived:\n%s", out)
	}
	seedSample(t, a, issueSpans()[:1], nil)
	if out := screen(a); !strings.Contains(out, "1 exception span · last 24h") {
		t.Errorf("header did not follow the new sample:\n%s", out)
	}
}

// The header is a line of its own: the table gives it one row of its height.
func TestSampleLineTakesARowOfThePage(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	disablePreview(a)
	tv := a.top().(*tableView)
	seedSample(t, a, nil, nil)
	screen(a)
	bare := tv.pageSize()

	seedSample(t, a, manySpans(catalog.ExceptionSampleRows), nil)
	out := screen(a)
	if got := tv.pageSize(); got != bare-1 {
		t.Errorf("page size = %d with the header, %d without; want one less", got, bare)
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "limit hit: newest 1000 exception spans") {
			if !strings.Contains(lines[i+1], "COUNT") {
				t.Errorf("the column header does not follow the sample line:\n%s", out)
			}
			return
		}
	}
	t.Errorf("no sample line:\n%s", out)
}

// With more issues than fit, the rows fill what the header leaves: of the
// 40 lines, the app's header and footer take two each, and the table's
// status line, sample line and column titles one each.
func TestSampleLineLeavesTheTableItsHeight(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	disablePreview(a)
	spans := make([]map[string]any, 80)
	for i := range spans {
		spans[i] = issueSpanRecord(i+1, "2026-07-15T11:59:58Z", "cart", fmt.Sprintf("ErrorKind%02d", i), "", "")
	}
	tv := seedSample(t, a, spans, nil)
	out := screen(a)
	if got := strings.Count(out, "ErrorKind"); got != 33 {
		t.Errorf("the table drew %d rows, want 33:\n%s", got, out)
	}
	if got := strings.Count(out, "\n") + 1; got != 40 {
		t.Errorf("screen has %d lines, want 40", got)
	}
	// The table itself keeps to the height it is given; the app would clip
	// an overflow and hide it.
	if got := strings.Count(tv.renderTable(120, 20), "\n") + 1; got != 20 {
		t.Errorf("a 20-line table rendered %d lines", got)
	}
}

// Enter opens the trace of the row's newest span, on that span.
func TestExceptionsEnterOpensTheNewestSpansTrace(t *testing.T) {
	a := testApp(t, "exceptions")
	seedSample(t, a, issueSpans(), nil)
	press(a, key("enter"))
	wf, ok := a.top().(*waterfallView)
	if !ok {
		t.Fatalf("enter opened %T, want the waterfall", a.top())
	}
	if want := fmt.Sprintf("%032x", 1); wf.traceID != want {
		t.Errorf("trace = %q, want the newest member's %q", wf.traceID, want)
	}
	if want := fmt.Sprintf("%016x", 1); wf.focusSpan != want {
		t.Errorf("focus span = %q, want %q", wf.focusSpan, want)
	}
	// The trace is fetched around the span's start, not over the view's
	// timeframe.
	if want := time.Date(2026, 7, 15, 11, 59, 58, 0, time.UTC); !wf.anchor.Equal(want) {
		t.Errorf("anchor = %v, want the newest member's start %v", wf.anchor, want)
	}
}

func TestExceptionsYankCopiesTheIssueKey(t *testing.T) {
	a := testApp(t, "exceptions")
	silent := issueSpanRecord(9, "2026-07-15T11:00:00Z", "cart", "", "", "")
	tv := seedSample(t, a, append(issueSpans(), silent), nil)

	text, label, ok := tv.YankText()
	issue, _ := catalog.IssueOf(tv.rows[0])
	if !ok || text != issue.Key || !strings.HasPrefix(text, "e1:") || label != "issue key "+text {
		t.Errorf("yank = %q, %q, %v", text, label, ok)
	}
	// The keyless row has no key to copy.
	tv.cursor = len(tv.rows) - 1
	if _, _, ok := tv.YankText(); ok {
		t.Error("the keyless row yanked something")
	}
	// A view that names no yank keeps the default.
	hosts := testApp(t, "hosts")
	seedRows(t, hosts, []map[string]any{{"id": "HOST-1", "name": "web-1"}})
	if _, _, ok := hosts.top().(*tableView).YankText(); ok {
		t.Error("a view without Yank must leave y to the app")
	}
}

// The facet picker offers the fields of the spans that were sampled, which
// the issue rows no longer carry.
func TestExceptionsFacetFieldsComeFromTheSample(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	got := tv.facetCandidates()
	want := []string{"dt.service.name", "dt.smartscape.service", "endpoint.name", "service.name", "span.id", "span.name", "start_time", "trace.id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facet fields = %q\nwant         %q", got, want)
	}
	if !tv.hasField("dt.service.name") || tv.hasField("k8s.namespace.name") {
		t.Error("hasField must answer from the sampled spans")
	}
	// The next sample replaces them.
	seedSample(t, a, nil, nil)
	if got := tv.facetCandidates(); len(got) != 0 {
		t.Errorf("facet fields of an empty sample = %q", got)
	}
}

// echoArgs pastes an echoed command into sh and returns the arguments dtctl
// would have been started with.
func echoArgs(t *testing.T, line string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	bin, work := t.TempDir(), t.TempDir()
	argv := filepath.Join(bin, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done >> '" + argv + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "dtctl"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := osexec.Command(sh, "-c", line)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("sh -c %q: err=%v output=%q", line, err, out)
	}
	if left, _ := os.ReadDir(work); len(left) > 0 {
		t.Fatalf("sh -c %q created %q: something besides dtctl ran", line, left[0].Name())
	}
	raw, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("sh -c %q never ran dtctl: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

// The echo is pasted into a shell. A facet value and a segment id come from
// the tenant's data, so neither may end the quoting, and the query must
// arrive as it ran.
func TestExceptionsEchoSurvivesTheShell(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := a.top().(*tableView)
	hostile := "it's; touch pwned $(touch pwned) `touch pwned`"
	tv.facets = []catalog.Facet{{Field: "span.name", Value: hostile}}
	a.ds.segments = []exec.FilterSegmentRef{{ID: "seg'; touch pwned #"}, {ID: "plain-id"}}
	tv.Refresh()

	echo := tv.Echo()
	if !strings.HasPrefix(echo, "dtctl query '") {
		t.Fatalf("echo = %q", echo)
	}
	got := echoArgs(t, echo)
	want := []string{"query", tv.DQL(), "-S", "seg'; touch pwned #", "-S", "plain-id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dtctl got %q\nwant      %q", got, want)
	}
	if !strings.Contains(tv.DQL(), "\n| filter ") || !strings.Contains(tv.DQL(), hostile) {
		t.Errorf("the query lost its lines or the value:\n%s", tv.DQL())
	}
}

// The footer has one line for the echo, however many the query has.
func TestExceptionsEchoIsOneLineInTheFooter(t *testing.T) {
	a := testApp(t, "exceptions")
	a.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	out := screen(a)
	if !strings.Contains(out, "≡ dtctl query 'fetch spans, from:now() - 2h | filter iAny(") {
		t.Errorf("footer echo is not flattened:\n%s", out)
	}
	if strings.Count(out, "\n") != 39 {
		t.Errorf("screen has %d lines, want 40", strings.Count(out, "\n")+1)
	}
}

// An issue key exists in dtui only: no span holds it. Typed into the filter
// it finds its row, and enter keeps the filter on the rows instead of
// sending the text to the server as a search no span can match.
func TestExceptionsFilterFindsAKeyAndEnterKeepsItLocal(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	issue, _ := catalog.IssueOf(tv.rows[0])
	before := tv.DQL()

	press(a, key("/"))
	if out := screen(a); !strings.Contains(out, "enter keep filter") || strings.Contains(out, "add server search") {
		t.Errorf("the filter hints promise a server search:\n%s", out)
	}
	typeText(a, issue.Key)
	if len(tv.rows) != 1 {
		t.Fatalf("typing the key left %d rows, want its one issue", len(tv.rows))
	}
	press(a, key("enter"))
	if len(tv.searches) != 0 || strings.Contains(tv.DQL(), issue.Key) || tv.DQL() != before {
		t.Errorf("enter sent the key to the server: searches=%q\n%s", tv.searches, tv.DQL())
	}
	if tv.filter != issue.Key || len(tv.rows) != 1 || tv.filtering {
		t.Errorf("enter did not keep the filter: filter=%q rows=%d filtering=%v", tv.filter, len(tv.rows), tv.filtering)
	}
	// The full type is findable too, though the column shortens it.
	tv.setFilter("java.sql.SQLException")
	if len(tv.rows) != 1 {
		t.Errorf("the full type matched %d rows, want 1", len(tv.rows))
	}
}

// alt+enter is the explicit way to narrow what is sampled by text.
func TestExceptionsAltEnterSearchesTheSpans(t *testing.T) {
	a := testApp(t, "exceptions")
	tv := seedSample(t, a, issueSpans(), nil)
	press(a, key("/"))
	typeText(a, "locked")
	press(a, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if len(tv.searches) != 1 || tv.searches[0] != "locked" || tv.filter != "" {
		t.Errorf("searches=%q filter=%q, want one server search and no filter", tv.searches, tv.filter)
	}
	if !strings.Contains(tv.DQL(), "\n| search \"*locked*\"\n") {
		t.Errorf("the search did not reach the query:\n%s", tv.DQL())
	}
}

// A sample that was cut short and holds nothing says only that: the table
// must not add that the timeframe has no data, which nothing showed.
func TestExceptionsEmptyCutSampleClaimsNoEmptyTimeframe(t *testing.T) {
	for _, notices := range [][]exec.QueryNotification{
		warning("SCAN_LIMIT_GBYTES", ""), warning("FETCH_TIMEOUT", ""), warning("BYTE_LIMIT_ADDED", ""),
	} {
		a := testApp(t, "exceptions")
		disablePreview(a)
		seedSample(t, a, nil, notices)
		out := screen(a)
		if strings.Contains(out, "no data in timeframe") {
			t.Errorf("%s: an empty cut sample reads as an empty timeframe:\n%s", notices[0].NotificationType, out)
		}
		if !strings.Contains(out, "hit: ") && !strings.Contains(out, "timed out: ") {
			t.Errorf("%s: no sample line:\n%s", notices[0].NotificationType, out)
		}
	}
}

// An issue last seen on another day shows its date and its time. Beside the
// preview pane the fixed columns are squeezed, and LAST must not give up the
// minute for it.
func TestExceptionsLastSeenKeepsItsTimeWhenSqueezed(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	seedSample(t, a, []map[string]any{
		issueSpanRecord(1, "2026-07-14T23:58:07.000000000Z", "cart", "java.lang.IllegalStateException", "cart 1 is locked", issueCartStack),
	}, nil)
	for _, size := range snapshotSizes {
		a.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		if out := screen(a); !strings.Contains(out, "  Jul 14 23:58  ") {
			t.Errorf("%dx%d: LAST lost part of the time:\n%s", size.w, size.h, out)
		}
	}
}

// How far back a cut sample reaches is the fact its counts depend on, so it
// leads the line: on 80 columns, under a custom window with a long label, it
// must still be on screen when the line is truncated.
func TestSampleLineKeepsItsReachOnANarrowScreen(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	from := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	a.setTimeframe(catalog.AbsoluteTimeframe(from, from.Add(2*time.Hour), snapshotNow))
	seedSample(t, a, manySpans(catalog.ExceptionSampleRows), nil)
	for _, size := range snapshotSizes {
		a.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		if out := screen(a); !strings.Contains(out, "limit hit: newest 1000 exception spans, back to 11:43:20") {
			t.Errorf("%dx%d: the sample line lost its reach:\n%s", size.w, size.h, out)
		}
	}
}
