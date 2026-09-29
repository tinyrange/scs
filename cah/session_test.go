//go:build linux

package cah

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"j5.nz/scs/repo"
)

func TestSessionEditBuildInspectAndFailureIsolation(t *testing.T) {
	for _, buildFails := range []bool{false, true} {
		t.Run(fmt.Sprint(buildFails), func(t *testing.T) {
			_, r, p := setup(t)
			w, err := r.Checkout("test")
			check(t, err)
			check(t, w.WriteFile("input", []byte("original")))
			source, err := w.Publish("test")
			check(t, err)
			sibling, err := r.Fork(source)
			check(t, err)
			_, err = sibling.Publish("sibling")
			check(t, err)
			var printed bytes.Buffer
			buildErr := errors.New("build failed")
			err = RunSession(context.Background(), r, SessionOptions{
				Source: "test", Target: "candidate", Print: &printed,
				Before: []byte(`workspace.write_file("input", "edited")`),
				After:  []byte(`print(workspace.read_file("output"))`),
			}, func(f *FS) error {
				fs.NewNodeFS(f.Root, &fs.Options{})
				n, e := f.get("input")
				check(t, e)
				fh, _, eno := n.Open(context.Background(), syscall.O_RDONLY)
				ok(t, eno)
				if read(t, fh.(*handle)) != "edited" {
					t.Fatal("API edits not visible")
				}
				ok(t, fh.(*handle).Release(context.Background()))
				h := create(t, f.Root, "temporary")
				_, eno = h.Write(context.Background(), []byte("build output"), 0)
				ok(t, eno)
				ok(t, f.Root.Rename(context.Background(), "temporary", f.Root, "output", 0))
				ok(t, h.Flush(context.Background()))
				ok(t, h.Release(context.Background()))
				if buildFails {
					return buildErr
				}
				return nil
			})
			if buildFails {
				if !errors.Is(err, buildErr) {
					t.Fatal(err)
				}
			} else {
				check(t, err)
			}
			if !strings.Contains(printed.String(), "build output") {
				t.Fatal("inspection did not run", printed.String())
			}
			if r.Refs()["test"] != source || r.Refs()["sibling"] != source {
				t.Fatal("source/sibling promoted")
			}
			check(t, r.Close())
			r, err = repo.OpenVerified(p)
			check(t, err)
			defer r.Close()
			w, err = r.Checkout("candidate")
			check(t, err)
			data, err := w.ReadFile("output")
			check(t, err)
			if string(data) != "build output" {
				t.Fatal("candidate not durable")
			}
		})
	}
}
func TestSessionDeniesPublicationAndReadWriteInspection(t *testing.T) {
	for _, source := range []string{
		`workspace.publish("escaped")`,
		`workspace.fork().publish("escaped")`,
		`workspace.readonly().publish("escaped")`,
		`workspace.snapshot()`,
		`workspace.write_file("bad", "bad"); fail("stop")`,
	} {
		t.Run(source, func(t *testing.T) {
			_, r, _ := setup(t)
			base := r.Refs()["test"]
			called := false
			err := RunSession(context.Background(), r, SessionOptions{Source: "test", Target: "candidate", Before: []byte(source)}, func(*FS) error { called = true; return nil })
			if err == nil || called || r.Refs()["candidate"] != base || len(r.Refs()) != 2 {
				t.Fatal("failed edit escaped session", err, r.Refs())
			}
		})
	}
	_, r, _ := setup(t)
	err := RunSession(context.Background(), r, SessionOptions{Source: "test", Target: "candidate", After: []byte(`workspace.write_file("bad", "bad")`)}, func(*FS) error { return nil })
	if err == nil {
		t.Fatal("writable inspection")
	}
	err = RunSession(context.Background(), r, SessionOptions{Source: "test", Target: "candidate"}, func(*FS) error { t.Fatal("reused candidate"); return nil })
	if err == nil {
		t.Fatal("accepted existing candidate")
	}
}

func TestPublicationConflictRetainsRetryableWorkspace(t *testing.T) {
	f, r, _ := setup(t)
	ctx := context.Background()
	h := create(t, f.Root, "pending")
	_, eno := h.Write(ctx, []byte("pending"), 0)
	ok(t, eno)
	other, err := r.Checkout("test")
	check(t, err)
	original := r.Refs()["test"]
	check(t, other.WriteFile("other", []byte("other")))
	_, err = other.Publish("test")
	check(t, err)
	if err = f.Sync(); !errors.Is(err, repo.ErrConflict) {
		t.Fatal(err)
	}
	if !f.dirty {
		t.Fatal("failed publication marked clean")
	}
	data, err := f.w.ReadFile("pending")
	check(t, err)
	if string(data) != "pending" {
		t.Fatal("failed publication discarded bytes")
	}
	// Restore expected CAS root through an independent checkout. A retry must
	// publish the retained native workspace even though overlays already flushed.
	check(t, other.Delete("other"))
	_, err = other.Publish("test")
	check(t, err)
	if r.Refs()["test"] != original {
		t.Fatal("test did not restore expected root")
	}
	check(t, f.Sync())
	ok(t, h.Release(ctx))
}
