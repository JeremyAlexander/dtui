package source

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A walk that kept exactly as many files as it may, and found no more, saw
// everything: it is partial only when a file was left out.
func TestScanIsNotPartialAtExactlyTheLimit(t *testing.T) {
	root := tree(t, map[string]string{"a/A.java": "class A {}\n", "b/B.java": "class B {}\n", "README.md": "x", "z/notes.txt": "x"})
	ix, err := Scan(context.Background(), root, Limits{Entries: 1000, Files: 2, Time: time.Minute})
	if err != nil || ix.Partial != "" || ix.Len() != 2 {
		t.Errorf("two source files with a limit of two: %d files, partial %q, %v", ix.Len(), ix.Partial, err)
	}
	ix, err = Scan(context.Background(), root, Limits{Entries: 1000, Files: 1, Time: time.Minute})
	if err != nil || ix.Partial != "more than 1 source files" || ix.Len() != 1 {
		t.Errorf("two source files with a limit of one: %d files, partial %q, %v", ix.Len(), ix.Partial, err)
	}
}

// A directory that cannot be read is an error, not a repository without
// source files: O would say the frame is a library's.
func TestScanOfAnUnreadableDirectoryIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions do not keep this user out of a directory")
	}
	root := tree(t, map[string]string{"a/A.java": "class A {}\n"})
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	if ix, err := Scan(context.Background(), root, DefaultLimits); err == nil {
		t.Errorf("an unreadable directory was walked: %d files, partial %q", ix.Len(), ix.Partial)
	}
	// One below the root is skipped as before: the rest is still source.
	_ = os.Chmod(root, 0o755)
	if err := os.Chmod(filepath.Join(root, "a"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "a"), 0o755) })
	if ix, err := Scan(context.Background(), root, DefaultLimits); err != nil || ix.Len() != 0 {
		t.Errorf("an unreadable directory under the root: %v", err)
	}
}

// A file is a source file whatever the case of its extension, and is then
// found for the frame as well.
func TestScanFindsAnUpperCaseExtension(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{
		"src/com/example/shop/CartService.JAVA": "package com.example.shop;\nclass CartService {\n    int total() { return 0; }\n}\n",
	}))
	got, unread, _ := parts(ix.Resolve(context.Background(), Parse("com.example.shop.CartService.total")))
	if len(got) != 1 || got[0].Path != "src/com/example/shop/CartService.JAVA" || got[0].Line != 3 || unread != 0 {
		t.Errorf("%+v, %d unread; want the file the walk counted", got, unread)
	}
}

// What a package manager installed is not the application's, under any of
// the names a label is refused for.
func TestScanSkipsEveryDependencyDirectory(t *testing.T) {
	files := map[string]string{"app/main.py": "def main():\n    pass\n"}
	for dir := range dependencies {
		files["lib/"+dir+"/pkg/mod.py"] = "def main():\n    pass\n"
	}
	ix := scan(t, tree(t, files))
	if ix.Len() != 1 {
		t.Errorf("%d files: every directory of dependencies is to be skipped (%v)", ix.Len(), dependencies)
	}
}

// A directory is the root, whatever it is called: the names that are
// skipped are skipped below it.
func TestScanWalksARootOfAnyName(t *testing.T) {
	for _, name := range []string{"build", ".config", "vendor"} {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if ix := scan(t, root); ix.Len() != 1 {
			t.Errorf("a root named %s: %d files", name, ix.Len())
		}
	}
}

// A .git entry that is a link to nothing is no repository.
func TestRootNeedsAGitEntryThatIsThere(t *testing.T) {
	outer := tree(t, map[string]string{".git/HEAD": "x", "a/b/f.go": ""})
	inner := filepath.Join(outer, "a")
	if err := os.Symlink(filepath.Join(outer, "missing"), filepath.Join(inner, ".git")); err != nil {
		t.Skip("no symbolic link here:", err)
	}
	if got, err := Root(filepath.Join(inner, "b"), ""); got != outer || err != nil {
		t.Errorf("Root = %q, %v; want %q, past the dangling .git", got, err, outer)
	}
}

// Outside a repository there is no directory to look in, up to the top.
func TestRootOutsideARepositoryIsNone(t *testing.T) {
	asked := 0
	got, err := rootIn(filepath.Join(t.TempDir(), "a", "b"), "", func(string) bool { asked++; return false })
	if got != "" || err != nil || asked < 3 {
		t.Errorf("rootIn = %q, %v after %d looks; want none", got, err, asked)
	}
}

