//go:build linux

package cah

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"j5.nz/scs/repo"
	"j5.nz/scs/script"
)

// SessionOptions describes a serialized edit/mount/inspect session. The target
// must be a NEW name. Only this candidate is published; source/siblings never
// move. Script publication is denied. After runs read-only, even on build failure.
type SessionOptions struct {
	Source, Target string
	Before, After  []byte
	Timeout        time.Duration
	Steps          uint64
	Print          io.Writer
	FS             Options
}

// RunSession keeps one repository handle and transfers exclusive workspace
// ownership to serve, then back to the API. serve must return ONLY after its
// mount has drained (even on error); it must not keep using f. The host supplies
// process execution and sandboxing. No processes or sandbox are created here.
// A serve/build error still syncs candidate outputs and runs inspection, then
// returns the error. A failed sync skips inspection. Source is never promoted.
func RunSession(ctx context.Context, r *repo.Repository, o SessionOptions, serve func(*FS) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if serve == nil || o.Source == o.Target {
		return errors.New("session requires a runner and distinct source/target")
	}
	if _, exists := r.Refs()[o.Target]; exists {
		return errors.New("session target already exists; choose a new candidate name")
	}
	if o.Timeout < 0 {
		return errors.New("negative script timeout")
	}
	if o.Timeout == 0 {
		o.Timeout = time.Minute
	}
	if o.FS.BufferLimit < 0 {
		return errors.New("negative buffer limit")
	}
	id, exists := r.Refs()[o.Source]
	if !exists {
		return fmt.Errorf("unknown source workspace %q", o.Source)
	}
	// Fork the immutable published source; do not rebind its checkout.
	w, err := r.Fork(id)
	if err != nil {
		return err
	}
	if _, err = w.Publish(o.Target); err != nil {
		return err
	}
	run := func(w *repo.Workspace, filename string, source []byte) error {
		if len(source) == 0 {
			return nil
		}
		scriptCtx, cancel := context.WithTimeout(ctx, o.Timeout)
		defer cancel()
		if err := script.Run(scriptCtx, w, filename, source, script.Options{MaxSteps: o.Steps, Print: o.Print, NoPublish: true}); err != nil {
			return err
		}
		return scriptCtx.Err()
	}
	if err = run(w, "before.star", o.Before); err != nil {
		return fmt.Errorf("session edit (candidate retains original root): %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, err = w.Publish(o.Target); err != nil {
		return err
	}
	f, err := NewWithOptions(w, o.Target, o.FS)
	if err != nil {
		return err
	}
	serveErr := serve(f)
	if err = f.Sync(); err != nil {
		return errors.Join(serveErr, fmt.Errorf("session final publication: %w", err))
	}
	inspectErr := run(w.Readonly(), "after.star", o.After)
	return errors.Join(serveErr, inspectErr, ctx.Err())
}
