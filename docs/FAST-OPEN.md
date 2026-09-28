# Fast opens and durable edits on full Linux history

Measured September 28, 2026 on Linux, Intel Core i7-1165G7 (8 logical CPUs),
about 40 GB RAM, Go 1.27. Raw samples, conversion statistics, and validation are in
`linux-fast-open-results.json`. This supersedes the interactive-open behavior in
`LINUX-INCREMENTAL.md`; those earlier measurements remain a historical baseline.

## Result

**The measured fresh-process interactive workloads meet the 100 ms target.**
The repository contains all 13,965,796 imported Git objects and 2,878 advertised
refs from the GitHub Linux corpus, not a shallow clone or HEAD-only fixture.

| Workload | Samples | Median | Minimum–maximum |
| --- | ---: | ---: | ---: |
| Open only (within read-process samples) | 20 | 2.92 ms | 2.53–3.20 ms |
| Fresh process: open, checkout, read Makefile, close | 20 | 6.34 ms | 6.07–6.65 ms |
| Fresh process: edit Makefile, durable publish, close | 20 | 12.92 ms | 8.78–19.73 ms |
| Fresh process: edit include/linux/sched.h, durable publish, close | 20 | 37.95 ms | 32.86–50.54 ms |
| Fresh process: ten-file edit, durable publish, close | 20 | 59.79 ms | 51.63–70.79 ms |
| Actual CLI: script edits Makefile with `run -publish` | 20 | 13.87 ms | 11.10–30.44 ms |

All process rows include startup and exit, opening the repository, and checkout.
Edits prepend a comment, shifting existing bytes. The ten-file workload uses
Makefile, init/main.c, kernel/sched/core.c, kernel/fork.c,
kernel/time/timekeeping.c, mm/memory.c, fs/open.c, fs/read_write.c,
include/linux/sched.h, and include/linux/mm.h. Each operation is a separate
process; there is no resident repository service or shared application cache.
Builds are outside the timed interval. Publish retains both object and root
`fsync`; no explicit checkpoint is needed between edits. Benchmarks are not
race-instrumented.

**OS caches were not flushed.** These are observed timings on this machine,
not a hard real-time bound or a cold-storage guarantee. Other storage, cache
states, larger edits, directory sizes, and compaction work can exceed 100 ms.

## What changed

- A persistent, paged native object index replaces reconstruction of the entire
  history index on every open. Native-ID and Git-ID lookup pages are compressed
  and checksummed, inside the same repository file.
- A fixed-size tail locator finds a small manifest. Normal open reads only the
  directories/pages needed for published roots, not millions of index entries.
- Checkout uses immutable lazy tree placeholders. Access materializes the touched
  path and that directory's index, rather than the complete filesystem tree.
  Forks keep independent lazy state and preserve copy-on-write isolation.
- Durable edits append small index runs and a new manifest. Four equal-tier runs
  merge into the next tier; the imported base index is not rewritten by ordinary
  edits. Decoded lookup-page caching is bounded to 128 pages.

No external index, Git pack, sidecar, mmap cache, or background daemon is required.
The in-file indexes are native derived records, not a retained Git index.

## Existing repositories: one-time conversion

With the new executable:

```sh
scs checkpoint repository.scs
```

For an older optimized file this must first load its old index, then build the
new paged index once. On an independent copy of the Linux artifact, the old open
took 17.575 seconds and conversion took 44.543 seconds. That migration is **not**
under 100 ms. Subsequent durable operations maintain the fast index automatically;
a no-change checkpoint does not rebuild or append a full index.

The converted file grew from 12,130,606,587 to 12,145,419,127 bytes, while native
body counts and encoded body bytes stayed unchanged. After all benchmark edits
it was 12,146,925,292 bytes. The original `.work/linux-gh-run6.scs` was left
unchanged; conversion and edits used `.work/linux-fast-open.scs`, with edits in a
separate `fast-cycle` workspace. The original main and experiment roots survive.

This extends the experimental SCSREPO3 format with record kinds 12–15. New readers
still accept the older checkpoint representation; old binaries do not understand
these new records. SCSREPO2 behavior is unchanged. Keep a backup before upgrading.

## Integrity and limits

Normal open is deliberately lazy, **not a whole-history verification**. Index
pages are checked as accessed, and native body reads retain physical/canonical
verification. Corruption in an untouched page or descriptor can surface later.
`PathsWithError` exposes traversal failures; failed lazy loads prevent publication.
Missing/torn tail locators fall back to physical scanning, not a search through
arbitrary user payload bytes. Complete corrupt records are errors.

Validation of the converted, edited Linux copy passed:

- Exact native body bytes and canonical Git identity checks against an independent
  Git `cat-file` process for all 2,878 advertised ref targets.
- `OpenVerified` physical scan: **35.586 seconds**. Indexed storage/Git statistics
  and named roots match the independent physical scan.
- Original main workspace: 102,326 paths and 95,943 files, with original Makefile
  unchanged; edited Makefile persisted across reopen and verified checkout.
- Full race suite, including every byte-truncation boundary of a small incremental
  publication, deferred descriptor/index corruption, lazy fork isolation,
  incremental Git SHA-1/SHA-256 aliases, and legacy-checkpoint migration.
- `go vet ./...`, formatting check, and `go mod tidy -diff` passed.

This does not claim reconstruction of every historical body: use `Scrub(true)`
for that stronger, more expensive check. Full traversal, global search, Git-ID
enumeration, large-directory loading, and full validation are not 100 ms targets.
Neither is arbitrary long-run compaction guaranteed to fit that budget.

The earlier full-import size/time results in `LINUX-OPTIMIZED.md` have not been
remeasured with this index. In particular, the combined ≤2× Git size/import-time
target remains unmet; interactive latency improvement is a separate result.

## Follow-up: measured process memory

Five additional fresh processes per workload measured peak resident set size
using Linux `wait4` / `ru_maxrss`. These use an independent copy after the latency
benchmark edits, with build/copy costs excluded. Raw samples are in
`linux-fast-memory-results.json`. The temporary copy was removed afterwards.

| Workload | Median peak RSS | Observed peak RSS range |
| --- | ---: | ---: |
| Open, checkout, read Makefile, close | 10.8 MiB | 10.8–11.0 MiB |
| One-file edit and durable publish | 12.7 MiB | 12.3–13.6 MiB |
| Deep-path edit and durable publish | 24.6 MiB | 24.1–24.6 MiB |
| Ten-file edit and durable publish | 37.3 MiB | 35.6–45.0 MiB |

RSS includes the process runtime and resident allocations, not the system-wide
filesystem page cache. These short operations do not represent a memory ceiling
for long-running handles, full imports, whole-tree searches, or full scans.
The native body-payload cache can retain up to 256 MiB plus bookkeeping; loaded
workspace metadata, new session objects, index directories, decoder state, and
object buffers require additional memory. Only decoded lookup pages have the
separate 128-page cap. Lazy open does not allocate an entry for every historical
object.

The follow-up also observed a **121.8 ms** deep-path operation (inside the child,
excluding process startup/exit); the other four deep-path samples were
40.6–47.1 ms. Thus the original 100 latency samples all met the target, but
additional observations confirm that **100 ms is not an absolute upper bound**.
OS caches were not flushed in either set. The original timing table is retained
as the original measurement, not presented as a guarantee.
