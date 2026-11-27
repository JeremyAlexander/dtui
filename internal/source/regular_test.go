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
		got, unread, late := parts(ix.Resolve(context.Background(), Parse(label)))
		if len(got) != 0 || unread != 1 || late {
			t.Errorf("%s: %+v, %d unread, late %v; want no match, 1 unread and not late", label, got, unread, late)
		}
		if abs, err := ix.Abs(rel); !errors.Is(err, errNotRegular) {
			t.Errorf("a directory is handed out as the file %q (%v)", abs, err)
		}
	}
}

// A symbolic link that took a file's place after the walk is not read,
// wherever it leads: what it leads to is not a file the walk saw, also when
// it is inside the root. It is counted as not read. One that leads nowhere,
// like a file that was removed, is no candidate any more.
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
		}, 0, 1},
	} {
		ix := swapped(t, cartJava, tc.put)
		got, unread, late := parts(ix.Resolve(context.Background(), Parse(cartFrame)))
		if len(got) != tc.matches || unread != tc.unread || late {
			t.Errorf("%s: %+v, %d unread, late %v; want %d matches and %d unread", tc.name, got, unread, late, tc.matches, tc.unread)
		}
		if abs, err := ix.Abs(cartJava); err == nil && tc.put != nil {
			t.Errorf("%s: a link is handed out as the file %q", tc.name, abs)
		}
	}
}

// A directory on the way to a file can become a link as well. The path then
// still names a regular file, and not the one the walk saw: it is refused,
// also when the link stays inside the root.
func TestAbsRefusesAFileBehindADirectoryThatBecameALink(t *testing.T) {
	root := tree(t, shop)
	ix := scan(t, root)
	dir := filepath.Join(root, filepath.FromSlash(filepath.Dir(cartJava)))
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(dir), "other"), dir); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(cartJava))); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the path is to name a regular file through the link: %v", err)
	}
	if abs, err := ix.Abs(cartJava); !errors.Is(err, errNotSeen) {
		t.Errorf("a file behind a linked directory is handed out as %q (%v)", abs, err)
	}
	got, unread, late := parts(ix.Resolve(context.Background(), Parse(cartFrame)))
	if len(got) != 0 || unread != 1 || late {
		t.Errorf("%+v, %d unread, late %v; want no match and 1 unread", got, unread, late)
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
	file, err := openSame(openNoWait, a, seen)
	if err != nil {
		t.Fatalf("the file that was seen is refused: %v", err)
	}
	if content, err := io.ReadAll(file); err != nil || string(content) != "package a\n" {
		t.Errorf("read %q, %v", content, err)
	}
	file.Close()
	if file, err := openSame(openNoWait, b, seen); !errors.Is(err, errNotRegular) {
		t.Errorf("another file is opened for the one seen: %v, %v", file, err)
	}
	// A directory is refused for what it is, also when it is what was seen.
	dir, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if file, err := openSame(openNoWait, root, dir); err == nil {
		t.Errorf("a directory is opened: %v", file.Name())
	}
	if file, err := openRegular(openNoWait, root); !errors.Is(err, errNotRegular) {
		t.Errorf("a directory is opened: %v, %v", file, err)
	}
	if _, err := openRegular(openNoWait, filepath.Join(root, "missing.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
}

// openPlain opens a file as a platform does that has no flag to open without
// waiting. The tests open with it on every platform: with the flag, the look
// at the opened file refuses what the look before the open let through, and
// a missing look before the open would not show.
func openPlain(abs string) (*os.File, error) { return os.OpenFile(abs, os.O_RDONLY, 0) }

// What is not a regular file is refused by the look at its path, before it
// is opened: where an open cannot be told not to wait, that look is all that
// keeps a lookup from one that would. So the open is not reached for a
// directory, a symbolic link or a missing file, and is reached once for a
// regular file.
func TestOpenRegularLooksBeforeItOpens(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n"})
	a := filepath.Join(root, "a.go")
	var opened []string
	recording := func(abs string) (*os.File, error) {
		opened = append(opened, abs)
		return openPlain(abs)
	}

	if file, err := openRegular(recording, root); !errors.Is(err, errNotRegular) {
		t.Errorf("a directory: %v, %v", file, err)
	}
	if file, err := openRegular(recording, filepath.Join(root, "missing.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v, %v", file, err)
	}
	// A link is refused for what it is, wherever it leads; Abs hands none out.
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(a, link); err != nil {
		t.Log("no symbolic link here:", err)
	} else if file, err := openRegular(recording, link); !errors.Is(err, errNotRegular) {
		t.Errorf("a symbolic link: %v, %v", file, err)
	}
	if len(opened) != 0 {
		t.Fatalf("opened %q: what is not a regular file is not to be opened at all", opened)
	}

	file, err := openRegular(recording, a)
	if err != nil {
		t.Fatalf("a regular file is refused: %v", err)
	}
	defer file.Close()
	if content, err := io.ReadAll(file); err != nil || string(content) != "package a\n" {
		t.Errorf("read %q, %v", content, err)
	}
	if len(opened) != 1 || opened[0] != a {
		t.Errorf("opened %q, want the regular file once", opened)
	}
}

// Only so much of a file is read. A declaration that ends on the bound is
// found; a file in which it was not found by then, and which goes on, is not
// known not to hold it and counts as not read.
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
	got, unread, late := parts(ix.Resolve(context.Background(), Parse("main.main")))
	if len(got) != 1 || got[0] != want || unread != 1 || late {
		t.Errorf("%+v, %d unread, late %v; want %+v, the declaration that ends on the bound, and the file with one past it unread", got, unread, late, want)
	}
}

// filler is a file's content made as it is read: n bytes of comment lines,
// held nowhere.
type filler struct{ n int }

func (f *filler) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, io.EOF
	}
	const line = "// filler\n"
	n := min(len(p), f.n)
	for i := range n {
		p[i] = line[(f.n-i)%len(line)]
	}
	f.n -= n
	return n, nil
}

