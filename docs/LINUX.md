# Linux corpus: paged-directory validation

For the separately completed **full-history native Git import**, see
`LINUX-NATIVE.md`. This document records the earlier HEAD-only workload.

## Corpus and scope

The Linux repository was cloned from `torvalds/linux` using a shallow,
no-checkout clone. This includes the complete selected commit tree and its blobs,
not the complete historical Git graph. No Linux code, hooks, builds, or kernel
tests are executed; this is a storage/workspace integration and performance test.

Pinned commit: `fd179f8a05be3ccae366b9b96e176b51fbe54aab`.

- 102,326 paths (directories, files, and symlinks).
- 95,943 regular files.
- 1,640,939,616 bytes of file/symlink contents verified against Git after reopening.
- Widest directory with a regular file: `include/linux`, 1,603 direct children.
- Edit target: `include/linux/8250_pci.h`.

`repo/linux_test.go` is opt-in. Normal `go test` never clones anything or requires
a network connection. `SCS_LINUX_CLONE` selects an existing local clone;
`SCS_LINUX_REV` pins the commit (defaults to HEAD).

## Results: v1 versus v2

These are local, single-run before/after measurements on the same pinned tree,
without the race detector. Both measured repository files were created under
`.work/`. The benchmark performs 100 independent clean-parent forks, replaces one
file with a changing short value, and durably snapshots the result. It isolates
metadata update cost from large-file ingestion. Times are sensitive to filesystem,
fsync, cache state, and system load; write volume is the stronger structural result.

| Metric | v1: flat directory records | v2: bounded directory pages |
| --- | ---: | ---: |
| Widest-directory fork + edit + snapshot | 4.064 ms/op | 1.299 ms/op |
| Bytes appended per edit | 200,507 | 4,060 |
| Allocated bytes per edit | 335,114 | 12,981 |
| Allocations per edit | 76 | 93 |
| New content objects per edit | 6 | 12 |
| Import | 8.152 s | 8.333 s |
| Initial publication | 570 ms | 589 ms |
| Repository bytes after initial publication | 1,704,101,402 | 1,698,351,775 |
| Content objects after initial publication | 556,003 | 574,717 |
| Reopen / checksum scan | 2.428 s | 2.375 s |
| Cold main checkout | 424 ms | 425 ms |

That is approximately **3.1x faster**, **49.4x less appended data**, and **25.8x
less allocated memory** for this edit workload. The paged representation writes
more, smaller objects; initial import/publication and cold loading are not the
target of this optimization and show no meaningful speedup here.

Clean snapshot/fork calls still appended zero bytes. Clean snapshots allocated
zero bytes; clean forks allocated 104 bytes in two allocations. Their timings in
this integration harness include assertion overhead and should not be compared
directly with the smaller synthetic benchmark's nanosecond numbers.

## Validation performed

The test:

1. Imports and publishes the pinned Git tree; compares all paths with `git ls-tree`.
2. Measures repeated clean snapshots/forks and edits in the widest directory.
3. Checks that edits cannot change the parent workspace.
4. Exercises rename, chmod, replacement, deletion, empty directories, and symlinks.
5. Retains 64 independently edited/snapshotted forks and verifies each separately.
6. Closes and reopens the repository, then verifies every original file/symlink
   against its Git blob ID, plus all expected modes/directories.
7. Reopens edited/mutated publications and checks their contents and metadata.
8. Checks stable snapshot identity after reopening.

The full project suite passed with this test enabled under `go test -race ./...`.
The Linux test took about 49 seconds in that race-enabled run. `go vet ./...` and
format checks also passed. Unit tests separately cover canonical page splits and
collapses, randomized persistent-index histories, malformed pages, checksum
failures, bounded writes in 10,000-entry directories (including edits after cold
reload), unchanged v1 files on rejection, and every-byte recovery through a split.

## Reproduce

From the project root:

```sh
mkdir -p .work
git -c core.hooksPath=/dev/null clone --depth=1 --no-checkout \
  https://github.com/torvalds/linux .work/linux

# If HEAD has moved, fetch the exact measured commit into the shallow clone.
git -C .work/linux fetch --depth=1 origin \
  fd179f8a05be3ccae366b9b96e176b51fbe54aab

SCS_LINUX_CLONE="$PWD/.work/linux" \
SCS_LINUX_REV=fd179f8a05be3ccae366b9b96e176b51fbe54aab \
go test ./repo -run '^TestLinuxWorkspace$' -v -count=1 -timeout=15m

SCS_LINUX_CLONE="$PWD/.work/linux" \
SCS_LINUX_REV=fd179f8a05be3ccae366b9b96e176b51fbe54aab \
go test -race ./... -count=1 -timeout=15m
```

The test normally uses a temporary repository removed by Go. To measure a chosen
filesystem and retain the file, set `SCS_LINUX_REPO` to an absolute **new** path.
Existing files are never overwritten. Allow space for the clone and roughly
1.7 GB per imported repository, plus updates and test/runtime overhead. Do not
run the integration test in constrained CI unless explicitly provisioned.

Local artifacts from this implementation run are ignored under `.work/`:
`linux/`, `linux-v1.scs`, `linux-v2.scs`, `linux-baseline.txt`, `linux-v2.txt`, and
`linux-race.txt`. They are not vendored source or mandatory checked-in fixtures.

## Compatibility

New files use `SCSREPO2`. The v1 baseline file is deliberately retained and is not
opened or rewritten by v2. Keep a v1 build to recover native edits from v1 files,
or re-import the Git tree into a new file. There is no automatic migration, and
snapshot IDs differ across the two formats.
