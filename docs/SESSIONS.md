# Serialized agent sessions

SCS now supports an explicit **API edit → cah mount → API inspect → review**
workflow. It uses one repository handle and one native workspace, never a host
backing-tree export/import cycle. Execution and its sandbox are supplied by the
host. There is deliberately no arbitrary-command runner in SCS or Starlark.

## CLI workflow

Build `scs` and `cah`, use `scs init` or import/clone a repository with a published `main`, and make
an empty mountpoint. Choose a **new** candidate name for every session:

```sh
go build -o bin/scs ./cmd/scs
go build -o bin/cah ./cmd/cah
mkdir -p mount
bin/cah -session -from main -workspace candidate-001 \
  -before edit.star -after inspect.star \
  -buffer-limit 67108864 project.scs mount
```

Example `edit.star`:

```python
workspace.write_file("agent-note.txt", "Prepared through the native API.\n")
```

Example `inspect.star`:

```python
print(workspace.list_dir(""))
print(workspace.read_file("agent-note.txt"))
# For a real build, inspect its output and result manifest here.
```

Wait for `cah: ready`, then use your host's approved sandbox runner to execute
commands with the mount as its working directory. SCS does not infer that an
ordinary shell or a command run from a mount is sandboxed. Stop/wait for the
build and its descendants before requesting ordinary unmount:

```sh
fusermount3 -u mount
```

Wait for the cah process to exit. A nonzero status is a failed session workflow;
never interpret a detached mount alone as successful publication. SIGINT/SIGTERM
also request ordinary unmount. Busy unmounts leave the daemon running and can be
retried with a subsequent signal after descriptors/processes have been closed.

`-before`, `-after`, and `-build-result` require `-session`; both scripts are read before mounting.
Each gets the configured `-timeout` (default 1m) and `-steps` (10 million).
The timeout meters script phases, **not** the external build's lifetime.

## State, ownership, and failure contract

1. The source snapshot is forked into a new, published candidate. Existing names
   are rejected in session mode. The source and siblings never move.
2. The edit script runs with file mutation capability but **no snapshot, fork,
   or publication capability**. Derived read-only views retain that restriction.
   A failed edit leaves the candidate at its original root and never mounts it.
3. Successful edits are durably published to the candidate before mounting.
4. The mount exclusively owns the live workspace until it drains. API access
   during this phase is unsupported; do not open a second repository handle.
5. File/directory fsync and sync writes publish the candidate. Final unmount sync
   does the same. Flush alone is not durable publication.
6. After successful final sync, inspection runs against that same workspace,
   read-only. An inspection error returns failure but does not roll back the
   candidate. A final sync error skips inspection and returns failure.
7. Review and acceptance are separate, explicit actions. A successful unmount
   does **not** prove a successful build. The external runner must retain its exit
   status (and ideally write a result manifest for inspection).

Failed builds retain candidate outputs for diagnosis. Without `-build-result`,
CLI mount mode cannot observe an externally launched build's exit code; do not
treat cah's exit code as the build status. With the flag, a trusted host supplies
a fresh receipt and cah propagates failure/timeout after publication/inspection.
A trusted Go host may also use `cah.RunSession` and return a
build error from its serving callback: the session still syncs and inspects the
candidate, then returns the build error. The callback must return only after all
mount requests have drained, including on failure. Context cancellation is
cooperative; the host remains responsible for stopping execution and unmounting.

For continued work on a candidate, use it as `-from` with a new target, or use the
existing non-session mount and `scs run` commands sequentially after closing each
repository handle. There is no simultaneous API/mount coherence protocol yet.

## Host build receipts

For a host-supervised build, add `-build-result /trusted/host/result.json` to the
session command. The file must not exist at startup, and its resolved parent
must be outside the mount. Cah prints a fresh `session=TOKEN` in its ready line.
After its sandbox runner and child processes finish, the host writes a receipt
in that trusted location **before ordinary unmount**:

```json
{"version":1,"session":"TOKEN_FROM_READY_LINE","exit_code":0,"timed_out":false,"duration_ms":123}
```

All fields are required; exit code must be -1 (signal-style failure) or 0–255,
timeout a boolean, and duration a nonnegative integer. Unknown fields, trailing
JSON, oversized/nonregular files, final symlinks, missing receipts, and wrong
session tokens fail the session. Only exit 0 with `timed_out=false` is success.
A failed receipt still allows final candidate publication and read-only inspection;
a final publication error still takes precedence over inspection.

The receipt is a **trusted host assertion**, not cryptographic proof of sandboxing
or build success. A build-authored JSON file inside the mount is not an authority.
Keep receipt parent directories stable and inaccessible to sandboxed writers;
do not allow a build to replace or write the host receipt. The token prevents
accidental reuse across invocations, not a malicious host from lying. The host
must supervise descendants and write atomically if concurrent readers are possible.

A complete fixed-fixture example is documented in `SESSION-BUILD-DEMO.md`. Its
host helper writes the receipt from the observed outer process status, separately
from the build's artifact/log manifest. No arbitrary-command executor was added
to SCS or the Starlark workspace capability.

## Review and use the results

After cah exits:

```sh
bin/scs diff project.scs main candidate-001
bin/scs diff -json project.scs main candidate-001
bin/scs export project.scs candidate-001 candidate-001.tar
# Optional explicit acceptance under a NEW name, preserving main:
bin/scs fork project.scs candidate-001 accepted-001
```

Refs accept a published name or a snapshot ID; names take precedence. `diff`
compares file contents, modes, link targets, directory presence/modes, and gitlink
IDs. It ignores timestamps. Human output is a sorted path-level summary with
quoted paths; JSON includes old/new metadata and SHA-256 file-content hashes.
Renames are deletion/addition pairs. This is **not** a unified text patch, merge
engine, or Git commit export. Diff now captures each immutable root independently
without publication, skips equal Merkle subtrees/index branches and equal content
references, and hashes files only when needed for a candidate change. Different
storage encodings of identical bytes still compare equal. Directory modes are
compared separately because they are stored in the parent, not the tree ID.

This is not a globally atomic capture of two concurrently edited workspaces.
The CLI compares published snapshots under the repository lock; Go callers get
one independently captured version of each input. Visited lazy directories still
load their directory index pages; pruning does not yet make those page loads
range-lazy. Changed/new/deleted file bodies may decode in full.

**Diff is not an integrity check.** Equal IDs skip the referenced subtree and its
bytes, so latent corruption inside an unchanged subtree can remain undetected.
Accessed metadata/content errors are reported, but use `scs check` or explicit
scrub for integrity validation.

`export` is a **full-tree PAX tar**, not an incremental patch. It preserves binary
bytes, executable modes, symlink targets without following them, empty native
directories, and nanosecond mtime. Ownership is normalized to UID/GID 0; root
metadata, atime, and ctime are not exported. Gitlinks/submodules are rejected
rather than silently omitted. The repository remains locked throughout export.

The CLI writes/syncs a temporary archive beside the destination and publishes it
with a no-clobber hard link. Existing destinations (including symlinks) are never
overwritten. The destination filesystem must support hard links and directory
fsync. Handled failures remove the temporary file; a killed exporter may leave
an unpublished `.scs-export-*` temporary file. A late directory-sync error can
leave a complete output file but still reports failure.

Archives can contain absolute or escaping **link targets** from the source.
Inspect and extract untrusted archives using an appropriate isolated/safe
extractor, never blindly into an existing checkout. Export contains only the
selected tree, not deleted historical objects from the `.scs` container.
