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
	"github.com/Dynatrace/dtctl/sdk/api/codelevelanalysis"
	"github.com/Dynatrace/dtctl/sdk/httpclient"
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
	// A status that says nothing about an analysis is shown as the API put it.
	hotspots, _ := sourcesAt(profilerOver(t, answering(401, nil, `{"error":{"code":401,"message":"token expired"}}`)), "")
	_, err := hotspots(context.Background(), scope, "")
	var f *profileFailure
	if err == nil || errors.As(err, &f) || !strings.Contains(err.Error(), "API error (401): token expired") {
		t.Errorf("HTTP 401: got %v, want the API's own error", err)
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
	// 2h, 24h and 7d are different selections and one request, sent once.
	want := []string{
		fmt.Sprintf("%d-%d", scenarioNow.Add(-30*time.Minute).UnixMilli(), scenarioNow.UnixMilli()),
		fmt.Sprintf("%d-%d", scenarioNow.Add(-2*time.Hour).UnixMilli(), scenarioNow.UnixMilli()),
	}
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

func (b *blockingProfiler) Subject(id string) (string, bool) { return dtctlProfiler{}.Subject(id) }

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

// recordingProfiler answers every analysis at once and records what it was
// asked for.
type recordingProfiler struct {
	mu      sync.Mutex
	queries []profileQuery
	err     error
}

func (p *recordingProfiler) Subject(id string) (string, bool) { return dtctlProfiler{}.Subject(id) }

func (p *recordingProfiler) Hotspots(_ context.Context, q profileQuery) (hotspotReading, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, q)
	if p.err != nil {
		return nil, p.err
	}
	return emptyHotspots{}, nil
}

func (p *recordingProfiler) Allocations(context.Context, profileQuery) (allocationReading, error) {
	return nil, errors.New("unused")
}

func (p *recordingProfiler) asked() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queries)
}

// pendingTimers stands in for the grace timers of a profileRuns: the test
// fires them when it wants the grace to be over.
type pendingTimers struct {
	mu sync.Mutex
	fs []func()
}

func (p *pendingTimers) after(_ time.Duration, f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fs = append(p.fs, f)
}

func (p *pendingTimers) fire(i int) {
	p.mu.Lock()
	f := p.fs[i]
	p.mu.Unlock()
	f()
}

func (p *pendingTimers) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.fs)
}

// The grace runs from when the last watcher left. The timer of an earlier
// leave, from before somebody joined again, ends nothing.
func TestProfilingGraceRestartsWhenAWatcherRejoins(t *testing.T) {
	p := &blockingProfiler{release: make(chan struct{}), finished: make(chan error, 1)}
	timers := &pendingTimers{}
	runs := newProfileRuns()
	runs.now = func() time.Time { return scenarioNow }
	runs.after = timers.after
	hotspots := hotspotsSource(p, runs, "")
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	watch := func() (leave func()) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		before := timers.count()
		go func() {
			_, err := hotspots(ctx, scope, "")
			done <- err
		}()
		return func() {
			t.Helper()
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("a watcher that left got %v", err)
			}
			if timers.count() != before+1 {
				t.Fatalf("leaving armed %d timers, want 1", timers.count()-before)
			}
		}
	}
	waiting := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			runs.mu.Lock()
			got := 0
			for _, run := range runs.runs {
				got += len(run.waiters)
			}
			runs.mu.Unlock()
			if got == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d watchers, want %d", got, n)
			}
			time.Sleep(time.Millisecond)
		}
	}

	leaveA := watch()
	waiting(1)
	leaveA() // timer 0
	leaveB := watch()
	waiting(1)
	leaveB() // timer 1

	timers.fire(0)
	if n := cachedRuns(runs); n != 1 {
		t.Fatalf("the first leave's timer ended the analysis although the grace of the second had not passed (%d runs left)", n)
	}
	timers.fire(1)
	select {
	case err := <-p.finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the abandoned analysis ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the grace of the last leave passed and the analysis still runs")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started != 1 {
		t.Errorf("%d analyses ran, want 1", p.started)
	}
}

