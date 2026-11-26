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
			got, unread, late := ix.Resolve(context.Background(), Parse(label))
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
		if got, unread, _ := ix.Resolve(context.Background(), Parse("Pipe.run")); len(got) != 0 || unread != 0 {
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
