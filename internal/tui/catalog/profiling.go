package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Code-level profiling of one process, as one view: where its threads spend
// their samples, one lens per thread state, and where it allocates, the last
// lens. It is API-backed — an analysis is run by the "hotspots" source in the
// main package, never by DQL — and needs a process: the scope's Entity, set
// by the 'h' drill on a processes row or by the command-bar argument
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

// ProfileRowCap is how many rows a profiling source returns at most, the
// first of its own order (functions by self samples, allocation paths by
// allocated bytes); the summary's ProfSumRows says how many there were.
const ProfileRowCap = 300

// ProfileFocusCap is how many callers, and how many callees, a focused
// function lists at most; ProfSumCallers and ProfSumCallees say how many it
// has.
const ProfileFocusCap = 25

// Row keys of the profiling view.
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
	ProfAllocPct      = "allocPct" // percent of all bytes allocated
	ProfSurvivedBytes = "survivedBytes"
	ProfAllocCount    = "allocCount"

	// ProfEntityID / ProfEntityName name the analysed process on every row, so
	// a row can hand its process to the sibling view, the pin and the web UI.
	// Hidden from the inspector like every "__" key (ADR-0016).
	ProfEntityID   = "__process.id"
	ProfEntityName = "__process.name"
)

// Summary keys of the profiling view.
const (
	ProfSumFrom     = "from"    // RFC3339: start of the window analysed
	ProfSumTo       = "to"      // RFC3339: its end
	ProfSumClamped  = "clamped" // true when that is only the end of the selection
	ProfSumSamples  = "samples" // hotspots: samples counted (the base of the percentages)
	ProfSumRows     = "rows"    // rows before ProfileRowCap
	ProfSumCallers  = "callers" // focus: callers before ProfileFocusCap
	ProfSumCallees  = "callees" // focus: callees before ProfileFocusCap
	ProfSumEmpty    = "empty"   // one of the ProfEmpty* reasons
	ProfSumAlloc    = "allocBytes"
	ProfSumSurvived = "survivedBytes"
)

// Why a profiling result has no rows.
const (
	ProfEmptyNoSamples = "no_samples"       // the window holds no samples at all
	ProfEmptyState     = "no_state_samples" // no samples in the lens' thread state
	ProfEmptyFocus     = "focus"            // the allocations lens on a focused function
)

// Relations of a focus row that the views tell apart; "self" is the fourth.
const (
	ProfRelTotal  = "total"
	ProfRelCaller = "caller"
	ProfRelCallee = "callee"
)

// profRootCaller is the caller of a function nothing above it called.
const profRootCaller = "(root)"

// profAllocations names the lens that reads the allocations analysis.
const profAllocations = "allocations"

// HotspotLenses are the readings of the profiling view: the thread states
// the hotspots analysis counts, one at a time, and the allocations analysis.
// A state's Name is also the value of dtctl's --state flag. The source reads
// the lens from Scope.Lens; there is no DQL filter to compose.
var HotspotLenses = []Lens{
	{Name: "running", Desc: "samples on CPU"},
	{Name: "lock", Desc: "samples waiting for a lock or monitor"},
	{Name: "net_io", Desc: "samples blocked on network I/O"},
	{Name: "disk_io", Desc: "samples blocked on disk I/O"},
	{Name: "wait", Desc: "samples of parked or sleeping threads"},
	{Name: profAllocations, Desc: "allocated bytes per allocation site and call path", Columns: allocationColumns},
}

// HotspotState is the thread state a hotspots scope counts. It is not one on
// the allocations lens: see ProfileAllocations.
func HotspotState(s Scope) string { return lensAt(HotspotLenses, s.Lens).Name }

// ProfileAllocations reports whether the scope reads the allocations
// analysis and not a thread state of the hotspots one.
func ProfileAllocations(s Scope) bool { return HotspotState(s) == profAllocations }

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

