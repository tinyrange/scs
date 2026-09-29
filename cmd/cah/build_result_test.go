//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestBuildReceiptValidation(t *testing.T) {
	dir := t.TempDir()
	mount := filepath.Join(dir, "mount")
	mustMount(t, os.Mkdir(mount, 0700))
	name, token, err := prepareBuildReceipt(filepath.Join(dir, "result.json"), mount)
	mustMount(t, err)
	if len(token) != 32 {
		t.Fatal(token)
	}
	valid := `{"version":1,"session":"` + token + `","exit_code":0,"timed_out":false,"duration_ms":12}`
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"success", valid, true},
		{"failure", strings.Replace(valid, `"exit_code":0`, `"exit_code":1`, 1), false},
		{"timeout", strings.Replace(valid, `"timed_out":false`, `"timed_out":true`, 1), false},
		{"stale", strings.Replace(valid, token, "other-session", 1), false},
		{"missing timeout", strings.Replace(valid, `"timed_out":false,`, "", 1), false},
		{"missing duration", strings.Replace(valid, `,"duration_ms":12`, "", 1), false},
		{"null timeout", strings.Replace(valid, `"timed_out":false`, `"timed_out":null`, 1), false},
		{"missing exit", strings.Replace(valid, `"exit_code":0,`, "", 1), false},
		{"unknown field", strings.Replace(valid, `"duration_ms":12`, `"duration_ms":12,"extra":true`, 1), false},
		{"trailing data", valid + ` {}`, false},
		{"oversized", strings.Repeat(" ", 65537), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustMount(t, os.WriteFile(name, []byte(tc.body), 0600))
			err := readBuildReceipt(name, token)
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
		})
	}
	if _, _, err = prepareBuildReceipt(name, mount); err == nil {
		t.Fatal("accepted stale receipt path")
	}
	mustMount(t, os.Remove(name))
	mustMount(t, os.Symlink(filepath.Join(dir, "missing"), name))
	if err = readBuildReceipt(name, token); err == nil {
		t.Fatal("followed receipt symlink")
	}
	mustMount(t, os.Remove(name))
	mustMount(t, syscall.Mkfifo(name, 0600))
	if err = readBuildReceipt(name, token); err == nil {
		t.Fatal("accepted nonregular receipt")
	}
	for _, inside := range []string{mount, filepath.Join(mount, "result.json")} {
		if _, _, err = prepareBuildReceipt(inside, mount); err == nil {
			t.Fatal("receipt inside mount", inside)
		}
	}
	alias := filepath.Join(dir, "alias")
	mustMount(t, os.Symlink(mount, alias))
	if _, _, err = prepareBuildReceipt(filepath.Join(alias, "receipt.json"), mount); err == nil {
		t.Fatal("receipt through mount alias")
	}
}
