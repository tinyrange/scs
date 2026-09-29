//go:build linux

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// buildReceipt is a host-supplied status, not a build-authored manifest. It must
// be written outside the sandbox mount and match this invocation's fresh token.
// It records the outer runner's status; artifacts/logs remain inside the candidate.
type buildReceipt struct {
	Version    int    `json:"version"`
	Session    string `json:"session"`
	ExitCode   *int   `json:"exit_code"`
	TimedOut   *bool  `json:"timed_out"`
	DurationMS *int64 `json:"duration_ms"`
}

func prepareBuildReceipt(name, mount string) (string, string, error) {
	// Resolve parent symlinks before checking that sandbox writes cannot directly
	// replace the host receipt. The host must keep this directory trusted/stable.
	parent, err := filepath.EvalSymlinks(filepath.Dir(name))
	if err != nil {
		return "", "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", "", err
	}
	mount, err = filepath.EvalSymlinks(mount)
	if err != nil {
		return "", "", err
	}
	mount, err = filepath.Abs(mount)
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(parent, filepath.Base(name))
	rel, err := filepath.Rel(mount, target)
	if err != nil {
		return "", "", err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("build receipt must be outside the mount")
	}
	if _, err = os.Lstat(target); err == nil {
		return "", "", errors.New("build receipt already exists; choose a fresh path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return "", "", err
	}
	return target, hex.EncodeToString(token), nil
}
func readBuildReceipt(name, session string) error {
	// Do not follow a final-component symlink. Parent directories belong to the
	// trusted host and must not be writable by sandboxed build processes.
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("read host build receipt: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	const maxReceipt = 64 << 10
	if !st.Mode().IsRegular() || st.Size() > maxReceipt {
		return errors.New("build receipt must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReceipt+1))
	if err != nil {
		return err
	}
	if len(data) > maxReceipt {
		return errors.New("build receipt too large")
	}
	var receipt buildReceipt
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&receipt); err != nil {
		return fmt.Errorf("invalid build receipt: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing build receipt data")
	}
	if receipt.Version != 1 || receipt.Session != session || receipt.ExitCode == nil || *receipt.ExitCode < -1 || *receipt.ExitCode > 255 || receipt.TimedOut == nil || receipt.DurationMS == nil || *receipt.DurationMS < 0 {
		return errors.New("invalid or stale build receipt")
	}
	if *receipt.TimedOut {
		return fmt.Errorf("sandbox runner timed out after %d ms (exit %d)", *receipt.DurationMS, *receipt.ExitCode)
	}
	if *receipt.ExitCode != 0 {
		return fmt.Errorf("sandbox runner failed with exit %d", *receipt.ExitCode)
	}
	return nil
}
