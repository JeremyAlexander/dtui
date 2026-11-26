package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dynatrace/dtctl/pkg/resources/profiling"
	"github.com/Dynatrace/dtctl/sdk/session"
	"github.com/dynatrace-oss/dtui/internal/tui"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

var updateScenarios = flag.Bool("update", false, "rewrite internal/tui/testdata/profiling/*.json with the sources' current output")

// profilingFixture is the synthetic recording of a Java web application the
// profiling views are rendered from: a hot JSON serialisation path, a
// connection pool its request threads queue for, JDBC and HTTP-client network
// waits, and a cache whose entries survive GC after GC.
const profilingFixture = "testdata/profiling/java-webapp"

var (
	shopProcess = &catalog.Entity{ID: "PROCESS-000000000000A001", Name: "shop-backend", Type: "PROCESS"}
	shop        = &scenarioEntity{ID: shopProcess.ID, Name: shopProcess.Name, Type: shopProcess.Type}
	// scenarioNow is the instant the scenarios are analysed at; it equals the
	// snapshot tests' pinned clock, so the captions agree with the header.
	scenarioNow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
)

// profilingScenario is what one call of a profiling source returned, in the
// shape internal/tui's snapshot tests replay: internal/tui cannot import the
// sources, so the rows and failures they render are recorded here.
type profilingScenario struct {
	View      string           `json:"view"`
	Entity    *scenarioEntity  `json:"entity,omitempty"`
	Timeframe string           `json:"timeframe"`
	Lens      string           `json:"lens,omitempty"`
	Arg       string           `json:"arg,omitempty"`
	Error     string           `json:"error,omitempty"`
	Records   []map[string]any `json:"records,omitempty"`
}

type scenarioEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (e *scenarioEntity) entity() *catalog.Entity {
	if e == nil {
		return nil
	}
	return &catalog.Entity{ID: e.ID, Name: e.Name, Type: e.Type}
}

// roundTrip is an environment that answers every request the same way.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func answering(status int, header http.Header, body string) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			return nil, errors.New("profiling sent a " + r.Method + ": dtui is read-only (ADR-0011)")
		}
		if header == nil {
			header = http.Header{}
		}
		header.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: status, Header: header, Request: r,
			Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

// profilerOver is the dtctl-backed profiler talking to a stand-in environment.
func profilerOver(t *testing.T, env http.RoundTripper) dtctlProfiler {
	t.Helper()
	c, err := session.NewClient("https://env.example.invalid", "token")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP().SetTransport(analysisObserver{next: env}).SetRetryCount(0)
	return dtctlProfiler{h: profiling.NewHandler(c)}
}

