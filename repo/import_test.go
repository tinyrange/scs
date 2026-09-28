package repo

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func TestGitImportCommittedTree(t *testing.T) {
	for _, algorithm := range []string{"sha1", "sha256"} {
		t.Run(algorithm, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, "init", "--object-format="+algorithm)
			files := map[string][]byte{
				"hello.txt": []byte("committed\n"), "bin": bytes.Repeat([]byte{0, 255, 42, 17}, 5000), "empty": {},
				"space tab\tline\nname": []byte("unusual name"), "run.sh": []byte("#!/bin/sh\nexit 0\n"),
			}
			for name, data := range files {
				must(t, os.WriteFile(filepath.Join(dir, name), data, 0644))
			}
			must(t, os.Chmod(filepath.Join(dir, "run.sh"), 0755))
			must(t, os.Symlink("hello.txt", filepath.Join(dir, "link")))
			git(t, dir, "add", "--all")
			git(t, dir, "commit", "-m", "initial")
			commit := git(t, dir, "rev-parse", "HEAD")
			must(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("dirty"), 0644))
			must(t, os.WriteFile(filepath.Join(dir, "untracked"), []byte("ignore me"), 0644))
			r, name := newRepo(t)
			w, err := r.ImportGit(context.Background(), dir, "HEAD")
			must(t, err)
			if w.s.source != commit {
				t.Fatal("lost source commit")
			}
			for name, want := range files {
				readEquals(t, w, name, want)
			}
			if _, err = w.Stat("untracked"); err == nil {
				t.Fatal("imported untracked file")
			}
			target, err := w.Readlink("link")
			must(t, err)
			if target != "hello.txt" {
				t.Fatal(target)
			}
			e, err := w.Stat("run.sh")
			must(t, err)
			if e.Mode != 0755 {
				t.Fatal("lost executable bit")
			}
			_, err = w.Publish("main")
			must(t, err)
			must(t, r.Close())
			r, err = Open(name)
			must(t, err)
			defer r.Close()
			w, err = r.Checkout("main")
			must(t, err)
			for name, want := range files {
				readEquals(t, w, name, want)
			}
		})
	}
}
func TestGitImportRejectsSubmoduleWithoutPublication(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init")
	git(t, dir, "commit", "--allow-empty", "-m", "base")
	id := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+id+",sub")
	git(t, dir, "commit", "-m", "gitlink")
	r, _ := newRepo(t)
	if _, err := r.ImportGit(context.Background(), dir, "HEAD"); err == nil || !strings.Contains(err.Error(), "submodules") {
		t.Fatalf("got %v", err)
	}
	if len(r.Refs()) != 0 {
		t.Fatal("failed import published roots")
	}
}
