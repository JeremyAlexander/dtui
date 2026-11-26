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
	// called, the heaviest first and equals by name. A caller weighs what
	// the function's frames under it weigh, a callee what its frames under
	// the function weigh.
	Callers, Callees []Share
}

// Share is a function and a weight it accounts for.
type Share struct {
	Label  string
	Weight int64
}

// Function adds up the function of a frame over the whole tree. The root is
// no function: for it, for no frame and for an unknown lens the answer is
// empty. It reads every frame once.
func (t *Tree) Function(n NodeID, lens int) Function {
	if !t.has(n) || n == Root || lens < 0 || lens >= t.lenses {
		return Function{}
	}
	label := t.nodes[n].label
	var f Function
	callers, callees := map[int32]int64{}, map[int32]int64{}
	// inside is the end of the outermost frame of the function the walk is
	// under: a frame before it is the function calling itself.
	inside := NodeID(0)
	for i := NodeID(1); int(i) < len(t.nodes); i++ {
		nd := t.nodes[i]
		total := t.Total(i, lens)
		if total < 1 {
			continue
		}
		if parent := t.nodes[nd.parent]; parent.label == label && nd.parent != Root && nd.label != label {
			callees[nd.label] = satAdd(callees[nd.label], total)
		}
		if nd.label != label {
			continue
		}
		f.Paths++
		f.Self = satAdd(f.Self, t.Self(i, lens))
		if i >= inside {
			f.Total = satAdd(f.Total, total)
			inside = nd.end
			callers[t.nodes[nd.parent].label] = satAdd(callers[t.nodes[nd.parent].label], total)
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