func fixtureProfilerAt(t *testing.T, path string) *fixtureProfiler {
	t.Helper()
	p, err := newFixtureProfiler(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sourcesAt builds both sources with the analysis clock pinned.
func sourcesAt(p profiler, fixture string) (hotspots, allocations tui.Source) {
	runs := newProfileRuns()
	runs.now = func() time.Time { return scenarioNow }
	return hotspotsSource(p, runs, fixture), allocationsSource(p, runs, fixture)
}

func lensIndex(t *testing.T, name string) int {
	t.Helper()
	for i, l := range catalog.HotspotLenses {
		if l.Name == name {
			return i
		}
	}
	t.Fatalf("no hotspots lens %q", name)
	return 0
}

func timeframe(t *testing.T, label string) catalog.Timeframe {
	t.Helper()
	tf, ok := catalog.ParseTimeframe(label)
	if !ok {
		t.Fatalf("bad timeframe %q", label)
	}
	return tf
}

// TestProfilingScenarios runs the sources over the fixture and over
// stand-in environments that refuse, and pins what the views are handed. The
// recorded scenarios are the input of internal/tui's profiling snapshots.
func TestProfilingScenarios(t *testing.T) {
	apiError := func(code int, msg string) string {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": msg}})
		return string(b)
	}
	fixture := fixtureProfilerAt(t, profilingFixture)
	const hot = "com.fasterxml.jackson.core.json.UTF8JsonGenerator.writeString"

	cases := []struct {
		name string
		p    profiler
		sc   profilingScenario
	}{
		{"hotspots-running", fixture, profilingScenario{View: "hotspots", Entity: shop, Timeframe: "2h", Lens: "running"}},
		// A 24h selection: the analysis covers its last 2h, and says so.
		{"hotspots-lock", fixture, profilingScenario{View: "hotspots", Entity: shop, Timeframe: "24h", Lens: "lock"}},
		{"hotspots-focus", fixture, profilingScenario{View: "hotspots", Entity: shop, Timeframe: "2h", Lens: "running", Arg: hot}},
		{"allocations", fixture, profilingScenario{View: "allocations", Entity: shop, Timeframe: "2h"}},
		{"no-data", profilerOver(t, answering(200, nil, `{"dataRootNodeId":"0","dataNodes":[]}`)),
			profilingScenario{View: "hotspots", Entity: shop, Timeframe: "2h", Lens: "running"}},
		{"no-lock-samples", profilerOver(t, answering(200, nil, `{"dataRootNodeId":"1","dataNodes":[
			{"id":"1","classPath":"com.example.shop","className":"Main","methodName":"run","samples":{"RUNNING":40},"childIds":[]}]}`)),
			profilingScenario{View: "hotspots", Entity: shop, Timeframe: "2h", Lens: "lock"}},
		{"forbidden", profilerOver(t, answering(403, nil, apiError(403, "Forbidden"))),
			profilingScenario{View: "hotspots", Entity: shop, Timeframe: "2h", Lens: "running"}},
		{"not-found", profilerOver(t, answering(404, nil, apiError(404, "Not found"))),
			profilingScenario{View: "hotspots", Entity: shop, Timeframe: "2h", Lens: "running"}},
		{"throttled", profilerOver(t, answering(429, http.Header{"Retry-After": []string{"0"}}, apiError(429, "Too many requests"))),
			profilingScenario{View: "allocations", Entity: shop, Timeframe: "2h"}},
		{"service-refused", fixture, profilingScenario{View: "hotspots", Timeframe: "2h", Lens: "running",
			Entity: &scenarioEntity{ID: "SERVICE-000000000000B001", Name: "shop-frontend", Type: "SERVICE"}}},
		{"no-process", fixture, profilingScenario{View: "hotspots", Timeframe: "2h", Lens: "running"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := tc.sc
			name := ""
			if tc.p == profiler(fixture) {
				name = filepath.Base(profilingFixture)
			}
			hotspots, allocations := sourcesAt(tc.p, name)
			scope := catalog.Scope{Entity: sc.Entity.entity(), Timeframe: timeframe(t, sc.Timeframe), Arg: sc.Arg}
			src := allocations
			if sc.View == "hotspots" {
				src = hotspots
				scope.Lens = lensIndex(t, sc.Lens)
			}
			records, err := src(context.Background(), scope, "")
			if err != nil {
				sc.Error = err.Error()
			}
			sc.Records = records
			assertScenario(t, tc.name, sc)
		})
	}
}

// assertScenario diffs a scenario against its recorded file, one record per
// line so a changed row reads as a one-line diff.
func assertScenario(t *testing.T, name string, sc profilingScenario) {
	t.Helper()
	records := sc.Records
	sc.Records = nil
	head, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.Write(bytes.TrimSuffix(head, []byte("}")))
	b.WriteString(",\n\"records\":[")
	for i, rec := range records {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n")
		b.Write(line)
	}
	b.WriteString("\n]}\n")

	path := filepath.Join("internal", "tui", "testdata", "profiling", name+".json")
	if *updateScenarios {
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test . -run TestProfilingScenarios -update` to record)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b.Bytes()) {
		t.Errorf("%s is stale: the source now returns something else (go test . -run TestProfilingScenarios -update, then update the snapshots)", path)
	}
}

// The fixture's shape is the story the views are meant to tell; a regenerated
// recording that loses it would still render.
func TestProfilingFixtureTellsItsStory(t *testing.T) {
	hotspots, allocations := sourcesAt(fixtureProfilerAt(t, profilingFixture), "java-webapp")
	read := func(src tui.Source, lens string, arg string) (map[string]any, []map[string]any) {
		t.Helper()
		scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h"), Arg: arg}
		if lens != "" {
			scope.Lens = lensIndex(t, lens)
		}
		records, err := src(context.Background(), scope, "")
		if err != nil {
			t.Fatal(err)
		}
		return catalog.SplitSummary(records)
	}
	top := func(rows []map[string]any, key string) string {
		t.Helper()
		if len(rows) == 0 {
			t.Fatal("no rows")
		}
		return catalog.Str(rows[0], key)
	}

	summary, rows := read(hotspots, "running", "")
	if got := top(rows, catalog.ProfFunction); !strings.Contains(got, "jackson") {
		t.Errorf("hottest running function = %s, want the JSON serialisation path", got)
	}
	if n, _ := catalog.FloatValue(summary[catalog.ProfSumRows]); n < 300 {
		t.Errorf("fixture has %v functions, want a few hundred", n)
	}
	if len(rows) != catalog.ProfileRowCap {
		t.Errorf("rows = %d, want the cap %d", len(rows), catalog.ProfileRowCap)
	}
	if summary[catalog.ProfSumFixture] != "java-webapp" {
		t.Errorf("summary does not name the fixture: %v", summary)
	}
	_, rows = read(hotspots, "lock", "")
	if got := top(rows, catalog.ProfFunction); got != "java.util.concurrent.locks.LockSupport.parkNanos" {
		t.Errorf("top lock function = %s, want the pool's parked borrowers", got)
	}
	_, rows = read(hotspots, "net_io", "")
	if got := top(rows, catalog.ProfFunction); got != "sun.nio.ch.NioSocketImpl.read" {
		t.Errorf("top net_io function = %s, want the socket read under JDBC", got)
	}

	// Focus: total and self first, then who calls it and what it calls.
	summary, rows = read(hotspots, "lock", "com.zaxxer.hikari.util.ConcurrentBag.borrow")
	var relations []string
	for _, r := range rows {
		relations = append(relations, catalog.Str(r, catalog.ProfRelation))
	}
	if got := strings.Join(relations, " "); got != "total self caller callee" {
		t.Errorf("focus relations = %q", got)
	}
	if total, _ := catalog.FloatValue(summary[catalog.ProfSumSamples]); total <= 0 {
		t.Errorf("focus summary carries no total: %v", summary)
	}
	// A function with no samples in the lens' state is empty, not an error.
	summary, rows = read(hotspots, "disk_io", "com.zaxxer.hikari.util.ConcurrentBag.borrow")
	if len(rows) != 0 || summary[catalog.ProfSumEmpty] != catalog.ProfEmptyState {
		t.Errorf("focus without samples: %d rows, summary %v", len(rows), summary)
	}

	summary, rows = read(allocations, "", "")
	if got := top(rows, catalog.ProfSite); got != "com.fasterxml.jackson.core.util.TextBuffer.contentsAsString" {
		t.Errorf("top allocation site = %s", got)
	}
	survivedMoreThanAllocated := false
	for _, r := range rows {
		alloc, _ := catalog.FloatValue(r[catalog.ProfAllocBytes])
		survived, _ := catalog.FloatValue(r[catalog.ProfSurvivedBytes])
		if survived > 4*alloc && strings.HasSuffix(catalog.Str(r, catalog.ProfSite), "ProductCache.put") {
			survivedMoreThanAllocated = true
		}
	}
	if !survivedMoreThanAllocated {
		t.Error("the cache's allocations should survive several GCs: survived well above allocated")
	}
	if a, _ := catalog.FloatValue(summary[catalog.ProfSumAlloc]); a <= 0 {
		t.Errorf("allocations summary carries no total: %v", summary)
	}
}

// A fixture is one file or a directory, and a view it has no recording for
// says so instead of asking the environment.
func TestProfilingFixtureOfOneKind(t *testing.T) {
	p := fixtureProfilerAt(t, filepath.Join(profilingFixture, "allocations.json"))
	hotspots, allocations := sourcesAt(p, "allocations.json")
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	if _, err := allocations(context.Background(), scope, ""); err != nil {
		t.Fatalf("allocations from a single file: %v", err)
	}
	_, err := hotspots(context.Background(), scope, "")
	var f *profileFailure
	if !errors.As(err, &f) || f.Reason != profileNoRecording {
		t.Fatalf("hotspots without a recording: %v", err)
	}

	bad := filepath.Join(t.TempDir(), "rows.json")
	if err := os.WriteFile(bad, []byte(`[{"self":1}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newFixtureProfiler(bad, 0); err == nil {
		t.Error("a file that is not a raw analysis must be refused")
	}
}

// The developer switch is the only way to a fixture, and a broken one fails
// the views rather than falling back to the environment.
func TestProfilingSourcesHonourTheFixtureSwitch(t *testing.T) {
	c, err := session.NewClient("https://env.example.invalid", "token")
	if err != nil {
		t.Fatal(err)
	}
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}

	t.Setenv(profilingFixtureEnv, profilingFixture)
	hotspots, _ := profilingSourcesFor(c)
	records, err := hotspots(context.Background(), scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if summary, _ := catalog.SplitSummary(records); summary[catalog.ProfSumFixture] != "java-webapp" {
		t.Errorf("fixture rows are not labelled: %v", summary)
	}

	t.Setenv(profilingFixtureEnv, filepath.Join(t.TempDir(), "missing"))
	hotspots, allocations := profilingSourcesFor(c)
	for _, src := range []tui.Source{hotspots, allocations} {
		if _, err := src(context.Background(), scope, ""); err == nil || !strings.Contains(err.Error(), profilingFixtureEnv) {
			t.Errorf("a missing fixture must fail naming the switch, got %v", err)
		}
	}
}

func TestProfilingFailuresAreClassified(t *testing.T) {
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	for status, want := range map[int]profileReason{
		400: profileBadRequest, 403: profileForbidden, 404: profileNotFound, 429: profileThrottled,
	} {
		env := answering(status, http.Header{"Retry-After": []string{"0"}}, `{"error":{"code":0,"message":"refused"}}`)
		hotspots, _ := sourcesAt(profilerOver(t, env), "")
		_, err := hotspots(context.Background(), scope, "")
		var f *profileFailure
		if !errors.As(err, &f) || f.Reason != want {
			t.Errorf("HTTP %d: got %v, want reason %s", status, err, want)
		}
	}
	// Both scopes and the IAM policy are what a 403 is about.
	msg := (&profileFailure{Reason: profileForbidden}).Error()
	for _, want := range []string{"storage:profiles:read", "storage:entities:read", "IAM policy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("403 wording lacks %q: %s", want, msg)
		}
	}
	// A 500 is not one of dtui's reasons: it is shown as the API put it.
	hotspots, _ := sourcesAt(profilerOver(t, answering(500, nil, `{"error":{"code":500,"message":"boom"}}`)), "")
	_, err := hotspots(context.Background(), scope, "")
	var f *profileFailure
	if err == nil || errors.As(err, &f) {
		t.Errorf("HTTP 500: got %v, want the API's own error", err)
	}
}

// A view without a process, or on a service, is refused before any request.
func TestProfilingRefusesWithoutAProcess(t *testing.T) {
	sent := 0
	env := roundTrip(func(*http.Request) (*http.Response, error) {
		sent++
		return nil, errors.New("no request expected")
	})
	hotspots, allocations := sourcesAt(profilerOver(t, env), "")
	for _, tc := range []struct {
		entity *catalog.Entity
		want   profileReason
	}{
		{nil, profileNoProcess},
		{&catalog.Entity{ID: "SERVICE-000000000000B001", Type: "SERVICE"}, profileNotAProcess},
		{&catalog.Entity{ID: "HOST-000000000000C001", Type: "HOST"}, profileNotAProcess},
	} {
		for _, src := range []tui.Source{hotspots, allocations} {
			_, err := src(context.Background(), catalog.Scope{Entity: tc.entity, Timeframe: timeframe(t, "2h")}, "")
			var f *profileFailure
			if !errors.As(err, &f) || f.Reason != tc.want {
				t.Errorf("entity %v: got %v, want %s", tc.entity, err, tc.want)
			}
		}
	}
	if sent != 0 {
		t.Errorf("a refused view sent %d requests", sent)
	}
}

// The analysed window is the timeframe, or its last 2h — never more.
func TestProfilingAsksForAtMostTwoHours(t *testing.T) {
	var asked []string
	env := roundTrip(func(r *http.Request) (*http.Response, error) {
		asked = append(asked, r.URL.Query().Get("from")+"-"+r.URL.Query().Get("to"))
		return answering(200, nil, `{"dataNodes":[]}`)(r)
	})
	hotspots, _ := sourcesAt(profilerOver(t, env), "")
	for _, label := range []string{"30m", "2h", "24h", "7d"} {
		records, err := hotspots(context.Background(), catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, label)}, "")
		if err != nil {
			t.Fatal(err)
		}
		summary, _ := catalog.SplitSummary(records)
		from, _ := time.Parse(time.RFC3339, catalog.Str(summary, catalog.ProfSumFrom))
		to, _ := time.Parse(time.RFC3339, catalog.Str(summary, catalog.ProfSumTo))
		want := min(timeframe(t, label).Dur, catalog.ProfileMaxWindow)
		if !to.Equal(scenarioNow) || to.Sub(from) != want {
			t.Errorf("%s: analysed %s..%s, want the last %s", label, from, to, want)
		}
	}
	// 24h and 7d are different selections but the same request.
	want := []string{
		fmt.Sprintf("%d-%d", scenarioNow.Add(-30*time.Minute).UnixMilli(), scenarioNow.UnixMilli()),
		fmt.Sprintf("%d-%d", scenarioNow.Add(-2*time.Hour).UnixMilli(), scenarioNow.UnixMilli()),
	}
	want = append(want, want[1], want[1])
	if strings.Join(asked, " ") != strings.Join(want, " ") {
		t.Errorf("asked the environment for %v, want %v", asked, want)
	}
}

// blockingProfiler runs analyses that end only when their context does.
type blockingProfiler struct {
	mu       sync.Mutex
	started  int
	release  chan struct{}
	finished chan error
}

func (b *blockingProfiler) Eligible(string) bool { return true }

func (b *blockingProfiler) Hotspots(ctx context.Context, _ profileQuery) (hotspotReading, error) {
	b.mu.Lock()
	b.started++
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		b.finished <- ctx.Err()
		return nil, ctx.Err()
	case <-b.release:
		b.finished <- nil
		return emptyHotspots{}, nil
	}
}

