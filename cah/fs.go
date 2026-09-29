//go:build linux

// Package cah exposes a native SCS workspace through FUSE, without a host-file
// backing tree. One mount exclusively owns its Workspace. Namespace and handle
// state are serialized; native immutable readers serve unmodified content.
package cah

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"j5.nz/scs/repo"
)

// FS holds all mutable state under mu. No caller may mutate w independently.
// Writable files use quota-controlled page overlays, not extracted host files.
// The quota covers overlay bytes, not metadata, native caches, or process RSS.
type FS struct {
	mu                    sync.Mutex
	bufferLimit, buffered int64
	w                     *repo.Workspace
	name                  string
	nodes                 map[string]*Node
	next                  uint64
	dirty                 bool
	uid, gid              uint32
	Root                  *Node
}

type Node struct {
	fs.Inode
	owner        *FS
	path         string // empty after unlink/replacement; descendants updated on rename
	entry        repo.Entry
	ino          uint64
	reader       *repo.Reader
	pages        map[int64][]byte
	baseLimit    int64
	dirty        bool
	contentDirty bool
	opens        int
	target       string
}

type handle struct {
	n     *Node
	flags uint32
}

// Options controls mount-local resources. A zero limit uses the default.
type Options struct{ BufferLimit int64 }

const DefaultBufferLimit int64 = 64 << 20

func New(w *repo.Workspace, name string) (*FS, error) { return NewWithOptions(w, name, Options{}) }
func NewWithOptions(w *repo.Workspace, name string, opts Options) (*FS, error) {
	if opts.BufferLimit < 0 {
		return nil, errors.New("negative buffer limit")
	}
	if opts.BufferLimit == 0 {
		opts.BufferLimit = DefaultBufferLimit
	}

	e, err := w.Stat(".")
	if err != nil {
		return nil, err
	}
	f := &FS{bufferLimit: opts.BufferLimit, w: w, name: name, nodes: make(map[string]*Node), next: 2, uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}
	f.Root = &Node{owner: f, path: ".", entry: e, ino: 1}
	f.nodes["."] = f.Root
	return f, nil
}

func (f *FS) Mount(mountpoint string) (*fuse.Server, error) {
	zero := time.Duration(0)
	return fs.Mount(mountpoint, f.Root, &fs.Options{
		MountOptions:    fuse.MountOptions{Name: "cah", FsName: "cah", Options: []string{"default_permissions"}},
		NullPermissions: true, AttrTimeout: &zero, EntryTimeout: &zero, NegativeTimeout: &zero,
		UID: f.uid, GID: f.gid,
	})
}

func errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var e syscall.Errno
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, iofs.ErrNotExist) {
		return syscall.ENOENT
	}
	if errors.Is(err, iofs.ErrExist) {
		return syscall.EEXIST
	}
	if errors.Is(err, repo.ErrReadOnly) {
		return syscall.EROFS
	}
	log.Printf("cah: %v", err)
	return syscall.EIO
}
func kind(e repo.Entry) uint32 {
	switch e.Kind {
	case "dir":
		return syscall.S_IFDIR
	case "symlink":
		return syscall.S_IFLNK
	default:
		return syscall.S_IFREG
	}
}
func (n *Node) attr(out *fuse.Attr) {
	out.Ino = n.ino
	out.Mode = kind(n.entry) | n.entry.Mode
	out.Size = uint64(n.entry.Size)
	out.Owner = fuse.Owner{Uid: n.owner.uid, Gid: n.owner.gid}
	out.Nlink = 1
	if n.path == "" {
		out.Nlink = 0
	}
	if n.entry.Kind == "dir" && n.path != "" {
		out.Nlink = 2
	}
	out.Blksize = 4096
	out.Blocks = (out.Size + 511) / 512
	a, m, c := time.Unix(0, n.entry.Times.A), time.Unix(0, n.entry.Times.M), time.Unix(0, n.entry.Times.C)
	out.SetTimes(&a, &m, &c)
}
func (f *FS) get(p string) (*Node, error) {
	if n := f.nodes[p]; n != nil {
		return n, nil
	}
	e, err := f.w.Stat(p)
	if err != nil {
		return nil, err
	}
	n := &Node{owner: f, path: p, entry: e, ino: f.next}
	f.next++
	if e.Kind == "symlink" {
		n.target, err = f.w.Readlink(p)
		if err != nil {
			return nil, err
		}
	}
	f.nodes[p] = n
	return n, nil
}
func (n *Node) child(name string) (string, syscall.Errno) {
	if n.path == "" {
		return "", syscall.ENOENT
	}
	if n.entry.Kind != "dir" {
		return "", syscall.ENOTDIR
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", syscall.EINVAL
	}
	return path.Join(n.path, name), 0
}
func (n *Node) inode(ctx context.Context, child *Node, out *fuse.EntryOut) *fs.Inode {
	child.attr(&out.Attr)
	return n.NewInode(ctx, child, fs.StableAttr{Mode: kind(child.entry), Ino: child.ino})
}
func (n *Node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	p, e := n.child(name)
	if e != 0 {
		return nil, e
	}
	child, err := f.get(p)
	if err != nil {
		return nil, errno(err)
	}
	return n.inode(ctx, child, out), 0
}
func (n *Node) Getattr(ctx context.Context, h fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	n.owner.mu.Lock()
	defer n.owner.mu.Unlock()
	n.attr(&out.Attr)
	return 0
}
func (n *Node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	if n.path == "" {
		return fs.NewListDirStream(nil), 0
	}
	names, err := f.w.ListDir(n.path)
	if err != nil {
		return nil, errno(err)
	}
	entries := make([]fuse.DirEntry, 0, len(names))
	for _, name := range names {
		child, err := f.get(path.Join(n.path, name))
		if err != nil {
			return nil, errno(err)
		}
		entries = append(entries, fuse.DirEntry{Name: name, Mode: kind(child.entry), Ino: child.ino})
	}
	return fs.NewListDirStream(entries), 0
}
func (n *Node) ensureReader() error {
	if n.reader != nil {
		return nil
	}
	r, err := n.owner.w.OpenReader(n.path)
	if err != nil {
		return err
	}
	n.reader = r
	n.baseLimit = r.Size()
	return nil
}

