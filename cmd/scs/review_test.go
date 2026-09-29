package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"j5.nz/scs/repo"
)

func TestReviewCLIAndNoClobberExport(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "r.scs")
	r, err := repo.CreateOptimized(file)
	must(t, err)
	w := r.Empty()
	must(t, w.WriteFile("f", []byte("old")))
	old, err := w.Publish("main")
	must(t, err)
	w, err = r.Fork(old)
	must(t, err)
	must(t, w.WriteFile("f", []byte("new")))
	_, err = w.Publish("candidate")
	must(t, err)
	must(t, r.Close())
	var out bytes.Buffer
	must(t, run(context.Background(), []string{"diff", "-json", file, string(old), "candidate"}, &out, io.Discard))
	var changes []repo.Change
	must(t, json.Unmarshal(out.Bytes(), &changes))
	if len(changes) != 1 || changes[0].Path != "f" || changes[0].Status != "modified" {
		t.Fatal(changes)
	}
	archive := filepath.Join(dir, "output.tar")
	must(t, run(context.Background(), []string{"export", file, "candidate", archive}, io.Discard, io.Discard))
	original, err := os.ReadFile(archive)
	must(t, err)
	tr := tar.NewReader(bytes.NewReader(original))
	h, err := tr.Next()
	must(t, err)
	data, err := io.ReadAll(tr)
	must(t, err)
	if h.Name != "f" || string(data) != "new" {
		t.Fatal(h, string(data))
	}
	if err := run(context.Background(), []string{"export", file, "main", archive}, io.Discard, io.Discard); err == nil {
		t.Fatal("overwrote export")
	}
	data, err = os.ReadFile(archive)
	must(t, err)
	if !bytes.Equal(data, original) {
		t.Fatal("existing archive changed")
	}
	link := filepath.Join(dir, "link.tar")
	must(t, os.Symlink(archive, link))
	if err := run(context.Background(), []string{"export", file, "main", link}, io.Discard, io.Discard); err == nil {
		t.Fatal("followed symlink output")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".scs-export-*"))
	must(t, err)
	if len(leftovers) != 0 {
		t.Fatal("temporary archives leaked", leftovers)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := filepath.Join(dir, "cancelled.tar")
	if err := run(ctx, []string{"export", file, "main", cancelled}, io.Discard, io.Discard); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := os.Stat(cancelled); !os.IsNotExist(err) {
		t.Fatal("published cancelled export")
	}
}
