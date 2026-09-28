# Snapshot and fork performance

## Approach

Live workspaces use persistent metadata trees with paged hash-trie child indexes. A fork
shares its parent's immutable root; editing copies only the affected ancestor
paths and index branches. Snapshot serialization memoizes content IDs and skips
unchanged subtrees. There is no chain of parent overlays to traverse or flatten.

A successfully synced workspace also remembers its durable snapshot ID. Repeated
unchanged snapshots return it without serialization or fsync. Clean workspace
forks allocate just a workspace and state handle. First mutations do not trigger
a deferred whole-tree clone. Record appends write their existing header/payload
buffers directly rather than allocating a 32 KiB copy buffer for each record.

The first optimization retained `SCSREPO1`. The subsequent paged-directory
optimization uses `SCSREPO2`; see `docs/DESIGN.md` for the format boundary.
Publication compare-and-swap, readonly capabilities, and durable snapshot/fork
semantics are unchanged. Snapshot IDs are canonical within each format, not
compatible across formats.

## Current v2: Linux corpus

The full real-repository workload and reproduction commands are in `docs/LINUX.md`.
On the pinned 102,326-path Linux tree, fork/edit/snapshot in its 1,603-child
`include/linux` directory changed from 4.06 ms / 200,507 appended bytes (v1) to
1.30 ms / 4,060 appended bytes (v2). This removes the full-directory rewrite cost,
not the durable fsync or initial import cost.

## Historical first-pass measurements (v1)

Local before/after measurements using the same benchmark workload and environment:
Linux/amd64, `go1.27.0-X:nodwarf5`, without the race detector. These are individual
benchmark runs, not statistical confidence intervals or physical-disk latency
claims. Storage hardware/filesystem characteristics were not characterized.

The fixture contains 100 files per directory, with 100 or 10,000 files total.
Setup and the first snapshot are outside the timer. Edits write a changing small
value to one file, then call durable Snapshot. Fork/edit/snapshot starts each
iteration from the same clean parent. Fork results are not retained, so allocation
figures do not measure the live memory retained by a fleet of long-lived agents.

| Files | Operation | Before | After | Before B/op | After B/op |
| ---: | --- | ---: | ---: | ---: | ---: |
| 100 | Unchanged snapshot | 190,076 ns | 30.87 ns | 122,022 | 0 |
| 100 | Clean workspace fork | 424,288 ns | 78.49 ns | 212,897 | 104 |
| 100 | Edit + snapshot | 219,242 ns | 45,673 ns | 292,646 | 26,021 |
| 100 | Fork + edit + snapshot | 634,365 ns | 47,490 ns | 506,480 | 26,320 |
| 10,000 | Unchanged snapshot | 15,674,947 ns | 30.89 ns | 12,175,036 | 0 |
| 10,000 | Clean workspace fork | 39,936,604 ns | 73.86 ns | 21,912,751 | 104 |
| 10,000 | Edit + snapshot | 16,327,121 ns | 81,736 ns | 12,345,410 | 44,735 |
| 10,000 | Fork + edit + snapshot | 55,149,609 ns | 81,459 ns | 34,256,942 | 44,861 |

Unchanged snapshots allocate zero objects; clean forks allocate two, independent
of tree size in this fixture. The 10,000-file edit/snapshot workload improves by
about 200x; fork/edit/snapshot by about 677x. The clean paths are now lock/handle
operations rather than repository-wide traversals.

The same synthetic workload remains available (running it now measures v2,
not the historical v1 implementation shown above):

```sh
go test ./repo -run '^$' -bench '^BenchmarkWorkspace$' -benchmem
# Multiple samples for comparison using your preferred benchmark analysis tool:
go test ./repo -run '^$' -bench '^BenchmarkWorkspace$' -benchmem -count=5
# Equivalent opt-in runner for environments without a -bench tool argument:
SCS_BENCH=1 go test ./repo -run '^TestWorkspacePerformance$' -v -count=1
```

## Limits and next targets

- **Dirty is not free.** The first snapshot serializes all metadata. Subsequent
  dirty snapshots serialize changed descriptors and ancestor directories, then
  fsync. A dirty fork must perform this work before sharing its root. Clean
  snapshots do not flush unrelated unpublished work from another workspace.
- **Wide-directory rewrites are addressed in v2.** Edits path-copy a bounded
  hash-trie leaf and its ancestors; snapshots reuse all other page IDs. Directory
  enumeration now sorts across leaves instead of traversing an ordered AVL map.
  Page splitting adds records, so smaller update bytes do not imply fewer objects.
- **Cold loading is unchanged in complexity.** `Repository.Fork(id)` and checkout
  load and validate metadata; `Open` scans/hashes the log. No repository-wide
  cache pins all historical snapshots in memory. The first Snapshot of a cold
  loaded workspace syncs rather than assuming read records were durably flushed.
- **Memory is shared, not eliminated.** Retained forks hold their reachable nodes
  alive. Divergent edits allocate new path nodes; replacing/releasing handles
  permits Go to collect unreachable metadata. Persistent indexes also add node
  overhead compared with a single mutable map.
- **Content I/O and publication have separate costs.** Writes still ingest whole
  file streams, and publishing still writes a full name catalog. Neither becomes
  constant-time through metadata sharing.
- **Contention and production latency remain to measure.** Linux tests now cover
  a large real tree, its widest directory, and 64 retained edited forks. Timings
  remain single-threaded and hardware-dependent, not production guarantees.

## Correctness checks

`go test -race ./...`, `go vet ./...`, and Go formatting checks pass. New tests
exercise persistent-index versions against a map model, mixed mutations across
fork generations, canonical snapshot reconstruction, reopen persistence, shared
subtree identity, concurrent snapshots/edits, failed-stream isolation, cached
snapshot rejection after repository poison/close, and atomic rename failure at
the depth limit. Existing byte-boundary crash recovery, publication conflicts,
Git import, script, and CLI tests remain in the suite. V2 adds strict page-parser
validation, split/collapse canonicalization, page-write bounds on wide directories,
and every-byte recovery across a page split. The full suite has also passed with
the Linux integration test enabled under the race detector.
