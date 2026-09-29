// scs imports a committed Git tree and runs workspace scripts against a single
// repository file. cah supplies FUSE; process isolation belongs to the host.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"time"

	"go.starlark.net/starlark"
	"j5.nz/scs/repo"
	"j5.nz/scs/script"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var eval *starlark.EvalError
		if errors.As(err, &eval) {
			fmt.Fprintln(os.Stderr, eval.Backtrace())
		} else {
			fmt.Fprintln(os.Stderr, "scs:", err)
		}
		os.Exit(1)
	}
}

const usage = `usage:
  scs init [-name main] REPO
  scs clone [-name git] [-max-native-bytes 549755813888] URL REPO
  scs git-info [-catalog git] REPO
  scs git-cat [-catalog git] REPO REVISION
  scs git-parents [-catalog git] REPO REVISION
  scs git-checkout [-catalog git] REPO REVISION WORKSPACE
  scs import [-rev HEAD] [-name main] CLONE REPO
  scs run [-workspace main] [-readonly] [-publish] [-timeout 1m] [-steps 10000000] REPO SCRIPT.star
  scs diff [-json] REPO BEFORE AFTER
  scs export REPO REF OUTPUT.tar
  scs refs REPO
  scs fork REPO SOURCE_NAME NEW_NAME
  scs fork -snapshot ID REPO NEW_NAME
  scs check REPO
  scs checkpoint REPO
  scs drop REPO NAME

Scripts must publish explicitly, or use run -publish to publish the selected
workspace after successful execution. Failed scripts never auto-publish.
Flags precede positional arguments. Repositories are exclusively locked.
`

func flags(name string, out io.Writer) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(out)
	return f
}
func run(ctx context.Context, args []string, out, errs io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(errs, usage)
		return errors.New("command required")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	case "clone", "git-info", "git-cat", "git-checkout", "git-parents", "checkpoint":
		return runGit(ctx, args, out, errs)
	case "init":
		return runInit(ctx, args[1:], out, errs)
	case "diff", "export":
		return runReview(ctx, args, out, errs)
	case "import":
		f := flags("import", errs)
		rev := f.String("rev", "HEAD", "commit to import")
		name := f.String("name", "main", "initial workspace name")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 2 {
			return errors.New("usage: scs import [-rev HEAD] [-name main] CLONE REPO")
		}
		r, err := repo.Create(f.Arg(1))
		if err != nil {
			return err
		}
		defer r.Close()
		w, err := r.ImportGit(ctx, f.Arg(0), *rev)
		if err != nil {
			return fmt.Errorf("import failed (empty repository retained at %s): %w", f.Arg(1), err)
		}
		id, err := w.Publish(*name)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", *name, id)
		return nil
	case "run":
		f := flags("run", errs)
		name := f.String("workspace", "main", "named workspace")
		readonly := f.Bool("readonly", false, "deny mutations")
		publish := f.Bool("publish", false, "publish selected workspace on success")
		timeout := f.Duration("timeout", time.Minute, "script timeout")
		steps := f.Uint64("steps", 10_000_000, "Starlark step budget")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 2 {
			return errors.New("usage: scs run [flags] REPO SCRIPT.star")
		}
		if *readonly && *publish {
			return errors.New("-readonly and -publish cannot be combined")
		}
		if *timeout <= 0 || *steps == 0 {
			return errors.New("timeout and steps must be positive")
		}
		source, err := os.ReadFile(f.Arg(1))
		if err != nil {
			return err
		}
		r, err := repo.Open(f.Arg(0))
		if err != nil {
			return err
		}
		defer r.Close()
		w, err := r.Checkout(*name)
		if err != nil {
			return err
		}
		if *readonly {
			w = w.Readonly()
		}
		scriptCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		if err = script.Run(scriptCtx, w, f.Arg(1), source, script.Options{MaxSteps: *steps, Print: out}); err != nil {
			return err
		}
		if *publish {
			if err = scriptCtx.Err(); err != nil {
				return err
			}
			id, err := w.Publish(*name)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", *name, id)
		}
		return nil
	case "drop":
		if len(args) != 3 {
			return errors.New("usage: scs drop REPO NAME")
		}
		r, err := repo.Open(args[1])
		if err != nil {
			return err
		}
		defer r.Close()
		id, ok := r.Refs()[args[2]]
		if !ok {
			return errors.New("workspace not found")
		}
		if err := r.Drop(args[2], id); err != nil {
			return err
		}
		fmt.Fprintf(out, "dropped %s (recoverable snapshot %s)\n", args[2], id)
		return nil
	case "refs", "check":
		if len(args) != 2 {
			return fmt.Errorf("usage: scs %s REPO", args[0])
		}
		open := repo.Open
		if args[0] == "check" {
			open = repo.OpenVerified
		}
		r, err := open(args[1])
		if err != nil {
			return err
		}
		defer r.Close()
		refs := r.Refs()
		names := make([]string, 0, len(refs))
		for n := range refs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if args[0] == "check" {
				if _, err := r.Checkout(n); err != nil {
					return fmt.Errorf("%s: %w", n, err)
				}
			} else {
				fmt.Fprintf(out, "%s %s\n", n, refs[n])
			}
		}
		if args[0] == "check" {
			fmt.Fprintf(out, "ok: %d published workspaces\n", len(refs))
		}
		return nil
	case "fork":
		f := flags("fork", errs)
		snapshot := f.String("snapshot", "", "fork an immutable snapshot ID")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if (*snapshot == "" && f.NArg() != 3) || (*snapshot != "" && f.NArg() != 2) {
			return errors.New("usage: scs fork REPO SOURCE NEW, or scs fork -snapshot ID REPO NEW")
		}
		r, err := repo.Open(f.Arg(0))
		if err != nil {
			return err
		}
		defer r.Close()
		id := repo.ID(*snapshot)
		name := f.Arg(1)
		if *snapshot == "" {
			var ok bool
			id, ok = r.Refs()[f.Arg(1)]
			if !ok {
				return errors.New("source workspace not found")
			}
			name = f.Arg(2)
		}
		w, err := r.Fork(id)
		if err != nil {
			return err
		}
		id, err = w.Publish(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", name, id)
		return nil
	default:
		fmt.Fprint(errs, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}
