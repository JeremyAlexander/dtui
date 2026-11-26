package flame

import (
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A second, naive reading of the same stacks: every number is added up
// stack by stack, with no tree. What the tree says of a frame, of a function
// and of a search must be what the stacks say.

type refStack struct {
	frames []string
	weight []int64
}

// refStacks are a few random stacks over a handful of names, so that
// functions recur, call themselves and call each other.
func refStacks(r *rand.Rand, lenses int) []refStack {
	names := []string{"a", "b", "c", "d", "e"}[:2+r.Intn(4)]
	stacks := make([]refStack, 1+r.Intn(30))
	for i := range stacks {
		frames := make([]string, r.Intn(8))
		for j := range frames {
			frames[j] = names[r.Intn(len(names))]
		}
		weight := make([]int64, lenses)
		for j := range weight {
			weight[j] = int64(r.Intn(50)) * int64(r.Intn(3)) / 2
		}
		stacks[i] = refStack{frames, weight}
	}
	return stacks
}

// refFunction adds a function up over the stacks.
func refFunction(stacks []refStack, label string, lens int) (f Function, callers, callees map[string]int64) {
	callers, callees = map[string]int64{}, map[string]int64{}
	paths := map[string]bool{}
	for _, s := range stacks {
		w := s.weight[lens]
		first, last := slices.Index(s.frames, label), -1
		if w < 1 || first < 0 {
			continue
		}
		for i, frame := range s.frames {
			if frame == label {
				paths[strings.Join(s.frames[:i+1], ";")] = true
				last = i
			}
		}
		f.Total += w
		caller := RootLabel
		if first > 0 {
			caller = s.frames[first-1]
		}
		callers[caller] += w
		if last == len(s.frames)-1 {
			f.Self += w
		} else {
			callees[s.frames[last+1]] += w
		}
	}
	f.Paths = len(paths)
	return f, callers, callees
}

func shareMap(shares []Share) map[string]int64 {
	m := map[string]int64{}
	for _, s := range shares {
		m[s.Label] = s.Weight
	}
	return m
}

func TestTreeAgreesWithTheStacks(t *testing.T) {
	for seed := range int64(400) {
		r := rand.New(rand.NewSource(seed))
		lenses := 1 + r.Intn(3)
		stacks := refStacks(r, lenses)
		b := NewBuilder(lenses)
		for _, s := range stacks {
			b.Add(s.frames, s.weight)
		}
		tree := b.Tree()
		for lens := range lenses {
			for n := NodeID(0); int(n) < tree.Len(); n++ {
				path := tree.Path(n)
				var total, self int64
				for _, s := range stacks {
					if len(s.frames) >= len(path) && slices.Equal(s.frames[:len(path)], path) {
						total += s.weight[lens]
						if len(s.frames) == len(path) {
							self += s.weight[lens]
						}
					}
				}
				if tree.Total(n, lens) != total || tree.Self(n, lens) != self {
					t.Fatalf("seed %d, lens %d, frame %v: self %d and total %d, the stacks say %d and %d",
						seed, lens, path, tree.Self(n, lens), tree.Total(n, lens), self, total)
				}
				if n == Root {
					continue
				}
				got := tree.Function(n, lens)
				want, callers, callees := refFunction(stacks, tree.Label(n), lens)
				if got.Paths != want.Paths || got.Self != want.Self || got.Total != want.Total ||
					fmt.Sprint(shareMap(got.Callers)) != fmt.Sprint(callers) || fmt.Sprint(shareMap(got.Callees)) != fmt.Sprint(callees) {
					t.Fatalf("seed %d, lens %d, function %s:\n got  %+v\n want %+v callers %v callees %v\n of %v",
						seed, lens, tree.Label(n), got, want, callers, callees, stacks)
				}
			}
			checkShares(t, seed, tree, stacks, lens)
		}
	}
}

// checkShares holds a search's share to the stacks, at every zoom: a stack
// with a match anywhere on it counts once, and inside a zoomed frame when it
// passes through that frame.
func checkShares(t *testing.T, seed int64, tree *Tree, stacks []refStack, lens int) {
	t.Helper()
	for _, pattern := range []string{"a", "^c$", "a|d", "x"} {
		re := regexp.MustCompile(pattern)
		for zoom := NodeID(0); int(zoom) < tree.Len(); zoom++ {
			if tree.Total(zoom, lens) < 1 {
				continue
			}
			g := New(tree)
			g.SetLens(lens)
			g.Resize(400, 50)
			if zoom != Root {
				g.zooms = []NodeID{zoom}
			}
			if err := g.Search(pattern); err != nil {
				t.Fatal(err)
			}
			path := tree.Path(zoom)
			var want MatchShare
			for _, s := range stacks {
				if !slices.ContainsFunc(s.frames, re.MatchString) {
					continue
				}
				want.All += s.weight[lens]
				if len(s.frames) >= len(path) && slices.Equal(s.frames[:len(path)], path) {
					want.Zoom += s.weight[lens]
				}
			}
			if got := g.Share(); got != want {
				t.Fatalf("seed %d, lens %d, search %s zoomed into %v: %+v, the stacks say %+v", seed, lens, pattern, path, got, want)
			}
		}
	}
}
