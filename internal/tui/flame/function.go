package flame

import (
	"cmp"
	"slices"
)

// Function is what one function adds up to over the whole tree in one lens,
// wherever it is called from.
type Function struct {
	// Paths is the number of frames of the function that have weight: the
	// places in the call tree it is reached at.
	Paths int
	// Self is the weight of the stacks that end in the function, and Total
	// that of the stacks that pass through it, each stack counted once
	// however often the function calls itself.
	Self, Total int64
	// Callers and Callees are the functions that called it and that it
	// called, the heaviest first and equals by name. Each stack counts for
	// one caller and at most one callee, as in the functions table's callers
	// and callees: the caller is the frame above the function's outermost
	// call on the stack, and the callee the frame under its innermost one.
	// So the callers with Starts add up to Total, and the callees with Self
	// and Folded do.
	Callers, Callees []Share
	// Starts is the weight of the stacks the function is the outermost
	// frame of: they have no caller. The root is every stack, not a
	// function, and is not listed as one.
	Starts int64
	// Folded is the weight of the stacks that go on under the function into
	// frames a full tree did not keep: callees there is no name for.
	Folded int64
}

// Share is a function and a weight it accounts for.
type Share struct {
	Label  string
	Weight int64
}

// Function adds up the function of a frame over the whole tree. The root
// and a remainder are no function: for them, for no frame and for an
// unknown lens the answer is empty. It reads every frame once.
func (t *Tree) Function(n NodeID, lens int) Function {
	if !t.has(n) || n == Root || t.IsRest(n) || lens < 0 || lens >= t.lenses {
		return Function{}
	}
	label := t.nodes[n].label
	var f Function
	callers, callees := map[int32]int64{}, map[int32]int64{}
	// inside is the end of the outermost frame of the function the walk is
	// under: a frame before it is the function calling itself.
	inside := NodeID(0)
	// under are the subtrees the walk is in that decide whose weight a
	// deeper call of the function is: a callee of the function, which loses
	// what a deeper call of the function takes over, and such a deeper call
	// (callee -1), under which nothing is taken from that callee again.
	type subtree struct {
		callee int32
		end    NodeID
	}
	var under []subtree
	for i := NodeID(1); int(i) < len(t.nodes); i++ {
		for len(under) > 0 && i >= under[len(under)-1].end {
			under = under[:len(under)-1]
		}
		nd := t.nodes[i]
		total := t.Total(i, lens)
		if total < 1 {
			continue
		}
		if nd.label != label {
			if nd.parent != Root && t.nodes[nd.parent].label == label {
				if nd.label == restLabelID {
					// A remainder is no function, and has nothing under it.
					f.Folded = satAdd(f.Folded, total)
					continue
				}
				callees[nd.label] = satAdd(callees[nd.label], total)
				under = append(under, subtree{callee: nd.label, end: nd.end})
			}
			continue
		}
		if len(under) > 0 {
			if callee := under[len(under)-1].callee; callee >= 0 {
				callees[callee] = max(callees[callee]-total, 0)
			}
			under = append(under, subtree{callee: -1, end: nd.end})
		}
		f.Paths++
		f.Self = satAdd(f.Self, t.Self(i, lens))
		if i >= inside {
			f.Total = satAdd(f.Total, total)
			inside = nd.end
			if nd.parent == Root {
				f.Starts = satAdd(f.Starts, total)
			} else {
				callers[t.nodes[nd.parent].label] = satAdd(callers[t.nodes[nd.parent].label], total)
			}
		}
	}
	for callee, weight := range callees {
		if weight < 1 {
			delete(callees, callee) // all of it was a deeper call's
		}
	}
	f.Callers, f.Callees = t.shares(callers), t.shares(callees)
	return f
}

func (t *Tree) shares(weights map[int32]int64) []Share {
	if len(weights) == 0 {
		return nil
	}
	out := make([]Share, 0, len(weights))
	for label, w := range weights {
		out = append(out, Share{Label: t.labels[label], Weight: w})
	}
	slices.SortFunc(out, func(a, b Share) int {
		if c := cmp.Compare(b.Weight, a.Weight); c != 0 {
			return c
		}
		return cmp.Compare(a.Label, b.Label)
	})
	return out
}
