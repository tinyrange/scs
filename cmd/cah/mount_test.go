//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"j5.nz/scs/repo"
)

// This helper executes only this test binary against disposable test fixtures.
// It is NOT a sandbox or a way to run imported/untrusted build commands.
func TestCahDaemon(t *testing.T) {
	if os.Getenv("SCS_CAH_HELPER") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("SCS_CAH_ARGS")), &args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}
func requireMount(t *testing.T) {
	t.Helper()
	if os.Getenv("SCS_TEST_FUSE") != "1" {
		t.Skip("set SCS_TEST_FUSE=1 for disposable mounted integration tests")
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("fusermount3"); err != nil {
		t.Fatal(err)
	}
}
func mustMount(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type lockedLog struct {
	sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.buf.Write(p)
}
func (b *lockedLog) text() string { b.Lock(); defer b.Unlock(); return b.buf.String() }

type daemon struct {
	cmd   *exec.Cmd
	done  chan struct{}
	err   error
	log   lockedLog
	mount string
}

func unmount(mount string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fusermount3", "-u", mount).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ordinary unmount: %w: %s", err, out)
	}
	return nil
}
func startDaemon(t *testing.T, args []string, mount string) *daemon {
	t.Helper()
	binary, err := os.Executable()
	mustMount(t, err)
	d := &daemon{cmd: exec.Command(binary, "-test.run=^TestCahDaemon$"), done: make(chan struct{}), mount: mount}
	encoded, err := json.Marshal(args)
	mustMount(t, err)
	d.cmd.Env = append(os.Environ(), "SCS_CAH_HELPER=1", "SCS_CAH_ARGS="+string(encoded))
	stdoutR, stdoutW, err := os.Pipe()
	mustMount(t, err)
	d.cmd.Stdout = stdoutW
	d.cmd.Stderr = &d.log
	err = d.cmd.Start()
	stdoutW.Close()
	if err != nil {
		stdoutR.Close()
		t.Fatal(err)
	}
	ready := make(chan struct{})
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		defer stdoutR.Close()
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(&d.log, line)
			if strings.HasPrefix(line, "cah: ready ") {
				close(ready)
			}
		}
	}()
	go func() { d.err = d.cmd.Wait(); <-scanned; close(d.done) }()
	t.Cleanup(func() {
		select {
		case <-d.done:
		default:
			_ = unmount(mount)
			select {
			case <-d.done:
			case <-time.After(3 * time.Second):
				_ = d.cmd.Process.Kill()
				<-d.done
			}
		}
		// After a killed daemon the disconnected mount still needs ordinary detach.
		if _, err := os.Stat(filepath.Join(mount, "input")); err != nil && !os.IsNotExist(err) {
			if err := unmount(mount); err != nil {
				t.Error(err)
			}
		}
	})
	select {
	case <-ready:
		return d
	case <-d.done:
		t.Fatalf("daemon failed before readiness: %v %s", d.err, d.log.text())
	case <-time.After(15 * time.Second):
		t.Fatal("mount readiness timeout")
	}
	return nil
}
func (d *daemon) wait(t *testing.T, success bool) {
	t.Helper()
	select {
	case <-d.done:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon shutdown timeout")
	}
	if (d.err == nil) != success {
		t.Fatalf("daemon exit %v: %s", d.err, d.log.text())
	}
}
func fixture(t *testing.T) (file, mount string, source repo.ID) {
	t.Helper()
	dir := t.TempDir()
	file = filepath.Join(dir, "r.scs")
	mount = filepath.Join(dir, "mount")
	mustMount(t, os.Mkdir(mount, 0700))
	r, err := repo.CreateOptimized(file)
	mustMount(t, err)
	w := r.Empty()
	mustMount(t, w.WriteFile("input", []byte("original")))
	source, err = w.Publish("main")
	mustMount(t, err)
	_, err = w.Publish("sibling")
	mustMount(t, err)
	mustMount(t, r.Checkpoint())
	mustMount(t, r.Close())
	return
}

