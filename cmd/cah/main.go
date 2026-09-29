//go:build linux

// cah mounts native workspaces. Execution and sandboxing belong to the host.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"j5.nz/scs/cah"
	"j5.nz/scs/repo"
)

func run(args []string, out, errs io.Writer) error {
	flags := flag.NewFlagSet("cah", flag.ContinueOnError)
	flags.SetOutput(errs)
	name := flags.String("workspace", "cah-linux", "target workspace")
	from := flags.String("from", "main", "source workspace")
	session := flags.Bool("session", false, "new candidate: edit, mount, then read-only inspection")
	before := flags.String("before", "", "pre-mount Starlark script (session only)")
	after := flags.String("after", "", "post-unmount read-only Starlark script (session only)")
	buildResult := flags.String("build-result", "", "fresh host receipt outside mount; required runner status (session only)")
	timeout := flags.Duration("timeout", time.Minute, "per-script timeout")
	steps := flags.Uint64("steps", 10_000_000, "per-script step budget")
	budget := flags.Int64("buffer-limit", cah.DefaultBufferLimit, "aggregate overlay byte limit (not an RSS quota)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return errors.New("usage: cah [flags] repository.scs mountpoint")
	}
	if *name == *from {
		return errors.New("target workspace must differ from source")
	}
	if *budget <= 0 || *timeout <= 0 || *steps == 0 {
		return errors.New("buffer-limit, timeout, and steps must be positive")
	}
	if !*session && (*before != "" || *after != "" || *buildResult != "") {
		return errors.New("-before/-after/-build-result require -session")
	}
	var beforeSource, afterSource []byte
	var err error
	if *before != "" {
		beforeSource, err = os.ReadFile(*before)
		if err != nil {
			return err
		}
	}
	if *after != "" {
		afterSource, err = os.ReadFile(*after)
		if err != nil {
			return err
		}
	}
	var receiptPath, token string
	if *buildResult != "" {
		receiptPath, token, err = prepareBuildReceipt(*buildResult, flags.Arg(1))
		if err != nil {
			return err
		}
	}
	r, err := repo.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	defer r.Close()
	serve := func(f *cah.FS) error {
		if err := serveMount(f, flags.Arg(1), *name, token, out, errs); err != nil {
			return err
		}
		if receiptPath != "" {
			return readBuildReceipt(receiptPath, token)
		}
		return nil
	}
	opts := cah.Options{BufferLimit: *budget}
	if *session {
		err = cah.RunSession(context.Background(), r, cah.SessionOptions{
			Source: *from, Target: *name, Before: beforeSource, After: afterSource,
			Timeout: *timeout, Steps: *steps, Print: out, FS: opts,
		}, serve)
	} else {
		var w *repo.Workspace
		if _, ok := r.Refs()[*name]; ok {
			w, err = r.Checkout(*name)
		} else {
			w, err = r.Checkout(*from)
			if err == nil {
				_, err = w.Publish(*name)
			}
		}
		if err != nil {
			return err
		}
		f, e := cah.NewWithOptions(w, *name, opts)
		if e != nil {
			return e
		}
		err = serve(f)
		if syncErr := f.Sync(); syncErr != nil {
			err = errors.Join(err, fmt.Errorf("final publication: %w", syncErr))
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "cah: unmounted and published candidate (source unchanged)")
	return r.Close()
}

func serveMount(f *cah.FS, mountpoint, name, token string, out, errs io.Writer) error {
	server, err := f.Mount(mountpoint)
	if err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-signals:
				// A busy unmount leaves the session running. Later signals may retry.
				if err := server.Unmount(); err != nil {
					fmt.Fprintf(errs, "cah: unmount: %v\n", err)
				}
			case <-done:
				return
			}
		}
	}()
	if token != "" {
		fmt.Fprintf(out, "cah: ready workspace=%s mount=%s session=%s\n", name, mountpoint, token)
	} else {
		fmt.Fprintf(out, "cah: ready workspace=%s mount=%s\n", name, mountpoint)
	}
	server.Wait()
	close(done)
	<-stopped
	return nil
}
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
