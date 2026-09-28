# Native Git history

**Storage update:** the clone CLI now creates optimized SCSREPO3; see
`OPTIMIZED-STORAGE.md`. The 4 KiB representation and original Linux numbers below
describe SCSREPO2, still available via `repo.Create`. Native identities and exact
Git-byte preservation apply to both.

The original v2 full Linux measurement is in `LINUX-NATIVE.md`, with exact counters
in `linux-native-results.json`. Optimized results and the remaining speed gap are
in `LINUX-OPTIMIZED.md` and `linux-optimized-results.json`.

`scs clone` speaks upload-pack directly through Go transport code. It requests
all advertised refs with **no depth, filter, or thin-pack**. No `git` executable
is used by this path. The older `scs import` command still imports just one
committed tree from a local clone and uses Git as its input adapter.

```sh
go build -o scs ./cmd/scs
./scs clone -name git https://github.com/torvalds/linux.git linux.scs
./scs git-info linux.scs
./scs git-cat linux.scs HEAD
./scs git-parents linux.scs HEAD
./scs git-checkout linux.scs HEAD main
./scs fork linux.scs main experiment
./scs run -workspace experiment -publish linux.scs edit.star
```

## What is actually stored

The pack stream is **external temporary transport scratch**, never an SCS object.
The normal Clone API removes it on success or failure. The separate ReceivePack
and ImportPack APIs allow a caller to manage scratch explicitly for experiments
and retries. Abrupt termination may leave caller/OS temporary files to clean up.

Every reconstructed commit, tree, blob and annotated tag is ingested as its exact
canonical body. In SCSREPO3 those bytes are represented by compressed native
literal/copy extents, with bounded native base references and native SHA-256 IDs.
The Git identity is embedded in the body record when possible, or represented by
an additional native descriptor. Native workspaces share the same body IDs.

The legacy SCSREPO2 path splits bodies into native 4096-byte chunks (with a short
final chunk) and stores ordered chunk IDs in a typed descriptor. Both formats
have **no retained Git pack, Git delta instructions, pack index, zlib stream, or
hidden Git directory**. V3 does have its own native delta chains and derived index.

Original Git object IDs are a compatibility namespace, not native storage IDs.
Ingestion recomputes Git's framed object hash; native records separately hash
their kind and payload. The object API supports SHA-1 and SHA-256 namespaces; the
current **network pack decoder supports SHA-1 repositories only** and rejects
other advertised object formats. SHA-1 hashing uses collision detection on native
ingestion. V2 reopening scans record checksums and block links; V3 normal open
can use an embedded paged index with validation on access and lazy workspace
metadata. Existing optimized files can upgrade with `scs checkpoint`; subsequent
durable edits maintain incremental indexes automatically. See `FAST-OPEN.md`. `OpenVerified` scans all physical records
and native links, while `Scrub(true)` also reconstructs all bodies. `VerifyGitObject`
additionally recomputes one reconstructed Git body's identity. See
`OPTIMIZED-STORAGE.md` for exact verification and proof-cache semantics.

Commit parent order, tree bytes, executable and symlink modes, gitlinks, encodings,
unknown headers, multiline signatures and annotated tags pointing to commits,
trees, blobs or other tags survive without reserialization. Cryptographic
signature verification is not implemented. Gitlinks refer to external repositories
and are not required to exist in the imported object graph. Lightweight tags are
refs, not additional objects.

Before publishing the immutable Git catalog, the importer validates typed graph
closure (excluding external gitlinks), decoded object counts, advertised refs,
and the pack checksum. Failed imports may leave reusable unreferenced native
objects but do not publish the catalog. Git catalogs and editable workspace names
are separate. The importer currently expects one complete pack's unique objects
in a repository; it is not a general incremental fetch/merge implementation.

## Native use

`OpenGitObject` provides a seekable body reader (V3 currently reconstructs the
whole body on first access, not fine-grained paging). `ReadGitObject` is the
whole-body convenience API. `GitCommitParents`, `ParseGitTree`, `ResolveGit` and
`PeelGit` expose the imported graph without invoking Git. Full IDs, HEAD, full
refs, and branch/tag shorthand resolve; arbitrary Git revision expressions do not.

`CheckoutGit` makes an editable native workspace from a commit or tree, peeling
annotated tags. Files and symlinks share existing native body or chunk IDs without copying
body bytes. Gitlinks become non-traversable `gitlink` entries with `git_oid` in
stat results. Forks and snapshots retain ordinary native workspace isolation.
Names not representable by the workspace's UTF-8/path rules remain lossless in
raw Git objects, but checkout rejects them explicitly. Unusual file modes outside
100644, 100755 and 120000 likewise require raw object access.

## Limits and measurement

HTTP(S), git:// and SSH transports are admitted; integration coverage includes
local smart HTTP and a real HTTPS Linux transfer, not every authentication setup.
There is no push, incremental fetch, shallow clone, partial clone, or automatic
submodule checkout or garbage collection. V3 adds compression and an embedded
lookup checkpoint; V2 has neither. In-memory indexes and decoder metadata can be
large. V3 checks declared object sizes before inflation and uses bounded worker
and header queues, but per-object limits are not total memory quotas. Network
input is not an OS-level sandbox. Use external process memory/disk limits for
untrusted servers.

Defaults are a 64 GiB transport limit, a 512 GiB native-file guard, and a 1 GiB
reconstructed-object ingestion guard. The native file guard is sampled and may
overshoot. `GOMEMLIMIT` is a Go runtime target, not a hard memory quota. Repository
opens are exclusively locked. This remains an experimental format: new Git record
kinds 7 and 8 and directory entry kind 4 are not readable by older implementations.

Report quantities deliberately distinguish:

- transport pack bytes (wire baseline only);
- logical bytes (sum of all decoded canonical object bodies);
- native body count, canonical and encoded body bytes, and maximum delta depth;
- legacy unique chunk payload bytes and chunk count;
- actual native file bytes, including descriptors, record headers and checkpoints;
- native content-object count, elapsed import time and sampled peak Go heap.

For V2, chunk deduplication factor is logical bytes / unique chunk payload bytes.
End-to-end native storage factor is logical bytes / native file bytes. Comparing
native file bytes / transport pack bytes shows the cost relative to Git's packed
representation, not a deduplication claim. A size comparison with an installed
Git repository must include Git's index files too. Small metadata records can
grow; V2's fixed 4 KiB boundaries do not provide similarity-based deltas. V3's
bounded native chains trade space for bounded reconstruction work.
