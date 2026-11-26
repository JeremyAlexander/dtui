package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dynatrace-oss/dtui/internal/exckey"
)

// The exceptions view: the newest exception-bearing spans in scope, grouped
// into issues by the key exckey derives from each span's exception events.
// Span exceptions carry no grouping key in Grail, and no DQL computes this
// one, so the view fetches a bounded sample and groups it in Go. Every count
// is therefore a share of the sample, which the table's header spells out.

// The sample's two caps. Both are provisional: they were chosen from the
// size of single spans on a small OneAgent-fed tenant (about 5 KB per
// projected span, most of it stack text) and have not been measured on an
// environment that fills them. Revisit them there: a cap is right when a
// full sample still arrives within the time a view may take to open.
const (
	// ExceptionSampleRows is how many spans the view fetches at most; the
	// query's limit.
	ExceptionSampleRows = 1000
	// ExceptionSampleBytes caps the result. A sample of spans with long
	// stack traces is cut at this size before it reaches ExceptionSampleRows;
	// the header then says so.
	ExceptionSampleBytes = 8 << 20
)

// exceptionSampleFields are the span fields the sample keeps: what the key
// is computed from (span.events), what a row shows, and what enter needs to
// open the newest span's trace. Everything else on a span is left on the
// server, which halves the result (measured on single spans).
const exceptionSampleFields = "start_time, span.id, trace.id, span.name, endpoint.name, " +
	"dt.service.name, service.name, dt.smartscape.service, span.events"

var exceptionsSpec = &Spec{
	Name:    "exceptions",
	Aliases: []string{"exc"},
	Kind:    KindSignal,
	Desc:    "Exception issues: the newest exception spans, grouped by what was thrown where",
	// Narrowing stages land before the sort and the limit (tailStart), so a
	// search or a facet changes what is sampled and can use any span field;
	// the projection comes last.
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch spans, from:%s", s.Timeframe.DQL())
		if f := ScopeSpanFilter(s); f != "" {
			fmt.Fprintf(&b, "\n| filter %s", f)
		}
		fmt.Fprintf(&b, "\n| filter %s", exceptionSpanFilter)
		fmt.Fprintf(&b, "\n| sort start_time desc\n| limit %d", ExceptionSampleRows)
		b.WriteString("\n| fields " + exceptionSampleFields)
		return b.String()
	},
	Sample: &SampleSpec{
		Noun:  "exception span",
		Time:  "start_time",
		Rows:  ExceptionSampleRows,
		Bytes: ExceptionSampleBytes,
		Group: GroupExceptionIssues,
		// The key is in no cell and the EXCEPTION cell shortens the type.
		Match: func(row map[string]any) string {
			issue := issueOf(row)
			return issue.Key + " " + issue.Type
		},
	},
	Columns: []Column{
		{Title: "COUNT", Width: 5, Right: true,
			Value: func(rec map[string]any) string { return strconv.Itoa(issueOf(rec).Count) },
			Sort:  func(rec map[string]any) any { return issueOf(rec).Count }},
		// Min keeps "Jan 02 15:04" whole when the fixed columns are squeezed.
		{Title: "LAST", Width: 12, Min: 12,
			Value: func(rec map[string]any) string { return FormatTime(issueOf(rec).Newest) },
			Sort:  func(rec map[string]any) any { return issueOf(rec).Newest }},
		{Title: "EXCEPTION", Width: 22, Min: 12, Class: func(string) string { return "error" },
			Value: func(rec map[string]any) string { return ExceptionTypeShort(issueOf(rec).name()) }},
		{Title: "MESSAGE", Value: func(rec map[string]any) string {
			issue := issueOf(rec)
			if issue.Message == "" && len(issue.Frames) > 0 {
				return "at " + issue.Frames[0]
			}
			return issue.Message
		}},
		{Title: "SERVICE", Width: 12, Min: 8, Value: func(rec map[string]any) string {
			services := issueOf(rec).Services
			switch len(services) {
			case 0:
				return ""
			case 1:
				return services[0]
			}
			return fmt.Sprintf("%s +%d", services[0], len(services)-1)
		}},
		// What the row is grouped by says how specific its key is: a row
		// keyed on the type alone is visibly coarser than one keyed on the
		// application's frames ("app"; the preview pane has the full label).
		{Title: "BY", Width: 7, Class: func(string) string { return "dim" },
			Value: func(rec map[string]any) string {
				return strings.TrimSuffix(issueOf(rec).Rung, " frames")
			}},
	},
	// A row stands for its newest span, whose trace enter opens.
	EnterTarget: "waterfall",
	Trace:       func(rec map[string]any) string { return Str(rec, "trace.id") },
	Drills:      map[string]string{"l": "trace-logs"},
	Scopable:    func(e Entity) bool { return SpanScopable(e.Type) },
	Yank: func(rec map[string]any) (string, string) {
		key := issueOf(rec).Key
		if key == "" {
			return "", ""
		}
		return key, "issue key " + key
	},
}

// ExceptionIssue is one row of the exceptions view: the spans of the sample
// that share an issue key.
type ExceptionIssue struct {
	// Key is the issue key ("e1:" and 12 hex characters). "" on the one row
	// that collects the spans whose exception events say nothing.
	Key string
	// Rung labels the step of the fallback ladder the key was computed on.
	Rung string
	// Type is the exception type in the key; EventType the type the newest
	// span's event recorded, where that is an outer exception of Type.
	Type, EventType string
	// Message is the newest span's message, parameterised.
	Message string
	// Frames are the frames in the key, the throw site first.
	Frames []string
	// Count is how many spans of the sample share the key.
	Count int
	// Services are the services those spans ran in, sorted.
	Services []string
	// Newest and Oldest are the start times of the group's newest and
	// oldest span in the sample, as the records spelled them.
	Newest, Oldest string
}

