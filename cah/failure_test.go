//go:build linux

package cah

import (
	"context"
	"syscall"
	"testing"
)

func TestFlushFailureRetainsDirtyData(t *testing.T) {
	f, r, _ := setup(t)
	h := create(t, f.Root, "data")
	ctx := context.Background()
	_, e := h.Write(ctx, []byte("must not be discarded"), 0)
	ok(t, e)
	check(t, r.Close()) // Simulate unavailable native storage before close/flush.
	if e = h.Flush(ctx); e != syscall.EIO {
		t.Fatalf("flush error: %v", e)
	}
	if !h.n.dirty || !f.dirty {
		t.Fatal("failed flush marked data clean")
	}
	ok(t, h.Release(ctx))
	if string(h.n.pages[0][:21]) != "must not be discarded" {
		t.Fatal("release discarded failed write")
	}
}
