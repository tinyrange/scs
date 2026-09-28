package repo

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in real-repository integration test. It does not build or execute Linux.
// Supply a complete local clone; no network access happens during the test.
func TestLinuxWorkspace(t *testing.T) {
	clone := os.Getenv("SCS_LINUX_CLONE")
	if clone == "" {
		t.Skip("set SCS_LINUX_CLONE to a local Linux clone")
	}
	clone, err := filepath.Abs(clone)
	must(t, err)
	ctx := context.Background()
	revision := os.Getenv("SCS_LINUX_REV")
	if revision == "" {
		revision = "HEAD"
	}
	raw, err := gitOutput(ctx, clone, "rev-parse", "--verify", revision+"^{commit}")
	must(t, err)
	commit := strings.TrimSpace(string(raw))
	listing, err := gitOutput(ctx, clone, "ls-tree", "-r", "-t", "-z", "--full-tree", commit)
	must(t, err)
	entries := []gitEntry{}
	paths := []string{}
	fanout := map[string]int{}
	selected := map[string]string{}
	files := 0
	for _, record := range strings.Split(string(listing), "\x00") {
		if record == "" {
			continue
		}
		meta, p, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			t.Fatal("invalid ls-tree record")
		}
		entries = append(entries, gitEntry{fields[0], fields[2], p})
		paths = append(paths, p)
		dir := path.Dir(p)
		fanout[dir]++
		if fields[0] == "100644" || fields[0] == "100755" {
			files++
			if selected[dir] == "" {
				selected[dir] = p
			}
		}
	}
	sort.Strings(paths)
	widest := ""
	for dir, n := range fanout {
		if selected[dir] != "" && (n > fanout[widest] || n == fanout[widest] && dir < widest) {
			widest = dir
		}
	}
	target := selected[widest]
	t.Logf("commit=%s paths=%d regular_files=%d widest=%s children=%d target=%s", commit, len(paths), files, widest, fanout[widest], target)
	filename := filepath.Join(t.TempDir(), "linux.scs")
	if p := os.Getenv("SCS_LINUX_REPO"); p != "" {
		filename, err = filepath.Abs(p)
		must(t, err)
	}
	r, err := Create(filename)
	must(t, err)
	defer func() { r.Close() }()
	start := time.Now()
	w, err := r.ImportGit(ctx, clone, commit)
	must(t, err)
	t.Logf("import=%s", time.Since(start))
	start = time.Now()
	baseID, err := w.Publish("main")
	must(t, err)
	info, err := os.Stat(filename)
	must(t, err)
	t.Logf("initial_publish=%s repository_bytes=%d objects=%d", time.Since(start), info.Size(), len(r.objects))
	gotPaths := w.Paths()
	if len(gotPaths) != len(paths) {
		t.Fatalf("path count %d != %d", len(gotPaths), len(paths))
	}
	for i, p := range paths {
		if gotPaths[i] != p {
			t.Fatalf("path mismatch %q != %q", gotPaths[i], p)
		}
	}
	original, err := w.ReadFile(target)
	must(t, err)
	measure := func(label string, n int, operation func(int)) {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		st, err := os.Stat(filename)
		must(t, err)
		objects := len(r.objects)
		start := time.Now()
		for i := 0; i < n; i++ {
			operation(i)
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		end, err := os.Stat(filename)
		must(t, err)
		t.Logf("%s n=%d ns/op=%d B/op=%d allocs/op=%d appended_B/op=%d objects/op=%.2f", label, n, elapsed.Nanoseconds()/int64(n), (after.TotalAlloc-before.TotalAlloc)/uint64(n), (after.Mallocs-before.Mallocs)/uint64(n), (end.Size()-st.Size())/int64(n), float64(len(r.objects)-objects)/float64(n))
	}
	measure("clean_snapshot", 10000, func(_ int) {
		id, err := w.Snapshot()
		must(t, err)
		if id != baseID {
			t.Fatal("snapshot changed")
		}
	})
	measure("clean_fork", 10000, func(_ int) {
		f, err := w.Fork()
		must(t, err)
		if f.s.root != w.s.root {
			t.Fatal("fork copied metadata")
		}
	})
	const edits = 100
	var edited *Workspace
	measure("widest_fork_edit_snapshot", edits, func(i int) {
		f, err := w.Fork()
		must(t, err)
		// A small replacement isolates directory metadata cost from file ingestion.
		must(t, f.WriteFile(target, []byte(fmt.Sprintf("SCS Linux stress edit %d\n", i))))
		_, err = f.Snapshot()
		must(t, err)
		edited = f
	})
	readEquals(t, w, target, original)
	_, err = edited.Publish("edited")
	must(t, err)
	// Namespace and mode mutations in the widest directory, preserving parent.
	mutated, err := w.Fork()
	must(t, err)
	renamed := target + ".scs-renamed"
	must(t, mutated.Rename(target, renamed))
	must(t, mutated.Chmod(renamed, 0755))
	must(t, mutated.WriteFile(target, []byte("new file")))
	must(t, mutated.Delete(target))
	must(t, mutated.Mkdir(path.Join(widest, ".scs-empty")))
	must(t, mutated.Symlink(path.Join(widest, ".scs-link"), path.Base(renamed)))
	_, err = mutated.Publish("mutated")
	must(t, err)
	// Exercise independent retained forks as well as the allocation-only timing.
	retained := make([]*Workspace, 64)
	for i := range retained {
		retained[i], err = w.Fork()
		must(t, err)
		must(t, retained[i].WriteFile(target, []byte(strconv.Itoa(i))))
		_, err = retained[i].Snapshot()
		must(t, err)
	}
	for i, f := range retained {
		readEquals(t, f, target, []byte(strconv.Itoa(i)))
	}
	readEquals(t, w, target, original)
	must(t, r.Close())
	start = time.Now()
	r, err = Open(filename)
	must(t, err)
	t.Logf("reopen=%s", time.Since(start))
	start = time.Now()
	w, err = r.Checkout("main")
	must(t, err)
	t.Logf("checkout=%s", time.Since(start))
	// Hash every imported file/symlink against the independently obtained Git ID.
	// This verifies persisted bytes, names, modes, and link targets, not just samples.
	start = time.Now()
	var verifiedBytes int64
	for _, e := range entries {
		st, err := w.Stat(e.name)
		must(t, err)
		switch e.mode {
		case "040000":
			if st.Kind != "dir" || st.Mode != 0755 {
				t.Fatalf("directory metadata: %s", e.name)
			}
			continue
		case "100644", "100755":
			mode, err := strconv.ParseUint(e.mode[3:], 8, 32)
			must(t, err)
			if st.Kind != "file" || st.Mode != uint32(mode) {
				t.Fatalf("file metadata: %s", e.name)
			}
		case "120000":
			if st.Kind != "symlink" || st.Mode != 0777 {
				t.Fatalf("link metadata: %s", e.name)
			}
		default:
			t.Fatalf("unsupported Git mode %s", e.mode)
		}
		var data []byte
		if st.Kind == "symlink" {
			target, err := w.Readlink(e.name)
			must(t, err)
			data = []byte(target)
		} else {
			data, err = w.ReadFile(e.name)
			must(t, err)
		}
		var h hash.Hash = sha1.New()
		if len(e.oid) == 64 {
			h = sha256.New()
		}
		fmt.Fprintf(h, "blob %d%c", len(data), 0)
		h.Write(data)
		if hex.EncodeToString(h.Sum(nil)) != e.oid {
			t.Fatalf("Git content mismatch: %s", e.name)
		}
		verifiedBytes += int64(len(data))
	}
	t.Logf("full_content_verification=%s bytes=%d", time.Since(start), verifiedBytes)
	got, err := w.Snapshot()
	must(t, err)
	if got != baseID {
		t.Fatal("snapshot identity changed on reopen")
	}
	edited, err = r.Checkout("edited")
	must(t, err)
	readEquals(t, edited, target, []byte(fmt.Sprintf("SCS Linux stress edit %d\n", edits-1)))
	mutated, err = r.Checkout("mutated")
	must(t, err)
	readEquals(t, mutated, renamed, original)
	if _, err := mutated.Stat(target); err == nil {
		t.Fatal("deleted path survived")
	}
	st, err := mutated.Stat(renamed)
	must(t, err)
	if st.Mode != 0755 {
		t.Fatal("chmod lost")
	}
	names, err := mutated.ListDir(path.Join(widest, ".scs-empty"))
	must(t, err)
	if len(names) != 0 {
		t.Fatal("empty directory changed")
	}
	link, err := mutated.Readlink(path.Join(widest, ".scs-link"))
	must(t, err)
	if link != path.Base(renamed) {
		t.Fatal("link changed")
	}
	t.Log("PASS: all Git paths/content/modes, fork isolation, namespace mutations, and reopen persistence")
}
