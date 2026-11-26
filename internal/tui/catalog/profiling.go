package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Code-level profiling of one process: where its threads spend their samples
// (hotspots) and where it allocates (allocations). Both are API-backed — an
// analysis is run by the "hotspots" / "allocations" sources in the main
// package, never by DQL — and both need a process: the scope's Entity, set by
// the 'h' drill on a processes row or by the command-bar argument
// (`hotspots PROCESS-…`).
//
// The rows are dtui's own vocabulary (the Prof* keys below), filled in by the
// source, so the view does not depend on how dtctl names things. The source
// leads its result with a summary record (SummaryKey) carrying the ProfSum*
// keys: the window it actually analysed, the totals the shares are of, and why
// a result is empty.

// ProfileMaxWindow is the longest window one profiling analysis covers. The
// timeframe picker offers longer ones (24h, 7d), so a view over them analyses
// the last ProfileMaxWindow of the selection and says so (ADR-0007).
const ProfileMaxWindow = 2 * time.Hour

// ProfileRowCap is how many rows a profiling source returns at most; the
// summary's ProfSumRows says how many there were.
const ProfileRowCap = 300

// Row keys of the profiling views.
const (
	ProfFunction = "function" // frame label: package.Class.method
	ProfSelf     = "self"     // samples with the function on top of the stack
	ProfSelfPct  = "selfPct"
	ProfTotal    = "total" // samples with the function anywhere on the stack
	ProfTotalPct = "totalPct"
	ProfPaths    = "paths" // distinct call paths reaching the function

	ProfRelation = "relation" // focus rows: total, self, caller, callee
	ProfSamples  = "samples"
	ProfShare    = "share" // percent of the focused function's total

	ProfSite          = "site"    // allocation site: the allocating frame
	ProfCallers       = "callers" // []any of frames, innermost caller first
	ProfAllocBytes    = "allocBytes"
	ProfSurvivedBytes = "survivedBytes"
	ProfAllocCount    = "allocCount"
	ProfSurvivedCount = "survivedCount"

	// ProfEntityID / ProfEntityName name the analysed process on every row, so
	// a row can hand its process to the sibling view, the pin and the web UI.
	// Hidden from the inspector like every "__" key (ADR-0016).
	ProfEntityID   = "__process.id"
	ProfEntityName = "__process.name"
)

// Summary keys of the profiling views.
const (
	ProfSumFrom     = "from"    // RFC3339: start of the window analysed
	ProfSumTo       = "to"      // RFC3339: its end
	ProfSumSamples  = "samples" // hotspots: samples counted (the base of the percentages)
	ProfSumRows     = "rows"    // rows before ProfileRowCap
	ProfSumEmpty    = "empty"   // one of the ProfEmpty* reasons
	ProfSumFixture  = "fixture" // set when the rows come from a recorded analysis
	ProfSumAlloc    = "allocBytes"
	ProfSumSurvived = "survivedBytes"
)

// Why a profiling result has no rows.
const (
	ProfEmptyNoSamples = "no_samples"       // the window holds no samples at all
	ProfEmptyAppOnly   = "app_only"         // the app-only filter removed every frame
	ProfEmptyState     = "no_state_samples" // no samples in the lens' thread state
)

// Relations of a focus row.
const (
	ProfRelTotal  = "total"
	ProfRelSelf   = "self"
	ProfRelCaller = "caller"
	ProfRelCallee = "callee"
)

// ProfRootCaller is the caller of a function nothing above it called.
const ProfRootCaller = "(root)"

// HotspotLenses are the thread states the hotspots view counts, one at a
// time. Name is also the value of dtctl's --state flag. The source reads the
// state from Scope.Lens; there is no DQL filter to compose.
var HotspotLenses = []Lens{
	{Name: "running", Desc: "samples on CPU"},
	{Name: "lock", Desc: "samples waiting for a lock or monitor"},
	{Name: "net_io", Desc: "samples blocked on network I/O"},
	{Name: "disk_io", Desc: "samples blocked on disk I/O"},
	{Name: "wait", Desc: "samples of parked or sleeping threads"},
}

// HotspotState is the thread state a hotspots scope counts.
func HotspotState(s Scope) string { return lensAt(HotspotLenses, s.Lens).Name }

