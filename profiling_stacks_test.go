package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/flame"
)

// stackTree builds the flame graph's tree from a stacks result the way the
// view does: one stack per record, split at the separator, weighed per
// thread state.
func stackTree(records []map[string]any) *flame.Tree {
	states := catalog.HotspotStates()
	b := flame.NewBuilder(len(states))
	weights := make([]int64, len(states))
	for _, rec := range records {
		if rec[catalog.SummaryKey] == true {
			continue
		}
		for i, state := range states {
			n, _ := catalog.FloatValue(rec[state])
			weights[i] = int64(n)
		}
		b.Add(strings.Split(catalog.Str(rec, catalog.ProfStack), catalog.ProfStackSep), weights)
	}
	return b.Tree()
}

// The flame graph and the functions table are two readings of one analysis,
// so their numbers agree: per thread state the graph's root is the samples
// the table counts, and a function's self and total samples are those of its
// frames in the graph.
func TestStacksReconcileWithTheFunctionsTable(t *testing.T) {
	sources := allSourcesAt(recordedProfiler(t))
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	records, err := sources[catalog.HotspotStacksAPI](context.Background(), scope, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) < 100 {
		t.Fatalf("the recording has %d stacks", len(records)-1)
	}
	tree := stackTree(records)
	g := flame.New(tree)
	g.Resize(200, 50)

	for lens, state := range catalog.HotspotStates() {
		scope.Lens = lensIndex(t, state)
		table, err := sources["hotspots"](context.Background(), scope, "")
		if err != nil {
			t.Fatal(err)
		}
		samples, _ := catalog.FloatValue(table[0][catalog.ProfSumSamples])
		if got := tree.Total(flame.Root, lens); got != int64(samples) || got == 0 {
			t.Errorf("%s: the graph's root has %d samples, the table counts %d", state, got, int64(samples))
		}
		g.SetLens(lens)
		for _, row := range table[1:min(len(table), 40)] {
			fn := catalog.Str(row, catalog.ProfFunction)
			self, _ := catalog.FloatValue(row[catalog.ProfSelf])
			total, _ := catalog.FloatValue(row[catalog.ProfTotal])
			var graphSelf int64
			for n := flame.NodeID(1); int(n) < tree.Len(); n++ {
				if tree.Label(n) == fn {
					graphSelf += tree.Self(n, lens)
				}
			}
			if graphSelf != int64(self) {
				t.Errorf("%s %s: self %d in the graph, %d in the table", state, fn, graphSelf, int64(self))
			}
			// A search for the function counts each stack it is on once,
			// which is what the table's total is.
			if !g.SearchLabel(fn) {
				t.Errorf("%s: %s is in the table and not in the graph", state, fn)
			} else if got := g.Share().All; got != int64(total) {
				t.Errorf("%s %s: total %d in the graph, %d in the table", state, fn, got, int64(total))
			}
		}
	}
}

// Opening the flame graph asks the environment for nothing: it reads the
// analysis the table holds, even one older than the table would reuse. Its
// refresh is the table's: within the reuse time nothing is asked, after it
// the analysis runs again. Without an analysis to read, it runs one.
func TestTheFlameGraphReadsTheAnalysisTheTableHolds(t *testing.T) {
	p := &recordingProfiler{}
	runs := newProfileRuns()
	now := scenarioNow
	runs.now = func() time.Time { return now }
	sources := profileSources(p, runs)
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "2h")}
	read := func(name string) {
		t.Helper()
		if _, err := sources[name](context.Background(), scope, ""); err != nil {
			t.Fatal(err)
		}
	}
	asked := func() int {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.queries)
	}

	read(catalog.HotspotStacksAPI)
	if asked() != 1 {
		t.Fatalf("with no analysis held, the graph asked %d times, want 1", asked())
	}
	read("hotspots")
	read(catalog.HotspotStacksFreshAPI)
	if asked() != 1 {
		t.Fatalf("within the reuse time, the table and a refresh of the graph asked again (%d)", asked())
	}
	now = now.Add(profileCacheTTL + time.Minute)
	read(catalog.HotspotStacksAPI)
	if asked() != 1 {
		t.Fatalf("opening the graph on an older analysis asked again (%d)", asked())
	}
	read(catalog.HotspotStacksFreshAPI)
	if asked() != 2 {
		t.Fatalf("refreshing the graph after the reuse time asked %d times, want 2", asked())
	}
	// The table reads what the refresh fetched.
	read("hotspots")
	if asked() != 2 {
		t.Fatalf("the table asked again after the graph's refresh (%d)", asked())
	}
}

// A stacks result carries the window it is from and a count per thread state
// the catalog names, and refuses what the table refuses.
func TestStacksSourceShape(t *testing.T) {
	sources := allSourcesAt(recordedProfiler(t))
	scope := catalog.Scope{Entity: shopProcess, Timeframe: timeframe(t, "24h")}
	records, err := sources[catalog.HotspotStacksAPI](context.Background(), scope, "")
	if err != nil {
		t.Fatal(err)
	}
	summary := records[0]
	if summary[catalog.SummaryKey] != true || summary[catalog.ProfSumClamped] != true ||
		catalog.Str(summary, catalog.ProfSumFrom) != "2026-07-15T10:00:00Z" {
		t.Errorf("summary %v", summary)
	}
	if rows, _ := catalog.FloatValue(summary[catalog.ProfSumRows]); int(rows) != len(records)-1 {
		t.Errorf("the summary counts %v stacks of %d", rows, len(records)-1)
	}
	seen := map[string]bool{}
	for _, rec := range records[1:] {
		if catalog.Str(rec, catalog.ProfStack) == "" {
			t.Fatalf("a row without a stack: %v", rec)
		}
		for key := range rec {
			seen[key] = true
		}
	}
	delete(seen, catalog.ProfStack)
	for _, state := range catalog.HotspotStates() {
		if !seen[state] {
			t.Errorf("no stack has %s samples", state)
		}
		delete(seen, state)
	}
	if len(seen) > 0 {
		t.Errorf("stack rows carry keys the catalog does not name: %v", seen)
	}

	for name, scope := range map[string]catalog.Scope{
		"no process":    {Timeframe: timeframe(t, "2h")},
		"not a process": {Entity: &catalog.Entity{ID: "SERVICE-000000000000B001"}, Timeframe: timeframe(t, "2h")},
	} {
		if _, err := sources[catalog.HotspotStacksAPI](context.Background(), scope, ""); err == nil {
			t.Errorf("%s: the stacks were read", name)
		}
	}
}
