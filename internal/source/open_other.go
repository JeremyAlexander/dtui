//go:build !unix

package source

import "os"

// openFlags open a file for reading. Where no flag asks an open not to wait,
// the check before the open is what keeps a lookup from one that would.
const openFlags = os.O_RDONLY
