//go:build linux

package cah

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"j5.nz/scs/repo"
)

func check(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func ok(t *testing.T, e syscall.Errno) {
	t.Helper()
	if e != 0 {
		t.Fatal(e)
	}
}
func setup(t *testing.T) (*FS, *repo.Repository, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.scs")
	r, e := repo.CreateOptimized(p)
	check(t, e)
	w := r.Empty()
	_, e = w.Publish("test")
	check(t, e)
	f, e := New(w, "test")
	check(t, e)
	fs.NewNodeFS(f.Root, &fs.Options{})
	t.Cleanup(func() { r.Close() })
	return f, r, p
}
func create(t *testing.T, n *Node, name string) *handle {
	t.Helper()
	_, h, _, e := n.Create(context.Background(), name, syscall.O_RDWR, 0644, &fuse.EntryOut{})
	ok(t, e)
	return h.(*handle)
}
func read(t *testing.T, h *handle) string {
	t.Helper()
	r, e := h.Read(context.Background(), make([]byte, 128), 0)
	ok(t, e)
	b, status := r.Bytes(nil)
	if status != 0 {
		t.Fatal(status)
	}
	return string(b)
}
func TestHandlesRenameTruncateAndDurability(t *testing.T) {
	f, r, p := setup(t)
	ctx := context.Background()
	a := create(t, f.Root, "a")
	b := create(t, f.Root, "b")
	_, e := a.Write(ctx, []byte("source"), 0)
	ok(t, e)
	_, e = b.Write(ctx, []byte("target"), 0)
	ok(t, e)
	ok(t, f.Root.Rename(ctx, "a", f.Root, "b", 0))
	if read(t, a) != "source" || read(t, b) != "target" {
		t.Fatal("replacement changed open inode")
	}
	_, e = b.Write(ctx, []byte("old"), 0)
	ok(t, e)
	ok(t, b.Flush(ctx))
	if read(t, a) != "source" {
		t.Fatal("unlinked write changed replacement")
	}
	in := &fuse.SetAttrIn{}
	in.Valid = fuse.FATTR_SIZE
	in.Size = 2
	ok(t, a.n.Setattr(ctx, a, in, &fuse.AttrOut{}))
	in.Size = 8
	ok(t, a.n.Setattr(ctx, a, in, &fuse.AttrOut{}))
	if read(t, a) != "so\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("truncate/extend: %q", read(t, a))
	}
	ok(t, a.Fsync(ctx, 0)) // Persist even though the descriptor remains open.
	ok(t, a.Release(ctx))
	ok(t, b.Release(ctx))
	check(t, r.Close())
	reopened, err := repo.Open(p)
	check(t, err)
	defer reopened.Close()
	w, err := reopened.Checkout("test")
	check(t, err)
	data, err := w.ReadFile("b")
	check(t, err)
	if string(data) != "so\x00\x00\x00\x00\x00\x00" {
		t.Fatal(data)
	}
	en, err := w.Stat("b")
	check(t, err)
	if en.Times.M == 0 {
		t.Fatal("mtime not durable")
	}
}
func TestDirectoryRenameOpenChildAndConcurrentWrites(t *testing.T) {
	f, _, _ := setup(t)
	ctx := context.Background()
	inode, e := f.Root.Mkdir(ctx, "d", 0755, &fuse.EntryOut{})
	ok(t, e)
	d := inode.Operations().(*Node)
	h := create(t, d, "f")
	ok(t, f.Root.Rename(ctx, "d", f.Root, "moved", 0))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := h.Write(ctx, []byte(fmt.Sprintf("%02d", i)), int64(2*i))
			if e != 0 {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	ok(t, h.Flush(ctx))
	check(t, f.Sync())
	data, err := f.w.ReadFile("moved/f")
	check(t, err)
	if string(data) != "00010203040506070809101112131415" {
		t.Fatal(string(data))
	}
	ok(t, d.Unlink(ctx, "f"))
	if read(t, h) != string(data) {
		t.Fatal("open-unlinked read")
	}
	ok(t, h.Release(ctx))
}
