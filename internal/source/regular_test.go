package source

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	cartJava  = "backend/src/main/java/com/example/shop/CartService.java"
	cartFrame = "com.example.shop.CartService.total"
	cartGo    = "api/internal/cart/service.go"
	goFrame   = "github.com/acme/shop/api/internal/cart.(*Service).AddItem"
)

// swapped is the shop, walked, with one of its files replaced by whatever
// put makes at its path.
func swapped(t *testing.T, rel string, put func(p string) error) *Index {
	t.Helper()
	root := tree(t, shop)
	ix := scan(t, root)
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if put != nil {
		if err := put(p); err != nil {
			t.Skip("cannot be made here:", err)
		}
	}
	return ix
}

// A directory that took a file's place after the walk is not read and is
// not offered, whether the frame's file is told by its place or by what it
// declares. It is counted as not read.
func TestResolveDoesNotReadADirectory(t *testing.T) {
	for label, rel := range map[string]string{cartFrame: cartJava, goFrame: cartGo} {
		ix := swapped(t, rel, func(p string) error { return os.Mkdir(p, 0o755) })
		got, unread, late := ix.Resolve(context.Background(), Parse(label))
		if len(got) != 0 || unread != 1 || late {
			t.Errorf("%s: %+v, %d unread, late %v; want no match, 1 unread and not late", label, got, unread, late)
		}
		if abs, err := ix.Abs(rel); !errors.Is(err, errNotRegular) {
			t.Errorf("a directory is handed out as the file %q (%v)", abs, err)
		}
	}
}

// A symbolic link that took a file's place after the walk is read only
// where it leads to a regular file inside the root. One that leads out of
// the root is counted as not read; one that leads nowhere, like a file that
// was removed, is no candidate any more.
func TestResolveAfterAFileBecameALink(t *testing.T) {
	outside := tree(t, map[string]string{"CartService.java": "class CartService {\n  private String total(Item item) {}\n}\n"})
	for _, tc := range []struct {
		name    string
		put     func(p string) error
		matches int
		unread  int
	}{
		{"out of the root", func(p string) error { return os.Symlink(filepath.Join(outside, "CartService.java"), p) }, 0, 1},
		{"to a directory out of the root", func(p string) error { return os.Symlink(outside, p) }, 0, 1},
		{"to a directory in the root", func(p string) error { return os.Symlink(filepath.Dir(filepath.Dir(p)), p) }, 0, 1},
		{"nowhere", func(p string) error { return os.Symlink(filepath.Join(outside, "missing"), p) }, 0, 0},
		{"removed", nil, 0, 0},
		{"to a file in the root", func(p string) error {
			return os.Symlink(filepath.Join(filepath.Dir(filepath.Dir(p)), "other", "CartService.java"), p)
		}, 1, 0},
	} {
		ix := swapped(t, cartJava, tc.put)
		got, unread, late := ix.Resolve(context.Background(), Parse(cartFrame))
		if len(got) != tc.matches || unread != tc.unread || late {
			t.Errorf("%s: %+v, %d unread, late %v; want %d matches and %d unread", tc.name, got, unread, late, tc.matches, tc.unread)
		}
		for _, m := range got {
			// The link's target declares the type and no total: the match
			// is the path the walk saw, with the line found in the target.
			if m != (Match{Path: cartJava, Line: 2, How: TypeOnly}) {
				t.Errorf("%s: matched %+v", tc.name, m)
			}
		}
	}
}