// A call that was cancelled before it got here starts nothing.
func TestProfilingStartsNothingForACancelledCall(t *testing.T) {
	p := &recordingProfiler{}
	runs := newProfileRuns()
	hotspots := hotspotsSource(p, runs, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := hotspots(ctx, catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}, "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call returned %v", err)
	}
	if n := cachedRuns(runs); n != 0 {
		t.Errorf("a cancelled call left %d analyses running or cached", n)
	}
	if n := p.asked(); n != 0 {
		t.Errorf("a cancelled call started %d analyses", n)
	}
}

// cachedRuns counts the analyses a profileRuns holds, running or finished.
func cachedRuns(runs *profileRuns) int {
	runs.mu.Lock()
	defer runs.mu.Unlock()
	return len(runs.runs)
}

// An analysis is reused for two minutes from when it arrived, however long it
// took: one that ran for 90s is not already stale 30s later.
func TestProfilingCacheAgesFromCompletion(t *testing.T) {
	p := &blockingProfiler{release: make(chan struct{}), finished: make(chan error, 4)}
	now := scenarioNow
	var clock sync.Mutex
	runs := newProfileRuns()
	runs.now = func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clock.Lock()
		now = now.Add(d)
		clock.Unlock()
	}
	hotspots := hotspotsSource(p, runs, "")
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	read := func() map[string]any {
		t.Helper()
		records, err := hotspots(context.Background(), scope, "")
		if err != nil {
			t.Fatal(err)
		}
		summary, _ := catalog.SplitSummary(records)
		return summary
	}
	started := func() int {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.started
	}

	first := make(chan map[string]any, 1)
	go func() { first <- read() }()
	for started() == 0 {
		time.Sleep(time.Millisecond)
	}
	advance(90 * time.Second) // the analysis takes 90s
	p.release <- struct{}{}
	if got := (<-first)[catalog.ProfSumTo]; got != "2026-07-15T12:00:00Z" {
		t.Fatalf("the first reading covers up to %v", got)
	}

	// A read that the cache does not answer starts an analysis, which the
	// test has to release: within reports whether one was needed.
	within := func() (map[string]any, bool) {
		t.Helper()
		got := make(chan map[string]any, 1)
		go func() { got <- read() }()
		select {
		case summary := <-got:
			return summary, false
		case p.release <- struct{}{}:
			return <-got, true
		}
	}
	advance(119 * time.Second)
	if summary, fresh := within(); fresh || summary[catalog.ProfSumTo] != "2026-07-15T12:00:00Z" {
		t.Fatalf("119s after it arrived: a new analysis=%v, window to %v; want the first one reused", fresh, summary[catalog.ProfSumTo])
	}
	advance(2 * time.Second)
	if summary, fresh := within(); !fresh || summary[catalog.ProfSumTo] != "2026-07-15T12:03:31Z" || started() != 2 {
		t.Errorf("121s after it arrived: a new analysis=%v, window to %v, %d analyses; want a second one up to 12:03:31",
			fresh, summary[catalog.ProfSumTo], started())
	}
}

