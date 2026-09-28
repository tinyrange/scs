package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"j5.nz/scs/gitstore"
	"j5.nz/scs/repo"
)

func runGit(ctx context.Context, args []string, out, errs io.Writer) error {
	f := flags(args[0], errs)
	if args[0] == "clone" {
		name := f.String("name", "git", "Git catalog name")
		limit := f.Int64("max-native-bytes", 512<<30, "native file size guard")
		temp := f.String("temp-dir", "", "external transport scratch directory")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 2 {
			return errors.New("usage: scs clone [flags] URL REPO")
		}
		r, err := repo.CreateOptimized(f.Arg(1))
		if err != nil {
			return err
		}
		defer r.Close()
		report, err := gitstore.Clone(ctx, r, f.Arg(0), gitstore.Options{Name: *name, MaxNativeBytes: *limit, TempDir: *temp, Progress: errs})
		if err != nil {
			return fmt.Errorf("clone incomplete; unpublished native objects retained at %s: %w", f.Arg(1), err)
		}
		return json.NewEncoder(out).Encode(report)
	}
	catalog := f.String("catalog", "git", "Git catalog name")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	want := 2
	if args[0] == "git-info" || args[0] == "checkpoint" {
		want = 1
	}
	if args[0] == "git-checkout" {
		want = 3
	}
	if f.NArg() != want {
		return errors.New("incorrect arguments; see scs help")
	}
	r, err := repo.Open(f.Arg(0))
	if err != nil {
		return err
	}
	defer r.Close()
	if args[0] == "checkpoint" {
		return r.Checkpoint()
	}
	if args[0] == "git-info" {
		c, err := r.GitCatalog(*catalog)
		if err != nil {
			return err
		}
		s, err := r.StorageStats()
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Catalog repo.GitCatalog
			Objects repo.GitStats
			Storage repo.StorageStats
		}{c, r.GitStatistics(), s})
	}
	if args[0] == "git-checkout" {
		w, err := r.CheckoutGit(ctx, *catalog, f.Arg(1))
		if err != nil {
			return err
		}
		id, err := w.Publish(f.Arg(2))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s %s\n", f.Arg(2), id)
		return err
	}
	id, err := r.ResolveGit(*catalog, f.Arg(1))
	if err != nil {
		return err
	}
	if args[0] == "git-parents" {
		id, _, err = r.PeelGit(id)
		if err != nil {
			return err
		}
		parents, err := r.GitCommitParents(id)
		if err != nil {
			return err
		}
		for _, p := range parents {
			if _, err = fmt.Fprintln(out, p.String()); err != nil {
				return err
			}
		}
		return nil
	}
	body, err := r.OpenGitObject(id)
	if err != nil {
		return err
	}
	defer body.Close()
	_, err = io.Copy(out, body)
	return err
}