// The files of one lookup are read with patterns made once: the frame is
// the same for each of them.
func TestLookForCompilesOnce(t *testing.T) {
	look := looking(Parse("com.example.shop.CartService.total"), "com.example.shop")
	java, again := look.in(".java"), look.in(".java")
	if java.function == nil || java.typ == nil || java.function != again.function || java.typ != again.typ {
		t.Errorf("the patterns of one extension were made twice: %p %p", java.function, again.function)
	}
	if kt := look.in(".kt"); kt.function == java.function {
		t.Error("another extension has the same pattern")
	}
	if look.pkg == nil {
		t.Error("no pattern for the package")
	}
}

// Every lookup ends in time, whoever asks: the bound is the lookup's own.
func TestResolveBoundsItsOwnTime(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{"main.go": "package main\nfunc main() {}\n"}))
	asked := false
	ix.resolve(context.Background(), Parse("main.main"), func(ctx context.Context, rel string, look *lookFor) (Match, holds, reading) {
		asked = true
		if by, ok := ctx.Deadline(); !ok || time.Until(by) > maxResolveTime {
			t.Errorf("a file is read without the lookup's bound (deadline %v, %v)", by, ok)
		}
		return ix.locate(ctx, rel, look)
	})
	if !asked {
		t.Fatal("no file was read")
	}
}

// A file that may only be read is read: a lookup asks for nothing more.
func TestResolveReadsAReadOnlyFile(t *testing.T) {
	root := tree(t, map[string]string{"main.go": "package main\nfunc main() {}\n"})
	if err := os.Chmod(filepath.Join(root, "main.go"), 0o444); err != nil {
		t.Fatal(err)
	}
	got, unread, _ := parts(scan(t, root).Resolve(context.Background(), Parse("main.main")))
	if len(got) != 1 || got[0].Line != 2 || unread != 0 {
		t.Errorf("%+v, %d unread", got, unread)
	}
}

// A file that is opened and then refused is closed again.
func TestOpenSameClosesWhatItRefuses(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	seen, err := os.Lstat(filepath.Join(root, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	var opened *os.File
	file, err := openSame(func(abs string) (*os.File, error) {
		f, err := openNoWait(abs)
		opened = f
		return f, err
	}, filepath.Join(root, "b.go"), seen)
	if !errors.Is(err, errNotRegular) || file != nil || opened == nil {
		t.Fatalf("another file: %v, %v", file, err)
	}
	if _, err := io.ReadAll(opened); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the refused file is still open: %v", err)
	}
}

// Whether a Go frame could have a file is asked for every frame the cursor
// rests on, and is the same for all of them: the walk says it once.
func TestLikelyOfAGoFrameReadsNoList(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{"api/main.go": "package main\n", "api/main_test.go": "package main\n"}))
	if !ix.Likely(Parse("example.com/shop/api.Run")) || !ix.Likely(Parse("main.main")) {
		t.Error("a Go frame has no file in a tree with one")
	}
	if n := testing.AllocsPerRun(20, func() { ix.Likely(Frame{Notation: Go, Package: "example.com/shop/api", Function: "Run"}) }); n != 0 {
		t.Errorf("Likely of a Go frame allocates %v times", n)
	}
	only := scan(t, tree(t, map[string]string{"api/main_test.go": "package main\n", "web/a.js": ""}))
	if only.Likely(Parse("example.com/shop/api.Run")) {
		t.Error("a Go frame has a file in a tree with tests only")
	}
}

// A frame of Go's standard library opens nothing, also where a script of the
// checkout is called like its package.
func TestResolveOpensNoScriptForTheGoRuntime(t *testing.T) {
	ix := scan(t, tree(t, map[string]string{"scripts/time.py": "def Now():\n    pass\n", "scripts/runtime.py": "def main():\n    pass\n"}))
	for _, label := range []string{"time.Now", "runtime.main", "time.Time.Add"} {
		f := Parse(label)
		if got, unread, _ := parts(ix.Resolve(context.Background(), f)); len(got) != 0 || unread != 0 || f.Runtime == "" || ix.Likely(f) {
			t.Errorf("%s: %+v, runtime %q", label, got, f.Runtime)
		}
	}
}
