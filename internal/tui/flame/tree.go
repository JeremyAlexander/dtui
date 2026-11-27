// Package flame is the model of dtui's flame graph: the call tree folded
// stacks add up to, and what a terminal of a given size shows of it. It
// knows no terminal framework (TestFlameIsFrameworkFree), so every rule of
// the graph (widths, movement, zoom, search) is tested without a screen.
//
// The interaction model is that of flamelens (github.com/YS-L/flamelens,
// MIT), re-derived from its behaviour; no code of it is in here.
package flame

import (
	"math"
	"slices"
	"strings"
)

// NodeID names one frame of a Tree: one function at one place in the call
// tree. The same function called from two places is two nodes.
type NodeID int32

const (
	// Root is the synthetic frame every stack hangs under.
	Root NodeID = 0
	// None is the absence of a frame.
	None NodeID = -1
)

// RootLabel is the label of the root frame: all stacks.
const RootLabel = "all"

// RestLabel is the label of a remainder frame: what a full tree could not
// give frames of their own, folded under the deepest frame a stack reached.
const RestLabel = "(frames not kept)"

// MaxFrames is the most frames a tree keeps, the remainders aside. A frame
// costs about 90 bytes: the analysis of a process at the most stacks dtctl
// returns stays far below this, and a tree that reaches it holds about
// 90 MB instead of growing with whatever arrives.
const MaxFrames = 1_000_000

// The labels a function cannot have: a function spelled the same is another
// label, so that a search and IsRest can tell the two apart.
const (
	rootLabelID int32 = iota
	restLabelID
)

// Tree is a call tree with a self and a total weight per frame and lens. A
// lens is one way of counting the same stacks (a thread state); all lenses
// share the frames, so a frame keeps its NodeID from one lens to the next.
// A Tree is immutable once built and safe for concurrent reads.
type Tree struct {
	lenses int
	labels []string
	// nodes are in preorder with the children of a frame ordered by label
	// and its remainder last, so one frame's subtree is the run of nodes from itself to its end.
	nodes []node
	// self and total hold lenses values per node, node by node.
	self, total []int64
	levels      int
	folded      int
}

type node struct {
	label  int32
	parent NodeID
	level  int32
	// end is one past the last node of the subtree.
	end NodeID
}

// Builder collects stacks into a Tree.
type Builder struct {
	lenses   int
	maxNodes int
	labelIDs map[string]int32
	labels   []string
	// children finds a frame's child by label.
	children map[edge]int32
	nodes    []pending
	self     []int64
	// rests is the number of remainder frames among nodes, folded the
	// number of stacks that ended in one.
	rests, folded int
}

type edge struct{ parent, label int32 }

type pending struct{ parent, label int32 }

// NewBuilder starts a tree whose stacks are weighed in lenses ways. A count
// below one is read as one.
func NewBuilder(lenses int) *Builder {
	lenses = max(lenses, 1)
	b := &Builder{
		lenses:   lenses,
		maxNodes: MaxFrames,
		labelIDs: map[string]int32{},
		labels:   []string{rootLabelID: RootLabel, restLabelID: RestLabel},
		children: map[edge]int32{},
		nodes:    []pending{{parent: -1}},
		self:     make([]int64, lenses),
	}
	return b
}

// Limit sets the most frames the tree keeps, the remainders aside, in place
// of MaxFrames. A limit below one keeps the root alone.
func (b *Builder) Limit(frames int) { b.maxNodes = max(frames, 1) }

