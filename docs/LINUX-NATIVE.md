# Full Linux history: original v2 native ingestion

This records the historical **SCSREPO2** experiment. The CLI now creates
SCSREPO3; see `OPTIMIZED-STORAGE.md` for the newer representation and
`LINUX-OPTIMIZED.md` for its measured results. The original
artifact and measurements below have not been rewritten.

This is the **full advertised history** experiment, not the earlier HEAD-only
workload in `LINUX.md`. No shallow/filter request was used. The remote was
`https://github.com/torvalds/linux.git`, with HEAD
`fd179f8a05be3ccae366b9b96e176b51fbe54aab`, 2,878 advertised refs,
and transport checksum `2d2f6d93391a68d0d3c2e2de879a7a348bb5f8b5`.
This pins the measured input rather than implying a permanently current HEAD.
Full history here means objects reachable from the server's advertised refs;
it does not claim hidden refs, unreachable objects, or external submodule history.

**Completed successfully:** all 13,965,796 transport objects were reconstructed,
Git-hash checked, stored as native deduplicated chunks and typed records, and
validated for typed graph closure before publishing the native Git catalog.
No pack/index was embedded or retained in SCS. The external transport spool was
deleted after native reopen, ref-body verification, checkout and isolation checks.
The native artifact remains at `.work/linux-native.scs` in the experiment workspace.

## Object inventory

| Type | Objects | Canonical body bytes |
| --- | ---: | ---: |
| Commit | 1,723,992 | 1,613,310,874 |
| Tree | 8,291,179 | 26,296,455,108 |
| Blob | 3,949,681 | 175,151,648,212 |
| Annotated tag | 944 | 554,266 |
| **Total** | **13,965,796** | **203,061,968,460** |

Refs include lightweight tags and other advertised refs, so ref count is not
annotated-tag-object count. Canonical bytes exclude Git's hash-framing header.

## Actual native storage efficiency

All GB figures below are decimal. Exact counters are also checked into
`linux-native-results.json`.

| Quantity | Bytes / count |
| --- | ---: |
| Temporary Git transport pack | 8,634,987,626 bytes (8.63 GB) |
| Reconstructed canonical bodies | 203,061,968,460 bytes (203.06 GB) |
| Unique native chunk payload | 122,619,581,189 bytes (122.62 GB) |
| Unique native chunks | 37,921,748 |
| Native content objects, including descriptors/catalog | 51,887,545 |
| **Native repository before checkout** | **127,057,736,725 bytes (127.06 GB)** |
| Headers, descriptors, catalog, format overhead | 4,438,155,536 bytes |

- Chunk deduplication: **1.656×** (logical / unique chunk payload).
- End-to-end native storage factor: **1.598×** (logical / complete native file).
- Native storage relative to the wire pack: **14.714× larger**.

**Conclusion:** the current fixed-4-KiB, uncompressed native format does deduplicate
Linux history, but it is dramatically less space-efficient than Git's compressed
delta pack. This is not a claim that retaining a Git pack demonstrates native
deduplication. All reported native bytes are actually reconstructed native storage.
A 4 KiB boundary shifts after insertions, and the format lacks compression and
similarity-based deltas. Those are important limitations, not measurement exclusions.

## Time and memory

- Receive and verify transport: 932.216 s (15.54 min).
- Decode, hash, ingest, check closure, publish: 5,124.649 s (85.41 min).
- Reopen, scan and validate the entire native file: 229.128 s (3.82 min).
- HEAD checkout, path inspection, snapshot publication and fork/edit isolation:
  11.424 s combined (not a pure checkout-only benchmark).
- Sampled peak Go heap during import: 18,677,609,208 bytes (18.68 GB).

The run used `GOMEMLIMIT=24GiB` on a Linux host with roughly 40 GB RAM and enough
free disk for the native file plus external scratch. The heap figure is sampled,
not peak RSS; Go's memory target is not a hard resource quota. Timings are one
observed run, not controlled repeated benchmarks. Import used the then-built
native importer; the subsequent reopen/checkout used the completed native APIs.

## Validation performed

1. Independently verify transport size and trailer before native decoding.
2. Recompute every decoded object's canonical Git hash while ingesting its body.
3. Require all commit/tree/tag edges to resolve with the expected object type;
   external gitlinks are excluded by design. Require native unique-object count
   and decoded transport count to agree before publishing refs.
4. Close and reopen the native file: scan record checksums, validate native chunk
   references, and confirm counts and storage statistics match the import report.
5. Rehash native reconstructed bodies for all 2,878 advertised ref targets,
   HEAD and its immediate parents. This is additional object-root verification,
   not a second rehash of all 203 GB of canonical bodies.
6. Check out HEAD from native objects: 102,326 paths, 95,943 regular files,
   1,640,936,478 regular-file bytes. Publish `main`.
7. Confirm checkout added **zero body chunks**. The file grew to
   127,103,919,376 bytes from workspace descriptors, directory indexes and snapshot
   metadata; unique chunk count and payload bytes stayed unchanged.
8. Fork the snapshot, edit `Makefile`, publish `experiment`, and confirm the original
   workspace's bytes are unchanged. The source commit's native body is immutable.
9. Delete the external transport spool after these checks succeeded.

HEAD's parents:

- `fddfc3ec31799a932bb92f1b8a84cb3d1f963be9`
- `113dcdfadf30ea11fbbdfcd4f6ea87687655cc5b`

Published HEAD workspace snapshot:
`6c29b5f409e5a5a151ff9852fac6a367523f7ec73adcf2faa5bced3a622357c3`.

Separate automated fixtures compare **every** imported object byte-for-byte with
Git's `cat-file` output, including REF/OFS delta histories, binary data, signed
metadata, nested tags, tags to blobs/trees, symlinks, executables and gitlinks.
Failure tests cover corrupt transport and missing graph closure. Native SHA-1 and
SHA-256 identity namespaces, chunk sharing, random reads, immutable reader views,
reopen and fork isolation are tested. The race-enabled suite and `go vet ./...`
passed. Git is an independent fixture server/oracle, not the native implementation.

## Reproduction

```sh
go build -o scs ./cmd/scs
GOMEMLIMIT=24GiB ./scs clone -name linux \
  https://github.com/torvalds/linux.git linux.scs > report.json
./scs git-info -catalog linux linux.scs > native-info.json
./scs git-parents -catalog linux linux.scs HEAD
./scs git-checkout -catalog linux linux.scs HEAD main
./scs fork linux.scs main experiment
```

The remote will evolve, so later object counts need not match this pinned run.
The experiment separated ReceivePack and ImportPack using caller-owned external
scratch to avoid downloading again after an implementation failure; the CLI
performs both stages and automatically removes its temporary spool. See `GIT.md`
for exact storage semantics, API constraints and resource limitations.
