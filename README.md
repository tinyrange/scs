# SCS — single-file agent workspaces

An experimental workspace runtime, not a Git replacement. Clone full advertised
Git history into native deduplicated objects, or import one committed tree, then
run Starlark scripts against native workspace state. No working-directory
export/import cycle is involved. See `docs/GIT.md` for native protocol cloning and `docs/LINUX-NATIVE.md`
for the original v2 full Linux history experiment (13,965,796 objects).

This is the first vertical slice toward exposed trees backed by FUSE and sandboxed
execution. **FUSE, process execution, and network sync are not implemented yet.**

## Optimized native history

`scs clone` now uses compressed native extent bodies, a batched writer, and an
embedded persistent index. Checkout shares those bodies; native edits can share
unchanged byte ranges across shifted boundaries. See `docs/OPTIMIZED-STORAGE.md`
for the new format, explicit scrub semantics, and current limits. Existing v2
repositories remain readable and are not rewritten.

Full-history benchmarks in `docs/LINUX-OPTIMIZED.md` reduce the original Linux
artifact from 127.06 GB to 12.10 GB. Native size is within 2× installed Git on both
measured corpora; conversion time is still 2.20–2.52× Git, so the combined target
is **not yet met**. Exact results and validation are recorded there.

`docs/FAST-OPEN.md` records the new paged-index/lazy-checkout results on full
Linux history: 2.92 ms median open, 12.92 ms fresh-process one-file durable edit,
and 59.79 ms for ten files. The original latency samples were below 100 ms; a
follow-up memory run observed a 122 ms outlier. OS caches were not flushed, and
100 ms is not a hard bound. Interactive peak RSS was roughly 11–45 MiB. Existing optimized files need a one-time
`scs checkpoint` conversion. `docs/LINUX-INCREMENTAL.md` retains the older
17-second-open baseline.

## Quick start

Requirements: Go 1.27 (as specified in `go.mod`), Git on `PATH` only for the older
local-tree `import` command and test oracles (not native `clone`), and a
local Linux filesystem supporting advisory file locks and `fsync`. Linux is the
initial tested platform. The repository format is experimental and versioned,
with no migration or backward-compatibility promise yet.

```sh
go build -o scs ./cmd/scs

# Native network clone: all advertised history, stored as native chunks.
# ./scs clone https://example.org/project.git project.scs
# ./scs git-checkout project.scs HEAD main

# Or use an existing clone. Import reads one selected commit, not dirty files.
./scs import -rev HEAD /path/to/clone project.scs

# Inspect directly through the workspace API.
./scs run -readonly project.scs examples/inspect.star

# Fork two isolated agent workspaces from main.
./scs run project.scs examples/agents.star
./scs refs project.scs
./scs check project.scs
```

To edit a published workspace, create `edit.star`:

```python
workspace.write_file("agent-notes.txt", "Edited directly in the repository.\n")
workspace.publish("main")
```

```sh
./scs run project.scs edit.star
```

Alternatively, omit `publish()` from the script and run with `-publish`:

```sh
./scs run -workspace agent-a -publish project.scs edit-without-publish.star
```

Flags precede positional arguments. `./scs help` lists commands.

## State and durability

- Mutations update the current **live workspace** and are visible immediately to
  its read-only views. They do not move a published root automatically.
- `workspace.snapshot()` syncs an immutable snapshot and returns its ID without
  changing any named workspace. Keep the ID to reopen it later.
- `workspace.fork()` snapshots the current tree and opens an independent writable
  workspace sharing immutable storage. Editing a fork does not edit its parent.
- `workspace.publish(name)` syncs the snapshot, then durably updates a name. A
  checkout can update its own name; publishing under another name requires that
  name to be unused. Stale checkouts cannot silently overwrite newer publications.
- `scs run -publish` publishes the selected workspace only after script success.
  Without it or an explicit `publish()`, live edits are not retained as a named tree.
- A script is **not one transaction**: explicit publications completed before a
  later script error remain durable. Unpublished edits do not replace them.
- `scs fork project.scs main experiment` creates a published fork. To reopen a
  snapshot by ID, use `scs fork -snapshot ID project.scs recovered`.
- `scs drop project.scs experiment` removes a name, but retains immutable objects.
  There is no garbage collection yet; dropped snapshots remain recoverable by ID.

Only one process can open a repository at a time, including read-only scripts.
Concurrent Go clients sharing one repository handle are synchronized; independent
checkouts use compare-and-swap publication. Read-only means a workspace capability,
not a physically read-only repository open.

## Workspace API

