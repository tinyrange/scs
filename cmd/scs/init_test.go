package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"j5.nz/scs/repo"
)

func TestNativeInitAndScriptWithoutGit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "new.scs")
	var output bytes.Buffer
	must(t, run(context.Background(), []string{"init", "-name", "base", file}, &output, io.Discard))
	r, err := repo.OpenVerified(file)
	must(t, err)
	if len(r.Refs()) != 1 || r.Refs()["base"] == "" {
		t.Fatal(r.Refs())
	}
	w, err := r.Checkout("base")
	must(t, err)
	paths, err := w.PathsWithError()
	must(t, err)
	if len(paths) != 0 {
		t.Fatal(paths)
	}
	must(t, r.Close())
	before, err := os.ReadFile(file)
	must(t, err)
	if !bytes.HasPrefix(before, []byte("SCSREPO3")) {
		t.Fatal("not optimized format")
	}
	for _, dest := range []string{file, filepath.Join(dir, "alias.scs")} {
		if dest != file {
			must(t, os.Symlink(file, dest))
		}
		if err = run(context.Background(), []string{"init", dest}, io.Discard, io.Discard); err == nil {
			t.Fatal("overwrote existing path")
		}
	}
	after, err := os.ReadFile(file)
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("existing repository changed")
	}
	edit := filepath.Join(dir, "edit.star")
	must(t, os.WriteFile(edit, []byte(`workspace.write_file("f", "native")`), 0600))
	must(t, run(context.Background(), []string{"run", "-workspace", "base", "-publish", file, edit}, io.Discard, io.Discard))
	r, err = repo.Open(file)
	must(t, err)
	defer r.Close()
	w, err = r.Checkout("base")
	must(t, err)
	data, err := w.ReadFile("f")
	must(t, err)
	if string(data) != "native" {
		t.Fatal(string(data))
	}
	if !strings.HasPrefix(output.String(), "base ") {
		t.Fatal(output.String())
	}
}
func TestInitRejectsInvalidNameBeforeCreating(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "with space", strings.Repeat("x", 256)} {
		dest := filepath.Join(t.TempDir(), "new.scs")
		if err := run(context.Background(), []string{"init", "-name", name, dest}, io.Discard, io.Discard); err == nil {
			t.Fatal("accepted invalid name", name)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("created invalid repository", err)
		}
	}
}
