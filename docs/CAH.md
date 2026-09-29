# cah — Clients at Home

`cah` is a Linux FUSE filesystem backed by the native SCS repository/workspace
implementation. It does **not** extract files to a host backing directory. Native
immutable readers supply unchanged file content; dirty regular files are buffered
in memory and ingested into the same repository file on flush.

## Mount

```sh
go build -o bin/cah ./cmd/cah
mkdir -p .work/cah-mount
bin/cah -workspace cah-linux -from main .work/linux-fast-open.scs .work/cah-mount
# In another terminal, using the approved OS sandbox for commands:
# cd .work/cah-mount
# mkdir -p .cah-tmp
# export TMPDIR="$PWD/.cah-tmp"
# export KBUILD_BUILD_USER=cah KBUILD_BUILD_HOST=sandbox
# make --jobserver-style=pipe tinyconfig
# make --jobserver-style=pipe -j4 bzImage
# Unmount from outside the mount:
fusermount3 -u .work/cah-mount
```

The CLI runs in the foreground and prints `cah: ready` only after mount readiness.
An existing target workspace reopens; an absent target is published from `-from`.
Target and source names must differ. The daemon exclusively owns its repository
handle. Do not open that repository with another process until the daemon exits.
SIGINT/SIGTERM request an ordinary unmount; a busy mount is not forcibly detached.
External ordinary unmount is also supported. Final publication errors produce a
nonzero daemon exit status.

The mount itself is **not a sandbox**. This session's `cah_test.exec` uses the
host's actual `sandbox=True` facility, with fixed cwd at the mount. Its mount and
unmount helpers are separately authorized. Host C development headers are granted
read-only using `readonly_workspaces=[host_c_headers]`, where `host_c_headers` is
a read-only workspace rooted at `/usr/include`. No unsandboxed fallback is
permitted.

Use the pipe jobserver because cah does not implement FIFO nodes. Explicit
`KBUILD_BUILD_USER`/`KBUILD_BUILD_HOST` values avoid requiring host account or
hostname lookup for build metadata. These are build settings, not extra sandbox
grants.

For a measured clean build, feed `cah/kernel_check.py` to sandboxed `python3 -`
at the mount root. It runs `make clean`, `tinyconfig`, and `-j4 bzImage`, records
separate wall-clock timings and build logs in `.cah-kernel-build.json`, checks
ELF/x86 boot signatures, and hashes the outputs. After ordinary unmount and
remount, feed the same script to `python3 - --verify` to compare file bytes and
metadata with that manifest. This is build/persistence validation, not a boot
test or a cold-cache performance benchmark.

## Semantics and durability

- Lazy lookup/getattr/readdir; no startup enumeration of the Linux source tree.
- Stable session inode identities, shared content across concurrent handles,
  offset reads/writes, append, truncate/zero-fill, chmod, symlinks and readlink.
- File creation, directories, unlink/rmdir, and native atomic replacement rename.
  `RENAME_NOREPLACE` is supported; exchange/whiteout flags are not.
- Open-unlinked and replaced files retain their own content. Cached descendant
  paths follow directory renames. No delete-then-rename replacement sequence.
- Nanosecond access/modification/change timestamp fields persist in native file
  descriptors and directory tree descriptors. Old metadata defaults to the Unix
  epoch. Reads do not automatically update atime. The FUSE layer maintains write
  and namespace timestamps; native clients can explicitly call `SetTimes`.
- Flush ingests dirty bytes and reports storage errors, but is **not a durability
  barrier**. File fsync and directory fsync flush all reachable dirty files, then
  durably publish the selected workspace (object sync before root publication).
  O_SYNC/O_DSYNC writes also publish. Orderly unmount does the same after the
  server drains. Unpublished changes can be lost on daemon crash.
- Fallible persistence occurs in Flush/Fsync, not Release. A failed flush retains
  dirty buffers; Release does not silently discard them.
- Zero attribute/entry cache timeouts and no writeback-cache option. Buffered
  kernel reads permit mmap and execution; kernel permissions are enforced with
  `default_permissions` and `NullPermissions`.

The library caller must exclusively own the supplied Workspace for the mount's
lifetime; direct concurrent workspace edits are not reflected in cached nodes.
Inode numbers are session-local, not persisted identities.

## Current limitations

This is an experimental implementation, not a production POSIX filesystem:

- No FIFOs, hard links, xattrs/ACLs, devices, special permission bits, arbitrary ownership,
  advisory locks, allocation guarantees, or meaningful statfs capacity reporting.
  Ownership is the mounting process's UID/GID; root mode is fixed at 0755.
- Native UTF-8/path restrictions apply, including no backslashes. Files cannot
  be mutated beyond 1 GiB. No memory quota or sparse-page overlay is implemented.
- Writes materialize a complete file in memory. Closed clean buffers are released,
  but metadata for visited nodes remains resident until unmount. Native body
  reads may reconstruct an entire compressed body. This is not bounded-memory
  operation for arbitrary workloads.
- A mount-wide mutex serializes operations. Directory rename validates the moved
  subtree to retain native path limits; large renames can be expensive.