// What is asked for decides whether two views share an analysis: the request,
// not how the selection was spelled.
func TestProfilingSharesAnAnalysisPerRequest(t *testing.T) {
	at := func(ms int) time.Time { return time.Date(2026, 7, 15, 9, 0, 0, ms*int(time.Millisecond), time.UTC) }
	closed := func(from, to time.Time) catalog.Timeframe {
		return catalog.Timeframe{From: from, To: to, Dur: to.Sub(from)}
	}
	instance := &catalog.Entity{ID: "PROCESS_GROUP_INSTANCE-000000000000A001", Type: "PROCESS_GROUP_INSTANCE"}
	for _, tc := range []struct {
		name   string
		scopes []catalog.Scope
		want   int
	}{
		{"24h and 7d are the same last 2h", []catalog.Scope{
			{Entity: shopProcess, Timeframe: timeframe(t, "24h")},
			{Entity: shopProcess, Timeframe: timeframe(t, "7d")},
			{Entity: shopProcess, Timeframe: timeframe(t, "2h")},
		}, 1},
		{"30m is not 2h", []catalog.Scope{
			{Entity: shopProcess, Timeframe: timeframe(t, "30m")},
			{Entity: shopProcess, Timeframe: timeframe(t, "2h")},
		}, 2},
		{"windows that differ within one second", []catalog.Scope{
			{Entity: shopProcess, Timeframe: closed(at(100), at(100).Add(time.Hour))},
			{Entity: shopProcess, Timeframe: closed(at(900), at(900).Add(time.Hour))},
		}, 2},
		{"one closed window asked for twice", []catalog.Scope{
			{Entity: shopProcess, Timeframe: closed(at(100), at(100).Add(time.Hour))},
			{Entity: shopProcess, Timeframe: closed(at(100), at(100).Add(time.Hour))},
		}, 1},
		{"two spellings of one process", []catalog.Scope{
			{Entity: shopProcess, Timeframe: timeframe(t, "2h")},
			{Entity: instance, Timeframe: timeframe(t, "2h")},
		}, 1},
		{"two processes", []catalog.Scope{
			{Entity: shopProcess, Timeframe: timeframe(t, "2h")},
			{Entity: &catalog.Entity{ID: "PROCESS-000000000000A002", Type: "PROCESS"}, Timeframe: timeframe(t, "2h")},
		}, 2},
	} {
		p := &recordingProfiler{}
		hotspots, _ := sourcesAt(p, "")
		for _, scope := range tc.scopes {
			if _, err := hotspots(context.Background(), scope, ""); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		}
		if got := p.asked(); got != tc.want {
			t.Errorf("%s: %d analyses, want %d", tc.name, got, tc.want)
		}
	}
}

// At most profileCacheSize finished analyses are kept, however many ran at
// once, and a failed one is not kept at all.
func TestProfilingKeepsAtMostFourAnalyses(t *testing.T) {
	kept := cachedRuns
	process := func(i int) *catalog.Entity {
		return &catalog.Entity{ID: fmt.Sprintf("PROCESS-00000000000000%02d", i), Type: "PROCESS"}
	}

	p := &blockingProfiler{release: make(chan struct{}), finished: make(chan error, 12)}
	runs := newProfileRuns()
	runs.now = func() time.Time { return scenarioNow }
	hotspots := hotspotsSource(p, runs, "")
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := hotspots(context.Background(), catalog.Scope{Entity: process(i), Timeframe: timeframe(t, "2h")}, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	for kept(runs) != 12 {
		time.Sleep(time.Millisecond)
	}
	close(p.release)
	wg.Wait()
	if got := kept(runs); got != 4 {
		t.Errorf("12 analyses that ran at once left %d cached, want %d", got, profileCacheSize)
	}

	failing := &recordingProfiler{err: errors.New("boom")}
	runs = newProfileRuns()
	hotspots = hotspotsSource(failing, runs, "")
	for i := range 4 {
		if _, err := hotspots(context.Background(), catalog.Scope{Entity: process(i), Timeframe: timeframe(t, "2h")}, ""); err == nil {
			t.Fatal("the analysis must fail")
		}
	}
	if got := kept(runs); got != 0 {
		t.Errorf("4 failed analyses left %d cached, want none", got)
	}
}

// Every failure the environment can answer with is worded for dtui: what
// happened and which key to press, never the SDK's own text, a task token or
// a sentence that ends in a colon.
func TestProfilingFailureWording(t *testing.T) {
	apiError := func(code int, msg string) string {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": msg}})
		return string(b)
	}
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		reason profileReason
		want   string
	}{
		{"400 with a message", 400, apiError(400, "from must be before to"), profileBadRequest,
			"the environment rejected the analysis (400): from must be before to"},
		{"400 without one", 400, "", profileBadRequest,
			"the environment rejected the analysis (400)"},
		{"429", 429, apiError(429, "Too many requests"), profileThrottled,
			"the environment is throttling profiling requests (429) and kept refusing this one — wait a moment, then press r"},
		{"500 with a message", 500, apiError(500, "boom"), profileServerError,
			"the environment failed on the analysis (500): boom — press r to ask again"},
		{"503 without one", 503, "", profileServerError,
			"the environment failed on the analysis (503) — press r to ask again"},
		{"a result that is not an object", 200, "[]", profileMalformed,
			"the environment's answer is not an analysis result (this API is experimental and may have changed): the result of a completed analysis is an array, not an object"},
	} {
		env := answering(tc.status, http.Header{"Retry-After": []string{"0"}}, tc.body)
		hotspots, _ := sourcesAt(profilerOver(t, env), "")
		_, err := hotspots(context.Background(), scope, "")
		var f *profileFailure
		if !errors.As(err, &f) || f.Reason != tc.reason {
			t.Errorf("%s: got %v, want reason %s", tc.name, err, tc.reason)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s reads\n %q, want\n %q", tc.name, err.Error(), tc.want)
		}
	}

	// What only a poll can meet, in the shape the SDK returns it.
	poll := func(err error) error {
		return profilingFailure(&codelevelanalysis.Error{Phase: codelevelanalysis.PhasePoll, Err: err}, shopProcess.ID)
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"an expired task",
			poll(fmt.Errorf("codelevelanalysis: poll (token t-4711): %w", httpclient.NewAPIError(404, "Unknown token", ""))),
			"the environment no longer knows this analysis (404 while waiting for its result) — press r to run it again"},
		{"an analysis that never completed",
			poll(fmt.Errorf("%w within 60s (token t-4711)", codelevelanalysis.ErrNotCompleted)),
			"the analysis was still running when dtui stopped asking for its result — press r to ask again"},
		{"a throttled poll",
			poll(fmt.Errorf("codelevelanalysis: poll (token t-4711): %w", httpclient.NewAPIError(429, "Too many requests", ""))),
			"the environment is throttling profiling requests (429) and kept refusing this one — wait a moment, then press r"},
		{"a window the profiler refuses",
			profilingFailure(&codelevelanalysis.Error{Phase: codelevelanalysis.PhaseValidate, Err: profiling.ErrWindowEmpty}, shopProcess.ID),
			"this analysis cannot be asked for: profiling: the window ends at or before its start"},
	} {
		var f *profileFailure
		if !errors.As(tc.err, &f) {
			t.Errorf("%s passed through unworded: %v", tc.name, tc.err)
			continue
		}
		if got := tc.err.Error(); got != tc.want || strings.Contains(got, "t-4711") {
			t.Errorf("%s reads\n %q, want\n %q", tc.name, got, tc.want)
		}
	}

	// A failure that is not one of an analysis keeps its own text.
	plain := errors.New("dial tcp: connection refused")
	if got := profilingFailure(plain, shopProcess.ID); got != plain {
		t.Errorf("a transport error became %v", got)
	}
}