const maxFileSize = 1 << 30

func (n *Node) changed() {
	now := time.Now().UnixNano()
	n.entry.Times.M = now
	n.entry.Times.C = now
	n.dirty = true
	n.owner.dirty = true
}
func (n *Node) resize(size uint64) error {
	if size > maxFileSize {
		return syscall.EFBIG
	}
	if err := n.ensureReader(); err != nil {
		return err
	}
	n.baseLimit = min(n.baseLimit, int64(size))
	for index, page := range n.pages {
		off := index * repo.BlockSize
		if off >= int64(size) {
			delete(n.pages, index)
			n.owner.buffered -= repo.BlockSize
		} else if off+repo.BlockSize > int64(size) {
			clear(page[int64(size)-off:])
		}
	}
	n.entry.Size = int64(size)
	n.contentDirty = true
	n.changed()
	return nil
}
func (n *Node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	if n.entry.Kind != "file" {
		return nil, 0, syscall.EOPNOTSUPP
	}
	if flags&syscall.O_TRUNC != 0 {
		if flags&syscall.O_ACCMODE == syscall.O_RDONLY {
			return nil, 0, syscall.EACCES
		}
		if err := n.resize(0); err != nil {
			return nil, 0, errno(err)
		}
	}
	if err := n.ensureReader(); err != nil {
		return nil, 0, errno(err)
	}
	n.opens++
	// Buffered reads are required for executable mmap. No writeback cache: each
	// kernel write is immediately visible to every handle of this inode.
	return &handle{n: n, flags: flags}, 0, 0
}
func (h *handle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	n := h.n
	n.owner.mu.Lock()
	defer n.owner.mu.Unlock()
	if h.flags&syscall.O_ACCMODE == syscall.O_WRONLY {
		return nil, syscall.EBADF
	}
	if off < 0 {
		return nil, syscall.EINVAL
	}
	count, err := n.readAt(dest, off)
	if err != nil && err != io.EOF {
		return nil, errno(err)
	}
	return fuse.ReadResultData(dest[:count]), 0
}
func (h *handle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	n := h.n
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	if h.flags&syscall.O_ACCMODE == syscall.O_RDONLY {
		return 0, syscall.EBADF
	}
	if len(data) == 0 {
		return 0, 0
	}
	// The kernel supplies append offsets; serializing here also makes concurrent
	// append requests atomic in this non-writeback-cache implementation.
	if h.flags&syscall.O_APPEND != 0 {
		off = n.entry.Size
	}
	if n.entry.Size > maxFileSize || off < 0 || off > maxFileSize || int64(len(data)) > maxFileSize-off {
		return 0, syscall.EFBIG
	}
	if err := n.writePages(data, off); err != nil {
		return 0, errno(err)
	}
	n.entry.Size = max(n.entry.Size, off+int64(len(data)))
	n.contentDirty = true
	n.changed()
	if h.flags&(syscall.O_SYNC|syscall.O_DSYNC) != 0 {
		if err := f.sync(); err != nil {
			return uint32(len(data)), errno(err)
		}
	}
	return uint32(len(data)), 0
}
func (n *Node) flush() error {
	if !n.dirty || n.path == "" {
		return nil
	}
	if n.contentDirty {
		if err := n.owner.w.WritePages(n.path, n.reader, n.entry.Size, n.baseLimit, n.pages); err != nil {
			return err
		}
	}

	if err := n.owner.w.SetTimes(n.path, n.entry.Times); err != nil {
		return err
	}
	n.dirty = false
	n.contentDirty = false
	n.dropBuffers()
	return nil
}
func (h *handle) Flush(ctx context.Context) syscall.Errno {
	h.n.owner.mu.Lock()
	defer h.n.owner.mu.Unlock()
	return errno(h.n.flush())
}
func (h *handle) Release(ctx context.Context) syscall.Errno {
	n := h.n
	n.owner.mu.Lock()
	defer n.owner.mu.Unlock()
	n.opens--
	if n.opens == 0 && (!n.dirty || n.path == "") {
		n.dropBuffers()
	}
	return 0
}
func (f *FS) sync() error {
	for _, n := range f.nodes {
		if err := n.flush(); err != nil {
			return err
		}
	}
	if !f.dirty {
		return nil
	}
	if _, err := f.w.Publish(f.name); err != nil {
		return err
	}
	f.dirty = false
	return nil
}

