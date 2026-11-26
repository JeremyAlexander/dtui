package source

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
)

// errNotRegular is the refusal of a path that is in the index and is no
// longer a regular file: a named pipe, a device, a socket or a directory
// took its place after the walk. Reading one can wait forever or never end.
var errNotRegular = errors.New("not a regular file any more")

// openRegular opens the regular file at abs for reading, and nothing else.
// The path is looked at without following a link, opened without waiting,
// and the opened file is looked at again: what is read is the regular file
// that was seen, also when the path was swapped in between.
func openRegular(abs string) (*os.File, error) {
	seen, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !seen.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return openSame(abs, seen)
}

// openSame opens abs and refuses it unless it is the regular file seen.
func openSame(abs string, seen fs.FileInfo) (*os.File, error) {
	file, err := os.OpenFile(abs, openFlags, 0)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err == nil && (!opened.Mode().IsRegular() || !os.SameFile(seen, opened)) {
		err = errNotRegular
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// readChunk is the most one read asks a file for: the time is looked at
// between reads, so a large file cannot hold a lookup past its end.
const readChunk = 64 << 10

// bounded reads r a chunk at a time, up to left bytes, and until ctx ends.
// A file that grows while it is read ends at the byte bound like any other.
type bounded struct {
	ctx  context.Context
	r    io.Reader
	left int64
}

func (b *bounded) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.left <= 0 {
		return 0, io.EOF
	}
	p = p[:min(int64(len(p)), b.left, readChunk)]
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}