// ProfileWindow is the window a profiling view analyses for a timeframe.
type ProfileWindow struct {
	From, To time.Time
	// Clamped reports that the timeframe is longer than ProfileMaxWindow and
	// only its last ProfileMaxWindow is analysed.
	Clamped bool
}

// ProfileWindowOf resolves a timeframe to the window a profiling analysis
// covers: the timeframe itself, or its last ProfileMaxWindow when it is longer.
func ProfileWindowOf(tf Timeframe, now time.Time) ProfileWindow {
	to := now
	from := now.Add(-tf.Dur)
	if tf.Absolute() {
		from = tf.From
		if !tf.To.IsZero() {
			to = tf.To
		}
	}
	w := ProfileWindow{From: from, To: to}
	if to.Sub(from) > ProfileMaxWindow {
		w.From, w.Clamped = to.Add(-ProfileMaxWindow), true
	}
	return w
}

// profileWindowLabel words the window for the caption. It never shows the
// selected timeframe alone when less than it was analysed.
func profileWindowLabel(s Scope, summary map[string]any) string {
	w := ProfileWindowOf(s.Timeframe, Now())
	var label string
	switch {
	case s.Timeframe.Absolute() && w.Clamped:
		label = "last 2h of the selected window"
	case s.Timeframe.Absolute():
		label = "selected window"
	case w.Clamped:
		label = "last 2h of the selected " + s.Timeframe.Label
	default:
		label = "last " + s.Timeframe.Label
	}
	// The instants the source reports are what was analysed; the scope only
	// says what would be asked for now.
	from, errFrom := time.Parse(time.RFC3339, Str(summary, ProfSumFrom))
	to, errTo := time.Parse(time.RFC3339, Str(summary, ProfSumTo))
	if errFrom == nil && errTo == nil {
		label += fmt.Sprintf(" (%s–%s UTC)", from.UTC().Format("15:04"), to.UTC().Format("15:04"))
	}
	return label
}

// profileEchoWindow renders the window as dtctl's explicit window flags.
func profileEchoWindow(tf Timeframe) string {
	if !tf.Absolute() {
		label := tf.Label
		if tf.Dur > ProfileMaxWindow {
			label = "2h"
		}
		return fmt.Sprintf("--default-timeframe-start 'now()-%s'", label)
	}
	w := ProfileWindowOf(tf, Now())
	out := "--default-timeframe-start " + w.From.UTC().Format(time.RFC3339)
	if !tf.To.IsZero() {
		out += " --default-timeframe-end " + w.To.UTC().Format(time.RFC3339)
	}
	return out
}

// profileEntityArg is the entity argument of the echoed command: the scoped
// process, or a placeholder that says what is missing.
func profileEntityArg(s Scope) string {
	if s.Entity == nil || s.Entity.ID == "" {
		return "<PROCESS-id>"
	}
	return s.Entity.ID
}

// profileEntity reads the analysed process off a row.
func profileEntity(rec map[string]any) *Entity {
	id := Str(rec, ProfEntityID)
	if id == "" {
		return nil
	}
	typ := id
	if i := strings.LastIndex(id, "-"); i > 0 {
		typ = id[:i]
	}
	return &Entity{ID: id, Name: Str(rec, ProfEntityName), Type: typ}
}

func profileLoading(what string) func(Scope) string {
	return func(s Scope) string {
		if s.Entity == nil {
			return ""
		}
		name := s.Entity.Name
		if name == "" {
			name = s.Entity.ID
		}
		return fmt.Sprintf("analysing %s of %s, which can take minutes", what, name)
	}
}

func profileEmpty(s Scope, summary map[string]any) string {
	switch Str(summary, ProfSumEmpty) {
	case ProfEmptyNoSamples:
		return "no samples in this window — the process may have been idle or restarted"
	case ProfEmptyAppOnly:
		return "the app-only filter hid every frame — the profiler classified none as application code"
	case ProfEmptyState:
		where := "in this window"
		if s.Arg != "" {
			where = "with this function on the stack"
		}
		return fmt.Sprintf("no %s samples %s — [ and ] switch the thread state", HotspotState(s), where)
	}
	return ""
}

