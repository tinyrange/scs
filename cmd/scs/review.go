package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"j5.nz/scs/repo"
)

func reviewWorkspace(r *repo.Repository, ref string) (*repo.Workspace, error) {
	if id, ok := r.Refs()[ref]; ok {
		return r.Fork(id)
	}
	return r.Fork(repo.ID(ref))
}
func runReview(ctx context.Context, args []string, out, errs io.Writer) error {
	f := flags(args[0], errs)
	asJSON := f.Bool("json", false, "machine-readable diff")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 3 {
		return fmt.Errorf("usage: scs diff [-json] REPO BEFORE AFTER, or scs export REPO REF OUTPUT.tar")
	}
	if args[0] == "export" && *asJSON {
		return errors.New("-json is only valid for diff")
	}
	r, err := repo.Open(f.Arg(0))
	if err != nil {
		return err
	}
	defer r.Close()
	before, err := reviewWorkspace(r, f.Arg(1))
	if err != nil {
		return err
	}
	if args[0] == "diff" {
		after, err := reviewWorkspace(r, f.Arg(2))
		if err != nil {
			return err
		}
		changes, err := repo.Diff(ctx, before, after)
		if err != nil {
			return err
		}
		if *asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(changes)
		}
		for _, c := range changes {
			if _, err := fmt.Fprintf(out, "%s\t%q\n", c.Status, c.Path); err != nil {
				return err
			}
		}
		return nil
	}
	// Publish the finished archive with a no-clobber hard link. Never follow or
	// replace an existing destination, and never expose partially written output.
	dest := f.Arg(2)
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".scs-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err = repo.ExportTar(ctx, before, tmp); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), dest); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "exported %s to %s\n", f.Arg(1), dest)
	return err
}