// The file that is opened is the one that was looked at: a path that names
// something else by the time it is opened is refused, and so is anything
// that is not a regular file.
func TestOpenSameRefusesAnotherFile(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	seen, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openSame(a, seen)
	if err != nil {
		t.Fatalf("the file that was seen is refused: %v", err)
	}
	if content, err := io.ReadAll(file); err != nil || string(content) != "package a\n" {
		t.Errorf("read %q, %v", content, err)
	}
	file.Close()
	if file, err := openSame(b, seen); !errors.Is(err, errNotRegular) {
		t.Errorf("another file is opened for the one seen: %v, %v", file, err)
	}
	// A directory is refused for what it is, also when it is what was seen.
	dir, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if file, err := openSame(root, dir); err == nil {
		t.Errorf("a directory is opened: %v", file.Name())
	}
	if file, err := openRegular(root); !errors.Is(err, errNotRegular) {
		t.Errorf("a directory is opened: %v, %v", file, err)
	}
	if _, err := openRegular(filepath.Join(root, "missing.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
}

// Only so much of a file is read: a declaration beyond the bound is not
// found, and the file still counts as read.
func TestResolveReadsAFileUpToTheByteBound(t *testing.T) {
	const decl = "func main() {}\n"
	head := "package main\n" + strings.Repeat("// filler\n", maxFileBytes/10)
	onBound := head[:maxFileBytes-len(decl)-1] + "\n" + decl
	if len(onBound) != maxFileBytes {
		t.Fatalf("the file is %d bytes", len(onBound))
	}
	ix := scan(t, tree(t, map[string]string{
		// The declaration's last byte is the last one read, and here only
		// its first word is read.
		"cmd/a/main.go": onBound + "// more\n",
		"cmd/b/main.go": head[:maxFileBytes-len("func")-1] + "\n" + decl,
	}))
	want := Match{Path: "cmd/a/main.go", Line: strings.Count(onBound, "\n"), How: Declaration}
	got, unread, late := ix.Resolve(context.Background(), Parse("main.main"))
	if len(got) != 1 || got[0] != want || unread != 0 || late {
		t.Errorf("%+v, %d unread, late %v; want %+v, the declaration that ends on the bound, and not the one past it", got, unread, late, want)
	}
}

// grower is a file that grows by as much as is read from it, each time it is
// read, so a reader never comes to its end by itself. It is bounded by
// construction: it grows by budget bytes in all and then stops growing, and
// it fails the test at the first read past the bytes a reader may take, so a
// reader that does not stop cannot keep it writing.
type grower struct {
	t       *testing.T
	file    *os.File
	budget  int
	mayRead int
	read    int
	largest int
}

func (g *grower) Read(p []byte) (int, error) {
	g.t.Helper()
	if g.read >= g.mayRead {
		g.t.Fatalf("a read after %d bytes: the reader was to stop at %d", g.read, g.mayRead)
	}
	g.largest = max(g.largest, len(p))
	n, err := g.file.Read(p)
	g.read += n
	if grow := min(n, g.budget); grow > 0 {
		g.budget -= grow
		w, werr := os.OpenFile(g.file.Name(), os.O_WRONLY|os.O_APPEND, 0)
		if werr != nil {
			g.t.Fatal(werr)
		}
		_, werr = w.Write(bytes.Repeat([]byte("y"), grow))
		if cerr := w.Close(); werr != nil || cerr != nil {
			g.t.Fatal(werr, cerr)
		}
	}
	return n, err
}

// A file that grows while it is read is read to the byte bound and no
// further, a chunk at a time. The bound is a small one of the test's own,
// and the file can grow to four times it at most.
func TestBoundedStopsAtTheBoundOnAFileThatGrows(t *testing.T) {
	const bound = 3*readChunk + 7
	p := filepath.Join(t.TempDir(), "grows.go")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), readChunk), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openRegular(p)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	g := &grower{t: t, file: file, budget: 3 * bound, mayRead: bound}
	b, buf, n := &bounded{ctx: context.Background(), r: g, left: bound}, make([]byte, 4*readChunk), 0
	// More reads than the bound has chunks is a reader that does not stop.
	for reads := 0; err == nil && reads < 8; reads++ {
		var got int
		got, err = b.Read(buf)
		n += got
	}
	if n != bound || err != io.EOF {
		t.Errorf("read %d bytes of a file that grows, %v; want %d and the end", n, err, bound)
	}
	if g.largest != readChunk {
		t.Errorf("the largest read asked for %d bytes, want %d", g.largest, readChunk)
	}
	if info, err := os.Stat(p); err != nil || info.Size() != readChunk+bound {
		t.Errorf("the file grew to %d bytes, %v; want what it held and what was read", info.Size(), err)
	}
	if n, err := (&bounded{ctx: context.Background(), r: g, left: 0}).Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Errorf("a read at the bound: %d, %v", n, err)
	}
}