func (b *blockingProfiler) Allocations(context.Context, profileQuery) (allocationReading, error) {
	return nil, errors.New("unused")
}

type emptyHotspots struct{}

func (emptyHotspots) Functions(string) ([]functionRow, int, string, error) {
	return nil, 0, catalog.ProfEmptyNoSamples, nil
}
func (emptyHotspots) Focus(string, string) ([]focusRow, string, error) { return nil, "", nil }

// Lenses and focus drills read one analysis: a call joins the one in flight,
// a later one gets the cached result, and the analysis is cancelled only once
// nobody has waited for it for the grace period.
func TestProfilingSharesAndCancelsOneAnalysis(t *testing.T) {
	p := &blockingProfiler{release: make(chan struct{}), finished: make(chan error, 4)}
	runs := newProfileRuns()
	runs.now = func() time.Time { return scenarioNow }
	runs.grace = 150 * time.Millisecond
	hotspots := hotspotsSource(p, runs, "")
	call := func(ctx context.Context, lens string) chan error {
		done := make(chan error, 1)
		scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h"), Lens: lensIndex(t, lens)}
		go func() {
			_, err := hotspots(ctx, scope, "")
			done <- err
		}()
		return done
	}
	started := func() int {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.started
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(time.Millisecond)
		}
	}
	waiters := func() int {
		runs.mu.Lock()
		defer runs.mu.Unlock()
		n := 0
		for _, run := range runs.runs {
			n += len(run.waiters)
		}
		return n
	}

	// A lens switch cancels the view's call first and issues the next one
	// after: for a moment nobody waits, and the analysis must survive that.
	first, cancelFirst := context.WithCancel(context.Background())
	firstDone := call(first, "running")
	waitFor("the analysis to start", func() bool { return started() == 1 })
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("superseded call returned %v", err)
	}
	second, cancelSecond := context.WithCancel(context.Background())
	secondDone := call(second, "lock")
	waitFor("the lock lens to join", func() bool { return waiters() == 1 })
	select {
	case err := <-p.finished:
		t.Fatalf("the analysis ended (%v) while a lens waits for it", err)
	case <-time.After(2 * runs.grace):
	}
	if started() != 1 {
		t.Fatalf("a lens switch started analysis number %d", started())
	}

	// Leaving the view cancels the last waiter and, after the grace, the
	// analysis.
	cancelSecond()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("left call returned %v", err)
	}
	select {
	case err := <-p.finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the abandoned analysis ended with %v, want it cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned analysis kept running: it would hold a concurrency slot")
	}

	// Coming back runs it again; once it finished, other lenses reuse it.
	thirdDone := call(context.Background(), "running")
	waitFor("the second analysis to start", func() bool { return started() == 2 })
	close(p.release)
	if err := <-thirdDone; err != nil {
		t.Fatal(err)
	}
	for _, lens := range []string{"lock", "net_io", "wait"} {
		if err := <-call(context.Background(), lens); err != nil {
			t.Fatal(err)
		}
	}
	if started() != 2 {
		t.Errorf("%d analyses ran, want 2: lenses must share one", started())
	}
}