// A file that is cut, at the byte bound or at a line too long to take, is
// read only if what was looked for was found before the cut. Otherwise the
// rest of it may hold the frame: the file has no answer and is not read.
func TestScanFileSaysWhenItWasCutBeforeAnAnswer(t *testing.T) {
	const pkg, decl = "package main\n", "func main() {}\n"
	goFrame, jvmFrame := Parse("main.main"), Parse("com.example.Big.work")
	long := strings.Repeat("x", 1<<20+1) + "\n"
	for _, tc := range []struct {
		name    string
		path    string
		f       Frame
		pkg     string
		content io.Reader
		want    Match
		has     holds
		how     reading
	}{
		{"the function past the byte bound", "main.go", goFrame, "main",
			io.MultiReader(strings.NewReader(pkg), &filler{maxFileBytes}, strings.NewReader(decl)),
			Match{Path: "main.go"}, holds{}, partly},
		{"the function before the byte bound", "main.go", goFrame, "main",
			io.MultiReader(strings.NewReader(pkg+decl), &filler{maxFileBytes}),
			Match{Path: "main.go", Line: 2, How: Declaration}, holds{function: true, pkg: true}, read},
		{"a file that ends on the byte bound", "main.go", goFrame, "",
			&filler{maxFileBytes},
			Match{Path: "main.go"}, holds{}, read},
		{"a file one byte longer", "main.go", goFrame, "",
			&filler{maxFileBytes + 1},
			Match{Path: "main.go"}, holds{}, partly},
		{"the function after a line too long", "main.go", goFrame, "main",
			strings.NewReader(pkg + long + decl),
			Match{Path: "main.go"}, holds{}, partly},
		{"the function before a line too long", "main.go", goFrame, "main",
			strings.NewReader(pkg + decl + long + decl),
			Match{Path: "main.go", Line: 2, How: Declaration}, holds{function: true, pkg: true}, read},
		{"the package past the byte bound", "main.go", goFrame, "main",
			io.MultiReader(strings.NewReader(decl), &filler{maxFileBytes}, strings.NewReader(pkg)),
			Match{Path: "main.go"}, holds{}, partly},
		// The type's line is no answer while the method may be further on.
		{"a method past the byte bound", "Big.java", jvmFrame, "",
			io.MultiReader(strings.NewReader("class Big {\n"), &filler{maxFileBytes}, strings.NewReader("  void work() {}\n}\n")),
			Match{Path: "Big.java"}, holds{}, partly},
		// The method is the answer, also where the type's line was not seen.
		{"a method before the byte bound, without its type", "Big.java", jvmFrame, "",
			io.MultiReader(strings.NewReader("  void work() {}\n"), &filler{maxFileBytes}),
			Match{Path: "Big.java", Line: 1, How: Declaration}, holds{function: true}, read},
		// A frame with no name to look for is looked up by its type.
		{"a type past the byte bound", "Big.java", Parse("com.example.Big.<clinit>"), "",
			io.MultiReader(&filler{maxFileBytes}, strings.NewReader("class Big {\n")),
			Match{Path: "Big.java"}, holds{}, partly},
		{"a frame without a name", "Big.java", Parse("com.example.Big.<clinit>"), "",
			io.MultiReader(strings.NewReader("class Big {\n"), &filler{maxFileBytes}),
			Match{Path: "Big.java", Line: 1, How: TypeOnly}, holds{}, read},
	} {
		m, has, how := scanFile(context.Background(), tc.content, Match{Path: tc.path}, tc.f, tc.pkg)
		if m != tc.want || has != tc.has || how != tc.how {
			t.Errorf("%s: %+v, %+v, reading %d; want %+v, %+v, reading %d", tc.name, m, has, how, tc.want, tc.has, tc.how)
		}
	}
}