func profileFixture(summary map[string]any) []string {
	if name := Str(summary, ProfSumFixture); name != "" {
		return []string{"FIXTURE " + name + " (a recording, not this environment)"}
	}
	return nil
}

var hotspotsSpec = &Spec{
	Name:    "hotspots",
	Aliases: []string{"hot", "cpu", "profile"},
	Kind:    KindSignal,
	Desc:    "CPU and wait hotspots of a process, per function — enter shows callers and callees",
	API:     "hotspots",
	Lenses:  HotspotLenses,
	Columns: []Column{
		{Title: "SELF%", Width: 5, Right: true, Field: ProfSelfPct, Value: pctCell(ProfSelfPct)},
		{Title: "SELF", Width: 7, Right: true, Field: ProfSelf, Value: countCell(ProfSelf)},
		barColumn("SELF ▇", "__rel."+ProfSelf),
		{Title: "TOTAL%", Width: 6, Right: true, Field: ProfTotalPct, Value: pctCell(ProfTotalPct)},
		{Title: "TOTAL", Width: 7, Right: true, Field: ProfTotal, Value: countCell(ProfTotal)},
		barColumn("TOTAL ▇", "__rel."+ProfTotal),
		{Title: "PATHS", Width: 5, Right: true, Field: ProfPaths, Value: countCell(ProfPaths)},
		frameColumn("FUNCTION", ProfFunction),
	},
	// A focused function (Scope.Arg) reads as its callers and callees.
	ScopeColumns: func(s Scope) []Column {
		if s.Arg == "" {
			return nil
		}
		return []Column{
			{Title: "RELATION", Width: 8, Field: ProfRelation},
			{Title: "SHARE%", Width: 6, Right: true, Field: ProfShare, Value: pctCell(ProfShare)},
			barColumn("SHARE ▇", "__rel."+ProfShare),
			{Title: "SAMPLES", Width: 8, Right: true, Field: ProfSamples, Value: countCell(ProfSamples)},
			frameColumn("FUNCTION", ProfFunction),
		}
	},
	Decode: func(records []map[string]any) []map[string]any {
		stampRelative(records, ProfSelf)
		stampRelative(records, ProfTotal)
		for _, rec := range records {
			if share, ok := FloatValue(rec[ProfShare]); ok {
				rec["__rel."+ProfShare] = share / 100
			}
		}
		return records
	},
	Echo: func(s Scope, _ string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "dtctl exec profiling hotspots %s", profileEntityArg(s))
		if s.Arg != "" {
			fmt.Fprintf(&b, " --focus %s", shellSingleQuote(s.Arg))
		} else {
			b.WriteString(" --view functions")
		}
		state := HotspotState(s)
		fmt.Fprintf(&b, " --state %s", state)
		if state != "running" {
			// Threads that are not running are only sampled when asked for.
			b.WriteString(" --show-waiting")
		}
		return b.String() + " " + profileEchoWindow(s.Timeframe)
	},
	Caption: func(s Scope, summary map[string]any) []string {
		if summary == nil {
			return nil
		}
		segs := profileFixture(summary)
		segs = append(segs, profileWindowLabel(s, summary))
		state := HotspotState(s)
		samples, _ := FloatValue(summary[ProfSumSamples])
		rows, _ := FloatValue(summary[ProfSumRows])
		if s.Arg != "" {
			segs = append(segs, fmt.Sprintf("%s %s samples with the function on the stack",
				groupInt(int64(samples)), state))
		} else {
			segs = append(segs, fmt.Sprintf("%s %s samples (stacks seen, not time)",
				groupInt(int64(samples)), state))
			if rows > ProfileRowCap {
				segs = append(segs, fmt.Sprintf("%d of %s functions", ProfileRowCap, groupInt(int64(rows))))
			}
		}
		segs = append(segs, "all threads, leaf: total")
		return segs
	},
	Empty:   profileEmpty,
	Loading: profileLoading("the hotspots"),
	// The table shortens long frames; the selected one is spelled out here.
	Footnote: func(_ Scope, _, selected map[string]any) string {
		if fn := Str(selected, ProfFunction); fn != "" {
			return "▸ " + fn
		}
		return ""
	},
	ArgLabel:      func(arg string) string { return ShortFrame(arg, 32) },
	Entity:        profileEntity,
	EntityArg:     true,
	NoPreview:     true,
	CancelOnLeave: true,
	EnterTarget:   "function-focus", // sentinel: the same view, focused on the row's function
	Drills:        map[string]string{"h": "allocations"},
}

