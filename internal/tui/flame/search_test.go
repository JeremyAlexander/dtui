package flame

import (
	"testing"
)

func TestSearch(t *testing.T) {
	for _, tc := range []struct {
		name, steps, answers, want string
		pattern                    string
		share                      MatchShare
	}{
		{"no search: nothing to step through", "next prev", "--", "sel=all zoom=all top=0", "", MatchShare{}},
		{
			"a search marks frames and leaves the selection",
			"search:a$", "+", "sel=all zoom=all top=0", "a$", MatchShare{All: 13, Zoom: 13},
		},
		{
			"next goes to the first match in reading order, then on, then around",
			"search:^a$ next next next next", "+++w+", "sel=other;a zoom=all top=0", "^a$", MatchShare{All: 13, Zoom: 13},
		},
		{
			"prev goes the other way, around from the first",
			"search:^a$ next prev", "++w", "sel=other;a zoom=all top=0", "^a$", MatchShare{All: 13, Zoom: 13},
		},
		{
			"next from a frame goes right on its level before it goes down",
			"search:^[axy]$ down down right next next", "++++++", "sel=main;a;x zoom=all top=0", "^[axy]$", MatchShare{All: 13, Zoom: 13},
		},
		{
			"prev from a frame goes left on its level before it goes up",
			"search:[bx] down down down prev", "+++++", "sel=main;b zoom=all top=0", "[bx]", MatchShare{All: 10, Zoom: 10},
		},
		{
			"a frame under a match is not counted again",
			"search:main|x", "+", "sel=all zoom=all top=0", "main|x", MatchShare{All: 13, Zoom: 13},
		},
		{
			"the pattern is a regular expression, unanchored and case-sensitive",
			"search:M search:^ma.n$", "++", "sel=all zoom=all top=0", "^ma.n$", MatchShare{All: 13, Zoom: 13},
		},
		{
			"nothing matches: nothing to step to",
			"search:nothing next prev", "+--", "sel=all zoom=all top=0", "nothing", MatchShare{},
		},
		{
			"a pattern that does not compile leaves the search as it was",
			"search:x search:( next", "+-+", "sel=main;a;x zoom=all top=0", "x", MatchShare{All: 4, Zoom: 4},
		},
		{
			"the only match, already selected, is no move",
			"search:x next next prev", "++++", "sel=main;a;x zoom=all top=0", "x", MatchShare{All: 4, Zoom: 4},
		},
		{
			"same searches for the selected function wherever it is called",
			"down down same next", "++++", "sel=other;a zoom=all top=0", "^a$", MatchShare{All: 13, Zoom: 13},
		},
		{"same on the root does nothing", "same", "-", "sel=all zoom=all top=0", "", MatchShare{}},
		{"same on the root keeps the search there is", "search:x same", "+-", "sel=all zoom=all top=0", "x", MatchShare{All: 4, Zoom: 4}},
		{"clear forgets the search", "search:a clear clear next", "++--", "sel=all zoom=all top=0", "", MatchShare{}},
		{
			"the root is not a function and is never matched",
			"search:all next", "+-", "sel=all zoom=all top=0", "all", MatchShare{},
		},
		{
			"zoomed, the share of the zoom is what matches inside it",
			"search:^a$ down zoom", "+++", "sel=main zoom=main top=0", "^a$", MatchShare{All: 13, Zoom: 6},
		},
		{
			"zoomed under a match, all of the zoom is matched",
			"search:main down down zoom", "++++", "sel=main;a zoom=main;a top=0", "main", MatchShare{All: 13, Zoom: 6},
		},
		{
			"zoomed, only frames on screen are stepped to",
			"search:^a$ down zoom next next", "+++++", "sel=main;a zoom=main top=0", "^a$", MatchShare{All: 13, Zoom: 6},
		},
		{
			"the share follows the lens",
			"search:^a$ lens:1", "+.", "sel=all zoom=all top=0", "^a$", MatchShare{All: 4, Zoom: 4},
		},
		{
			"a match scrolls into view",
			"size:20x2 search:x next", ".++", "sel=main;a;x zoom=all top=2", "x", MatchShare{All: 4, Zoom: 4},
		},
		{
			"a match narrower than a cell is passed over",
			"size:4x10 search:c|b next next", ".+++", "sel=main;b zoom=all top=0", "c|b", MatchShare{All: 7, Zoom: 7},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := New(build(t, 2, walkLines...))
			g.Resize(20, 10)
			if got := run(t, g, tc.steps); got != tc.answers {
				t.Errorf("answers %q, want %q", got, tc.answers)
			}
			if got := where(g); got != tc.want {
				t.Errorf("%s, want %s", got, tc.want)
			}
			if g.Pattern() != tc.pattern || g.Searching() != (tc.pattern != "") {
				t.Errorf("searching %v for %q, want %q", g.Searching(), g.Pattern(), tc.pattern)
			}
			if got := g.Share(); got != tc.share {
				t.Errorf("share %+v, want %+v", got, tc.share)
			}
		})
	}
}