// cancelling ends a lookup's time after its first read: the time runs out in
// the middle of a file.
type cancelling struct {
	r      io.Reader
	cancel context.CancelFunc
	reads  int
}

func (c *cancelling) Read(p []byte) (int, error) {
	c.reads++
	c.cancel()
	return c.r.Read(p)
}

// The time is looked at between the reads of one file, not only between
// files: a file the time runs out on is not read on, and what was found in
// its first part is not an answer.
func TestScanFileStopsWhenTheTimeRunsOut(t *testing.T) {
	content := "package main\n\nfunc main() {}\n" + strings.Repeat("// filler\n", readChunk)
	f := Parse("main.main")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelling{r: strings.NewReader(content), cancel: cancel}
	m, has, how := scanFile(ctx, src, Match{Path: "main.go"}, f, "main")
	if how != cut || m != (Match{Path: "main.go"}) || has != (holds{}) {
		t.Errorf("out of time: %+v, %+v, reading %d; want cut and no answer", m, has, how)
	}
	if src.reads != 1 {
		t.Errorf("%d reads after the time ran out, want the one that was under way", src.reads)
	}
	m, has, how = scanFile(context.Background(), strings.NewReader(content), Match{Path: "main.go"}, f, "main")
	if how != read || m != (Match{Path: "main.go", Line: 3, How: Declaration}) || has != (holds{function: true, pkg: true}) {
		t.Errorf("in time: %+v, %+v, reading %d", m, has, how)
	}
}

// failing reads some content and then fails.
type failing struct {
	r   io.Reader
	err error
}

func (f *failing) Read(p []byte) (int, error) {
	if n, err := f.r.Read(p); err != io.EOF {
		return n, err
	}
	return 0, f.err
}

// A file whose reading fails is not known to hold or not to hold the frame:
// it is not read. A line too long to take ends the reading like the byte
// bound does, and what was found before it stands.
func TestScanFileOnAReadThatFails(t *testing.T) {
	f := Parse("main.main")
	src := &failing{r: strings.NewReader("package main\n\nfunc main() {}\n"), err: errors.New("input/output error")}
	m, has, how := scanFile(context.Background(), src, Match{Path: "main.go"}, f, "main")
	if how != refused || m != (Match{Path: "main.go"}) || has != (holds{}) {
		t.Errorf("a failed read: %+v, %+v, reading %d; want refused and no answer", m, has, how)
	}
	long := "package main\n\nfunc main() {}\n" + strings.Repeat("x", 1<<20+1) + "\nfunc main() {}\n"
	m, has, how = scanFile(context.Background(), strings.NewReader(long), Match{Path: "main.go"}, f, "main")
	if how != read || m != (Match{Path: "main.go", Line: 3, How: Declaration}) || has != (holds{function: true, pkg: true}) {
		t.Errorf("a line too long: %+v, %+v, reading %d", m, has, how)
	}
}

// When the time runs out in the middle of a file, that file and every one
// after it are unread, and the answer says the time ran out.
func TestResolveCountsTheFileTheTimeRanOutOn(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{
		"cmd/a/main.go": "package main\n\nfunc main() {}\n",
		"cmd/b/main.go": "package main\n\nfunc main() {}\n",
		"cmd/c/main.go": "package main\n\nfunc main() {}\n",
		"cmd/d/main.go": "package main\n\nfunc main() {}\n",
	}))
	f := Parse("main.main")
	asked := []string{}
	got, unread, late := ix.resolve(context.Background(), f, func(ctx context.Context, rel string, f Frame, pkg string) (Match, holds, reading) {
		asked = append(asked, rel)
		if rel == "cmd/b/main.go" {
			return Match{Path: rel}, holds{}, cut
		}
		return ix.locate(ctx, rel, f, pkg)
	})
	if len(got) != 1 || got[0].Path != "cmd/a/main.go" || unread != 3 || !late {
		t.Errorf("%+v, %d unread, late %v; want the first file, 3 unread and late", got, unread, late)
	}
	if strings.Join(asked, " ") != "cmd/a/main.go cmd/b/main.go" {
		t.Errorf("files read: %v", asked)
	}
}