// Sync durably publishes all reachable writes and metadata, including writes on
// still-open handles. It must be called after the server has drained on unmount.
func (f *FS) Sync() error { f.mu.Lock(); defer f.mu.Unlock(); return f.sync() }
func (h *handle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	return errno(h.n.owner.Sync())
}
func (n *Node) Fsync(ctx context.Context, h fs.FileHandle, flags uint32) syscall.Errno {
	return errno(n.owner.Sync())
}

func (n *Node) Setattr(ctx context.Context, h fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	if uid, ok := in.GetUID(); ok && uid != f.uid {
		return syscall.EPERM
	}
	if gid, ok := in.GetGID(); ok && gid != f.gid {
		return syscall.EPERM
	}
	if mode, ok := in.GetMode(); ok {
		if mode&07000 != 0 {
			return syscall.EOPNOTSUPP
		}
		if n.path == "." {
			return syscall.EOPNOTSUPP
		}
		if n.path != "" {
			if err := f.w.Chmod(n.path, mode&0777); err != nil {
				return errno(err)
			}
		}
		n.entry.Mode = mode & 0777
		n.entry.Times.C = time.Now().UnixNano()
		n.dirty = true
		f.dirty = true
	}
	if size, ok := in.GetSize(); ok {
		if n.entry.Kind != "file" {
			return syscall.EISDIR
		}
		if err := n.resize(size); err != nil {
			return errno(err)
		}
	}
	if a, ok := in.GetATime(); ok {
		n.entry.Times.A = a.UnixNano()
		n.dirty = true
		f.dirty = true
	}
	if m, ok := in.GetMTime(); ok {
		n.entry.Times.M = m.UnixNano()
		n.dirty = true
		f.dirty = true
	}
	if n.dirty {
		n.entry.Times.C = time.Now().UnixNano()
	}
	n.attr(&out.Attr)
	return 0
}

