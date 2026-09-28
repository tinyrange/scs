# Optimized native storage (SCSREPO3)

The `clone` CLI now creates SCSREPO3. Go callers select it explicitly with
`repo.CreateOptimized`; `repo.Create` retains the original SCSREPO2 block format
for compatibility and comparison. Both formats remain readable. No existing
repository is silently rewritten. V3 is experimental, not an interchange promise.

## Native identity versus physical encoding

V3 introduces immutable native bodies. A body's ID is SHA-256 over its native
kind and **reconstructed bytes**, independent of compression, its base, and Git
identity. Bodies use Zstandard-compressed native literal/copy extents. Copies
refer to byte ranges in a previously stored native body by native SHA-256 ID.
The format has its own instructions, checksums, base references, and index.
There is no retained Git pack/index, zlib stream, or Git decoder in the read path.

The transport scanner translates Git copy/insert instructions into these native
extents. Every body is reconstructed and canonically Git-hashed before native
publication; this is not blind retention of the transfer representation.
Git identities are embedded in typed bodies when possible; a separate native
compatibility descriptor handles additional identities for an existing body.
Commits, trees, blobs and tags all use the same native representation. Their exact
original bytes, including signatures and names, are preserved.

A base chain is at most 16 native delta edges. At that limit the next body is
materialized and compressed as literals. The encoder chooses a delta only when
its uncompressed instruction stream is smaller than a literal stream. This is a
fast heuristic, not a globally optimal compressor. A bounded 256 MiB body-payload
cache accelerates base reuse; cache bookkeeping uses additional memory. Individual
objects are limited to 1 GiB, and transport deltas to 1,048,576 instructions.

For transport spools supporting independent `ReadAt` calls, a scanner feeds
bounded batches of object boundaries and dependency links while up to six
workers ingest ready objects (bounded by `GOMAXPROCS`). Each worker has an
independent transport reader and Zstandard
encoder. Children become runnable only after their base has been committed.
Hashing, reconstruction from fetched base bytes, and compression run outside the
repository lock; native append/index updates and base-cache reads remain locked.
Semantic object parsing runs in workers; shared edge-type and closure accounting
is serial, and no refs are published until the whole import
passes validation. Failure cancels and joins all workers. Generic seekable inputs
without `ReadAt` retain the serial importer.

The dependency table is proportional to object count and has pointer-free
records. The scanner queues at most two 1,024-header batches; inflated bodies
are not retained for the entire pack. It inflates once to locate object boundaries;
workers independently inflate scheduled objects again. At most six jobs
are in flight, but each can use several object-sized buffers; the per-object
1 GiB limit is **not** a 1 GiB total import-memory limit. Indexes, the body cache,
worker buffers, and dependency tables require additional memory.

## Workspaces and edits

Checkout shares native body IDs without copying payloads. The old block-backed
entries still work. Reads of body-backed entries currently reconstruct the entire
body on first access, so a small random read can decompress more than its requested
range; this is not yet a fine-grained extent paging implementation.

Writes up to 16 MiB in optimized repositories use the same body engine. Replacing
a body-backed file shares its unchanged prefix and suffix, even after insertions
shift every 4 KiB boundary. Larger streamed writes fall back to the original block
path. Matching multiple separated edit regions, content-defined chunking, and
background optimal compaction are not implemented. Fork and read-only capability
semantics are unchanged; immutable readers retain the previous file version.

## Writer, index, and durability

The optimized writer batches appends in a 1 MiB buffer, maintains its append
offset, and flushes before required disk reads and publication. Publication retains
the existing object-sync-before-root and root-sync ordering. A failed write poisons
the handle; reopen is required.

`Repository.Checkpoint()` builds an immutable sorted base index inside the
**same file**. Native-ID lookup pages hold locations; Git-ID lookup pages hold
ordinals into native pages. Pages contain up to 1,024 entries, are compressed and
checksummed, and are addressed through a checksummed run directory. A small
manifest records runs, catalogs, published roots, and aggregate statistics; a
fixed 85-byte terminal locator addresses that manifest. Record kinds 12–15 extend
the experimental V3 format; older binaries cannot read these records.

After this one-time build, each durable sync seals pending changes into a small
sorted run and appends a new manifest/locator. Four equal-tier runs merge into
the next tier; the imported base remains immutable during ordinary edits. Old
runs remain physical records; this is not whole-file garbage collection. The
bounded decoded lookup-page cache holds at most 128 pages. Normal open loads
only index directories and pages required for named roots. Checkout loads lazy
placeholders and materializes touched paths, including each visited directory's
own index, rather than every filesystem descendant.

Older optimized files can be upgraded once:

```sh
scs checkpoint repository.scs
```

Opening/building the old full index during conversion is still expensive.
Subsequent durable writes maintain the new index automatically; a no-change
checkpoint does not rebuild it. New native imports call checkpoint automatically.
The earlier published full-import timing/size measurements used the old index
and have not been remeasured. See `FAST-OPEN.md` for migration and interactive
measurements, with raw results and explicit cache policy.

Missing/torn terminal locators recover through complete-record scanning and
incomplete-tail truncation, not by finding locator-like bytes in arbitrary user
payloads. Complete corrupt records are errors. Index pages are verified when
accessed, so corruption in an untouched page can be detected later. A failed
index lookup poisons writes; a failed lazy tree load prevents publication.
Derived indexes are not a substitute for full physical verification.

## Verification semantics

- `repo.Open`: validates the manifest and accessed index pages when a persistent
  index is available; otherwise loads a legacy checkpoint or scans. Workspace
  metadata is lazy on the paged-index path; untouched history is not scanned.
  Native body reads verify physical checksum, base bounds/depth, and canonical ID.
  A per-handle proof cache remembers the verified physical-encoding fingerprint:
  unchanged encodings need not rehash reconstructed bytes again. Changed encodings
  must pass canonical verification again. Cached immutable payloads can be reused.
- `repo.OpenVerified`: ignores derived lookup indexes and verifies every physical record
  and stored native base linkage. It does not reconstruct every historical body.
- `scs check`: uses the verified-open path and validates published workspaces.
- `Repository.Scrub(true)`: additionally reconstructs and checks every native body.
  This currently builds a second index during scanning; allow additional memory.
- `VerifyGitObject`: additionally verifies one framed canonical Git identity.

Normal open is therefore deliberately **not a full scrub**. The legacy v2 open
continues scanning every record. Strict whole-body verification remains available
rather than being silently discarded for a benchmark advantage.

## Scope of the performance target

The goal is at most twice the Git size and ingestion time on a measured full Linux
corpus, including native indexes and metadata. It is not a universal guarantee for
all repository sizes, random reads, or all hardware. Fixed per-object metadata can
be expensive on very small inputs, and bounded delta depth trades size for read
work. Timings must compare both implementations on the same verified pack; network
receive time is separate. Measurements are recorded separately from these format
and implementation descriptions.
