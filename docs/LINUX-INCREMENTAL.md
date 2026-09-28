# Full Linux: open and incremental-edit latency

> Historical pre-paged-index baseline. See `FAST-OPEN.md` for the replacement
> implementation and fresh-process measurements below 100 ms.

Measured September 28, 2026, on the completed **full GitHub Linux** native
repository: 13,965,796 Git objects, 12,130,606,587 bytes including the published
`main` / `experiment` workspaces and checkpoint. This measures the current
implementation without changing repository code to improve the benchmark.
Raw samples and summaries are in `linux-incremental-results.json`.

**Conclusion:** edits are millisecond-scale once a repository/workspace is open,
but opening still takes seconds. Rebuilding the lookup checkpoint is expensive
and is not included in the small edit numbers.

## Results

| Operation | Median / observed time | Range | Samples |
| --- | ---: | ---: | ---: |
| Fresh-process `repo.Open`, existing checkpoint | **17.056 s** | 16.671–18.193 s | 5 |
| Load existing `main` workspace after open | **357 ms** | 335–421 ms | 5 |
| First `Makefile` body read in each new handle | **2.24 ms** | 1.74–2.78 ms | 5 |
| Fresh process: open + checkout + read + close | **17.568 s** | 17.329–18.844 s | 5 |
| Fork already-loaded clean workspace | **0.058 ms** | — | 1 |
| First one-file edit + durable publication, removing old checkpoint | **32.84 ms** | — | 1 |
| Subsequent one-file edit + durable publication | **2.249 ms** | 2.104–3.376 ms | 20 |
| Ten-file edit batch + one durable publication | **7.178 ms** | 6.561–23.781 ms | 20 |
| Reopen after edits, **without** a checkpoint | **36.109 s** | 35.782–37.406 s | 3 |
| Rebuild checkpoint in an open handle | **29.227 s** | — | 1 |
| Reopen after checkpoint rebuild | **17.703 s** | 16.396–18.643 s | 3 |

The first ten-file batch took **23.78 ms** because it included first body reads
for nine additional files. Later batches reuse the handle's body cache. The
first-write case includes discarding the terminal checkpoint; it is not hidden
among the faster steady-state samples. Each one-file edit touches an approximately
81 KB `Makefile`; the ten-file workload covers about **1.17 MB** of original data.

Median steady-state one-file time breaks down into approximately **0.375 ms** for
read/modify/write and **1.851 ms** for publication. Ten-file batches took
approximately **5.075 ms** for read/modify/write and **2.139 ms** for publication.
Medians of separate stages need not add to the median of their combined times.

## Why reopening gets slower after an edit

The current lookup checkpoint is a terminal derived index, not an incrementally
updated index. The first append removes it, without removing immutable objects
or published roots. Publication remains durable; no checkpoint is needed to
recover the edits. However, the next `Open` must scan the native record log until
a new checkpoint has been written.

Rebuilding the checkpoint emitted **954,185,362 bytes** and took 29.227 s. Running
that rebuild in a separate fresh process after edits took **64.644 s total**:
34.996 s to open the uncheckpointed repository, 29.227 s to rebuild, plus process
and cleanup overhead. Calling it on the already-open editing handle avoids that
extra open, but not the rebuild itself. `Close` does not rebuild it automatically.

Practical implications for the current implementation:

- A long-lived agent session pays opening/loading once, then performs millisecond
  incremental edits and publications.
- A process-per-edit workflow pays about 17–18 seconds initially and approximately
  36 seconds on subsequent uncheckpointed opens, before doing the edit.
- Rebuilding the whole index after every edit would replace one large cost with
  another. It is better amortized over a session, but an incremental/lazy index
  remains necessary to make short-lived CLI operations fast.

## Workload and method

- Same Linux host as `LINUX-OPTIMIZED.md`: Intel Core i7-1165G7, eight logical CPUs,
  roughly 40 GB RAM; Go `go1.27.0-X:nodwarf5`, `GOMEMLIMIT=20GiB`.
- Five fresh child processes opened the existing checkpointed source repository.
  No Go repository handle or in-memory index was reused between processes.
- A full independent native-file copy was then made for all mutations. Copy time
  is recorded separately and excluded from the latency table. The source's size
  and modification time were checked unchanged at completion.
- No OS cache flush was performed. The first observed open was **17.056 s**, but
  is not claimed to be a controlled cold-cache measurement. Repeated opens and
  the copy warm filesystem caches; edit-loop results also benefit from native
  body caching. No CPU profiler or race detector was enabled for timings.
- Open, workspace checkout, first body read, clean in-memory fork, mutations,
  durable publication, checkpoint rebuild and process wall time were timed
  separately. Whole-repository statistics scans are outside edit timers.
- Every edit prepends a unique short comment, shifting all subsequent bytes.
  One first edit, 20 further single-file edits, and 20 ten-file batches were run:
  **221 file versions and 41 durable publications**. These are small text edits,
  not arbitrary large rewrites, scattered changes, builds, or concurrent agents.
- Each publication includes the normal object-sync-before-root and root-sync
  ordering (`fsync`); these are not merely in-memory mutation timings. Ten-file
  batches publish once per batch, not once per file.
- The ten paths are `Makefile`, `init/main.c`, `kernel/sched/core.c`,
  `kernel/fork.c`, `kernel/time/timekeeping.c`, `mm/memory.c`, `fs/open.c`,
  `fs/read_write.c`, `include/linux/sched.h`, and `include/linux/mm.h`.
- All ten workload files in the loaded `main` workspace were compared with their
  pre-edit bytes. Every fresh post-edit open checked the edited `Makefile` hash
  and unchanged `main` snapshot, both before and after rebuilding the checkpoint.
  All persistence/isolation checks passed. The source `main` and `experiment`
  workspaces were not modified.

The 221 versions added **401,726 encoded native-body bytes**, plus directory,
file-descriptor, snapshot and ref metadata. This is not the full file-size delta:
checkpoint removal/replacement changes the file size independently, and a rebuilt
compressed index can vary in size. Retaining all these versions also means no
history was garbage-collected for the benchmark.

These are local observed samples, not tail-latency guarantees. Ranges are reported
rather than statistically unsupported percentile claims from a handful of opens.

## Reproduction and artifacts

The standalone harness is `.work/benchmark-incremental.go`. It builds once and
uses the resulting executable for all fresh child processes, excluding compilation
from measurements:

```sh
GOMEMLIMIT=20GiB go run .work/benchmark-incremental.go \
  .work/linux-gh-run6.scs .work/linux-incremental-new
```

The output prefix must not already have a `.scs` file. The harness creates an
independent `.scs` copy and a JSON report. The measured run remains at
`.work/linux-incremental-bench.scs` with the separate `incremental-bench` workspace,
and `.work/linux-incremental-bench.json`. No additional Linux download or Git pack
was needed. No repository implementation changes were made for this benchmark.