// FocusTarget names the function enter on a hotspots row focuses: the row's
// own, unless it is the synthetic root or the function already in focus.
func FocusTarget(s Scope, rec map[string]any) string {
	fn := Str(rec, ProfFunction)
	if fn == ProfRootCaller || fn == s.Arg {
		return ""
	}
	return fn
}

// SurvivedCaveat says what the survived figures of an allocations analysis
// are, where a reader would otherwise take them for retained memory.
const SurvivedCaveat = "* SURVIVED adds an object's bytes once per GC it lived through: not a size, can exceed ALLOC, not live heap"

var allocationsSpec = &Spec{
	Name:    "allocations",
	Aliases: []string{"alloc", "allocs", "memory"},
	Kind:    KindSignal,
	Desc:    "Allocation pressure of a process, per allocation site and call path",
	API:     "allocations",
	Columns: []Column{
		{Title: "ALLOC", Width: 9, Right: true, Field: ProfAllocBytes, Value: bytesCell(ProfAllocBytes)},
		barColumn("ALLOC ▇", "__rel."+ProfAllocBytes),
		{Title: "SURVIVED*", Width: 9, Right: true, Field: ProfSurvivedBytes, Value: bytesCell(ProfSurvivedBytes)},
		{Title: "SURV/ALLOC", Width: 10, Right: true, Value: survivedRatio, Sort: func(rec map[string]any) any {
			r, _ := survivedRatioOf(rec)
			return r
		}},
		{Title: "COUNT", Width: 11, Right: true, Field: ProfAllocCount, Value: countCell(ProfAllocCount)},
		{Title: "SITE ← CALLERS", ValueW: allocationPath},
	},
	Decode: func(records []map[string]any) []map[string]any {
		stampRelative(records, ProfAllocBytes)
		return records
	},
	Echo: func(s Scope, _ string) string {
		return fmt.Sprintf("dtctl exec profiling allocations %s %s", profileEntityArg(s), profileEchoWindow(s.Timeframe))
	},
	Caption: func(s Scope, summary map[string]any) []string {
		if summary == nil {
			return nil
		}
		segs := profileFixture(summary)
		segs = append(segs, profileWindowLabel(s, summary))
		alloc, _ := FloatValue(summary[ProfSumAlloc])
		survived, _ := FloatValue(summary[ProfSumSurvived])
		segs = append(segs, fmt.Sprintf("%s allocated, %s survived* (pressure, not heap content)",
			FormatBytes(int64(alloc)), FormatBytes(int64(survived))))
		if rows, _ := FloatValue(summary[ProfSumRows]); rows > ProfileRowCap {
			segs = append(segs, fmt.Sprintf("%d of %s call paths", ProfileRowCap, groupInt(int64(rows))))
		}
		return segs
	},
	Empty:         profileEmpty,
	Loading:       profileLoading("the allocations"),
	Footnote:      func(Scope, map[string]any, map[string]any) string { return SurvivedCaveat },
	Entity:        profileEntity,
	EntityArg:     true,
	NoPreview:     true,
	CancelOnLeave: true,
	Drills:        map[string]string{"h": "hotspots"},
}

// --- cells --------------------------------------------------------------------

func pctCell(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		f, ok := FloatValue(rec[key])
		if !ok {
			return ""
		}
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
}

func countCell(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		f, ok := FloatValue(rec[key])
		if !ok {
			return ""
		}
		return groupInt(int64(f))
	}
}

func bytesCell(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		f, ok := FloatValue(rec[key])
		if !ok {
			return ""
		}
		return FormatBytes(int64(f))
	}
}

