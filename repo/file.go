package repo

import (
	"io"
	"os"
)

// repositoryFile is the internal storage boundary. Production opens always use
// *os.File, retaining the same locking/fsync behavior. Tests wrap this boundary
// to inject partial writes and failed durability barriers without filling a disk
// or exposing fault-injection controls through the public API.
type repositoryFile interface {
	io.ReaderAt
	io.Writer
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(int64) error
	WriteAt([]byte, int64) (int, error)
}

// writePart rejects even a broken writer that returns a short count and nil.
// Retrying a partial record here could hide an ambiguous storage failure; poison
// the repository and require recovery on a fresh handle instead.
func writePart(w io.Writer, p []byte) error {
	n, err := w.Write(p)
	if err == nil && n != len(p) {
		return io.ErrShortWrite
	}
	return err
}