// touchDir records namespace changes without traversing the directory.
func (n *Node) touchDir() { n.changed() }
func (n *Node) create(ctx context.Context, name, typ, target string, mode uint32, out *fuse.EntryOut) (*Node, syscall.Errno) {
	f := n.owner
	p, e := n.child(name)
	if e != 0 {
		return nil, e
	}
	if _, err := f.get(p); err == nil {
		return nil, syscall.EEXIST
	} else if !errors.Is(err, iofs.ErrNotExist) {
		return nil, errno(err)
	}
	if mode&07000 != 0 {
		return nil, syscall.EOPNOTSUPP
	}
	var err error
	switch typ {
	case "dir":
		err = f.w.Mkdir(p)
	case "file":
		err = f.w.WriteFile(p, nil)
	case "symlink":
		err = f.w.Symlink(p, target)
	}
	if err != nil {
		return nil, errno(err)
	}
	if typ != "symlink" {
		if err = f.w.Chmod(p, mode&0777); err != nil {
			return nil, errno(err)
		}
	}
	child, err := f.get(p)
	if err != nil {
		return nil, errno(err)
	}
	child.changed()
	child.entry.Times.A = child.entry.Times.M
	n.touchDir()
	return child, 0
}
func (n *Node) Create(ctx context.Context, name string, flags, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	child, e := n.create(ctx, name, "file", "", mode, out)
	if e != 0 {
		return nil, nil, 0, e
	}
	child.opens++
	return n.inode(ctx, child, out), &handle{n: child, flags: flags}, 0, 0
}
func (n *Node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	child, e := n.create(ctx, name, "dir", "", mode, out)
	if e != 0 {
		return nil, e
	}
	return n.inode(ctx, child, out), 0
}
func (n *Node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	child, e := n.create(ctx, name, "symlink", target, 0777, out)
	if e != 0 {
		return nil, e
	}
	return n.inode(ctx, child, out), 0
}
func (n *Node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	n.owner.mu.Lock()
	defer n.owner.mu.Unlock()
	return []byte(n.target), 0
}
func (n *Node) remove(name string, dir bool) syscall.Errno {
	f := n.owner
	p, e := n.child(name)
	if e != 0 {
		return e
	}
	child, err := f.get(p)
	if err != nil {
		return errno(err)
	}
	if dir && child.entry.Kind != "dir" {
		return syscall.ENOTDIR
	}
	if !dir && child.entry.Kind == "dir" {
		return syscall.EISDIR
	}
	if dir {
		names, err := f.w.ListDir(p)
		if err != nil {
			return errno(err)
		}
		if len(names) != 0 {
			return syscall.ENOTEMPTY
		}
	}
	if child.entry.Kind == "file" {
		if err := child.ensureReader(); err != nil {
			return errno(err)
		}
	}
	if err := f.w.Delete(p); err != nil {
		return errno(err)
	}
	delete(f.nodes, p)
	child.path = ""
	if child.opens == 0 {
		child.dropBuffers()
	}
	n.touchDir()
	return 0
}
func (n *Node) Unlink(ctx context.Context, name string) syscall.Errno {
	n.owner.mu.Lock()
	defer n.owner.mu.Unlock()
	return n.remove(name, false)
}
func (n *Node) Rmdir(ctx context.Context, name string) syscall.Errno {
	n.owner.mu.Lock()
	defer n.owner.mu.Unlock()
	return n.remove(name, true)
}
func (n *Node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	f := n.owner
	f.mu.Lock()
	defer f.mu.Unlock()
	dstParent, ok := newParent.(*Node)
	if !ok || dstParent.owner != f {
		return syscall.EXDEV
	}
	if flags & ^uint32(1) != 0 {
		return syscall.EOPNOTSUPP
	}
	old, e := n.child(name)
	if e != 0 {
		return e
	}
	new, e := dstParent.child(newName)
	if e != 0 {
		return e
	}
	src, err := f.get(old)
	if err != nil {
		return errno(err)
	}
	dst, err := f.get(new)
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return errno(err)
	}
	if dst != nil && dst.entry.Kind == "file" {
		if err := dst.ensureReader(); err != nil {
			return errno(err)
		}
	}
	if err := f.w.RenameReplace(old, new, flags == 1); err != nil {
		return errno(err)
	}
	if old == new {
		return 0
	}
	if dst != nil {
		dst.path = ""
		if dst.opens == 0 {
			dst.dropBuffers()
		}
		delete(f.nodes, new)
	}
	moved := make(map[string]*Node)
	for p, child := range f.nodes {
		if p == old || strings.HasPrefix(p, old+"/") {
			delete(f.nodes, p)
			child.path = new + strings.TrimPrefix(p, old)
			moved[child.path] = child
		}
	}
	for p, child := range moved {
		f.nodes[p] = child
	}
	src.entry.Times.C = time.Now().UnixNano()
	src.dirty = true
	n.touchDir()
	dstParent.touchDir()
	return 0
}

var _ fs.NodeLookuper = (*Node)(nil)
var _ fs.NodeRenamer = (*Node)(nil)
var _ fs.NodeSetattrer = (*Node)(nil)
var _ fs.FileReader = (*handle)(nil)
var _ fs.FileWriter = (*handle)(nil)
var _ fs.FileFlusher = (*handle)(nil)
var _ fs.FileFsyncer = (*handle)(nil)
