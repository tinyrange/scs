# Full Linux history: optimized native storage

Measured September 27, 2026. This is full advertised history, not a HEAD-only,
shallow, or filtered clone. **The size target passes; the 2× conversion-time target
is not yet met.** Exact machine-readable counters are in
`linux-optimized-results.json`. The original SCSREPO2 experiment and its retained
127 GB artifact are unchanged; see `LINUX-NATIVE.md`.

## Final results

All GB are decimal. Native sizes include the embedded checkpoint, catalog,
physical headers and Git compatibility metadata. Git sizes include the installed
pack, indexes, and bare-repository scaffold.

| Corpus | Objects / refs | Git bytes | Native bytes | Size ratio | Git seconds | Native seconds | Time ratio |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Full GitHub advertised history | 13,965,796 / 2,878 | 9,178,045,786 | 12,101,453,606 | **1.319×** | 300.736 | 759.262 | **2.525×** |
| kernel.org advertised history | 11,844,313 / 948 | 3,679,280,138 | 7,271,507,795 | **1.976×** | 184.244 | 405.412 | **2.200×** |

The full GitHub corpus has the same advertised ref map, HEAD, object inventory,
and 203,061,968,460 canonical body bytes as the original experiment. Its native
file fell from **127.06 GB to 12.10 GB** (about 90.5% smaller), and conversion fell
from **5,124.65 s to 759.26 s** (about 6.75× faster). These old-versus-new times are
single observed experiments, not a controlled statistical speedup estimate.

The smaller kernel.org transfer is an additional, more tightly compressed test,
not a substitute for the original full GitHub corpus. Both HEADs are pinned to
`fd179f8a05be3ccae366b9b96e176b51fbe54aab`. Full history means all objects reachable
from the server's advertised refs; it does not include hidden/unreachable objects
or external submodule history.

### Transport identity

| Corpus | Transport bytes | Pack checksum |
| --- | ---: | --- |
| GitHub | 8,634,988,082 | `1fc8d2bf8a7236883c16dd8483a589c976e05d40` |
| kernel.org | 3,246,684,402 | `5de2137cad9d243648915f21a4dff26cb35482ec` |

The GitHub redownload has a different pack checksum from the original v2 run;
pack byte layout is not object-set identity. The refs and per-type inventories
were compared explicitly. Relative to **pack bytes alone**, native size is about
1.40× for GitHub and 2.24× for kernel.org. Do not mistake the 1.976× installed-Git
comparison for a claim of staying within twice kernel.org's wire-pack size.

## Method and caveats

- Linux, Intel Core i7-1165G7, eight logical CPUs, roughly 40 GB RAM;
  Go `go1.27.0-X:nodwarf5`, Git 2.55.0.
- Each pair used the same complete verified transport spool. Git and native
  benchmark jobs ran sequentially, without competing benchmark/test processes.
- Git baseline: fresh bare repository, then
  `git -C baseline.git -c core.fsync=all index-pack --stdin < transport.pack`.
  Git chose its default thread count. The baseline uses default canonical object
  hash validation, not `--strict`; refs were not installed in the Git baseline.
- Native elapsed time includes independent spool checksum verification,
  dependency scanning, inflation, collision-detecting Git hashes, canonical native
  hashes, compression, typed graph closure, catalog publication and checkpoint.
  Native validates typed closure in addition to Git's default index-pack checks.
- The final importer uses up to six workers plus a bounded streaming scanner.
  It still inflates transport entries twice: once to locate boundaries and once
  in a scheduled worker. No validation was disabled to improve the numbers.
- Native runs used `GOMEMLIMIT=20GiB` and CPU profiling. Profiling overhead is
  included. No CPU affinity, cold-cache reset, or repeated statistical trials
  were imposed; disk cache state, thermals and scheduler variation can matter.
- Network receive time is **excluded** from both conversion comparisons. Recorded
  transfers took 1,042.03 s for GitHub and 2,812.66 s for kernel.org. Adding these
  shared network costs would mask the remaining conversion-time regression.
- Sampled peak Go heap was 11,747,941,760 bytes for GitHub and 11,016,585,464 bytes
  for kernel.org. These are not peak RSS. Git's measured child-process peak RSS
  was 1,626,316,800 and 1,127,276,544 bytes respectively. Memory parity is not claimed;
  `GOMEMLIMIT` is not a hard quota.

## What changed

SCSREPO3 stores Zstandard-compressed **native** literal/copy extents, with
canonical SHA-256 body identities and a maximum native base depth of 16. It uses
binary in-memory keys, batched appends, embedded typed Git identities, an in-file
compressed lookup checkpoint, immutable-payload caching, and verified-encoding
proof caching. A bounded dependency scheduler parallelizes hashing, encoding and
semantic parsing while preserving base-before-child publication. All original
commit, tree, blob and tag bytes remain available without Git.

There is **no Git pack, pack index, Git delta decoder or zlib stream retained in
an SCS file or required by its read path**. Independent Git baseline repositories
are external benchmark oracles, not hidden native backing stores. Workspace
checkout shares native body IDs. Prefix/suffix edits reuse native byte ranges,
including insertions that shift block boundaries. See `OPTIMIZED-STORAGE.md` for
format, verification, memory and random-read limitations.

### Profile-driven progression

| Kernel trial | Conversion seconds | Native bytes | Change |
| --- | ---: | ---: | --- |
| Initial optimized encoding | 1,246.648 | 7,508,729,986 | Native compression and checkpoint |
| Binary indexes / verification caches | 992.911 | 7,271,747,710 | Remove GC-heavy string indexes and repeated hashing |
| Four-worker dependency scheduler | 538.189 | 7,271,503,400 | Parallel canonical hashing and compression |
| Streaming scan / worker graph parsing | 411.178 | 7,271,503,279 | Overlap scanning; avoid allocating unused tree names |
| Final six-worker pipeline | 405.412 | 7,271,507,795 | Use additional available CPU capacity |

