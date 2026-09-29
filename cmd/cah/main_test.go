//go:build linux

package main

import (
	"io"
	"testing"
)

func TestCLIRejectsUnsafeSessionOptionsBeforeOpening(t *testing.T) {
	for _, args := range [][]string{
		{"-workspace", "main", "missing.scs", "mount"},
		{"-before", "edit.star", "missing.scs", "mount"},
		{"-buffer-limit", "0", "missing.scs", "mount"},
		{"-timeout", "0", "missing.scs", "mount"},
		{"-steps", "0", "missing.scs", "mount"},
	} {
		if err := run(args, io.Discard, io.Discard); err == nil {
			t.Fatal("accepted invalid options", args)
		}
	}
}
