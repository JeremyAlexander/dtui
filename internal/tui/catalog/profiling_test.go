package catalog

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestProfileWindowIsAtMostTwoHours(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	rel := func(label string) Timeframe {
		tf, ok := ParseTimeframe(label)
		if !ok {
			t.Fatalf("bad timeframe %q", label)
		}
		return tf
	}
	abs := func(from, to time.Time) Timeframe { return Timeframe{From: from, To: to, Dur: to.Sub(from)} }
	for _, tc := range []struct {
		name     string
		tf       Timeframe
		from, to time.Time
		clamped  bool
	}{
		{"30m", rel("30m"), now.Add(-30 * time.Minute), now, false},
		{"2h", rel("2h"), now.Add(-2 * time.Hour), now, false},
		{"24h keeps its last 2h", rel("24h"), now.Add(-2 * time.Hour), now, true},
		{"closed window", abs(now.Add(-5*time.Hour), now.Add(-4*time.Hour)), now.Add(-5 * time.Hour), now.Add(-4 * time.Hour), false},
		{"long closed window keeps its end", abs(now.Add(-9*time.Hour), now.Add(-4*time.Hour)), now.Add(-6 * time.Hour), now.Add(-4 * time.Hour), true},
		{"open window runs to now", Timeframe{From: now.Add(-time.Hour), Dur: time.Hour}, now.Add(-time.Hour), now, false},
	} {
		w := ProfileWindowOf(tc.tf, now)
		if !w.From.Equal(tc.from) || !w.To.Equal(tc.to) || w.Clamped != tc.clamped {
			t.Errorf("%s: got %s..%s clamped=%v, want %s..%s clamped=%v", tc.name, w.From, w.To, w.Clamped, tc.from, tc.to, tc.clamped)
		}
		if w.To.Sub(w.From) > ProfileMaxWindow {
			t.Errorf("%s: window of %s exceeds %s", tc.name, w.To.Sub(w.From), ProfileMaxWindow)
		}
	}
}

// The echo names the window that was analysed, never the longer selection.
func TestProfileEchoWindow(t *testing.T) {
	day, _ := ParseTimeframe("24h")
	half, _ := ParseTimeframe("30m")
	if got := profileEchoWindow(day); got != "--default-timeframe-start 'now()-2h'" {
		t.Errorf("24h echoes %q", got)
	}
	if got := profileEchoWindow(half); got != "--default-timeframe-start 'now()-30m'" {
		t.Errorf("30m echoes %q", got)
	}
	from := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	got := profileEchoWindow(Timeframe{From: from, To: from.Add(5 * time.Hour), Dur: 5 * time.Hour})
	want := "--default-timeframe-start 2026-07-15T06:00:00Z --default-timeframe-end 2026-07-15T08:00:00Z"
	if got != want {
		t.Errorf("absolute window echoes %q, want %q", got, want)
	}
}

func TestShortFrameKeepsTheMethod(t *testing.T) {
	const fn = "com.fasterxml.jackson.core.json.UTF8JsonGenerator.writeString"
	for _, width := range []int{80, 52, 40, 30, 19, 8} {
		got := ShortFrame(fn, width)
		if utf8.RuneCountInString(got) > width {
			t.Errorf("width %d: %q is %d wide", width, got, utf8.RuneCountInString(got))
		}
		if width >= 30 && !strings.HasSuffix(got, "UTF8JsonGenerator.writeString") {
			t.Errorf("width %d: %q lost the class and method", width, got)
		}
	}
	if got := ShortFrame(fn, 80); got != fn {
		t.Errorf("a frame that fits must stay whole, got %q", got)
	}
}

func TestShareBar(t *testing.T) {
	if got := ShareBar(1, 8); got != "████████" {
		t.Errorf("full bar = %q", got)
	}
	if got := ShareBar(0, 8); strings.TrimSpace(got) != "" {
		t.Errorf("empty bar = %q", got)
	}
	// A share too small for a cell still shows, so a row with samples never
	// reads as none.
	if got := ShareBar(0.001, 8); strings.TrimSpace(got) == "" {
		t.Error("a non-zero share must draw something")
	}
	if got := ShareBar(0.5, 8); utf8.RuneCountInString(strings.TrimRight(got, " ")) != 4 {
		t.Errorf("half bar = %q", got)
	}
}

// Why a view is empty is worded per reason: no samples at all, none in the
// thread state, and every frame hidden are three different next steps.
func TestProfileEmptyReasons(t *testing.T) {
	lock := Scope{Lens: 1}
	for reason, want := range map[string]string{
		ProfEmptyNoSamples: "idle or restarted",
		ProfEmptyState:     "no lock samples in this window",
		ProfEmptyAppOnly:   "app-only filter hid every frame",
	} {
		if got := profileEmpty(lock, map[string]any{ProfSumEmpty: reason}); !strings.Contains(got, want) {
			t.Errorf("%s reads %q, want it to say %q", reason, got, want)
		}
	}
	if got := profileEmpty(lock, map[string]any{}); got != "" {
		t.Errorf("a view with rows has no empty text, got %q", got)
	}
}

func TestFocusTarget(t *testing.T) {
	row := func(fn string) map[string]any { return map[string]any{ProfFunction: fn} }
	focused := Scope{Arg: "com.example.shop.CartService.total"}
	if got := FocusTarget(Scope{}, row("com.example.shop.CartService.total")); got != "com.example.shop.CartService.total" {
		t.Errorf("a function row focuses its function, got %q", got)
	}
	if got := FocusTarget(focused, row("com.example.shop.CartService.total")); got != "" {
		t.Errorf("the focused function's own rows must not re-open it, got %q", got)
	}
	if got := FocusTarget(focused, row(ProfRootCaller)); got != "" {
		t.Errorf("the synthetic root is not a function, got %q", got)
	}
}

func TestSplitSummary(t *testing.T) {
	summary, rows := SplitSummary([]map[string]any{{SummaryKey: true, "samples": 3.0}, {"a": 1}, {"a": 2}})
	if summary["samples"] != 3.0 || len(rows) != 2 {
		t.Errorf("summary %v, %d rows", summary, len(rows))
	}
	for _, first := range []map[string]any{{"a": 1}, {SummaryKey: false}, {SummaryKey: "true"}, {SummaryKey: nil}} {
		summary, rows = SplitSummary([]map[string]any{first, {"a": 2}})
		if summary != nil || len(rows) != 2 {
			t.Errorf("first record %v is a row: got summary %v, %d rows", first, summary, len(rows))
		}
	}
	if summary, rows = SplitSummary(nil); summary != nil || rows != nil {
		t.Errorf("no records: %v, %v", summary, rows)
	}
	for _, s := range All() {
		if want := s.Name == "hotspots" || s.Name == "allocations"; s.ReadsSummary() != want {
			t.Errorf("%s: ReadsSummary = %v, want %v", s.Name, s.ReadsSummary(), want)
		}
	}
}
