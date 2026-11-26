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
var skipped = map[string]bool{
	"node_modules": true, "vendor": true, "target": true, "build": true, "dist": true, "out": true,
	"bin": true, "obj": true, "__pycache__": true, "venv": true, "site-packages": true, "bower_components": true,
}

// extensions are the files the index keeps, by what reads them.
var extensions = map[string]bool{
	".java": true, ".kt": true, ".scala": true, ".groovy": true,
	".cs": true, ".fs": true, ".vb": true,
	".go": true, ".py": true, ".rb": true, ".php": true,
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true, ".ts": true, ".mts": true, ".cts": true, ".tsx": true,
}

// Index is the source files under one directory, found by one walk. Every
// path it hands out is one the walk saw: a frame's text selects among them
// and never forms a path of its own.
type Index struct {
	root string
	// byName holds the root-relative, slash-separated paths by file name,
	// and byExt by extension.
	byName, byExt map[string][]string
	known         map[string]bool
	// Partial says why the walk stopped early; "" when it saw everything.
	Partial string
}

// Root is the directory frames are looked up in: the nearest directory at or
// above dir that holds a .git entry, or dir itself.
func Root(dir string) string {
	for at := dir; ; at = filepath.Dir(at) {
		if _, err := os.Lstat(filepath.Join(at, ".git")); err == nil {
			return at
		}
		if filepath.Dir(at) == at {
			return dir
		}
	}
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
	ix := &Index{root: root, byName: map[string][]string{}, byExt: map[string][]string{}, known: map[string]bool{}}
	deadline := time.Now().Add(lim.Time)
	entries := 0
	stop := errors.New("stop")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
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
		case len(ix.known) >= lim.Files:
			ix.Partial = fmt.Sprintf("more than %d source files", lim.Files)
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
		rel = filepath.ToSlash(rel)
		ix.byName[name] = append(ix.byName[name], rel)
		ix.byExt[ext] = append(ix.byExt[ext], rel)
		ix.known[rel] = true
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
// walk did not see, and one that no longer resolves to a place inside the
// root: a file can have become a symbolic link since.
func (ix *Index) Abs(rel string) (string, error) {
	if !ix.known[rel] {
		return "", fmt.Errorf("%q is not a file of %s", rel, ix.root)
	}
	abs := filepath.Join(ix.root, filepath.FromSlash(path.Clean("/"+rel)))
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resolved, ix.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%q leads outside %s", rel, ix.root)
	}
	return resolved, nil
}
