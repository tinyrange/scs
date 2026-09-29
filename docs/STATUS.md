# Implementation status and next work

This is the current implementation map; historical benchmark documents describe
specific older builds and must not be read as current feature inventories.

## Implemented

- Native immutable storage, forks, CAS publication, and torn-tail recovery.
- V2 block storage; V3 compressed bodies, paged indexes, lazy checkout.
- Linux cah FUSE mount with durable publication and documented POSIX limits.
- Serialized agent edit/mount/inspect sessions with new candidate isolation.
- `scs init` for Git-free native setup; session-token-bound host build receipts.
- Actual approved sandboxed C build/execute, compiler-failure, and timeout cases
  with post-inspection, ordinary unmount, artifact reopen checks, and source isolation.
- 4 KiB dirty-page overlays, aggregate overlay budget, atomic quota rejection,
  streaming/block-sharing ingestion, and buffer-usage accounting.
- Merkle-pruned content/mode/link diff with stable root capture and JSON hashes;
  full-tree tar export.
- Disposable opt-in mounted crash/shutdown/session tests and Linux CI checks.
- Storage-boundary fault injection for partial writes, ENOSPC, and failed syncs
  across V2, V3, paged V3, and legacy-checkpoint V3. Poisoned Close preserves the
  failure without retrying buffered writes. See `FAILURE-SEMANTICS.md`.

## Intentional boundaries

- Builds require an external sandbox and external process/resource management.
- API and mounted edits are serialized, not concurrently coherent.
- Candidate durability is distinct from build success and explicit acceptance.
- Overlay budget is not a total-memory limit: native body decoding, metadata,
  descriptor arrays, kernel buffers, and Go runtime overhead remain additional.
- One mount mutex; no production POSIX compatibility or stable format promise.
- Diff is path-level, not a text patch; tar is a full tree, not Git export.

## Next increments, in order

1. Generalize the now-validated disposable sandboxed build workflow into a
   production host runner: stronger descendant containment, host deadline/cancel
   handling, reusable orchestration, and admission/resource policy. The fixed C
   fixture covers real success, intentional compiler failure, and controlled child
   timeout with durable artifacts; it is not a general process supervisor.
   See `SESSION-BUILD-DEMO.md` and `session-build-results.json`.
2. Add bounded native compressed-range reads/cache accounting and visited-node
   eviction. Profile representative multi-file builds and repeated flushes before
   narrowing locks. Full-body decoding and block-list costs are still relevant.
3. Extend the new storage-boundary fault matrix to mounted error propagation,
   checkpoint truncate/seek failures, and a controlled filesystem/power-failure
   model. Current tests write real prefixes, inject ENOSPC/short-write/sync errors,
   and reopen both visible bytes and the last successful-sync image. They are not
   hardware power-loss certification or a real full-disk stress test.
4. Add text hunks/change bundles and explicit application or Git export. Merkle
   subtree/index pruning is implemented; lazy loading of only selected directory
   index pages remains a separate optimization.
5. Revisit conversion-time/memory targets with controlled repeated profiling.
   The historical <=2x conversion-time target remains unmet.
6. Design retention/GC/compaction and synchronization publication/conflict policy
   before implementing remote object transfer. No sync or GC exists yet.

No broad POSIX expansion or kernel boot test is required for the current session
milestone. Add unsupported filesystem operations when concrete workloads need them.
