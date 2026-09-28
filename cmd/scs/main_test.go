package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"j5.nz/scs/repo"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestCLIEndToEndAgainstDirectory(t *testing.T) {
	dir := t.TempDir()
	clone := filepath.Join(dir, "clone")
	must(t, os.Mkdir(clone, 0755))
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", clone}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
	}
	git("init")
	must(t, os.WriteFile(filepath.Join(clone, "README"), []byte("base\n"), 0644))
	must(t, os.WriteFile(filepath.Join(clone, "remove"), []byte("bye"), 0644))
	git("add", ".")
	git("commit", "-m", "base")
	name := filepath.Join(dir, "project.scs")
	var output bytes.Buffer
	cli := func(args ...string) { t.Helper(); must(t, run(context.Background(), args, &output, &output)) }
	cli("import", clone, name)
	scriptFile := filepath.Join(dir, "edit.star")
	source := `workspace.replace("README", "base", "updated")
workspace.mkdir("new")
workspace.write_file("new/a", "new content\n")
workspace.rename("new/a", "new/b")
workspace.delete("remove")
workspace.chmod("new/b", 0o755)
`
	must(t, os.WriteFile(scriptFile, []byte(source), 0644))
	cli("run", "-publish", name, scriptFile)
	// Apply the same operations to an ordinary-directory reference, then compare
	// every resulting path, type, byte, and permission against the reopened tree.
	reference := filepath.Join(dir, "reference")
	must(t, os.Mkdir(reference, 0755))
	must(t, os.WriteFile(filepath.Join(reference, "README"), []byte("base\n"), 0644))
	must(t, os.WriteFile(filepath.Join(reference, "remove"), []byte("bye"), 0644))
	must(t, os.WriteFile(filepath.Join(reference, "README"), []byte("updated\n"), 0644))
	must(t, os.Mkdir(filepath.Join(reference, "new"), 0755))
	must(t, os.WriteFile(filepath.Join(reference, "new/a"), []byte("new content\n"), 0644))
	must(t, os.Rename(filepath.Join(reference, "new/a"), filepath.Join(reference, "new/b")))
	must(t, os.Remove(filepath.Join(reference, "remove")))
	must(t, os.Chmod(filepath.Join(reference, "new/b"), 0755))
	must(t, os.Chmod(filepath.Join(reference, "README"), 0644))
	must(t, os.Chmod(filepath.Join(reference, "new"), 0755))
	r, err := repo.Open(name)
	must(t, err)
	w, err := r.Checkout("main")
	must(t, err)
	expected := []string{}
	must(t, filepath.WalkDir(reference, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == reference {
			return nil
		}
		rel, err := filepath.Rel(reference, p)
		if err != nil {
			return err
		}
		expected = append(expected, rel)
		e, err := w.Stat(rel)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if uint32(info.Mode().Perm()) != e.Mode {
			t.Fatalf("mode mismatch for %s", rel)
		}
		if d.IsDir() {
			if e.Kind != "dir" {
				t.Fatalf("not dir: %s", rel)
			}
		} else {
			want, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			got, err := w.ReadFile(rel)
			if err != nil {
				return err
			}
			if !bytes.Equal(want, got) {
				t.Fatalf("content mismatch: %s", rel)
			}
		}
		return nil
	}))
	if !reflect.DeepEqual(expected, w.Paths()) {
		t.Fatalf("paths %v != %v", expected, w.Paths())
	}
	must(t, r.Close())
	cli("fork", name, "main", "agent-a")
	cli("fork", name, "main", "agent-b")
	for _, agent := range []string{"agent-a", "agent-b"} {
		must(t, os.WriteFile(scriptFile, []byte(`workspace.write_file("README", "`+agent+`")`), 0644))
		cli("run", "-workspace", agent, "-publish", name, scriptFile)
	}
	cli("check", name)
	cli("refs", name)
	if !strings.Contains(output.String(), "ok: 3 published workspaces") {
		t.Fatal(output.String())
	}
	// Failure must not perform CLI auto-publication.
	must(t, os.WriteFile(scriptFile, []byte("workspace.write_file('README', 'bad')\nfail('stop')"), 0644))
	if err := run(context.Background(), []string{"run", "-publish", name, scriptFile}, &output, &output); err == nil {
		t.Fatal("failure accepted")
	}
	r, err = repo.Open(name)
	must(t, err)
	defer r.Close()
	w, err = r.Checkout("main")
	must(t, err)
	got, err := w.ReadFile("README")
	must(t, err)
	if string(got) != "updated\n" {
		t.Fatal("failed script published")
	}
	for _, agent := range []string{"agent-a", "agent-b"} {
		w, err = r.Checkout(agent)
		must(t, err)
		got, err = w.ReadFile("README")
		must(t, err)
		if string(got) != agent {
			t.Fatal("fork isolation lost")
		}
	}
}
