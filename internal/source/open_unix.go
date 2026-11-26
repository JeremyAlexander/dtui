//go:build unix

package source

import (
	"os"
	"syscall"
)

// openFlags open a file for reading without waiting. Opening a named pipe
// for reading otherwise waits for a writer that may never come, and a device
// can wait too; a regular file is read the same with the flag as without.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK
