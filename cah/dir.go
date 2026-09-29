//go:build linux

package cah

import (
	"context"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Each directory handle has a deterministic snapshot and independent cursor.
// Exposing Fsyncdir is important: the default go-fuse directory adapter does
// not forward directory fsync to Node.Fsync.
type directory struct {
	mu      sync.Mutex
	owner   *FS
	entries []fuse.DirEntry
	pos     uint64
}

func (n *Node) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	stream, e := n.Readdir(ctx)
	if e != 0 {
		return nil, 0, e
	}
	defer stream.Close()
	d := &directory{owner: n.owner}
	for stream.HasNext() {
		entry, e := stream.Next()
		if e != 0 {
			return nil, 0, e
		}
		d.entries = append(d.entries, entry)
	}
	return d, 0, 0
}
func (d *directory) Readdirent(ctx context.Context) (*fuse.DirEntry, syscall.Errno) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pos >= uint64(len(d.entries)) {
		return nil, 0
	}
	entry := d.entries[d.pos]
	d.pos++
	entry.Off = d.pos
	return &entry, 0
}
func (d *directory) Seekdir(ctx context.Context, off uint64) syscall.Errno {
	d.mu.Lock()
	defer d.mu.Unlock()
	if off > uint64(len(d.entries)) {
		return syscall.EINVAL
	}
	d.pos = off
	return 0
}
func (d *directory) Fsyncdir(ctx context.Context, flags uint32) syscall.Errno {
	return errno(d.owner.Sync())
}

var _ fs.FileReaddirenter = (*directory)(nil)
var _ fs.FileSeekdirer = (*directory)(nil)
var _ fs.FileFsyncdirer = (*directory)(nil)
