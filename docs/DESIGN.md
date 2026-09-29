# Architecture and V2 format

This document retains the V2 format design. For V3 compressed bodies and paged
indexes, read `OPTIMIZED-STORAGE.md` and `FAST-OPEN.md`. For current implemented
features and remaining work, use `STATUS.md`; session ownership is in `SESSIONS.md`.

## Product boundary

The primary client is an agent running Starlark against a workspace capability.
The native tree is the filesystem state, not a cache of a materialized Git working
copy. Git supplies initial data. cah provides FUSE as a separate client layer;
process sandboxing is supplied by the host, and synchronization remains future
work. None is a prerequisite for the storage API.

The core design separates:

1. **Immutable content**: data chunks, file descriptors, directory trees, snapshots.
2. **Live workspace state**: an independently writable tree of entries.
3. **Authority**: writable versus read-only capabilities over that live state.
4. **Published names**: durable pointers to immutable snapshots.

Capabilities are not hashed content. A read-only view shares the live tree; a fork
has an independent root pointer and shares immutable in-memory nodes as well as
stored objects. Independent checkout handles of the same name are not automatically
coherent live views. Serialized sessions transfer exclusive ownership of the same
workspace between API and mount phases, not a second checkout. Concurrent direct
API/mount edits remain unsupported.

## Experimental file format: SCSREPO2

One file starts with the eight ASCII bytes `SCSREPO2`. Following records contain:

| Field | Size |
| --- | --- |
| Kind | 1 byte |
| Payload length | 8 bytes, unsigned big-endian |
| SHA-256 of `kind-byte || payload` | 32 bytes |
| Payload | Length bytes |

Record kinds are 1 = data block, 2 = file descriptor, 3 = directory tree,
4 = snapshot, 5 = published-root catalog, 6 = directory index page. Payloads are uncompressed. Records are
variable-length and **not yet a fixed-page allocator**; file data is chunked at
4,096-byte logical offsets with a possibly shorter final block.

Data payloads contain raw bytes. Directory index pages use the bounded binary
encoding below. Other metadata uses the Go structs' JSON encodings in `repo/`;
catalog keys are sorted by the JSON encoder. SHA-256 includes the kind to separate
object domains. Object IDs are lowercase hexadecimal in JSON and raw 32-byte
digests inside index pages. Serialization is experimental and versioned.

- A file descriptor contains kind, mode, size, and the ordered block IDs.
- A directory contains an index-page ID, or an empty ID for an empty directory.
- A snapshot contains a tree ID and optional imported source-commit provenance.
  There are no timestamps, parent edges, messages, or Git IDs for native edits.
- A catalog contains the complete name-to-snapshot map. Catalog records are
  publication events, not deduplicated content objects. The last complete valid
  catalog determines the published roots.

Empty directories are explicit. Symlink contents use the same chunk storage as
files, but their descriptor kind is distinct. File descriptors and parent tree
entries both contain kind/mode, and loading checks their agreement.

An in-memory object index maps content IDs to record payload offsets, lengths,
and kinds. It is rebuilt by scanning the log, not persisted as a separate file.
Complete records are hash-verified on open; fetched objects are verified on read.
The implementation never changes physical locations or reclaims records.

## Directory index pages

The same persistent hash trie is used in memory and on disk. SHA-256 of the UTF-8
child name selects 4-bit slots, high nibble first, for up to 64 levels. Hashes
partition names only; contents/modes do not alter the partitioning. Leaves hold
sorted names. Split/collapse rules depend only on entry names and encoded sizes,
so insertion order and deletion history cannot affect the canonical tree ID.

Payload integers are unsigned big endian. Pages begin with a tag byte:

- **Leaf (tag 0):** uint16 entry count, followed by entries. Each entry is a uint16
  byte length, UTF-8 name bytes, kind byte (1=file, 2=directory, 3=symlink), uint16
  mode, and raw 32-byte descriptor/tree ID. At most 32 entries and 4,096 payload
  bytes. A single oversized name is allowed up to the path-length limit, giving
  a maximum singleton payload of 4,136 bytes. Empty leaves are not stored.
