//go:build linux

package cah

import (
	"bytes"
	"context"
	"math/rand"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
	"j5.nz/scs/repo"
)

func TestOverlayQuotaAtomicAndUnlinkedRelease(t *testing.T) {
	f, _, _ := setup(t)
	f.bufferLimit = 2 * repo.BlockSize
	ctx := context.Background()
	a, b := create(t, f.Root, "a"), create(t, f.Root, "b")
	_, e := a.Write(ctx, []byte("first"), 0)
	ok(t, e)
	_, e = b.Write(ctx, []byte("second"), 0)
	ok(t, e)
	before := a.n.entry
	if n, e := a.Write(ctx, bytes.Repeat([]byte("X"), repo.BlockSize), 2); e != syscall.ENOSPC || n != 0 {
		t.Fatalf("write: %d %v", n, e)
	}
	if read(t, a) != "first" || a.n.entry.Size != before.Size || a.n.entry.Times != before.Times {
		t.Fatal("rejected write changed file")
	}
	if used, _ := f.BufferUsage(); used != 2*repo.BlockSize {
		t.Fatal(used)
	}
	ok(t, b.Flush(ctx))
	if used, _ := f.BufferUsage(); used != repo.BlockSize {
		t.Fatal("flush did not release quota", used)
	}
	// Open-unlinked pages remain charged until the last handle closes.
	ok(t, f.Root.Unlink(ctx, "a"))
	if read(t, a) != "first" {
		t.Fatal("unlinked bytes lost")
	}
	if used, _ := f.BufferUsage(); used != repo.BlockSize {
		t.Fatal(used)
	}
	ok(t, a.Release(ctx))
	if used, _ := f.BufferUsage(); used != 0 {
		t.Fatal("unlinked quota leak", used)
	}
	// No-open dirty inodes must also release pages when unlinked/replaced.
	c := create(t, f.Root, "c")
	_, e = c.Write(ctx, []byte("c"), 0)
	ok(t, e)
	ok(t, c.Release(ctx)) // Direct test deliberately omits Flush.
	ok(t, f.Root.Unlink(ctx, "c"))
	if used, _ := f.BufferUsage(); used != 0 {
		t.Fatal("unopened quota leak", used)
	}
	ok(t, b.Release(ctx))
}

func TestOverlaySparseTruncateAndRandomPersistence(t *testing.T) {
	f, r, p := setup(t)
	ctx := context.Background()
	h := create(t, f.Root, "file")
	// Extending creates no dirty pages, even for a large logical file.
	check(t, h.n.resize(128<<20))
	_, e := h.Write(ctx, []byte("tail"), (128<<20)-4)
	ok(t, e)
	if used, _ := f.BufferUsage(); used != repo.BlockSize {
		t.Fatalf("small sparse write charged %d", used)
	}
	check(t, h.n.resize(0))
	reference := []byte{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		if rng.Intn(4) == 0 {
			size := rng.Intn(24000)
			in := &fuse.SetAttrIn{}
			in.Valid = fuse.FATTR_SIZE
			in.Size = uint64(size)
			ok(t, h.n.Setattr(ctx, h, in, &fuse.AttrOut{}))
			if size < len(reference) {
				reference = reference[:size]
			} else {
				reference = append(reference, make([]byte, size-len(reference))...)
			}
		} else {
			off := rng.Intn(24000)
			data := make([]byte, rng.Intn(6000)+1)
			rng.Read(data)
			_, e := h.Write(ctx, data, int64(off))
			ok(t, e)
			if off+len(data) > len(reference) {
				reference = append(reference, make([]byte, off+len(data)-len(reference))...)
			}
			copy(reference[off:], data)
		}
		if i%7 == 0 {
			ok(t, h.Flush(ctx))
		}
		got, errno := h.Read(ctx, make([]byte, len(reference)+1), 0)
		ok(t, errno)
		data, status := got.Bytes(nil)
		if status != 0 || !bytes.Equal(data, reference) {
			t.Fatalf("iteration %d content mismatch", i)
		}
	}
	ok(t, h.Fsync(ctx, 0))
	ok(t, h.Release(ctx))
	check(t, r.Close())
	r, err := repo.OpenVerified(p)
	check(t, err)
	defer r.Close()
	w, err := r.Checkout("test")
	check(t, err)
	data, err := w.ReadFile("file")
	check(t, err)
	if !bytes.Equal(data, reference) {
		t.Fatal("reopened content mismatch")
	}
}
