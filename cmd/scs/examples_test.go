package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"j5.nz/scs/repo"
)

func TestBundledExamplesAndDropRecovery(t *testing.T) {
	name := filepath.Join(t.TempDir(), "example.scs")
	r, err := repo.Create(name)
	must(t, err)
	_, err = r.Empty().Publish("main")
	must(t, err)
	must(t, r.Close())
	var output bytes.Buffer
	cli := func(args ...string) { t.Helper(); must(t, run(context.Background(), args, &output, &output)) }
	cli("run", "-readonly", name, filepath.Join("..", "..", "examples", "inspect.star"))
	cli("run", name, filepath.Join("..", "..", "examples", "agents.star"))
	r, err = repo.Open(name)
	must(t, err)
	main, err := r.Checkout("main")
	must(t, err)
	if len(main.Paths()) != 0 {
		t.Fatal("example changed main")
	}
	id := r.Refs()["agent-a"]
	must(t, r.Close())
	cli("drop", name, "agent-a")
	cli("fork", "-snapshot", string(id), name, "recovered")
	r, err = repo.Open(name)
	must(t, err)
	defer r.Close()
	if _, ok := r.Refs()["agent-a"]; ok {
		t.Fatal("drop failed")
	}
	w, err := r.Checkout("recovered")
	must(t, err)
	got, err := w.ReadFile("scs-agent-example/result.txt")
	must(t, err)
	if string(got) != "Result from agent A\n" {
		t.Fatalf("recovered example: %q", got)
	}
	entries, err := os.ReadDir(filepath.Dir(name))
	must(t, err)
	if len(entries) != 1 {
		t.Fatal("repository has sidecars")
	}
}