- **Branch (tag 1):** uint16 occupied-slot bitmap, then raw 32-byte page IDs for
  occupied slots in increasing slot order. At most 16 references (515 payload
  bytes). Empty branches and branches whose contents fit in a leaf are invalid.

A leaf splits when either bound is exceeded, recursively partitioning names by
the next hash nibble. Deletion collapses a branch if its remaining entries fit a
leaf. Only bounded leaf entries and ancestor pages are copied/serialized for an
edit; unchanged pages retain their IDs. A full SHA-256 collision bucket exceeding
leaf capacity is rejected at snapshot time rather than recursing without limit.

Loading validates page bounds, sorted unique names, hash-prefix routing, kinds,
modes, path limits, and referenced object types. References must point to earlier
records: snapshots to trees, trees to pages, pages to pages/descriptors/trees, and
file descriptors to blocks. This rejects cycles/forward references. Checksum
validation alone is not sufficient to accept a malformed directory graph.

**Compatibility:** v1 used one JSON child list per directory. New builds reject
`SCSREPO1` without modifying it. No in-place migration or cross-version snapshot
identity is promised. Preserve old files and use a v1 build to recover native
edits, or import the original Git tree into a new v2 repository.

## Mutation and publication

`WriteFrom` streams a file into blocks. It installs the new file descriptor in the
live tree only after input succeeds. Failed ingestion can leave unreferenced data
blocks but not a partially updated path. `WriteFile` uses this path. Rename and
other namespace changes take the workspace mutex. Repository I/O takes a separate
mutex, always after the workspace mutex when both are needed.

Live metadata is a persistent hierarchy. Each directory uses the paged hash trie
above: an edit copies a bounded leaf and its radix ancestors per path component,
with expected O(log base 16 of fanout) index work. Forks share a root pointer
without cloning the tree or file block lists; readonly views still share the
mutable workspace handle. There are no overlay chains growing with fork depth.

Snapshot creation serializes only nodes without a cached content ID. Unchanged
subtrees are skipped. Cached node IDs are memoized under the repository mutex;
all other installed metadata is immutable. Cached page IDs similarly skip
unchanged portions of each directory. Directory modes live in the parent's child
entry, so chmod need not reserialize the directory's descendants.

After successful fsync the workspace remembers its durable snapshot ID. Repeated
Snapshot and clean Workspace.Fork calls take locks but perform no I/O or traversal.
Mutations invalidate that workspace's snapshot ID, not its siblings'. A dirty fork
first completes a durable snapshot, then shares its root while still holding the
workspace lock. No unbounded repository-wide snapshot cache is retained. Loading
by ID still validates the full tree and syncs on its first Snapshot; reading an
object is not treated as proof that a previous process durably flushed it.

Publication under the repository mutex:

1. Check the target name against the handle's expected ID, or require a new name.
2. Append any missing objects and the snapshot.
3. `fsync` the file so all referenced objects precede publication durably.
4. Append the complete new root catalog.
5. `fsync` again.
6. Update in-memory roots and the publishing handle's expected ID.

`Snapshot()` stops after syncing objects, without a catalog update. `Drop()` appends
and syncs a catalog without the removed name. Removal does not free content.
Initial creation syncs the file and its parent directory. Appends/sync failures
poison the repository handle so no further write attempt can continue from an
uncertain state; reopening is required. A reported sync failure can have an
ambiguous publication outcome and must not be interpreted as guaranteed rollback.

The repository uses an exclusive advisory inode lock for its lifetime. There are
no lock/journal/index sidecars. This is a local, single-process-owner MVP, not a
multi-host locking protocol. Advisory locks require cooperating users; network
filesystem semantics are not validated.

## Recovery and corruption

Open scans complete records in order. A physically incomplete final header or
payload is truncated at its record boundary and the truncation is synced. Complete
unpublished objects remain reusable. A partial catalog cannot replace a previous
published catalog. Tests cut a publication at every byte and reopen/write again.

