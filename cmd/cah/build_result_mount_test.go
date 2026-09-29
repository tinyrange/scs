//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"j5.nz/scs/repo"
)

func TestMountedBuildReceiptGatesSuccessNotPersistence(t *testing.T) {
	requireMount(t)
	for _, mode := range []string{"success", "failure", "timeout", "missing", "stale"} {
		t.Run(mode, func(t *testing.T) {
			file, mount, source := fixture(t)
			result := filepath.Join(filepath.Dir(file), "host-result.json")
			after := filepath.Join(filepath.Dir(file), "inspect.star")
			mustMount(t, os.WriteFile(after, []byte(`print("INSPECTED", workspace.read_file("output"))`), 0600))
			d := startDaemon(t, []string{"-session", "-workspace", "candidate", "-build-result", result, "-after", after, file, mount}, mount)
			log := d.log.text()
			index := strings.Index(log, "session=")
			if index < 0 {
				t.Fatal("missing host session token", log)
			}
			token := strings.Fields(log[index+len("session="):])[0]
			mustMount(t, os.WriteFile(filepath.Join(mount, "output"), []byte("retained build output"), 0644))
			if mode != "missing" {
				code := 0
				if mode == "failure" {
					code = 7
				}
				if mode == "timeout" {
					code = -1
				}
				if mode == "stale" {
					token = "not-this-invocation"
				}
				timedOut, duration := mode == "timeout", int64(10)
				receipt := buildReceipt{Version: 1, Session: token, ExitCode: &code, TimedOut: &timedOut, DurationMS: &duration}
				data, err := json.Marshal(receipt)
				mustMount(t, err)
				mustMount(t, os.WriteFile(result, data, 0600))
			}
			mustMount(t, unmount(mount))
			d.wait(t, mode == "success")
			if !strings.Contains(d.log.text(), "INSPECTED retained build output") {
				t.Fatal("failed build skipped inspection", d.log.text())
			}
			expected := map[string]string{"failure": "runner failed with exit 7", "timeout": "runner timed out", "missing": "read host build receipt", "stale": "invalid or stale build receipt"}
			if mode != "success" && !strings.Contains(d.log.text(), expected[mode]) {
				t.Fatal("wrong session failure", d.log.text())
			}
			r, err := repo.OpenVerified(file)
			mustMount(t, err)
			defer r.Close()
			if r.Refs()["main"] != source || r.Refs()["sibling"] != source {
				t.Fatal("source promoted")
			}
			w, err := r.Checkout("candidate")
			mustMount(t, err)
			data, err := w.ReadFile("output")
			mustMount(t, err)
			if string(data) != "retained build output" {
				t.Fatal("build failure discarded candidate")
			}
		})
	}
}