// name is what a row is called where its type would stand: the type, or a
// stand-in when the event recorded none, or nothing at all.
func (issue ExceptionIssue) name() string {
	if issue.Key == "" {
		return "(nothing recorded)"
	}
	return firstNonEmpty(issue.Type, "exception")
}

// exceptionIssueKey holds a row's ExceptionIssue. The "__" prefix keeps it
// out of the record inspector and the facet picker: it is no Grail field.
const exceptionIssueKey = "__issue"

// IssueOf returns the issue an exceptions-view row stands for.
func IssueOf(rec map[string]any) (ExceptionIssue, bool) {
	issue, ok := rec[exceptionIssueKey].(ExceptionIssue)
	return issue, ok
}

func issueOf(rec map[string]any) ExceptionIssue {
	issue, _ := IssueOf(rec)
	return issue
}

// GroupExceptionIssues turns a sample of exception-bearing spans into one
// row per issue key: the loudest issue first, then the one seen last, then
// by key. Every span is counted in exactly one row; a span whose exception
// events carry no type, message or stack has no key and is counted in a
// keyless last row.
//
// A row holds the issue under exceptionIssueKey and, as real span fields,
// the start time and the ids of the group's newest span. It shares no memory
// with the spans: their stack traces are released with them.
func GroupExceptionIssues(records []map[string]any) []map[string]any {
	type group struct {
		issue          ExceptionIssue
		newest, oldest time.Time
		spanID, trace  string
		services       map[string]bool
	}
	groups := map[string]*group{}
	var order []*group
	for _, rec := range records {
		fp, _ := FingerprintExceptions(parseSpanExceptions(rec))
		start := Str(rec, "start_time")
		at, _ := time.Parse(time.RFC3339Nano, start)
		g := groups[fp.Key]
		if g == nil {
			g = &group{services: map[string]bool{}, newest: at, oldest: at}
			g.issue.Newest, g.issue.Oldest = start, start
			groups[fp.Key] = g
			order = append(order, g)
		}
		g.issue.Count++
		if svc := SpanService(rec); svc != "" {
			g.services[svc] = true
		}
		if at.Before(g.oldest) {
			g.oldest, g.issue.Oldest = at, start
		}
		// The first span of a group describes it until a newer one arrives.
		if g.issue.Count == 1 || at.After(g.newest) {
			g.newest, g.issue.Newest = at, start
			g.spanID, g.trace = Str(rec, "span.id"), Str(rec, "trace.id")
			describeIssue(&g.issue, fp)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		switch {
		case (a.issue.Key == "") != (b.issue.Key == ""):
			return b.issue.Key == ""
		case a.issue.Count != b.issue.Count:
			return a.issue.Count > b.issue.Count
		case !a.newest.Equal(b.newest):
			return a.newest.After(b.newest)
		}
		return a.issue.Key < b.issue.Key
	})
	rows := make([]map[string]any, len(order))
	for i, g := range order {
		for svc := range g.services {
			g.issue.Services = append(g.issue.Services, strings.Clone(svc))
		}
		sort.Strings(g.issue.Services)
		rows[i] = map[string]any{
			exceptionIssueKey: g.issue,
			"start_time":      strings.Clone(g.issue.Newest),
			"span.id":         strings.Clone(g.spanID),
			"trace.id":        strings.Clone(g.trace),
		}
	}
	return rows
}

// describeIssue copies what a fingerprint says about its exception into the
// issue. The fingerprint's strings may be slices of a span's stack trace, so
// each is cloned.
func describeIssue(issue *ExceptionIssue, fp exckey.ExceptionFingerprint) {
	issue.Key = fp.Key
	issue.Rung = fp.Rung.String()
	issue.Type = strings.Clone(fp.Type)
	issue.EventType = strings.Clone(fp.EventType)
	issue.Message = strings.Clone(fp.Message)
	issue.Frames = make([]string, len(fp.Frames))
	for i, f := range fp.Frames {
		issue.Frames[i] = strings.Clone(joinNonEmpty(".", f.Module, f.Function))
	}
}

// issuePreview is the preview pane of an exceptions-view row: what the key
// was computed from, so a group can be judged without opening a trace.
func issuePreview(issue ExceptionIssue) []PreviewFact {
	var b previewBuilder
	b.add("issue", issue.Key)
	spans := "spans"
	if issue.Count == 1 {
		spans = "span"
	}
	b.add("in sample", fmt.Sprintf("%d %s", issue.Count, spans))
	b.add("grouped by", issue.Rung)
	if issue.EventType != issue.Type {
		b.add("recorded as", issue.EventType)
	}
	b.wrapped("message", issue.Message)
	for i, frame := range issue.Frames {
		label := "then"
		if i == 0 {
			label = "thrown at"
		}
		b.facts = append(b.facts, PreviewFact{Label: label, Value: frame})
	}
	b.add("services", strings.Join(issue.Services, ", "))
	b.add("last seen", FormatTime(issue.Newest))
	if issue.Oldest != issue.Newest {
		b.add("first seen", FormatTime(issue.Oldest))
	}
	return b.facts
}
