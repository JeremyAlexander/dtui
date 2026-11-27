//go:build unix

package source

import (
	"os"
	"syscall"
)

// openFlags open a file for reading without waiting and without following a
// link in the last step of the path. Opening a named pipe for reading
// otherwise waits for a writer that may never come, and a device can wait
// too; a path that became a link since it was looked at would otherwise be
// opened, whatever it leads to, before it is refused. A regular file is read
// the same with the flags as without.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