Scripts receive `workspace` plus standard Starlark builtins. The supported subset
matches the agent workspace method names and argument shapes; it is **not yet a
complete drop-in replacement**. See `docs/API.md` for precise semantics and gaps.

```python
workspace.read_file("README.md")
workspace.read_file("README.md", line_start=1, line_end=20, output_limit=4096)
workspace.write_file("notes.txt", "hello\n")
workspace.replace("notes.txt", "hello", "goodbye")
workspace.list_dir("")
workspace.glob("**/*.go")
workspace.search("TODO", glob="**/*.go", max_matches=100)
workspace.mkdir("generated")
workspace.rename("notes.txt", "generated/notes.txt")
workspace.chmod("generated/notes.txt", 0o644)
workspace.delete("generated/notes.txt")
workspace.readonly()
```

Regular file bytes, directories (including empty native directories), executable
modes, and symlink targets are retained. Symlinks are **never followed** by workspace
operations. Paths are relative to the virtual root and cannot traverse `..`.

The script environment provides no host filesystem, network, subprocess, or
`load()` capability. Execution has a configurable Starlark step budget and timeout.
**This is not an OS sandbox or a hard memory/CPU quota**: native operations and
allocations need additional isolation before running adversarial scripts.

## Storage and import

- One file containing immutable, checksummed, SHA-256-addressed objects.
  A terminal derived index may be replaced; published content is not truncated.
- Native `clone` uses SCSREPO3 compressed byte-range bodies. The older local-tree
  `import` and `repo.Create` use SCSREPO2's 4 KiB chunks (the final chunk can be
  shorter). Identical chunks are shared across files, snapshots, and forks.
- File descriptors reference chunks; Merkle directory trees reference descriptors
  and other directories. Snapshots reference roots and source-commit provenance.
- Local-tree import accepts Git SHA-1 and SHA-256 repositories and verifies each
  blob against its Git ID. Native network clone currently negotiates SHA-1 and
  preserves all four Git object types and advertised refs. Native edits do not
  compute Git IDs.
- Local-tree `import` selects one committed tree. Git history, submodules, LFS expansion, filters,
  hooks, dirty files, and untracked files are not imported. Submodules produce a
  clear error; LFS pointer blobs are imported as their literal committed bytes.
- Interrupted incomplete final records are truncated on reopen. Complete records
  with checksum failures are reported as corruption rather than silently discarded.
- `scs check` verifies record checksums and graphs reachable from published names.
  Opening/checking may repair an incomplete tail; these are not forensic read-only
  operations. For failed imports the unpublished repository file is retained.

See `docs/DESIGN.md` for the format, publication protocol, scalability limits, and
next steps. Live workspaces use persistent, structurally shared metadata trees:
unchanged snapshots and clean workspace forks are O(1), and edits copy only the
affected ancestor paths. Directories use bounded, content-addressed hash-trie
pages, so dirty snapshots write changed leaf/ancestor pages instead of entire
child lists. Page partitioning is deterministic: edit order does not change a
snapshot's identity within the format.

**Formats:** `clone` creates `SCSREPO3`; local-tree `import` and `repo.Create` use
`SCSREPO2`. Both remain readable. `SCSREPO1` files are rejected
without modification; there is no automatic migration. Keep old files and use a
v1 build to recover native edits, or re-import Git into a new repository. Snapshot
IDs change across formats.

Durable snapshots still require `fsync` after edits; a dirty fork first performs
that snapshot. V2 opening scans the object log; indexed V3 opens use persistent
lookup pages and lazy tree metadata, verified as accessed. Whole-file physical
scanning remains available through `scs check`. See `docs/FAST-OPEN.md` for current
interactive measurements, `docs/PERFORMANCE.md` for earlier measurements, and
`docs/LINUX.md` for the full Linux corpus test and before/after results.

## Validation

```sh
go test -race ./...
go vet ./...
```

The tests cover SHA-1/SHA-256 import, committed-versus-dirty inputs, binary data,
symlinks, modes, block sharing, forks, read-only capabilities, publication conflicts,
Starlark limits, failure boundaries, and CLI edits compared against an ordinary
directory. Recovery tests truncate an update at every byte boundary and verify
that reopening exposes the old or fully published new root, never a partial tree.

The opt-in Linux integration suite imports a pinned committed tree, checks every
persisted file/link against its Git object ID after reopening, and exercises
wide-directory edits, snapshots, retained forks, rename/delete/chmod, and links.
It never builds or executes Linux. See `docs/LINUX.md` for clone and test commands.
