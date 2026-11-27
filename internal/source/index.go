package source

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Limits bound a walk of the source directory: a home directory or a
// monorepo must cost a moment, not a minute.
type Limits struct {
	// Entries is the most directory entries looked at, Files the most
	// source files kept, and Time the longest the walk may take.
	Entries, Files int
	Time           time.Duration
}

// DefaultLimits are the limits dtui walks with.
var DefaultLimits = Limits{Entries: 300_000, Files: 60_000, Time: 3 * time.Second}

// skipped are directories whose files are not the application's source:
// dependencies, build output and tool state. Hidden directories are skipped
// as well.
var skipped = func() map[string]bool {
	skip := map[string]bool{
		"target": true, "build": true, "dist": true, "out": true, "bin": true, "obj": true, "__pycache__": true, "venv": true,
	}
	// What a label is refused for is not indexed either.
	for dir := range dependencies {
		skip[dir] = true
	}
	return skip
}()

// extensions are the files the index keeps, by what reads them.
var extensions = map[string]bool{
	".java": true, ".kt": true, ".scala": true, ".groovy": true,
	".cs": true, ".fs": true, ".vb": true,
	".go": true, ".py": true, ".rb": true, ".php": true,
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true, ".ts": true, ".mts": true, ".cts": true, ".tsx": true,
}

// Index is the source files under one directory, found by one walk. Every
// path it hands out is one the walk saw, and still the file it was then as
// far as a path can tell (Abs): a frame's text selects among them and never
// forms a path of its own.
type Index struct {
	root string
	// byName holds the root-relative, slash-separated paths by file name,
	// and byExt by extension. The extension is in lower case in both keys,
	// whatever the file's own is: CartService.JAVA is found as
	// CartService.java.
	byName, byExt map[string][]string
	known         map[string]bool
	// goFiles are the Go files that are not tests, which is where a Go
	// frame is looked for.
	goFiles []string
	// opener is what a file of the index is opened with.
	opener opener
	// Partial says why the walk stopped early; "" when it saw everything.
	Partial string
}

// The refusals of Root: a repository is found, and it is no place to look
// for an application's source. Each reads on after "dtui was started in".
var (
	ErrHomeDirectory = errors.New("the repository here is your home directory, which is not searched: start dtui inside the application's checkout")
	ErrVolumeRoot    = errors.New("the repository here is a whole volume, which is not searched: start dtui inside the application's checkout")
)

// Root is the directory frames are looked up in: the repository dir is in,
// which is the nearest directory at or above it that holds a .git entry.
// Outside a repository it is "": there is nothing to say where an
// application's source ends, and a home directory is not walked for being
// where dtui was started.
//
// A repository that is the home directory (home, "" when none is known) or
// the root of a volume is refused with an error, also though it holds .git:
// a walk of either reads a person's files and not an application's, and a
// home directory kept in git is common. A repository inside one is found
// before it and is looked in.
func Root(dir, home string) (string, error) {
	return rootIn(dir, home, func(p string) bool {
		// The entry is followed: a link to nothing is no repository.
		_, err := os.Stat(p)
		return err == nil
	})
}

// rootIn is Root with the look at a path as a parameter, so that the root of
// a volume can be tested without one that holds a .git entry.
func rootIn(dir, home string, exists func(path string) bool) (string, error) {
	for at := filepath.Clean(dir); ; at = filepath.Dir(at) {
		top := filepath.Dir(at) == at
		if exists(filepath.Join(at, ".git")) {
			switch {
			case top:
				return "", ErrVolumeRoot
			case sameDir(at, home):
				return "", ErrHomeDirectory
			}
			return at, nil
		}
		if top {
			return "", nil
		}
	}
}

// sameDir reports whether two paths name one directory, however each is
// spelled; "" names none.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

// Scan walks root and indexes its source files. Symbolic links are not
// followed and not kept, so nothing outside root is ever in the index.
func Scan(ctx context.Context, root string, lim Limits) (*Index, error) {
	root, err := filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return nil, fmt.Errorf("source directory: %w", err)
	}
	ix := &Index{root: root, byName: map[string][]string{}, byExt: map[string][]string{}, known: map[string]bool{}, opener: openNoWait}
	deadline := time.Now().Add(lim.Time)
	entries := 0
	stop := errors.New("stop")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err // nothing of it was read: that is no empty repository
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable: not ours to index
			}
			return nil
		}
		entries++
		switch {
		case ctx.Err() != nil:
			ix.Partial = "the search was interrupted"
		case entries > lim.Entries:
			ix.Partial = fmt.Sprintf("more than %d files and directories", lim.Entries)
		case entries%512 == 0 && time.Now().After(deadline):
			ix.Partial = fmt.Sprintf("reading the directory took more than %s", lim.Time)
		}
		if ix.Partial != "" {
			return stop
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (skipped[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !d.Type().IsRegular() || !extensions[ext] {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		// Partial only when a file is left out: a directory that holds
		// exactly as many as are kept was seen whole.
		if len(ix.known) >= lim.Files {
			ix.Partial = fmt.Sprintf("more than %d source files", lim.Files)
			return stop
		}
		rel = filepath.ToSlash(rel)
		key := name[:len(name)-len(ext)] + ext
		ix.byName[key] = append(ix.byName[key], rel)
		ix.byExt[ext] = append(ix.byExt[ext], rel)
		ix.known[rel] = true
		if ext == ".go" && !strings.HasSuffix(key, "_test.go") {
			ix.goFiles = append(ix.goFiles, rel)
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return nil, fmt.Errorf("source directory: %w", err)
	}
	return ix, nil
}

// Root is the directory the index is of, with symbolic links resolved.
func (ix *Index) Root() string { return ix.root }

// Len is the number of source files in the index.
func (ix *Index) Len() int { return len(ix.known) }

// Abs is the absolute path of a file of the index. It refuses a path the
// walk did not see, one that is no longer the file the walk saw (the file,
// or a directory on the way to it, can have become a symbolic link since,
// and what a link leads to was never indexed, inside the root or out of it)
// and one that is no longer a regular file: a named pipe or a directory is
// not handed to an editor.
func (ix *Index) Abs(rel string) (string, error) {
	if !ix.known[rel] {
		return "", fmt.Errorf("%q is not a file of %s", rel, ix.root)
	}
	abs := filepath.Join(ix.root, filepath.FromSlash(path.Clean("/"+rel)))
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	// The root has its links resolved and the walk followed none, so the
	// path of a file the walk saw resolves to itself.
	if resolved != abs {
		return "", fmt.Errorf("%q is %w", rel, errNotSeen)
	}
	if info, err := os.Lstat(abs); err != nil {
		return "", err
	} else if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is %w", rel, errNotRegular)
	}
	return abs, nil
}
