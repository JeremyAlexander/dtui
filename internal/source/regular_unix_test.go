//go:build unix

package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// pipeAt puts a named pipe where a file of the tree is. Opening one for
// reading waits for a writer, and none ever comes.
func pipeAt(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skip("no named pipes here:", err)
	}
	return p
}

// within fails the test when f has not returned in a few seconds. pipe is
// then opened for writing, which lets a reader stuck on it go.
func within(t *testing.T, pipe string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(maxResolveTime + 3*time.Second):
		if w, err := os.OpenFile(pipe, os.O_RDWR, 0); err == nil {
			w.Close()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
		t.Fatal("the lookup waited on a named pipe")
	}
}

// A file of the index that is a named pipe by the time it is read is not
// opened: the lookup answers at once and counts it as not read, whether the
// frame's file is told by its place or by what it declares.
func TestResolveDoesNotWaitOnANamedPipe(t *testing.T) {
	for label, rel := range map[string]string{
		"com.example.shop.CartService.total":                        "backend/src/main/java/com/example/shop/CartService.java",
		"github.com/acme/shop/api/internal/cart.(*Service).AddItem": "api/internal/cart/service.go",
	} {
		root := tree(t, shop)
		ix := scan(t, root)
		pipe := pipeAt(t, root, rel)
		within(t, pipe, func() {
			got, unread, late := parts(ix.Resolve(context.Background(), Parse(label)))
			if len(got) != 0 || unread != 1 || late {
				t.Errorf("%s: %+v, %d unread, late %v; want no match, 1 unread and not late", label, got, unread, late)
			}
		})
		if abs, err := ix.Abs(rel); err == nil {
			t.Errorf("a named pipe is handed out as the file %q", abs)
		}
	}
}

// A named pipe that is there when the directory is walked is not a source
// file, whatever it is called: the walk does not open it and does not keep
// it.
func TestScanKeepsNoNamedPipe(t *testing.T) {
	root := tree(t, map[string]string{".git/HEAD": "x", "src/Real.java": "class Real {}\n"})
	pipe := pipeAt(t, root, "src/Pipe.java")
	within(t, pipe, func() {
		ix := scan(t, root)
		if ix.Len() != 1 || ix.Partial != "" {
			t.Errorf("%d files, partial %q; want the one regular file", ix.Len(), ix.Partial)
		}
		if _, err := ix.Abs("src/Pipe.java"); err == nil {
			t.Error("a named pipe is in the index")
		}
		if got, unread, _ := parts(ix.Resolve(context.Background(), Parse("Pipe.run"))); len(got) != 0 || unread != 0 {
			t.Errorf("a named pipe is a candidate: %+v, %d unread", got, unread)
		}
	})
}

// A named pipe that takes a file's place between the look at it and the open
// does not hold the open: it is opened without waiting, seen not to be the
// file, and refused.
func TestOpenSameDoesNotWaitOnANamedPipe(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n"})
	seen, err := os.Lstat(filepath.Join(root, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	pipe := pipeAt(t, root, "a.go")
	within(t, pipe, func() {
		if file, err := openSame(openNoWait, pipe, seen); !errors.Is(err, errNotRegular) {
			t.Errorf("a named pipe is opened for the file seen: %v, %v", file, err)
		}
	})
}

// A named pipe is refused before it is opened. It is opened here as a
// platform without the flag opens a file, where the open of a pipe waits for
// a writer: only the look before the open keeps the lookup from it.
func TestOpenRegularDoesNotOpenANamedPipe(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n"})
	pipe := pipeAt(t, root, "a.go")
	within(t, pipe, func() {
		file, err := openRegular(openPlain, pipe)
		if !errors.Is(err, errNotRegular) {
			t.Errorf("a named pipe is opened: %v, %v", file, err)
		}
		if file != nil {
			file.Close()
		}
	})
}

// A file that became a link between the look at it and the open is not
// opened: the open itself refuses to follow the last step of the path.
func TestOpenSameDoesNotFollowALink(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n"})
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(root, "a.go")
	seen, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, a); err != nil {
		t.Skip("no symbolic link here:", err)
	}
	opened := false
	file, err := openSame(func(abs string) (*os.File, error) {
		f, err := openNoWait(abs)
		opened = err == nil
		return f, err
	}, a, seen)
	if file != nil || opened || !errors.Is(err, syscall.ELOOP) {
		t.Errorf("the link was opened (%v) before it was refused: %v", opened, err)
	}
}

// What an index opens with does not wait: a file that became a named pipe
// after the look at it is refused at once.
func TestIndexOpensWithoutWaiting(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n"})
	ix := scan(t, root)
	real := ix.opener
	var pipe string
	ix.opener = func(abs string) (*os.File, error) {
		pipe = pipeAt(t, root, "a.go")
		return real(abs)
	}
	within(t, filepath.Join(root, "a.go"), func() {
		if file, err := ix.open("a.go"); !errors.Is(err, errNotRegular) {
			t.Errorf("a named pipe is opened: %v, %v", file, err)
		}
	})
	if pipe == "" {
		t.Fatal("the index did not open with its opener")
	}
}

// The file at a path is swapped for another regular file between the look
// and the open: the same path, another file. It is refused, because the look
// kept the file's device and inode. (TestOpenSameRefusesAnotherFile opens a
// second path, which a comparison that reads the identity through the path
// would refuse as well.) This is the half of the check Windows does not
// have: there the identity of what was seen is read through the path, after
// the swap, so the test is for Unix only.
func TestOpenSameRefusesAFileSwappedInAtTheSamePath(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n", "other.go": "package other\n"})
	a := filepath.Join(root, "a.go")
	seen, err := os.Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "other.go"), a); err != nil {
		t.Fatal(err)
	}
	if now, err := os.Lstat(a); err != nil || !now.Mode().IsRegular() {
		t.Fatalf("the fixture: %v, %v", now, err)
	}
	if file, err := openSame(openNoWait, a, seen); !errors.Is(err, errNotRegular) {
		if file != nil {
			file.Close()
		}
		t.Errorf("a file swapped in at the path that was seen is opened: %v", err)
	}
	// Seen again, it is the file there is.
	file, err := openRegular(openNoWait, a)
	if err != nil {
		t.Fatalf("the file that is there now: %v", err)
	}
	file.Close()
}