// profileWindowLabel words the window for the caption, from what the source
// says it analysed: never the selected timeframe alone when less than it was
// analysed, and not by the clock, which has moved on since.
func profileWindowLabel(s Scope, summary map[string]any) string {
	clamped := summary[ProfSumClamped] == true
	var label string
	switch {
	case s.Timeframe.Absolute() && clamped:
		label = "last 2h of the selected window"
	case s.Timeframe.Absolute():
		label = "selected window"
	case clamped:
		label = "last 2h of the selected " + s.Timeframe.Label
	default:
		label = "last " + s.Timeframe.Label
	}
	from, errFrom := time.Parse(time.RFC3339, Str(summary, ProfSumFrom))
	to, errTo := time.Parse(time.RFC3339, Str(summary, ProfSumTo))
	if errFrom != nil || errTo != nil {
		return label
	}
	// A clock time alone means today.
	from, to = from.UTC(), to.UTC()
	const day = "2006-01-02"
	fromFmt, toFmt := "15:04", "15:04"
	if from.Format(day) != Now().UTC().Format(day) {
		fromFmt = "Jan 2 15:04"
	}
	if to.Format(day) != from.Format(day) {
		toFmt = "Jan 2 15:04"
	}
	return label + fmt.Sprintf(" (%s–%s UTC)", from.Format(fromFmt), to.Format(toFmt))
}

// profileEchoWindow renders the window as dtctl's window flags, so that the
// pasted command asks for what the view asks for. dtctl reads a start alone
// as the two hours after it, at most until now.
func profileEchoWindow(tf Timeframe) string {
	const lastTwoHours = "--default-timeframe-start 'now()-2h'"
	switch {
	case !tf.Absolute() && tf.Dur > ProfileMaxWindow:
		return lastTwoHours
	case !tf.Absolute():
		return fmt.Sprintf("--default-timeframe-start 'now()-%s'", tf.Label)
	case tf.To.IsZero() && ProfileWindowOf(tf, Now()).Clamped:
		// An open window that began more than two hours ago: its end is
		// now, whenever the command runs, and so is the cut.
		return lastTwoHours
	case tf.To.IsZero():
		return "--default-timeframe-start " + echoInstant(tf.From)
	}
	w := ProfileWindowOf(tf, tf.To)
	return "--default-timeframe-start " + echoInstant(w.From) + " --default-timeframe-end " + echoInstant(w.To)
}

// echoInstant is an instant as the analysis is asked for it: to the
// millisecond, which is written only when it is not zero.
func echoInstant(t time.Time) string {
	t = t.UTC().Truncate(time.Millisecond)
	if t.Nanosecond() == 0 {
		return t.Format(time.RFC3339)
	}
	return t.Format("2006-01-02T15:04:05.000Z07:00")
}

// profilable reports whether an analysis can run on the entity, by its type.
// The source has the last word; this decides what is offered and echoed.
func profilable(e Entity) bool {
	return e.Type == "PROCESS" || e.Type == "PROCESS_GROUP_INSTANCE" || e.Type == "PROCESS_GROUP"
}

