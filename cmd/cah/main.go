//go:build linux

// cah mounts native SCS workspaces; it never extracts a backing directory.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"j5.nz/scs/cah"
	"j5.nz/scs/repo"
)

func run() error {
	name := flag.String("workspace", "cah-linux", "named workspace to reopen or create")
	from := flag.String("from", "main", "source workspace when target does not exist")
	flag.Parse()
	if flag.NArg() != 2 {
		return fmt.Errorf("usage: cah [-workspace name] [-from main] repository.scs mountpoint")
	}
	if *name == *from {
		return fmt.Errorf("target workspace must differ from source (protect the source root)")
	}
	r, err := repo.Open(flag.Arg(0))
	if err != nil {
		return err
	}
	defer r.Close()
	refs := r.Refs()
	var w *repo.Workspace
	if _, ok := refs[*name]; ok {
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
	f, err := cah.New(w, *name)
	if err != nil {
		return err
	}
	server, err := f.Mount(flag.Arg(1))
	if err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			if err := server.Unmount(); err != nil {
				log.Printf("unmount: %v", err)
			}
		case <-done:
		}
	}()
	fmt.Printf("cah: ready workspace=%s mount=%s\n", *name, flag.Arg(1))
	server.Wait()
	close(done)
	if err = f.Sync(); err != nil {
		return fmt.Errorf("final publication: %w", err)
	}
	fmt.Println("cah: unmounted and published")
	return r.Close()
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
