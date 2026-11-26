package catalog

import (
	"math"
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
		got := shortFrame(fn, width)
		if utf8.RuneCountInString(got) > width {
			t.Errorf("width %d: %q is %d wide", width, got, utf8.RuneCountInString(got))
		}
		if width >= 30 && !strings.HasSuffix(got, "UTF8JsonGenerator.writeString") {
			t.Errorf("width %d: %q lost the class and method", width, got)
		}
	}
	if got := shortFrame(fn, 80); got != fn {
		t.Errorf("a frame that fits must stay whole, got %q", got)
	}
}

func TestShareBar(t *testing.T) {
	if got := shareBar(1, 8); got != "████████" {
		t.Errorf("full bar = %q", got)
	}
	if got := shareBar(0, 8); strings.TrimSpace(got) != "" {
		t.Errorf("empty bar = %q", got)
	}
	// A share too small for a cell still shows, so a row with samples never
	// reads as none.
	if got := shareBar(0.001, 8); strings.TrimSpace(got) == "" {
		t.Error("a non-zero share must draw something")
	}
	if got := shareBar(0.5, 8); utf8.RuneCountInString(strings.TrimRight(got, " ")) != 4 {
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
	if got := FocusTarget(focused, row(profRootCaller)); got != "" {
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

// The path column of the allocations lens is drawn to its width, but what the
// filter matches and the sort orders by is the whole path, spelled out.
func TestAllocationPathColumnHasText(t *testing.T) {
	col := allocationColumns[len(allocationColumns)-1]
	rec := map[string]any{
		ProfSite:    "com.example.shop.cache.ProductCache.load",
		ProfCallers: []any{"com.example.shop.CatalogService.find", "com.example.web.Handler.get"},
	}
	const want = "com.example.shop.cache.ProductCache.load ← com.example.shop.CatalogService.find ← com.example.web.Handler.get"
	if got := col.Text(rec); got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got := col.Sort(rec); got != want {
		t.Errorf("sort key = %q, want %q", got, want)
	}
	if got := col.TextAt(rec, 40); got != "c.e.s.c.ProductCache.load ← CatalogService.find ← Handler.get" {
		t.Errorf("drawn at 40 = %q", got)
	}
}

// A frame label is not always package.Class.method: a signature carries dots
// of its own, and a script frame is a path. Neither may lose what names the
// function, and wide runes count as the two cells they take.
func TestShortFrameReadsOtherLabelShapes(t *testing.T) {
	for _, tc := range []struct {
		label string
		width int
		want  string
	}{
		{"com.example.Foo.bar(java.lang.String)", 30, "c.e.Foo.bar(java.lang.String)"},
		{"com.example.Foo.bar(java.lang.String)", 20, "Foo.bar(…)"},
		{"com.example.Foo.bar(java.lang.String)", 9, "…o.bar(…)"},
		{"graphql/type/definition.js:resolveField", 30, "…pe/definition.js:resolveField"},
		{"node:internal/timers:processTimers", 20, "…imers:processTimers"},
		{"com.example.shop.CartService.total", 24, "c.e.s.CartService.total"},
		{"com.example.shop.CartService.total", 17, "CartService.total"},
		{"com.example.shop.CartService.total", 8, "…e.total"},
		{"com.例子.店.CartService.total", 25, "c.例.店.CartService.total"},
		{"com.例子.店.CartService.total", 24, "CartService.total"},
		{"main", 2, "…n"},
		{"", 5, ""},
	} {
		if got := shortFrame(tc.label, tc.width); got != tc.want {
			t.Errorf("shortFrame(%q, %d) = %q, want %q", tc.label, tc.width, got, tc.want)
		}
	}
}

// One pass over the label: a frame with thousands of segments costs what its
// length does, not its length squared.
func TestShortFrameIsLinear(t *testing.T) {
	label := strings.Repeat("pkg.", 20000) + "Class.method"
	allocs := testing.AllocsPerRun(5, func() { shortFrame(label, 40) })
	if allocs > 20 {
		t.Errorf("shortening a 20,000 segment label allocated %.0f times, want a handful", allocs)
	}
	if got := shortFrame(label, 40); got != "Class.method" {
		t.Errorf("got %q", got)
	}
}

// A share that is not a number draws nothing. On amd64 the conversion of NaN
// to int is negative, and the bar used to panic there.
func TestShareBarOfNotANumber(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		want := ""
		if math.IsInf(f, 1) {
			want = "████"
		}
		if got := shareBar(f, 4); got != want {
			t.Errorf("shareBar(%v, 4) = %q, want %q", f, got, want)
		}
	}
}

// The largest value FormatBytes writes in a unit is ten cells wide.
func TestByteColumnsFitTheirWidestValue(t *testing.T) {
	const widest = "1023.9 MiB"
	if got := FormatBytes(1023*1024*1024 + 900*1024*1024/1000); got != widest {
		t.Fatalf("FormatBytes = %q, want %q", got, widest)
	}
	for _, col := range allocationColumns {
		if strings.HasPrefix(col.Title, "ALLOC") && col.Right || strings.HasPrefix(col.Title, "SURVIVED") {
			if col.Width < len(widest) {
				t.Errorf("column %s is %d wide, %q needs %d", col.Title, col.Width, widest, len(widest))
			}
		}
	}
}

// A site that allocated nothing has no ratio: the cell is blank, and it sorts
// with the blanks, not as a ratio of zero.
func TestBlankSurvivedRatioSortsAsBlank(t *testing.T) {
	var ratio Column
	for _, col := range allocationColumns {
		if col.Title == "SURV/ALLOC" {
			ratio = col
		}
	}
	none := map[string]any{ProfAllocBytes: 0.0, ProfSurvivedBytes: 0.0}
	if text, key := ratio.Text(none), ratio.Sort(none); text != "" || key != nil {
		t.Errorf("nothing allocated: cell %q, sort key %v; want blank and nil", text, key)
	}
	some := map[string]any{ProfAllocBytes: 200.0, ProfSurvivedBytes: 50.0}
	if text, key := ratio.Text(some), ratio.Sort(some); text != "0.25×" || key != 0.25 {
		t.Errorf("a quarter survived: cell %q, sort key %v", text, key)
	}
}

// The functions table holds the top rows by self samples, and says so: a
// frame high on every stack with no samples of its own can be missing.
func TestCaptionSaysWhatTheRowCapKept(t *testing.T) {
	twoHours, _ := ParseTimeframe("2h")
	summary := map[string]any{ProfSumSamples: 5000.0, ProfSumRows: 1234.0}
	got := strings.Join(hotspotsSpec.Caption(Scope{Timeframe: twoHours}, summary), " · ")
	if want := "top 300 of 1,234 functions by self samples"; !strings.Contains(got, want) {
		t.Errorf("caption %q does not say %q", got, want)
	}
	focus := map[string]any{ProfSumSamples: 900.0, ProfSumCallers: 87.0, ProfSumCallees: 12.0}
	got = strings.Join(hotspotsSpec.Caption(Scope{Timeframe: twoHours, Arg: "a.B.c"}, focus), " · ")
	if want := "top 25 of 87 callers"; !strings.Contains(got, want) {
		t.Errorf("focus caption %q does not say %q", got, want)
	}
	if strings.Contains(got, "callees") {
		t.Errorf("focus caption %q mentions callees, which were all kept", got)
	}
}

// The SELF and TOTAL bars of the functions table share one scale, the share
// of all samples: equal shares draw equal bars, and a flat profile looks flat.
func TestHotspotBarsShareOneScale(t *testing.T) {
	rows := hotspotsSpec.Decode([]map[string]any{
		{ProfSelf: 40.0, ProfSelfPct: 4.0, ProfTotal: 500.0, ProfTotalPct: 50.0},
		{ProfSelf: 20.0, ProfSelfPct: 2.0, ProfTotal: 40.0, ProfTotalPct: 4.0},
	})
	selfBar, totalBar := hotspotsSpec.Columns[2], hotspotsSpec.Columns[5]
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"4% self", selfBar.TextAt(rows[0], 8), "▎"},
		{"50% total", totalBar.TextAt(rows[0], 8), "████"},
		{"2% self", selfBar.TextAt(rows[1], 8), "▏"},
		{"4% total", totalBar.TextAt(rows[1], 8), "▎"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s draws %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if k := selfBar.Sort(rows[0]); k != 0.04 {
		t.Errorf("the 4%% self bar sorts as %v, want 0.04", k)
	}
}
