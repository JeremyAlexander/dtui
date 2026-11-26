package flame

import (
	"reflect"
	"testing"
)

func TestFunction(t *testing.T) {
	// a is called from main and from other, and calls itself under main.
	tree := build(t, 2, "main;a;x 4,0", "main;a;y 2,1", "main;a;a;x 3,0", "main;b 6,0", "other;a 7,3", "other;a;x 1,0")
	for _, tc := range []struct {
		name string
		at   []string
		lens int
		want Function
	}{
		{"a function on several paths", []string{"main", "a"}, 0, Function{
			Paths: 3, Self: 7, Total: 17,
			Callers: []Share{{"main", 9}, {"other", 8}},
			Callees: []Share{{"x", 8}, {"y", 2}},
		}},
		{"the same from another of its frames", []string{"other", "a"}, 0, Function{
			Paths: 3, Self: 7, Total: 17,
			Callers: []Share{{"main", 9}, {"other", 8}},
			Callees: []Share{{"x", 8}, {"y", 2}},
		}},
		{"in the second lens, without what weighs nothing there", []string{"main", "a"}, 1, Function{
			Paths: 2, Self: 3, Total: 4,
			Callers: []Share{{"other", 3}, {"main", 1}},
			Callees: []Share{{"y", 1}},
		}},
		{"a leaf", []string{"main", "b"}, 0, Function{
			Paths: 1, Self: 6, Total: 6, Callers: []Share{{"main", 6}},
		}},
		{"callers of a frame under the root are the root", []string{"main"}, 0, Function{
			Paths: 1, Self: 0, Total: 15, Callers: []Share{{RootLabel, 15}},
			Callees: []Share{{"a", 9}, {"b", 6}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tree.Function(find(t, tree, tc.at...), tc.lens); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
	// The root is no function, and neither is no frame or an unknown lens.
	for _, n := range []NodeID{Root, None, NodeID(tree.Len())} {
		if got := tree.Function(n, 0); !reflect.DeepEqual(got, Function{}) {
			t.Errorf("Function(%d) = %+v", n, got)
		}
	}
	if got := tree.Function(find(t, tree, "main", "a"), 2); !reflect.DeepEqual(got, Function{}) {
		t.Errorf("an unknown lens: %+v", got)
	}
}

// Equal shares are in the order of their names, so that a screen is the same
// each time it is drawn.
func TestFunctionSharesAreOrdered(t *testing.T) {
	tree := build(t, 1, "q;f;c 2", "p;f;b 2", "r;f;a 2", "p;f;d 5")
	got := tree.Function(find(t, tree, "p", "f"), 0)
	want := Function{
		Paths: 3, Total: 11,
		Callers: []Share{{"p", 7}, {"q", 2}, {"r", 2}},
		Callees: []Share{{"d", 5}, {"a", 2}, {"b", 2}, {"c", 2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func BenchmarkFunction(b *testing.B) {
	tree := benchTree(b)
	n := NodeID(tree.Len() / 2)
	b.ReportAllocs()
	for b.Loop() {
		tree.Function(n, 0)
	}
}