- Flush is not publication. A crash without fsync can lose even previously closed
  files. Failed final publication must be treated as a failed unmount workflow.
- Old readers do not preserve newly added timestamp metadata when rewriting it.
  Use the updated implementation for workspaces with these fields.

The bundled go-fuse v2.10.1 does not dispatch `COPY_FILE_RANGE_64`. It returns
`ENOSYS` and logs `Unimplemented opcode COPY_FILE_RANGE_64`; the kernel falls
back to older copy/splice operations. This is an unsupported acceleration path,
not a failed unmount or evidence of data loss. The mounted regression check now
exercises `os.copy_file_range` in multiple chunks, verifies offsets/EOF, and
compares source and destination bytes. The upstream warning is not suppressed;
64-bit copy-opcode support has not been added to the dependency.

## Validation on September 28, 2026

The real artifact `.work/linux-fast-open.scs` was mounted as `fuse.cah` at
`.work/cah-mount`, with edits isolated in `cah-linux`. The 81,066-byte native
Makefile identifies Linux 7.3.0-rc4. No host checkout was used.

Passed:

- `go test -race ./...`, `go vet ./...`; final focused cah race tests after small
  follow-up changes; formatting, `go mod tidy -diff`, and CLI build.
- Unit checks for atomic failed replacement, immutable reader/fork isolation,
  open replacement handles, parallel writes, directory rename, truncate/extend,
  fsync publication, and error retention when native storage is unavailable.
- Timestamp round trips through v2/v3, ordinary/lazy open, and OpenVerified.
- Actual sandboxed mounted checks (`cah/mount_check.py`): replacement rename,
  open-unlinked content and link count, truncate/zero-fill, mmap reads, parallel
  offset writes, directory rename, symlink traversal, chmod, utime, nonempty
  directory replacement failure, and cleanup.
- Additional mounted execution of a generated executable shell script and 100
  concurrent append records, plus directory fsync.
- Clean unmount/publication and remount. Exact SHA-256, size, mode, nanosecond
  mtime, and symlink verified against a manifest created before unmount.
  Proof files remain inside `cah-linux` under `.cah-persistence-proof`.
- Both mount processes exited successfully after ordinary unmount. No mount was
  left running at the end of this session.

The September 28 attempt stopped at `HOSTCC scripts/basic/fixdep` because the
sandbox hid `/usr/include`. That historical failure is retained in
`cah-linux-results.json`; it is resolved by the approved read-only header grant.

## Kernel build and persistence validation on September 29, 2026

**Completed:** a clean tinyconfig x86 kernel build inside the mounted native
`cah-linux` workspace, with sandboxing enabled throughout. Nothing was installed
and no source/output tree was extracted to the host. This is Linux 7.3.0-rc4,
32-bit x86 (`CONFIG_X86_32=y`), with XZ compression—not a distribution kernel.

The first `make -j4 bzImage` succeeded in 157.740 seconds but emitted FIFO
jobserver and missing-user-name warnings. The corrected clean validation used
pipe jobserver mode and explicit build user/host metadata; all phases exited 0
with empty stderr:

| Phase | Wall time |
| --- | ---: |
| `make --jobserver-style=pipe clean` | 34.996 s |
| `make --jobserver-style=pipe tinyconfig` | 9.203 s |
| `make --jobserver-style=pipe -j4 bzImage` | 144.976 s |

The harness took 189.342 seconds overall. These are observed warm-cache build
wall times, not a cold-cache benchmark. The earlier roughly 13-second run only
configured the kernel and built host tools; it did not compile a kernel image.

Verified artifacts within the filesystem:

| Path | Bytes |
| --- | ---: |
| `arch/x86/boot/bzImage` | 619,008 |
| `vmlinux` | 3,089,936 |
| `System.map` | 379,950 |

`file` recognized the bzImage as a Linux x86 boot executable and vmlinux as a
statically linked 32-bit i386 ELF. The harness independently checked ELF magic,
the x86 boot signature, and the Linux `HdrS` header. The bzImage SHA-256 is:

```text
94386cf7cacffd84aa8bd32bd68ec1cae61a9ba287102e2e6bff7421f3c24ccf
```

After ordinary unmount/publication, a fresh mount verified SHA-256, size, mode,
and nanosecond mtime for `bzImage`, `vmlinux`, `System.map`, `.config`, and the
unchanged source Makefile. Results also matched the independently retained
pre-unmount manifest. Both mount daemons exited 0; the filesystem was cleanly
unmounted again. The artifacts remain in workspace `cah-linux` inside
`.work/linux-fast-open.scs`, not in the empty unmounted mountpoint directory.

The mounted semantic suite, including the new copy-file-range regression, passed.
The upstream unsupported-opcode diagnostic remains a documented fallback, not a
silenced error. No kernel boot test or Git commit was performed.

Published validation logs replace the developer's absolute project path with
`<project-root>`; command output is otherwise retained.

Full build logs and pre-unmount manifest: `cah-linux-kernel-build.json`.
Final persistence results and historical header failure: `cah-linux-results.json`.