// The share is of the search as it is now: another pattern, another share.
func TestSearchShareFollowsTheSearch(t *testing.T) {
	g := New(build(t, 2, walkLines...))
	g.Resize(20, 10)
	want := []MatchShare{{All: 4, Zoom: 4}, {All: 6, Zoom: 6}, {}, {All: 13, Zoom: 13}}
	for i, pattern := range []string{"^x$", "^b$", "nothing", "^a$"} {
		if err := g.Search(pattern); err != nil {
			t.Fatal(err)
		}
		if got := g.Share(); got != want[i] {
			t.Errorf("after searching %s: share %+v, want %+v", pattern, got, want[i])
		}
	}
	if g.ClearSearch(); g.Share() != (MatchShare{}) {
		t.Errorf("without a search: share %+v", g.Share())
	}
}

func TestSearchMarks(t *testing.T) {
	tree := build(t, 1, "main;a;x 8", "other;a 7", "all 1")
	g := New(tree)
	g.Resize(20, 10)
	mainA, otherA, x, all := find(t, tree, "main", "a"), find(t, tree, "other", "a"), find(t, tree, "main", "a", "x"), find(t, tree, "all")

	if g.Matches(mainA) || g.SameAsSelected(mainA) {
		t.Error("without a search or a selected function, a frame is marked")
	}
	run(t, g, "down down") // main;a
	if g.Selected() != mainA {
		t.Fatalf("selected %v", tree.Path(g.Selected()))
	}
	for n, want := range map[NodeID]bool{otherA: true, mainA: false, x: false, Root: false, None: false} {
		if got := g.SameAsSelected(n); got != want {
			t.Errorf("SameAsSelected(%v) = %v, want %v", tree.Path(n), got, want)
		}
	}
	if err := g.Search("a"); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[NodeID]bool{otherA: true, mainA: true, x: false, Root: false, all: true, None: false, NodeID(tree.Len()): false} {
		if got := g.Matches(n); got != want {
			t.Errorf("Matches(%v) = %v, want %v", tree.Path(n), got, want)
		}
	}

	// A function spelled like the root can be searched for by its label; a
	// label the tree does not have cannot, and leaves no search behind.
	if !g.SearchLabel("all") || !g.Matches(all) || g.Matches(Root) || g.Pattern() != "^all$" {
		t.Errorf("searching the label of a function called like the root: pattern %q", g.Pattern())
	}
	if !g.SearchLabel("a") || g.Pattern() != "^a$" || g.SearchedFunction() != "a" {
		t.Errorf("searching a label: pattern %q, function %q", g.Pattern(), g.SearchedFunction())
	}
	if err := g.Search("^a$"); err != nil || g.SearchedFunction() != "" {
		t.Errorf("a search by pattern is for the function %q (%v)", g.SearchedFunction(), err)
	}
	if run(t, g, "same"); g.SearchedFunction() != "a" {
		t.Errorf("searching the selected frame is for the function %q", g.SearchedFunction())
	}
	if g.ClearSearch(); g.SearchedFunction() != "" {
		t.Errorf("no search is for the function %q", g.SearchedFunction())
	}
	if g.SearchLabel("a.") || g.Searching() {
		t.Errorf("a label that is not in the tree was found (pattern %q)", g.Pattern())
	}
	lone := New(build(t, 1, "main 1"))
	if lone.SearchLabel("all") || lone.Searching() {
		t.Error("the root's own label was found as a function")
	}
	// The label is searched as it is, not as a pattern.
	dotted := New(build(t, 1, "a.b 1", "axb 1"))
	if !dotted.SearchLabel("a.b") || dotted.Matches(2) || !dotted.Matches(1) {
		t.Error("a label with a dot matched as a pattern")
	}
}