Each row followed implementation changes; these are not repeated samples of the
same build. The final GitHub trial improved only slightly over the four-worker,
two-pass trial (763.761 s to 759.262 s). This is why the kernel speedup must not be
extrapolated to the original corpus.

For fresh-process open, durable incremental edits, and checkpoint rebuild latency,
see `LINUX-INCREMENTAL.md`. Millisecond edit timings do not include opening or
rebuilding the full lookup index.

## Remaining speed gap

The strict limits would be 601.473 s for GitHub and 368.488 s for kernel.org.
The implementation still exceeds them by 157.789 s and 36.924 s respectively.
The final GitHub CPU profile is dominated by collision-detecting SHA-1, native
SHA-256, Zstandard encoding and repeated transport inflation; shared base reads
and index updates also serialize work. Simply adding workers did not solve this.
A further iteration must reduce actual decoding/reconstruction work and lock
contention without dropping hash verification or retaining a Git pack.

Fine-grained native extent paging, content-defined chunking, multi-region edit
matching, and optimal compaction are still not implemented. The current result
is a substantial native-storage improvement, **not completion of the combined
2× size-and-time goal**.

## Validation of the final artifacts

Both final imports and validations completed successfully:

1. Independently checked the full transport checksum and canonical Git hash of
   every ingested object; required exact decoded/unique counts and typed graph
   closure before catalog publication.
2. Closed and reopened each checkpoint, requiring statistics to match the import
   report. Independently compared raw bodies with `git cat-file --batch` for every
   advertised ref target plus approximately every 9,973rd historical object ID,
   and rechecked each sampled Git identity: **4,279 comparisons for GitHub** and
   **2,136 for kernel.org**. These are samples of historical bodies, not a claim
   of independently byte-comparing every historical body a second time.
3. Checked out and published HEAD: **102,326 paths, 95,943 regular files,
   1,640,936,478 regular-file bytes** for both. Checkout added zero native bodies
   and zero blocks; it shared the imported native data.
4. Forked the native workspace, inserted a prefix into `Makefile`, published the
   edited fork, and verified the original bytes remained unchanged.
5. Wrote a fresh checkpoint, closed, and reopened through `OpenVerified`, checking
   every physical record and native base linkage. Reopened the original workspace
   and checked isolation again. This is a full physical scan, not an exhaustive
   post-import canonical-body reconstruction (`Scrub(true)` performs that).

| Operation | GitHub seconds | kernel.org seconds |
| --- | ---: | ---: |
| Checkpoint reopen | 15.700 | 13.534 |
| Full physical-record scan | 35.574 | 26.724 |
| HEAD checkout, inspection and publication | 1.862 | 2.278 |
| Fork edit and publication | 0.00234 | 0.00312 |

After checkout, the isolated edit, and a fresh checkpoint, the final files are
12,130,606,587 bytes (GitHub) and 7,299,997,036 bytes (kernel.org). Both remain below
twice the measured installed Git size. Intermediate post-checkout sizes are lower
because appending native workspace metadata first discards the terminal **derived
checkpoint**, not immutable bodies or published roots. The final figures include
its replacement.

Final native artifacts in the experiment workspace:

- `.work/linux-gh-run6.scs` — original full GitHub corpus, `main` and `experiment`.
- `.work/linux-opt-run6.scs` — additional kernel.org corpus, same workspaces.
- `.work/linux-native.scs` — original v2 artifact, left untouched.

The race-enabled test suite, `go vet ./...`, clean module-tidy diff, Go formatting
check and CLI build passed. Fixtures cover every raw object against Git, signed
metadata, tags to all object types, SHA-1/SHA-256 identity namespaces, native
corruption, checkpoint recovery, shifted-boundary edits, REF/OFS dependencies,
forward and branching references, cancellation, and failed-import nonpublication.
A gated transport test requires ingestion to start before scanning finishes.

## Reproduction

Ordinary native cloning (no Git executable required by this path):

```sh
go build -o scs ./cmd/scs
GOMEMLIMIT=20GiB ./scs clone -name linux REMOTE_URL linux.scs > report.json
./scs git-checkout -catalog linux linux.scs HEAD main
./scs fork linux.scs main experiment
./scs checkpoint linux.scs
./scs check linux.scs
```

For a fair conversion comparison, use `gitstore.ReceivePack` once to create a
caller-owned temporary spool and its `Download` metadata; run Git's baseline
command above and `gitstore.ImportPack` against that same spool sequentially.
Create the native destination with `repo.CreateOptimized`. Include checkpoint
and publication in native timing, keep the Git reference repository outside SCS,
and exclude the shared network receive time from both conversion measurements.
Never feed a new pack with stale download metadata.

The experiment harnesses remain in `.work/git-baseline.go`,
`.work/run-native-bench.go`, and `.work/validate-native.go`; their arguments select
artifact tags, manifest/pack inputs, and the independent Git oracle. Full reports,
logs and CPU profiles retain the `linux-opt-run6` / `linux-gh-run6` prefixes.
Live remote refs can change, so a fresh clone need not reproduce these counts.

After final validation, both caller-owned raw transport spools were deleted.
The independently installed Git baselines remain separately as benchmark oracles;
future conversion trials can read their pack files with the matching manifests.
Native reads, checkouts and edits do not reference those external repositories.
