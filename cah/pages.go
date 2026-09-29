//go:build linux

package cah

import (
	"io"
	"j5.nz/scs/repo"
	"syscall"
)

// BufferUsage reports charged overlay bytes and their limit. Native decode
// caches, visited-node metadata, and temporary ingestion allocations are extra.
func (f *FS) BufferUsage() (used, limit int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buffered, f.bufferLimit
}
func (n *Node) dropBuffers() {
	n.owner.buffered -= int64(len(n.pages)) * repo.BlockSize
	n.pages = nil
	if n.reader != nil {
		n.reader.Close()
		n.reader = nil
	}
}
func (n *Node) readAt(dst []byte, off int64) (int, error) {
	if err := n.ensureReader(); err != nil {
		return 0, err
	}
	count := 0
	for len(dst) > 0 && off < n.entry.Size {
		index, within := off/repo.BlockSize, off%repo.BlockSize
		size := min(int64(len(dst)), repo.BlockSize-within, n.entry.Size-off)
		part := dst[:size]
		clear(part)
		if page := n.pages[index]; page != nil {
			copy(part, page[within:])
		} else if off < n.baseLimit {
			size := min(size, n.baseLimit-off)
			if _, err := n.reader.ReadAt(part[:size], off); err != nil {
				return count, err
			}
		}
		off += size
		count += int(size)
		dst = dst[size:]
	}
	if len(dst) > 0 {
		return count, io.EOF
	}
	return count, nil
}

// Stage every missing page before changing the inode. ENOSPC and read failures
// are atomic: no partial contents, length, timestamps, or accounting changes.
func (n *Node) writePages(data []byte, off int64) error {
	first, last := off/repo.BlockSize, (off+int64(len(data))-1)/repo.BlockSize
	missing := int64(0)
	for i := first; i <= last; i++ {
		if n.pages[i] == nil {
			missing++
		}
	}
	if missing*repo.BlockSize > n.owner.bufferLimit-n.owner.buffered {
		return syscall.ENOSPC
	}
	if err := n.ensureReader(); err != nil {
		return err
	}
	staged := make(map[int64][]byte)
	for i := first; i <= last; i++ {
		if n.pages[i] != nil {
			continue
		}
		page := make([]byte, repo.BlockSize)
		if _, err := n.readAt(page, i*repo.BlockSize); err != nil && err != io.EOF {
			return err
		}
		staged[i] = page
	}
	if n.pages == nil {
		n.pages = make(map[int64][]byte)
	}
	for i, page := range staged {
		n.pages[i] = page
	}
	n.owner.buffered += missing * repo.BlockSize
	for len(data) > 0 {
		within := off % repo.BlockSize
		count := copy(n.pages[off/repo.BlockSize][within:], data)
		data = data[count:]
		off += int64(count)
	}
	return nil
}
