package repo

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// gitCommand intentionally operates on committed objects, not the checkout,
// filters, hooks, or attributes. The host chooses the clone path and revision.
func gitCommand(ctx context.Context, clone string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--no-replace-objects", "-C", clone}, args...)...)
	// Do not inherit a host GIT_DIR, index, object directory, or config override.
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	return cmd
}
func gitOutput(ctx context.Context, clone string, args ...string) ([]byte, error) {
	cmd := gitCommand(ctx, clone, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, stderr.String())
	}
	return out, nil
}

type gitEntry struct{ mode, oid, name string }

// ImportGit imports exactly one commit tree. Dirty, ignored, and untracked files
// are not inputs. No Git history, hooks, filters, LFS expansion, or submodules are
// imported. The caller explicitly publishes the returned workspace.
func (r *Repository) ImportGit(ctx context.Context, clone, revision string) (*Workspace, error) {
	if revision == "" {
		revision = "HEAD"
	}
	out, err := gitOutput(ctx, clone, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return nil, err
	}
	commit := strings.TrimSpace(string(out))
	raw, err := hex.DecodeString(commit)
	if err != nil || (len(raw) != 20 && len(raw) != 32) {
		return nil, errors.New("invalid Git commit ID")
	}
	listing, err := gitOutput(ctx, clone, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	entries := []gitEntry{}
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(record), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, errors.New("malformed git ls-tree output")
		}
		p, err := clean(name)
		if err != nil || p != name || p == "." {
			return nil, fmt.Errorf("unsupported Git path %q", name)
		}
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755" && fields[0] != "120000") {
			return nil, fmt.Errorf("unsupported Git entry %q (%s); submodules must be imported separately", name, fields[0])
		}
		oid, e := hex.DecodeString(fields[2])
		if e != nil || len(oid) != len(raw) {
			return nil, errors.New("invalid Git blob ID")
		}
		entries = append(entries, gitEntry{fields[0], fields[2], name})
	}
	w := r.Empty()
	w.s.source = commit
	cmd := gitCommand(ctx, clone, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	waited := false
	defer func() {
		stdin.Close()
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	for _, e := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		dirs := []string{}
		for p := path.Dir(e.name); p != "."; p = path.Dir(p) {
			dirs = append(dirs, p)
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			if _, ok := w.s.get(dirs[i]); !ok {
				if err = w.Mkdir(dirs[i]); err != nil {
					return nil, err
				}
			}
		}
		if _, err = fmt.Fprintln(stdin, e.oid); err != nil {
			return nil, err
		}
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != e.oid || fields[1] != "blob" {
			return nil, fmt.Errorf("unexpected cat-file response %q", header)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return nil, errors.New("invalid Git blob size")
		}
		var h hash.Hash
		if len(raw) == 20 {
			h = sha1.New()
		} else {
			h = sha256.New()
		}
		fmt.Fprintf(h, "blob %d%c", size, 0)
		limited := &io.LimitedReader{R: reader, N: size}
		input := io.TeeReader(limited, h)
		kind := "file"
		mode := uint32(0644)
		if e.mode == "100755" {
			mode = 0755
		}
		if e.mode == "120000" {
			kind = "symlink"
			mode = 0777
		}
		w.s.mu.Lock()
		err = w.write(e.name, input, kind, mode)
		w.s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if limited.N != 0 {
			return nil, io.ErrUnexpectedEOF
		}
		if hex.EncodeToString(h.Sum(nil)) != e.oid {
			return nil, fmt.Errorf("Git blob checksum mismatch: %s", e.name)
		}
		c, err := reader.ReadByte()
		if err != nil || c != '\n' {
			return nil, errors.New("missing cat-file record delimiter")
		}
	}
	stdin.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", err, stderr.String())
	}
	return w, nil
}