func TestMountedDurability(t *testing.T) {
	requireMount(t)
	for _, mode := range []string{"file-fsync", "directory-fsync", "sync-write", "unsynced", "orderly"} {
		t.Run(mode, func(t *testing.T) {
			file, mount, source := fixture(t)
			d := startDaemon(t, []string{"-session", "-workspace", "candidate", file, mount}, mount)
			flags := os.O_RDWR | os.O_CREATE
			if mode == "sync-write" {
				flags |= syscall.O_SYNC
			}
			h, err := os.OpenFile(filepath.Join(mount, "output"), flags, 0644)
			mustMount(t, err)
			_, err = h.Write([]byte("complete output"))
			mustMount(t, err)
			if mode == "file-fsync" {
				mustMount(t, h.Sync())
			}
			if mode == "directory-fsync" {
				dir, err := os.Open(mount)
				mustMount(t, err)
				mustMount(t, dir.Sync())
				mustMount(t, dir.Close())
			}
			mustMount(t, h.Close())
			if mode == "orderly" {
				mustMount(t, unmount(mount))
				d.wait(t, true)
			} else {
				mustMount(t, d.cmd.Process.Kill())
				d.wait(t, false)
				mustMount(t, unmount(mount))
			}
			r, err := repo.OpenVerified(file)
			mustMount(t, err)
			defer r.Close()
			if r.Refs()["main"] != source || r.Refs()["sibling"] != source {
				t.Fatal("promoted source")
			}
			w, err := r.Checkout("candidate")
			mustMount(t, err)
			if _, err := w.Stat("output"); mode == "unsynced" && errors.Is(err, os.ErrNotExist) {
				return
			}
			data, err := w.ReadFile("output")
			mustMount(t, err)
			if string(data) != "complete output" {
				t.Fatalf("partial output: %q", data)
			}
		})
	}
}

func TestMountedSessionAndBusyShutdown(t *testing.T) {
	requireMount(t)
	file, mount, source := fixture(t)
	before, after := filepath.Join(filepath.Dir(file), "before.star"), filepath.Join(filepath.Dir(file), "after.star")
	mustMount(t, os.WriteFile(before, []byte(`workspace.write_file("input", "edited")`), 0600))
	mustMount(t, os.WriteFile(after, []byte(`print("INSPECTED", workspace.read_file("output"))`), 0600))
	d := startDaemon(t, []string{"-session", "-workspace", "candidate", "-before", before, "-after", after, file, mount}, mount)
	input, err := os.ReadFile(filepath.Join(mount, "input"))
	mustMount(t, err)
	if string(input) != "edited" {
		t.Fatal("edit not mounted")
	}
	// A deterministic trusted fixture stands in for a build's filesystem I/O.
	mustMount(t, os.WriteFile(filepath.Join(mount, "temporary"), append(input, []byte(" output")...), 0755))
	mustMount(t, os.Rename(filepath.Join(mount, "temporary"), filepath.Join(mount, "output")))
	old, err := os.Open(filepath.Join(mount, "output"))
	mustMount(t, err)
	defer old.Close()
	mustMount(t, os.WriteFile(filepath.Join(mount, "replacement"), []byte("replacement"), 0644))
	mustMount(t, os.Rename(filepath.Join(mount, "replacement"), filepath.Join(mount, "output")))
	data, err := io.ReadAll(old)
	mustMount(t, err)
	if string(data) != "edited output" {
		t.Fatal("replaced open handle changed")
	}
	mustMount(t, d.cmd.Process.Signal(syscall.SIGTERM))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(d.log.text(), "cah: unmount:") && time.Now().Before(deadline) {
		select {
		case <-d.done:
			t.Fatal("busy mount exited", d.log.text())
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !strings.Contains(d.log.text(), "cah: unmount:") {
		t.Fatal("busy unmount not reported", d.log.text())
	}
	mustMount(t, old.Close())
	mustMount(t, d.cmd.Process.Signal(syscall.SIGTERM))
	d.wait(t, true)
	if !strings.Contains(d.log.text(), "INSPECTED replacement") {
		t.Fatal("inspection failed", d.log.text())
	}
	r, err := repo.OpenVerified(file)
	mustMount(t, err)
	defer r.Close()
	if r.Refs()["main"] != source {
		t.Fatal("source changed")
	}
	w, err := r.Checkout("candidate")
	mustMount(t, err)
	data, err = w.ReadFile("output")
	mustMount(t, err)
	if string(data) != "replacement" {
		t.Fatal("candidate not persisted")
	}
}