// profileEntityArg is the entity argument of the echoed command: the scoped
// process, or a placeholder where there is none an analysis runs on, so that
// what is copied cannot be mistaken for a command that works.
func profileEntityArg(s Scope) string {
	if s.Entity == nil || s.Entity.ID == "" || !profilable(*s.Entity) {
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

func profileLoading(s Scope) string {
	if s.Entity == nil {
		return ""
	}
	name := s.Entity.Name
	if name == "" {
		name = s.Entity.ID
	}
	what := "hotspots"
	if ProfileAllocations(s) {
		what = "allocations"
	}
	return fmt.Sprintf("analysing the %s of %s, which can take minutes", what, name)
}

func profileEmpty(s Scope, summary map[string]any) string {
	switch Str(summary, ProfSumEmpty) {
	case ProfEmptyNoSamples:
		return "no samples in this window — the process may have been idle or restarted"
	case ProfEmptyFocus:
		return "allocations are read per process, not per function — esc returns to the functions, [ and ] to a thread state"
	case ProfEmptyState:
		where := "in this window"
		if s.Arg != "" {
			where = "with this function on the stack"
		}
		return fmt.Sprintf("no %s samples %s — [ and ] switch the thread state", HotspotState(s), where)
	}
	return ""
}

var hotspotsSpec = &Spec{
	Name:    "hotspots",
	Aliases: []string{"hot", "cpu", "profile"},
	Kind:    KindSignal,
	Desc:    "CPU, wait and allocation hotspots of a process — enter shows a function's callers and callees",
	API:     "hotspots",
	Lenses:  HotspotLenses,
	Columns: []Column{
		{Title: "SELF%", Width: 5, Right: true, Field: ProfSelfPct, Value: pctCell(ProfSelfPct)},
		{Title: "SELF", Width: 7, Right: true, Field: ProfSelf, Value: countCell(ProfSelf)},
		barColumn("SELF ▇", "__rel."+ProfSelfPct),
		{Title: "TOTAL%", Width: 6, Right: true, Field: ProfTotalPct, Value: pctCell(ProfTotalPct)},
		{Title: "TOTAL", Width: 7, Right: true, Field: ProfTotal, Value: countCell(ProfTotal)},
		barColumn("TOTAL ▇", "__rel."+ProfTotalPct),
		{Title: "PATHS", Width: 5, Right: true, Field: ProfPaths, Value: countCell(ProfPaths)},
		frameColumn("FUNCTION", ProfFunction),
	},
	// A focused function (Scope.Arg) reads as its callers and callees.
	ScopeColumns: func(s Scope) []Column {
		if s.Arg == "" || ProfileAllocations(s) {
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
		// Every bar is a share of what was counted (samples, or bytes
		// allocated), on one scale, so that equal shares draw equal bars
		// and a flat profile looks flat.
		for _, rec := range records {
			for _, key := range []string{ProfSelfPct, ProfTotalPct, ProfShare, ProfAllocPct} {
				if pct, ok := FloatValue(rec[key]); ok {
					rec["__rel."+key] = pct / 100
				}
			}
		}
		return records
	},
	// The command that prints what the view holds: the analysis is asked
	// for the waiting threads whatever the lens, and dtctl prints fewer
	// rows than the view unless told.
	Echo: func(s Scope, _ string) string {
		window := profileEchoWindow(s.Timeframe)
		if ProfileAllocations(s) {
			return fmt.Sprintf("dtctl exec profiling allocations %s --limit %d %s", profileEntityArg(s), ProfileRowCap, window)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "dtctl exec profiling hotspots %s", profileEntityArg(s))
		limit := ProfileRowCap
		if s.Arg != "" {
			fmt.Fprintf(&b, " --focus %s", shellSingleQuote(s.Arg))
			limit = ProfileFocusCap
		} else {
			b.WriteString(" --view functions")
		}
		fmt.Fprintf(&b, " --state %s --show-waiting --limit %d %s", HotspotState(s), limit, window)
		return b.String()
	},
	Caption: func(s Scope, summary map[string]any) []string {
		if summary == nil || Str(summary, ProfSumEmpty) == ProfEmptyFocus {
			return nil
		}
		segs := []string{profileWindowLabel(s, summary)}
		if ProfileAllocations(s) {
			alloc, _ := FloatValue(summary[ProfSumAlloc])
			survived, _ := FloatValue(summary[ProfSumSurvived])
			segs = append(segs, fmt.Sprintf("%s allocated, %s survived* (pressure, not heap content)",
				FormatBytes(int64(alloc)), FormatBytes(int64(survived))))
			if rows, _ := FloatValue(summary[ProfSumRows]); rows > ProfileRowCap {
				segs = append(segs, fmt.Sprintf("top %d of %s call paths by allocated bytes", ProfileRowCap, groupInt(int64(rows))))
			}
			return segs
		}
		state := HotspotState(s)
		samples, _ := FloatValue(summary[ProfSumSamples])
		rows, _ := FloatValue(summary[ProfSumRows])
		if s.Arg != "" {
			segs = append(segs, fmt.Sprintf("%s %s samples with the function on the stack",
				groupInt(int64(samples)), state))
			for _, dir := range [][2]string{{ProfSumCallers, "callers"}, {ProfSumCallees, "callees"}} {
				if n, _ := FloatValue(summary[dir[0]]); n > ProfileFocusCap {
					segs = append(segs, fmt.Sprintf("top %d of %s %s", ProfileFocusCap, groupInt(int64(n)), dir[1]))
				}
			}
		} else {
			segs = append(segs, fmt.Sprintf("%s %s samples (stacks seen, not time)",
				groupInt(int64(samples)), state))
			if rows > ProfileRowCap {
				segs = append(segs, fmt.Sprintf("top %d of %s functions by self samples", ProfileRowCap, groupInt(int64(rows))))
			}
		}
		segs = append(segs, "all threads, leaf: total")
		return segs
	},
	Empty:   profileEmpty,
	Loading: profileLoading,
	// The table shortens long frames; the selected one is spelled out here.
	Footnote: func(s Scope, summary, selected map[string]any) string {
		if ProfileAllocations(s) {
			if Str(summary, ProfSumEmpty) == ProfEmptyFocus {
				return ""
			}
			return survivedCaveat
		}
		if fn := Str(selected, ProfFunction); fn != "" {
			return "▸ " + fn
		}
		return ""
	},
	// A pinned process is analysed like one named in the command bar. The
	// source has the last word on what an analysis runs on.
	Scopable:      profilable,
	ArgLabel:      func(arg string) string { return shortFrame(arg, 32) },
	Entity:        profileEntity,
	EntityArg:     true,
	NoPreview:     true,
	CancelOnLeave: true,
	EnterTarget:   "function-focus", // sentinel: the same view, focused on the row's function
}

// FocusTarget names the function enter on a hotspots row focuses, or says
// why the row has none: it is the function already in focus, or the
// synthetic root. An allocation path has neither.
func FocusTarget(s Scope, rec map[string]any) (fn, why string) {
	if ProfileAllocations(s) {
		return "", ""
	}
	switch fn = Str(rec, ProfFunction); fn {
	case profRootCaller:
		return "", profRootCaller + " is where the stacks start, not a function"
	case s.Arg:
		return "", "this row is the function in focus — enter a caller or callee to walk on"
	}
	return fn, ""
}

// survivedCaveat says what the survived figures of an allocations analysis
// are, where a reader would otherwise take them for retained memory.
// It fits the 80 column minimum: a footnote is one line and is cut beyond.
const survivedCaveat = "* SURVIVED counts bytes once per GC outlived: can exceed ALLOC, not live heap"

var allocationColumns = []Column{
	// "1023.9 MiB" is the widest a byte count gets.
	{Title: "ALLOC", Width: 10, Right: true, Field: ProfAllocBytes, Value: bytesCell(ProfAllocBytes)},
	barColumn("ALLOC ▇", "__rel."+ProfAllocPct),
	{Title: "SURVIVED*", Width: 10, Right: true, Field: ProfSurvivedBytes, Value: bytesCell(ProfSurvivedBytes)},
	{Title: "SURV/ALLOC", Width: 10, Right: true, Value: survivedRatio, Sort: func(rec map[string]any) any {
		if r, ok := survivedRatioOf(rec); ok {
			return r
		}
		return nil // blank, like its cell
	}},
	{Title: "COUNT", Width: 11, Right: true, Field: ProfAllocCount, Value: countCell(ProfAllocCount)},
	// The cell is drawn to its width; the filter and the sort read the
	// whole path.
	{
		Title: "SITE ← CALLERS", Value: allocationPathText, ValueW: allocationPath,
		Sort: func(rec map[string]any) any { return allocationPathText(rec) },
	},
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
			return shareBar(f, width)
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
		ValueW: func(rec map[string]any, width int) string { return shortFrame(Str(rec, key), width) },
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

// allocationPathText is an allocation row's site and who called it, in full.
func allocationPathText(rec map[string]any) string {
	parts := []string{Str(rec, ProfSite)}
	callers, _ := rec[ProfCallers].([]any)
	for _, c := range callers {
		parts = append(parts, FormatValue(c))
	}
	return strings.Join(parts, " ← ")
}

// allocationPath draws an allocation row's path into width cells.
func allocationPath(rec map[string]any, width int) string {
	site := Str(rec, ProfSite)
	callers, _ := rec[ProfCallers].([]any)
	if len(callers) == 0 {
		return shortFrame(site, width)
	}
	// The site is the row's identity: it gets the room it needs, up to
	// two thirds, and the callers share the rest.
	siteW := min(runewidth.StringWidth(site), max(width*2/3, 8))
	parts := []string{shortFrame(site, siteW)}
	for _, c := range callers {
		parts = append(parts, tailFrame(FormatValue(c)))
	}
	return strings.Join(parts, " ← ")
}

// tailFrame is a frame's Class.method, without its package.
func tailFrame(label string) string {
	head, params := splitParams(label)
	if strings.ContainsAny(head, "/:") {
		return label
	}
	if i := lastDots(head, 2); i >= 0 {
		return head[i+1:] + params
	}
	return label
}

// splitParams cuts a frame label before its parameter list, if it has one:
// the dots inside "(java.lang.String)" separate no package.
func splitParams(label string) (head, params string) {
	if i := strings.IndexByte(label, '('); i > 0 {
		return label[:i], label[i:]
	}
	return label, ""
}

// lastDots is the index of the n-th dot from the end of s, or -1.
func lastDots(s string, n int) int {
	i := len(s)
	for ; n > 0 && i >= 0; n-- {
		i = strings.LastIndexByte(s[:i], '.')
	}
	return i
}

// shortFrame fits a frame label into width cells. A package.Class.method
// label has its package segments shrunk to their initial, left to right, then
// loses the package, then its parameter list; only then is it cut — from the
// left, since the method at its end is what tells two frames apart. A label
// of another shape (a script's path/file.js:function) is only cut.
func shortFrame(label string, width int) string {
	if width <= 0 || runewidth.StringWidth(label) <= width {
		return label
	}
	head, params := splitParams(label)
	if cut := lastDots(head, 2); cut >= 0 && !strings.ContainsAny(head, "/:") {
		// head[:cut] is the package, head[cut:] is ".Class.method".
		pkg, tail := head[:cut], head[cut:]+params
		over := runewidth.StringWidth(label) - width
		// How many leading segments must shrink to their initial for the
		// label to fit: one pass, nothing built until it is known.
		segs, at := 0, 0
		for over > 0 && at < len(pkg) {
			end := strings.IndexByte(pkg[at:], '.')
			if end < 0 {
				end = len(pkg) - at
			}
			seg := pkg[at : at+end]
			_, size := utf8.DecodeRuneInString(seg)
			over -= runewidth.StringWidth(seg[size:])
			segs++
			at += end + 1
		}
		if over <= 0 {
			var b strings.Builder
			b.Grow(len(label))
			rest := pkg
			for ; segs > 0; segs-- {
				seg, after, _ := strings.Cut(rest, ".")
				_, size := utf8.DecodeRuneInString(seg)
				b.WriteString(seg[:size])
				b.WriteByte('.')
				rest = after
			}
			if rest != "" {
				b.WriteString(rest)
				b.WriteByte('.')
			}
			b.WriteString(tail[1:])
			return b.String()
		}
		label = tail[1:]
		if runewidth.StringWidth(label) <= width {
			return label
		}
	}
	if params != "" && params != "(…)" {
		label = strings.TrimSuffix(label, params) + "(…)"
		if runewidth.StringWidth(label) <= width {
			return label
		}
	}
	return cutLeft(label, width)
}

// cutLeft keeps the last width cells of s, the first of them an ellipsis.
func cutLeft(s string, width int) string {
	if width <= 1 {
		return "…"
	}
	i, room := len(s), width-1
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if w := runewidth.RuneWidth(r); w > room {
			break
		} else {
			room -= w
		}
		i -= size
	}
	return "…" + s[i:]
}

// shareBar draws a 0..1 fraction as a bar of width cells, in eighths.
// Anything that is not a positive number draws nothing.
func shareBar(frac float64, width int) string {
	if width <= 0 || !(frac > 0) {
		return ""
	}
	frac = min(frac, 1)
	// Anything measured gets at least a sliver.
	eighths := max(int(frac*float64(width)*8+0.5), 1)
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
