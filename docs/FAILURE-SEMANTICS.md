# Storage failures and recovery

A storage error is not a rollback guarantee. In particular, failure at the final
fsync can leave a complete new root visible in the file while the caller receives
an error. Reopen before deciding what state was recovered.

## Runtime contract

- Write counts are checked. Short writes become `io.ErrShortWrite`, including a
  writer that incorrectly returns a short count with no error.
- A failed append, buffered flush, or durability barrier poisons the repository
  handle. Subsequent normal operations return an error requiring reopen.
- `Refs()` reports the last successfully acknowledged in-memory names; failed
  publication does not advance that map. This does not assert that the physical
  file lacks a later complete publication.
- `Close()` releases the file, but preserves the original poisoned error. It does
  not retry buffered writes or syncs after poisoning. The first close reports the
  failure; a repeated close of an already closed handle remains idempotent.
- Reopening repairs only an incomplete physical tail. Complete bad checksums are
  corruption, not grounds to silently discard committed data.
- Recovery may expose the old or fully published new root. Never a half-tree.
  Independent named roots must remain intact.

Keep backups and handle close/unmount errors. After failed publication, close the
failed handle, reopen/check the recovered candidate, and explicitly decide how to
resume; do not blindly retry through a poisoned handle. A CAS conflict is a
logical conflict rather than storage poisoning and has a separate resolution
policy. In-memory candidate data and durable named roots are distinct.

## Validation and boundaries

The fault-injection matrix in `repo/io_failure_test.go` writes to real temporary
files, including actual partial prefixes, while returning injected ENOSPC,
short-write, and sync-EIO failures. It exercises eager V2/V3, paged V3, and removal
of legacy V3 checkpoints. For each failure it inspects both current visible bytes
and a separately saved image from the last successful sync barrier.

The saved image models complete loss of post-barrier data, not every possible
sector reorder/tear. The host disk is not actually filled and hardware failures
are not induced. Existing byte-cut recovery and mounted process-crash tests are
complementary; none is a production durability certification.