// barColumn renders a 0..1 fraction stored under key as a bar.
func barColumn(title, key string) Column {
	return Column{
		Title: title, Width: 8, Min: 4,
		ValueW: func(rec map[string]any, width int) string {
			f, _ := FloatValue(rec[key])
			return ShareBar(f, width)
		},
		Sort: func(rec map[string]any) any {
			f, _ := FloatValue(rec[key])
			return f
		},
	}
}

// frameColumn is the flex column holding a frame label, shortened to fit.
func frameColumn(title, key string) Column {
	return Column{
		Title: title, Field: key,
		ValueW: func(rec map[string]any, width int) string { return ShortFrame(Str(rec, key), width) },
	}
}

// stampRelative stores each record's value under key as a fraction of the
// largest one, for the bar next to it: a share of 4% is the top of a flat
// profile, and a bar scaled to 100% would not show it.
func stampRelative(records []map[string]any, key string) {
	top := 0.0
	for _, rec := range records {
		if f, ok := FloatValue(rec[key]); ok && f > top {
			top = f
		}
	}
	if top == 0 {
		return
	}
	for _, rec := range records {
		if f, ok := FloatValue(rec[key]); ok {
			rec["__rel."+key] = f / top
		}
	}
}

func survivedRatioOf(rec map[string]any) (float64, bool) {
	alloc, _ := FloatValue(rec[ProfAllocBytes])
	survived, ok := FloatValue(rec[ProfSurvivedBytes])
	if !ok || alloc <= 0 {
		return 0, false
	}
	return survived / alloc, true
}

// survivedRatio is how many GCs the average allocated byte lived through.
func survivedRatio(rec map[string]any) string {
	r, ok := survivedRatioOf(rec)
	if !ok {
		return ""
	}
	return strconv.FormatFloat(r, 'f', 2, 64) + "×"
}

// allocationPath reads an allocation row as its site and who called it.
func allocationPath(rec map[string]any, width int) string {
	site := Str(rec, ProfSite)
	callers, _ := rec[ProfCallers].([]any)
	if len(callers) == 0 {
		return ShortFrame(site, width)
	}
	// The site is the row's identity: it gets the room it needs, up to
	// two thirds, and the callers share the rest.
	siteW := min(utf8.RuneCountInString(site), max(width*2/3, 8))
	parts := []string{ShortFrame(site, siteW)}
	for _, c := range callers {
		parts = append(parts, tailFrame(FormatValue(c)))
	}
	return strings.Join(parts, " ← ")
}

// tailFrame is a frame's Class.method, without its package.
func tailFrame(label string) string {
	segs := strings.Split(label, ".")
	if len(segs) <= 2 {
		return label
	}
	return strings.Join(segs[len(segs)-2:], ".")
}

// ShortFrame fits a frame label (package.Class.method) into width cells:
// package segments shrink to their initial, left to right, then the package
// goes, and only then is the label cut — from the left, since the method at
// its end is what tells two frames apart.
func ShortFrame(label string, width int) string {
	if width <= 0 || utf8.RuneCountInString(label) <= width {
		return label
	}
	segs := strings.Split(label, ".")
	// The last two segments are Class.method; what precedes is the package.
	for i := 0; i < len(segs)-2; i++ {
		if r, size := utf8.DecodeRuneInString(segs[i]); size > 0 && size < len(segs[i]) {
			segs[i] = string(r)
		}
		if short := strings.Join(segs, "."); utf8.RuneCountInString(short) <= width {
			return short
		}
	}
	if len(segs) > 2 {
		label = strings.Join(segs[len(segs)-2:], ".")
		if utf8.RuneCountInString(label) <= width {
			return label
		}
	}
	runes := []rune(label)
	if width == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-width+1:])
}

// ShareBar draws a 0..1 fraction as a bar of width cells, in eighths.
func ShareBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	frac = min(max(frac, 0), 1)
	eighths := int(frac*float64(width)*8 + 0.5)
	// Anything measured gets at least a sliver; only zero is blank.
	if eighths == 0 && frac > 0 {
		eighths = 1
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("█", eighths/8))
	if rest := eighths % 8; rest > 0 {
		b.WriteRune([]rune("▏▎▍▌▋▊▉")[rest-1])
	}
	return b.String()
}

// groupInt renders an integer with thousands separators ("48,213").
func groupInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