// What the environment answers while an analysis runs reaches the waiting
// view as progress.
func TestAnalysisProgressNotes(t *testing.T) {
	var notes []string
	ctx := context.WithValue(context.Background(), profileNoteKey{}, func(s string) { notes = append(notes, s) })
	p := profilerOver(t, answering(429, http.Header{"Retry-After": []string{"0"}}, `{"error":{"code":429,"message":"busy"}}`))
	if _, err := p.Hotspots(ctx, profileQuery{Entity: shopProcess.ID, From: scenarioNow.Add(-time.Hour), To: scenarioNow}); err == nil {
		t.Fatal("a throttled analysis must fail in the end")
	}
	if len(notes) < 2 || !strings.Contains(notes[0], "throttled (429, 1× so far)") || !strings.Contains(notes[1], "2× so far") {
		t.Errorf("throttle notes = %q", notes)
	}

	var prog analysisProgress
	if got := prog.note(http.StatusAccepted, ""); !strings.Contains(got, "accepted") {
		t.Errorf("first 202 = %q", got)
	}
	if got := prog.note(http.StatusAccepted, ""); !strings.Contains(got, "asked 1×") {
		t.Errorf("second 202 = %q", got)
	}
	if got := prog.note(http.StatusOK, ""); got != "" {
		t.Errorf("a result needs no note, got %q", got)
	}
}