// A file that was cut before an answer is counted with the files not read,
// and is not a match.
func TestResolveCountsAFileCutBeforeAnAnswer(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{
		"cmd/a/main.go": "package main\n\nfunc main() {}\n",
		"cmd/b/main.go": "package main\n",
		"cmd/c/main.go": "package main\n",
	}))
	found := ix.resolve(context.Background(), Parse("main.main"), func(ctx context.Context, rel string, f Frame, pkg string) (Match, holds, reading) {
		if rel == "cmd/b/main.go" {
			return Match{Path: rel}, holds{}, partly
		}
		return ix.locate(ctx, rel, f, pkg)
	})
	if len(found.Matches) != 1 || found.Matches[0].Path != "cmd/a/main.go" || found.Unread != 1 || found.Late {
		t.Errorf("%+v; want the first file and 1 unread", found)
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
	file, err := openRegular(openNoWait, p)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// One byte past the bound is read, to tell whether the file goes on.
	g := &grower{t: t, file: file, budget: 3 * bound, mayRead: bound + 1}
	b, buf, n := &bounded{ctx: context.Background(), r: g, left: bound}, make([]byte, 4*readChunk), 0
	// More reads than the bound has chunks is a reader that does not stop.
	for reads := 0; err == nil && reads < 8; reads++ {
		var got int
		got, err = b.Read(buf)
		n += got
	}
	if n != bound || err != io.EOF || !b.more {
		t.Errorf("read %d bytes of a file that grows, %v, more %v; want %d, the end, and that there is more", n, err, b.more, bound)
	}
	if g.largest != readChunk {
		t.Errorf("the largest read asked for %d bytes, want %d", g.largest, readChunk)
	}
	if info, err := os.Stat(p); err != nil || info.Size() != readChunk+bound+1 {
		t.Errorf("the file grew to %d bytes, %v; want what it held and what was read", info.Size(), err)
	}
	if g.read != bound+1 {
		t.Errorf("%d bytes were read of the file, want the bound and the one byte past it", g.read)
	}
	// A reader that ends on the bound has no more, and one whose reading
	// fails there has failed.
	for _, tc := range []struct {
		r    io.Reader
		more bool
		err  error
	}{
		{strings.NewReader(""), false, io.EOF},
		{strings.NewReader("x"), true, io.EOF},
		{&failing{r: strings.NewReader(""), err: io.ErrUnexpectedEOF}, false, io.ErrUnexpectedEOF},
	} {
		at := &bounded{ctx: context.Background(), r: tc.r, left: 0}
		if n, err := at.Read(make([]byte, 8)); n != 0 || err != tc.err || at.more != tc.more {
			t.Errorf("a read at the bound: %d, %v, more %v; want 0, %v, more %v", n, err, at.more, tc.err, tc.more)
		}
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
	got, unread, late := parts(ix.resolve(context.Background(), f, func(ctx context.Context, rel string, f Frame, pkg string) (Match, holds, reading) {
		asked = append(asked, rel)
		if rel == "cmd/b/main.go" {
			return Match{Path: rel}, holds{}, cut
		}
		return ix.locate(ctx, rel, f, pkg)
	}))
	if len(got) != 1 || got[0].Path != "cmd/a/main.go" || unread != 3 || !late {
		t.Errorf("%+v, %d unread, late %v; want the first file, 3 unread and late", got, unread, late)
	}
	if strings.Join(asked, " ") != "cmd/a/main.go cmd/b/main.go" {
		t.Errorf("files read: %v", asked)
	}
}