// Add counts one stack: frames from the outermost caller to the frame that
// was running, and that frame's own weight per lens. A weight below zero
// counts as zero and weights beyond the lenses are ignored; a stack without
// any weight adds nothing. Frames without a label are skipped, and a stack
// without frames weighs on the root.
//
// Once the tree holds MaxFrames frames, a stack that needs a new one ends in
// the remainder frame of the deepest frame it reached. Totals stay those of
// the stacks; which frames were kept then depends on the order of the stacks.
func (b *Builder) Add(frames []string, weights []int64) {
	if len(weights) > b.lenses {
		weights = weights[:b.lenses]
	}
	if !slices.ContainsFunc(weights, func(w int64) bool { return w > 0 }) {
		return
	}
	at := int32(0)
	for _, frame := range frames {
		if frame == "" {
			continue
		}
		// The bound is looked at before the name is kept: a full tree
		// keeps no name it has no frame for.
		label, named := b.labelIDs[frame]
		child, known := int32(0), false
		if named {
			child, known = b.children[edge{at, label}]
		}
		if !known && len(b.nodes)-b.rests >= b.maxNodes {
			// The tree is full: what is left of the stack weighs on the
			// remainder of the deepest frame it reached. A frame has one
			// remainder at most, so they at most double the tree.
			rest, known := b.children[edge{at, restLabelID}]
			if !known {
				rest = b.child(at, restLabelID)
				b.rests++
			}
			at = rest
			b.folded++
			break
		}
		if !named {
			// A copy of its own: frame is a piece of the whole stack, and
			// would keep all of it alive for as long as the tree lives.
			frame = strings.Clone(frame)
			label = int32(len(b.labels))
			b.labelIDs[frame] = label
			b.labels = append(b.labels, frame)
		}
		if !known {
			child = b.child(at, label)
		}
		at = child
	}
	own := b.self[int(at)*b.lenses:]
	for lens, w := range weights {
		if w > 0 {
			own[lens] = satAdd(own[lens], w)
		}
	}
}

// child adds a frame under parent.
func (b *Builder) child(parent, label int32) int32 {
	id := int32(len(b.nodes))
	b.children[edge{parent, label}] = id
	b.nodes = append(b.nodes, pending{parent: parent, label: label})
	b.self = append(b.self, make([]int64, b.lenses)...)
	return id
}

// Tree is the tree of the stacks added so far. Below MaxFrames the same
// stacks give the same tree in whatever order they were added.
func (b *Builder) Tree() *Tree {
	n := len(b.nodes)
	// The children of each frame, by label: first[i] is where frame i's
	// children start in kids.
	first := make([]int32, n+1)
	for _, p := range b.nodes[1:] {
		first[p.parent+1]++
	}
	for i := 1; i <= n; i++ {
		first[i] += first[i-1]
	}
	kids := make([]int32, n-1)
	fill := slices.Clone(first[:n])
	for i, p := range b.nodes[1:] {
		kids[fill[p.parent]] = int32(i + 1)
		fill[p.parent]++
	}
	for i := range n {
		slices.SortFunc(kids[first[i]:first[i+1]], func(x, y int32) int {
			// A remainder is no function and stands after the functions,
			// whatever its label would sort like.
			if rx, ry := b.nodes[x].label == restLabelID, b.nodes[y].label == restLabelID; rx != ry {
				if rx {
					return 1
				}
				return -1
			}
			switch lx, ly := b.labels[b.nodes[x].label], b.labels[b.nodes[y].label]; {
			case lx < ly:
				return -1
			case lx > ly:
				return 1
			}
			return 0
		})
	}

	t := &Tree{
		folded: b.folded,
		lenses: b.lenses,
		labels: slices.Clone(b.labels),
		nodes:  make([]node, 0, n),
		self:   make([]int64, 0, n*b.lenses),
	}
	// Preorder without recursion: a stack of the frames still to place, and
	// for each the id its parent got.
	type item struct {
		old    int32
		parent NodeID
		level  int32
	}
	todo := []item{{old: 0, parent: None}}
	for len(todo) > 0 {
		it := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		id := NodeID(len(t.nodes))
		t.nodes = append(t.nodes, node{label: b.nodes[it.old].label, parent: it.parent, level: it.level})
		t.self = append(t.self, b.self[int(it.old)*b.lenses:int(it.old+1)*b.lenses]...)
		t.levels = max(t.levels, int(it.level)+1)
		for k := first[it.old+1] - 1; k >= first[it.old]; k-- {
			todo = append(todo, item{old: kids[k], parent: id, level: it.level + 1})
		}
	}
	// Children follow their parent, so one pass from the end closes every
	// subtree and adds every total up before its parent reads it.
	t.total = slices.Clone(t.self)
	for i := n - 1; i >= 0; i-- {
		nd := &t.nodes[i]
		if nd.end == 0 {
			nd.end = NodeID(i + 1)
		}
		if nd.parent == None {
			continue
		}
		parent := &t.nodes[nd.parent]
		parent.end = max(parent.end, nd.end)
		for lens := range t.lenses {
			at := int(nd.parent)*t.lenses + lens
			t.total[at] = satAdd(t.total[at], t.total[i*t.lenses+lens])
		}
	}
	return t
}