A complete record with a checksum mismatch, invalid kind, or oversized length is
reported as corruption, not silently treated as an interrupted append. This is a
conservative failure policy. The prototype does not promise automatic recovery
from arbitrary torn sectors, zero-filled writes, disk faults, or filesystem bugs.
Back up important data; crash-prefix testing is not exhaustive power-loss testing.

Catalog roots must refer to earlier snapshot records. Loading a workspace validates
its reachable tree structure, descriptor types, sizes, block counts, and block
locations. `scs check` loads all published roots after the checksum scan. It does
not validate the graph structure of every orphaned historical object.

## Git boundary

Import resolves a chosen revision to a commit and reads its object tree with Git's
plumbing commands. It uses NUL-delimited paths and streams blobs through one
`cat-file --batch` process. The complete Git blob hash (SHA-1 or SHA-256) is verified
while native chunks are written. Symlink targets and executable bits survive;
submodules fail explicitly. No checkout filters or hooks are run by the importer.
The host-selected Git clone and installed Git executable are trusted inputs, not
an OS-sandboxed boundary.

The selected commit ID is provenance, not a native identity requirement. No history
or exact commit/tag serialization is retained, so this MVP **does not promise exact
Git round-tripping or push support**. Adding that later must preserve enough raw
Git metadata; preserving a starting tree alone is insufficient.

## Current complexity and limits

- V2/legacy opening scans the object log. Paged V3 uses persistent lookup pages
  and lazy metadata, verifying content as accessed; full scrub remains explicit.
- V2/eager checkout loads tree metadata and block-ID lists, not file bytes.
  Paged V3 checkout/fork loads metadata lazily.
- Clean `Workspace.Fork()` and repeated unchanged `Snapshot()` are O(1). First
  snapshots serialize all metadata; later snapshots skip unchanged subtrees.
  Dirty snapshot cost includes changed descriptors, bounded directory leaf and
  radix ancestor pages along changed filesystem paths, and fsync. A dirty fork
  pays that cost once; it does not re-encode whole directory child lists.
- Metadata edits have expected O(sum of log fanouts along the path) index work,
  excluding file ingestion. Retained forks keep shared metadata alive until
  released. A single hash trie cannot exceed 64 branching levels.
- Script reads/replacements/search still materialize file bytes. Native immutable
  readers support range I/O; cah uses dirty-page overlays with a byte budget.
  Block reads are incremental, but compressed native bodies can decode in full.
  Streaming search and bounded compressed-range decoding remain future work.
- `list_dir` visits only the selected directory, collecting and sorting its
  entries when it spans multiple hash-trie leaves (O(fanout log fanout)). Small
  leaf-only directories are already sorted. This trades sorted-index traversal
  for canonical bounded disk updates without maintaining a second index.
  Rename reuses the moved subtree, but still walks it to validate destination
  path-length/depth limits before atomically changing either path.
- Records have a 64 MiB payload cap, including descriptor arrays and catalogs.
  Directory pages are independently bounded and no longer hit that cap merely
  because a directory is wide. There are no indirect file-block nodes yet;
  extremely large files or named-root catalogs can still exceed the cap.
- UTF-8 paths, a 4 KiB path-length limit, and a depth limit are intentional MVP
  restrictions. Graph size, heap usage, and script allocations have no hard quotas.
- No garbage collection, compaction, page allocator, built-in process sandbox,
  merge engine, network synchronization, or Git commit export. Compression,
  checkpoints, FUSE, Merkle-pruned path-level diff and full-tree tar export are
  implemented.

## Next vertical slices

Serialized API/mount/inspection sessions are implemented; integrate them with a
real host sandbox runner and measure resource use. Extend compressed-range
reading/cache limits, mounted failure injection, and review/export capabilities
before attempting concurrent live coherence. See `STATUS.md` for the ordered
follow-up list and `TESTING.md` for current validation.

Later, synchronize immutable objects and separately publish selected roots to a
second repository. Object existence must remain independent of physical location.
Concurrent root updates and authorization need explicit policies; block transfer
alone does not resolve them.