// focusTree is a hotspots result with three functions whose labels contain
// one another: X.run has running samples only, the two longer ones are the
// only ones that waited for a lock.
const focusTree = `{"dataRootNodeId":"1","dataNodes":[
	{"id":"1","classPath":"com.example","className":"Main","methodName":"main","samples":{},"childIds":["2","3","4"]},
	{"id":"2","classPath":"com.example","className":"X","methodName":"run","samples":{"RUNNING":40},"childIds":[]},
	{"id":"3","classPath":"com.example","className":"X","methodName":"runAsync","samples":{"RUNNING":5,"LOCK":7},"childIds":[]},
	{"id":"4","classPath":"com.example","className":"X","methodName":"runRetry","samples":{"LOCK":3},"childIds":[]}]}`

// A focused function is the one whose row was entered, by its whole label. In
// a thread state it has no samples in, the view is empty and says so: it is
// neither an error nor quietly another function whose name contains it.
func TestProfilingFocusIsExact(t *testing.T) {
	hotspots, _ := sourcesAt(profilerOver(t, answering(200, nil, focusTree)), "")
	focus := func(lens, fn string) (map[string]any, []map[string]any) {
		t.Helper()
		records, err := hotspots(context.Background(),
			catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h"), Lens: lensIndex(t, lens), Arg: fn}, "")
		if err != nil {
			t.Fatalf("focus on %s in %s: %v", fn, lens, err)
		}
		return catalog.SplitSummary(records)
	}

	summary, rows := focus("running", "com.example.X.run")
	if len(rows) != 3 || summary[catalog.ProfSumSamples] != 40.0 {
		t.Errorf("X.run running: %d rows, %v samples; want total, self, caller and 40", len(rows), summary[catalog.ProfSumSamples])
	}
	// Two longer labels have lock samples, X.run has none.
	summary, rows = focus("lock", "com.example.X.run")
	if len(rows) != 0 || summary[catalog.ProfSumEmpty] != catalog.ProfEmptyState {
		t.Errorf("X.run lock: %d rows, empty=%v; want none and %s", len(rows), summary[catalog.ProfSumEmpty], catalog.ProfEmptyState)
	}
	// Exactly one longer label has samples: still not the function meant.
	summary, rows = focus("lock", "com.example.X.runRet")
	if len(rows) != 0 || summary[catalog.ProfSumEmpty] != catalog.ProfEmptyState {
		t.Errorf("a partial label: %d rows, empty=%v; want none and %s", len(rows), summary[catalog.ProfSumEmpty], catalog.ProfEmptyState)
	}
}

// A window that has not happened yet is refused before anything is asked,
// with a reason of dtui's own, and is not cached as an empty result.
func TestProfilingRefusesAWindowInTheFuture(t *testing.T) {
	sent := 0
	env := roundTrip(func(r *http.Request) (*http.Response, error) {
		sent++
		return answering(200, nil, `{"dataNodes":[]}`)(r)
	})
	hotspots, allocations := sourcesAt(profilerOver(t, env), "")
	for _, tc := range []struct {
		name string
		tf   catalog.Timeframe
		want string
	}{
		{"closed, all of it ahead", catalog.Timeframe{From: scenarioNow.Add(time.Hour), To: scenarioNow.Add(2 * time.Hour), Dur: time.Hour},
			"the selected window starts in the future: there is nothing to analyse yet — t picks another window"},
		{"open, starting ahead", catalog.Timeframe{From: scenarioNow.Add(time.Hour), Dur: time.Hour},
			"the selected window starts in the future: there is nothing to analyse yet — t picks another window"},
		{"closed, ending ahead", catalog.Timeframe{From: scenarioNow.Add(-time.Hour), To: scenarioNow.Add(time.Hour), Dur: 2 * time.Hour},
			"the selected window ends in the future: an analysis covers past time only — t picks another window"},
		{"empty", catalog.Timeframe{From: scenarioNow.Add(-time.Hour), To: scenarioNow.Add(-time.Hour)},
			"the selected window is empty — t picks another window"},
	} {
		for _, src := range []tui.Source{hotspots, allocations} {
			_, err := src(context.Background(), catalog.Scope{Entity: shopProcess, Timeframe: tc.tf}, "")
			var f *profileFailure
			if !errors.As(err, &f) || err.Error() != tc.want {
				t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
			}
		}
	}
	if sent != 0 {
		t.Errorf("refused windows sent %d requests", sent)
	}
}

// The notes say what the environment answered and promise nothing about what
// the SDK does next. They carry no count, so the client's own retries beneath
// the SDK's do not multiply it, and a Retry-After is read as seconds or as a
// date.
func TestAnalysisProgressNotes(t *testing.T) {
	var prog analysisProgress
	for _, tc := range []struct {
		status     int
		retryAfter string
		want       string
	}{
		{429, "", "the environment answered 429: it is throttling profiling requests"},
		{429, "10", "the environment answered 429: it is throttling profiling requests and asks to wait 10s"},
		{429, "Wed, 15 Jul 2026 12:00:30 GMT", "the environment answered 429: it is throttling profiling requests and asks to wait until 12:00:30 UTC"},
		{429, "soon", "the environment answered 429: it is throttling profiling requests"},
		{202, "", "the environment accepted the analysis — waiting for its result"},
		{202, "", "the analysis is still running on the server"},
		{202, "", "the analysis is still running on the server"},
		{200, "", ""},
	} {
		if got := prog.note(tc.status, tc.retryAfter); got != tc.want {
			t.Errorf("note(%d, %q) = %q, want %q", tc.status, tc.retryAfter, got, tc.want)
		}
	}

	// A client as dtui builds it retries a 429 itself, beneath the SDK.
	c, err := session.NewClient("https://env.example.invalid", "token")
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	env := roundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests <= 3 {
			return answering(429, http.Header{"Retry-After": []string{"0"}}, "")(r)
		}
		return answering(200, nil, `{"dataNodes":[]}`)(r)
	})
	c.HTTP().SetTransport(analysisObserver{next: env}).SetRetryWaitTime(time.Millisecond).SetRetryMaxWaitTime(time.Millisecond)
	seen := map[string]int{}
	ctx := context.WithValue(context.Background(), profileNoteKey{}, func(s string) { seen[s]++ })
	if _, err := (dtctlProfiler{h: profiling.NewHandler(c)}).Hotspots(ctx, profileQuery{Entity: shopProcess.ID, From: scenarioNow.Add(-time.Hour), To: scenarioNow}); err != nil {
		t.Fatal(err)
	}
	const throttled = "the environment answered 429: it is throttling profiling requests and asks to wait 0s"
	if len(seen) != 1 || seen[throttled] != 3 {
		t.Errorf("three throttled answers gave the notes %v, want %q three times", seen, throttled)
	}
}