// satAdd adds two weights that are not negative and stops at the largest
// number there is, where a sum would otherwise turn negative.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Len is the number of frames, the root included.
func (t *Tree) Len() int { return len(t.nodes) }

// Folded is the number of stacks that did not get all their frames, because
// the tree was full: each ends in a remainder frame. 0 for a whole tree.
func (t *Tree) Folded() int { return t.folded }

// IsRest says whether the frame is a remainder: not a function, but the
// samples of the frames a full tree did not keep under its caller.
func (t *Tree) IsRest(n NodeID) bool { return t.has(n) && t.nodes[n].label == restLabelID }

// Lenses is the number of ways the stacks are weighed.
func (t *Tree) Lenses() int { return t.lenses }

// Levels is the number of levels of the tree: one for the root alone.
func (t *Tree) Levels() int { return t.levels }

func (t *Tree) has(n NodeID) bool { return n >= 0 && int(n) < len(t.nodes) }

// Label is the frame's function as the stacks spell it; "" for no frame.
func (t *Tree) Label(n NodeID) string {
	if !t.has(n) {
		return ""
	}
	return t.labels[t.nodes[n].label]
}

// Parent is the frame's caller: None for the root and for no frame.
func (t *Tree) Parent(n NodeID) NodeID {
	if !t.has(n) {
		return None
	}
	return t.nodes[n].parent
}

// Level is how far below the root the frame is: 0 for the root.
func (t *Tree) Level(n NodeID) int {
	if !t.has(n) {
		return 0
	}
	return int(t.nodes[n].level)
}

// Self is the weight of the stacks that end in the frame.
func (t *Tree) Self(n NodeID, lens int) int64 { return t.weight(t.self, n, lens) }

// Total is the weight of the stacks that pass through the frame: its own and
// that of everything it called.
func (t *Tree) Total(n NodeID, lens int) int64 { return t.weight(t.total, n, lens) }

func (t *Tree) weight(of []int64, n NodeID, lens int) int64 {
	if !t.has(n) || lens < 0 || lens >= t.lenses {
		return 0
	}
	return of[int(n)*t.lenses+lens]
}

// FirstChild is the first frame the frame called, by label; None without one.
func (t *Tree) FirstChild(n NodeID) NodeID {
	if !t.has(n) || t.nodes[n].end == n+1 {
		return None
	}
	return n + 1
}

// NextSibling is the next frame with the same caller, by label; None after
// the last.
func (t *Tree) NextSibling(n NodeID) NodeID {
	if !t.has(n) || t.nodes[n].parent == None {
		return None
	}
	if next := t.nodes[n].end; next < t.nodes[t.nodes[n].parent].end {
		return next
	}
	return None
}

// Within reports whether n is the frame or one of the frames under it.
func (t *Tree) Within(frame, n NodeID) bool {
	return t.has(frame) && n >= frame && n < t.nodes[frame].end
}

// Path is the frame's stack, from below the root down to the frame itself.
func (t *Tree) Path(n NodeID) []string {
	if !t.has(n) {
		return nil
	}
	path := make([]string, t.nodes[n].level)
	for ; n != Root; n = t.nodes[n].parent {
		path[t.nodes[n].level-1] = t.labels[t.nodes[n].label]
	}
	return path
}
