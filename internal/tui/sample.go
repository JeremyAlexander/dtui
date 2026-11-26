// Sampled views (catalog.Spec.Sample): the fetch that groups a bounded
// sample into rows, and the header line that says what the sample covers.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// sampleCut is why a sample holds less than its timeframe does.
type sampleCut int

const (
	cutNone    sampleCut = iota
	cutRows              // the query's own limit was reached
	cutBytes             // the result reached its size limit
	cutScan              // Grail stopped scanning at its scan limit
	cutTimeout           // the fetch timed out
)

// sampleFacts describe the records a sampled view's rows were computed from.
// They travel with those rows in one dataMsg, so the header can never
// describe another fetch than the table below it: not a later timeframe,
// and not another response's notices.
type sampleFacts struct {
	noun    string // one fetched record ("exception span")
	window  string // the timeframe the query ran over, as the header words it
	fetched int
	oldest  string // the oldest record's time field, "" when none carries one
	cut     sampleCut
	// fields are the scalar fields of the fetched records, sorted: the facet
	// picker's candidates, which the computed rows no longer carry.
	fields []string
}

// sampleWindow words a timeframe for the header.
func sampleWindow(tf catalog.Timeframe) string {
	if tf.Absolute() {
		return tf.Label
	}
	return "last " + tf.Label
}

// takeSample computes a sampled view's rows and the facts about the records
// they come from. It runs in the fetch command, off the update loop.
func takeSample(s *catalog.SampleSpec, window string, records []map[string]any, notices []exec.QueryNotification) ([]map[string]any, *sampleFacts) {
	facts := &sampleFacts{
		noun:    s.Noun,
		window:  window,
		fetched: len(records),
		cut:     cutFor(notices),
		fields:  scalarFacetFields(records),
	}
	if facts.cut == cutNone && int64(len(records)) >= s.Rows {
		facts.cut = cutRows
	}
	var oldest time.Time
	for _, rec := range records {
		raw := catalog.Str(rec, s.Time)
		if at, err := time.Parse(time.RFC3339Nano, raw); err == nil && (facts.oldest == "" || at.Before(oldest)) {
			oldest, facts.oldest = at, strings.Clone(raw)
		}
	}
	return s.Group(records), facts
}

// cutFor reads from a response's notices what cut its result short. Of
// several the one that says least about what arrived wins: after a scan that
// stopped early, the records are not known to be the newest of the
// timeframe, whatever limit also applied.
func cutFor(notices []exec.QueryNotification) sampleCut {
	cut := cutNone
	for _, n := range notices {
		if !noticeWarns(n) {
			continue
		}
		var c sampleCut
		switch classifyNotice(n.NotificationType, n.Message) {
		case noticeScanLimit:
			c = cutScan
		case noticeTimeout:
			c = cutTimeout
		case noticeSizeLimit:
			c = cutBytes
		case noticeResultLimit:
			c = cutRows
		}
		if c > cut {
			cut = c
		}
	}
	return cut
}

// line renders the header: what cut the sample, how many records arrived,
// how far back they reach, and last the window they were fetched over. The
// window comes last because a narrow table truncates the line, and the
// screen's own header names the window anyway; the reach is nowhere else.
// warn is set when the sample was cut. "" for an empty sample nothing cut:
// an empty view needs no caveat.
func (f *sampleFacts) line() (text string, warn bool) {
	if f == nil || (f.fetched == 0 && f.cut == cutNone) {
		return "", false
	}
	count := fmt.Sprintf("%d %s", f.fetched, f.noun)
	if f.fetched != 1 {
		count += "s"
	}
	reason := map[sampleCut]string{
		cutRows: "limit hit", cutBytes: "size limit hit",
		cutScan: "scan limit hit", cutTimeout: "fetch timed out",
	}[f.cut]
	var what string
	switch {
	case f.cut == cutNone && f.fetched == 1:
		return count + " · " + f.window, false
	case f.cut == cutNone:
		return "all " + count + " · " + f.window, false
	case f.fetched == 0 && f.cut == cutBytes:
		what = "no " + f.noun + " fit"
	case f.fetched == 0:
		what = "the scan gave up before it found " + f.noun + "s; narrow the timeframe (t)"
	case f.cut == cutScan || f.cut == cutTimeout:
		// Grail scans in no promised order, so these are not known to be
		// the newest.
		what = count + " of what was scanned; narrow the timeframe (t)"
	default:
		what = "newest " + count
		if f.oldest != "" {
			what += ", back to " + catalog.FormatTime(f.oldest)
		}
		what += "; older ones are not counted"
	}
	return reason + ": " + what + " · " + f.window, true
}

// sampleOpts are a sample query's execution options: the usual ones, with
// the spec's two caps.
func (d *dataSource) sampleOpts(s *catalog.SampleSpec) exec.DQLExecuteOptions {
	opts := d.execOpts(s.Rows, false)
	opts.MaxResultBytes = s.Bytes
	return opts
}

// sample runs a sampled view's list query and groups the result before it
// is delivered: the dataMsg carries the computed rows and the facts of the
// sample, and the fetched records are released here. Grouping a thousand
// stack traces is too slow for the update loop, which also draws. Like every
// query it is never cancelled; a superseded result is dropped by seq.
func (d *dataSource) sample(owner any, seq int, dql string, s *catalog.SampleSpec, window string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		msg := d.fetch(owner, seq, dql, d.sampleOpts(s))
		if msg.err == nil {
			msg.records, msg.sample = takeSample(s, window, msg.records, msg.notices)
		}
		msg.elapsed = time.Since(start)
		return msg
	}
}