type idleCloser struct {
	roundTrip
	closed int
}

func (c *idleCloser) CloseIdleConnections() { c.closed++ }

// The observer stays transparent to http.Client.CloseIdleConnections, which
// reaches the session client's shared pool through its transport.
func TestAnalysisObserverForwardsCloseIdleConnections(t *testing.T) {
	next := &idleCloser{}
	(&http.Client{Transport: analysisObserver{next: next}}).CloseIdleConnections()
	if next.closed != 1 {
		t.Errorf("CloseIdleConnections reached the transport %d times, want 1", next.closed)
	}
	// A transport without the method is left alone.
	(&http.Client{Transport: analysisObserver{next: roundTrip(nil)}}).CloseIdleConnections()
}

// A function with many callers lists the first of them and then its callees:
// each direction is cut on its own, and the summary says how many there were.
func TestProfilingFocusCapsEachDirection(t *testing.T) {
	var nodes []string
	var top []string
	// 40 callers of Hub.run, each on its own stack; Hub.run calls 3 callees.
	id := 1
	for i := range 40 {
		caller, hub := id+1, id+2
		id += 2
		top = append(top, fmt.Sprintf("%q", fmt.Sprint(caller)))
		nodes = append(nodes,
			fmt.Sprintf(`{"id":"%d","classPath":"com.example","className":"Caller%02d","methodName":"call","samples":{},"childIds":["%d"]}`, caller, i, hub),
			fmt.Sprintf(`{"id":"%d","classPath":"com.example","className":"Hub","methodName":"run","samples":{"RUNNING":%d},"childIds":["%d"]}`, hub, 100-i, id+1),
			fmt.Sprintf(`{"id":"%d","classPath":"com.example","className":"Callee%d","methodName":"work","samples":{"RUNNING":1},"childIds":[]}`, id+1, i%3))
		id++
	}
	tree := fmt.Sprintf(`{"dataRootNodeId":"1","dataNodes":[{"id":"1","classPath":"com.example","className":"Main","methodName":"main","samples":{},"childIds":[%s]},%s]}`,
		strings.Join(top, ","), strings.Join(nodes, ","))
	hotspots, _ := sourcesAt(profilerOver(t, answering(200, nil, tree)), "")
	records, err := hotspots(context.Background(),
		catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h"), Arg: "com.example.Hub.run"}, "")
	if err != nil {
		t.Fatal(err)
	}
	summary, rows := catalog.SplitSummary(records)
	count := map[string]int{}
	for _, row := range rows {
		count[catalog.Str(row, catalog.ProfRelation)]++
	}
	if count["caller"] != 25 || count["callee"] != 3 || len(rows) != 30 {
		t.Errorf("rows by relation %v (%d in all), want 25 callers, 3 callees, total and self", count, len(rows))
	}
	if summary[catalog.ProfSumCallers] != 40.0 || summary[catalog.ProfSumCallees] != 3.0 {
		t.Errorf("summary says %v callers and %v callees, want 40 and 3", summary[catalog.ProfSumCallers], summary[catalog.ProfSumCallees])
	}
	if last := rows[len(rows)-1]; catalog.Str(last, catalog.ProfRelation) != "callee" {
		t.Errorf("the last row is a %s, want a callee", catalog.Str(last, catalog.ProfRelation))
	}
}
