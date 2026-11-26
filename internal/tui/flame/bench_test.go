package flame

import (
	"strconv"
	"testing"
)

// benchStacks is a profile larger than any seen: 50,000 stacks, each 100
// frames deep. A stack follows one of 64 trunks for 60 frames, one of eight
// branches of it for 30 more, and ends in ten frames of its own, so the tree
// has about half a million frames.
func benchStacks() (stacks [][]string, weights [][]int64) {
	const n, depth = 50_000, 100
	label := func(kind string, i, j int) string {
		return "com.example.app." + kind + strconv.Itoa(i) + ".Handler.step" + strconv.Itoa(j)
	}
	trunks := make([][]string, 64)
	for i := range trunks {
		trunks[i] = make([]string, 60)
		for j := range trunks[i] {
			trunks[i][j] = label("trunk", i%16, j)
		}
		trunks[i][0] = "Thread.run" + strconv.Itoa(i)
	}
	stacks, weights = make([][]string, n), make([][]int64, n)
	for s := range stacks {
		frames := make([]string, 0, depth)
		frames = append(frames, trunks[s%len(trunks)]...)
		for j := range 30 {
			frames = append(frames, label("branch", s%8, j))
		}
		for j := range 10 {
			frames = append(frames, label("leaf", s%997, j+s%7))
		}
		stacks[s] = frames
		weights[s] = []int64{int64(1 + s%13), int64(s % 3), 0, int64(s % 2), 1}
	}
	return stacks, weights
}

func benchTree(b *testing.B) *Tree {
	b.Helper()
	stacks, weights := benchStacks()
	builder := NewBuilder(5)
	for i, frames := range stacks {
		builder.Add(frames, weights[i])
	}
	return builder.Tree()
}

// BenchmarkBuild is what an analysis costs once, when it arrives.
func BenchmarkBuild(b *testing.B) {
	stacks, weights := benchStacks()
	b.ReportAllocs()
	for b.Loop() {
		builder := NewBuilder(5)
		for i, frames := range stacks {
			builder.Add(frames, weights[i])
		}
		tree := builder.Tree()
		if tree.Len() < 400_000 {
			b.Fatalf("the profile has %d frames", tree.Len())
		}
	}
}

// BenchmarkLayout is what a zoom, a lens or a new width costs.
func BenchmarkLayout(b *testing.B) {
	tree := benchTree(b)
	b.ReportAllocs()
	for b.Loop() {
		if l := tree.Layout(0, Root, 250); len(l.Rows) != 101 {
			b.Fatalf("%d levels", len(l.Rows))
		}
	}
}

// BenchmarkSearch is what a search costs when it is typed: one match per
// label, and the share of the graph the matches cover.
func BenchmarkSearch(b *testing.B) {
	g := New(benchTree(b))
	g.Resize(250, 56)
	b.ReportAllocs()
	for b.Loop() {
		if err := g.Search(`leaf\d+\.Handler`); err != nil {
			b.Fatal(err)
		}
		if g.Share().All < 1 {
			b.Fatal("nothing matched")
		}
	}
}

// BenchmarkMove is what a cursor key costs: no layout, a walk over one level.
func BenchmarkMove(b *testing.B) {
	g := New(benchTree(b))
	g.Resize(250, 56)
	g.Move(Down)
	b.ReportAllocs()
	for b.Loop() {
		if !g.Move(Right) {
			for g.Move(Left) {
			}
		}
		g.Move(Down)
		g.Move(Up)
	}
}
