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

// errNotSeen is the refusal of a path that is in the index and now leads
// somewhere else: it, or a directory on the way to it, became a symbolic
// link after the walk.
var errNotSeen = errors.New("a symbolic link now, and no longer the file that was found")

// opener opens the file at a path for reading. A lookup opens with
// openNoWait; the type is there so that the look before the open can be
// tested with an open that waits, as it does where no flag keeps it from it.
type opener func(abs string) (*os.File, error)

// openNoWait opens a file for reading with the platform's openFlags: without
// waiting where a flag asks for that, plainly where none does.
func openNoWait(abs string) (*os.File, error) { return os.OpenFile(abs, openFlags, 0) }

// openRegular opens the regular file at abs for reading, and nothing else.
// The path is looked at without following a link and is opened only when it
// names a regular file; the opened file is then compared with what was seen.
//
// What the comparison holds for depends on the platform. On Unix the look
// keeps the file's device and inode, so a path swapped between the look and
// the open, for a link or for another regular file, is refused. On Windows
// the look keeps the path and the file's identity is read through it only
// when the two are compared, which is after the open: a swap for a link is
// still refused, a swap for another regular file is not seen. The first
// look is the one that holds on every platform. Where open cannot be told
// not to wait, it is all that keeps a lookup from a named pipe.
func openRegular(open opener, abs string) (*os.File, error) {
	seen, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !seen.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return openSame(open, abs, seen)
}

// openSame opens abs and refuses it unless it is the regular file seen.
func openSame(open opener, abs string, seen fs.FileInfo) (*os.File, error) {
	file, err := open(abs)
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
// At the bound it asks r for one byte more, once, and more says whether
// there was one: a file that goes on was not read to its end.
type bounded struct {
	ctx    context.Context
	r      io.Reader
	left   int64
	more   bool
	probed bool
}

func (b *bounded) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.left <= 0 {
		if !b.probed {
			b.probed = true
			var one [1]byte
			n, err := b.r.Read(one[:])
			b.more = n > 0
			if n == 0 && err != nil && err != io.EOF {
				return 0, err
			}
		}
		return 0, io.EOF
	}
	p = p[:min(int64(len(p)), b.left, readChunk)]
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}
