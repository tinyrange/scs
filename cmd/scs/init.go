package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"j5.nz/scs/repo"
)

// runInit creates a native repository without requiring a Git checkout. Create
// uses O_EXCL; neither an existing file nor a symlink destination is overwritten.
func runInit(ctx context.Context, args []string, out, errs io.Writer) (result error) {
	f := flags("init", errs)
	name := f.String("name", "main", "initial empty workspace")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return errors.New("usage: scs init [-name main] REPO")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Validate before creating anything. Keep the same publication-name grammar.
	if *name == "" || len(*name) > 255 || *name == "." || *name == ".." {
		return errors.New("invalid workspace name")
	}
	for _, c := range *name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return errors.New("invalid workspace name")
		}
	}
	r, err := repo.CreateOptimized(f.Arg(0))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, r.Close()) }()
	id, err := r.Empty().Publish(*name)
	if err != nil {
		return fmt.Errorf("initialize failed (repository retained at %s): %w", f.Arg(0), err)
	}
	if err = r.Checkpoint(); err != nil {
		return err
	}
	if err = r.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s %s\n", *name, id)
	return err
}
