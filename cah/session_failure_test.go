//go:build linux

package cah

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
)

func TestSessionFinalStorageFailureIsNotSuccess(t *testing.T) {
	_, r, _ := setup(t)
	var printed bytes.Buffer
	err := RunSession(context.Background(), r, SessionOptions{
		Source: "test", Target: "candidate", After: []byte(`print("must not inspect failed publication")`), Print: &printed,
	}, func(f *FS) error {
		fs.NewNodeFS(f.Root, &fs.Options{})
		h := create(t, f.Root, "output")
		_, e := h.Write(context.Background(), []byte("pending"), 0)
		ok(t, e)
		// Direct failure injection: storage disappears after the serving phase.
		check(t, r.Close())
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "final publication") || printed.Len() != 0 {
		t.Fatal(err, printed.String())
	}
}
