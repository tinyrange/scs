# Regression validation

## Ordinary checks

```sh
go test -race ./...
go vet ./...
go mod tidy -diff
go build ./cmd/scs ./cmd/cah
```

## Disposable Linux mounted suite

Requirements: Linux with usable `/dev/fuse`, `fusermount3` on PATH, and permission
to mount user FUSE filesystems. Tests fail rather than silently skip when opted
in but mounting fails. No packages are installed by the harness.

```sh
SCS_TEST_FUSE=1 go test -race -count=1 -run TestMounted -v ./cmd/cah
```

The suite creates tiny temporary repositories and mountpoints. It does not use
`.work`, the Linux corpus, the fixed `cah_test` helpers, or a host source checkout.
It executes its own trusted test binary and filesystem fixtures, **not untrusted
builds**. This validates mounted API/session semantics; it is not sandbox testing.
Cleanup uses ordinary unmount only (no force/lazy detach).

Covered mounted scenarios:

- File fsync, directory fsync, and O_SYNC followed by SIGKILL and verified reopen.
- Crash without fsync: no promise of retaining the unsynced file; any retained
  state must remain internally consistent.
- Orderly unmount and candidate publication, keeping source/sibling roots intact.
- Pre-mount API edits, mounted output/replacement rename, retained old descriptor,
  post-unmount API inspection, and verified reopen.
- Busy SIGTERM unmount failure, descriptor closure, and successful signal retry.
- Session build receipts: success, failure, timeout, missing receipt, and stale
  token; inspection and durable outputs survive failed builds without changing
  the source or sibling roots.

Ordinary unit tests additionally cover quota exhaustion without partial writes,
sparse extension, randomized writes/truncates/flushes against a byte-array oracle,
block sharing and snapshot isolation, failed storage flush retention, retry after
publication conflict, denied script publication, failed build inspection, final
storage failure, and diff/archive correctness and no-clobber destination safety.

SIGKILL tests are process-crash tests, not disk/power-failure emulation. Existing
repository byte-cut tests cover interrupted publication records separately.

`TestStorageWriteAndSyncFailureRecovery` now wraps the internal file I/O boundary
in four storage modes: V2, V3, paged V3, and legacy-checkpoint V3. It writes actual
prefixes of records, injects ENOSPC or a short count with nil error, and fails each
sync barrier with EIO. The matrix samples physical write boundaries/midpoints and
record-header edges; it does not claim every byte cut or every OS failure mode.

Assertions cover unchanged last-acknowledged refs, poisoned-handle rejection,
Close returning the original failure without retrying I/O, coherent old-or-new
recovery through ordinary and verified opens, and subsequent fresh-handle writes.
A copy of the last successful-sync image must recover the old root. Failed fsync
is ambiguous: the visible file may already contain a complete new root even
though publication reported failure. Never assume that failure means rollback.

This is deterministic injected failure testing—not filling the host disk, actual
power removal, or a complete model of filesystem ordering. Production still uses
an ordinary `os.File`; fault controls exist only in tests.

Pruned diff tests compare an exhaustive traversal oracle across eager/lazy and
published/unpublished trees, forward and reverse changes, leaf split/collapse,
type changes, mode-only edits, and differing storage encodings. An I/O guard
rejects reads into an equal subtree with 1,000 files. Separate corruption tests
show that skipped corruption needs scrub, while accessed corruption fails diff.
Concurrent live-root capture is race-tested without publishing either input.

The GitHub Actions workflow runs ordinary checks on Linux and attempts mounted
checks when `/dev/fuse` and `fusermount3` exist. Missing prerequisites produce an
explicit warning/summary, not a mounted pass. The workflow has not been remotely
executed as part of local development; a FUSE-capable runner is required for that
coverage. No privileged pull-request job or automatic dependency installation is
configured.

## Local validation of this change — September 29, 2026

Passed on the development host:

- `SCS_TEST_FUSE=1 go test -race -count=1 ./...` (uncached; all six packages passed,
  including actual temporary mounts, daemon-kill recovery, and session shutdown).
- `go vet ./...`.
- `go mod tidy -diff` (no module changes).
- Go formatting check (no files reported).
- Builds of `cmd/scs` and `cmd/cah` to `bin/scs` and `bin/cah`.

The mounted tests cleaned up their temporary mounts and processes. No large Linux
artifact was opened or modified, no kernel build was repeated, and no new
performance or power-loss guarantee is inferred from these tests. CI was added
but has not been run remotely.

### Follow-up: storage faults and pruned diff

The full uncached race suite was rerun with `SCS_TEST_FUSE=1` after adding storage
fault injection, sticky Close errors, and Merkle-pruned diff. All six packages
passed, including actual mounted tests. Vet, tidy-diff, formatting, and both CLI
builds also passed.

In the deterministic paged-repository I/O-guard fixture, diffing one changed file
plus a directory-mode change alongside 1,000 unchanged files issued **8 ReadAt
calls / 736 requested bytes after checkout**. The unchanged directory descriptor
was guarded against reads. Comparing identical captured roots issued zero reads.
These are fixture I/O counts, not cold-cache or wall-clock performance results.

### Follow-up: native initialization and sandboxed build sessions

After adding `scs init` and strict session build receipts (including required
non-null timeout and duration fields), the full uncached race suite was rerun
with `SCS_TEST_FUSE=1`. All six packages passed. Vet, tidy-diff, formatting, and
both CLI builds also passed.

Separately, the approved fixed `session_demo` helper exercised actual sandboxed
C compilation and execution after native API edits. Success, intentional compiler
failure, and controlled child timeout produced driver/cah exits 0/0, 1/1, and
124/1 respectively. All three ran post-unmount inspection and verified durable
artifact hashes, sizes, and modes after reopening; source/sibling roots stayed
unchanged. All mounts were ordinarily unmounted and daemons exited. See
[the demonstration report](SESSION-BUILD-DEMO.md) and
[the machine-readable results](session-build-results.json).

These are fixed trusted fixtures, not general hostile-build supervision. The
timeout case exercises an inner child deadline, not an outer tool timeout.
No package installation, sandbox bypass, or large Linux artifact change occurred.
