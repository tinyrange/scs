# Workspace API contract (MVP)

## Host API

The Go package is `j5.nz/scs/repo`:

```go
r, err := repo.Create("project.scs") // exclusive creation; never overwrites
// or repo.Open("project.scs")
// Check err; defer r.Close().
w, err := r.ImportGit(ctx, "/path/to/clone", "HEAD")
id, err := w.Publish("main")
w, err = r.Checkout("main")
child, err := r.Fork(id)
```

`Repository.Empty()` starts a new unpublished tree. `Refs()` returns a copied map
of published names to snapshot IDs. `Drop(name, expectedID)` removes a name using
compare-and-swap. `Fork(snapshotID)` opens independent mutable state. Snapshots
are content-addressed states, not commits in a history DAG.

Native network history ingestion is exposed by `j5.nz/scs/gitstore.Clone`; see
`docs/GIT.md` for object, graph, tag, and checkout APIs. `OpenReader` and
`OpenGitObject` provide seekable streaming reads without whole-body allocations.

The workspace exposes `ReadFile`, streaming `WriteFrom`, `WriteFile`, `Replace`,
`ListDir`, `Paths`, `PathsWithError`, `Glob`, `Mkdir`, `Delete`, `Rename`, `Chmod`, `Stat`, `Symlink`,
`Readlink`, `Readonly`, `Snapshot`, `Fork`, and `Publish`. Entry block lists returned
by `Stat` are copies, not mutable aliases into workspace state.

Indexed optimized repositories load workspace metadata lazily. Checkout does not
validate every descendant; access can return a deferred corruption error. Use
`PathsWithError() ([]string, error)` for full traversal with explicit errors. The
compatibility `Paths()` returns nil on a traversal error, never a partial listing;
a failed lazy load also prevents publication. Glob and script search propagate
traversal errors. `OpenVerified` and `scs check` retain eager verification paths.
See `FAST-OPEN.md` for scope, migration, and measured performance.

`script.Run(ctx, workspace, filename, source, options)` executes bytes supplied by
the host. It does not load the filename from disk and does not publish implicitly.
The CLI reads a host-selected script file before invoking it. No host path is
available through the Starlark workspace itself.

## Paths and metadata

- Paths use `/`, are relative to the exposed tree, and accept `""` or `"."` for root.
- Absolute paths, any `..` component, NULs, backslashes, and invalid UTF-8 are rejected.
  Repeated separators and `.` components normalize. These restrictions intentionally
  exclude some otherwise legal Git filenames rather than silently altering them.
- Paths are limited to 4,096 bytes and at most 255 separators after normalization.
- Symlinks are stored with their target bytes but never followed. Reading or writing
  through a symlink fails, including when it is a path's parent.
- Gitlinks are non-traversable entries with kind `gitlink` and a `git_oid` target.
  They cannot be read as files or chmodded, and do not require the external commit.
- Native files default to `0644`, directories to `0755`, symlinks to `0777`.
  Existing file modes survive content replacement. Root mode is fixed at `0755`.
- Mode bits are retained metadata, not API authorization checks. The workspace's
  writable/read-only capability determines permission to mutate. There are no
  UID/GID, timestamps, ACLs, xattrs, hard links, or device nodes in this MVP.
- Errors stop Starlark execution. Mutations return `None`; reads return values.

## Compatible operations

| Method | Behavior |
| --- | --- |
| `read_file(path, line_start=None, line_end=None, output_limit=None)` | Returns a byte-preserving Starlark string for a full read; supplying bounds returns a result struct. |
| `write_file(path, content)` | Creates/replaces one regular file. Parent must exist. Content may be a string or Starlark bytes. Never replaces a directory or symlink. |
| `replace(path, old, new)` | Replaces all non-overlapping literal occurrences. Empty or absent `old` is an error. |
| `list_dir(path)` | Sorted immediate child names. |
| `glob(pattern)` | Sorted relative paths, including directories; shell-style segments plus whole-segment `**`. Dotfiles are ordinary names. |
| `mkdir(path)` | Creates one directory; parent must exist and destination must not exist. |
| `delete(path)` | Removes a file, symlink, or empty directory. No recursive deletion; root cannot be deleted. |
| `rename(old, new)` | Moves a file or entire directory subtree. Destination must not exist; cannot move root or a directory into itself. |
| `chmod(path, mode)` | Sets regular-file/directory permission metadata, `0..0o777`; symlinks, gitlinks and root are rejected. |
| `readonly()` | Returns a live read-only view of the same workspace. Does not freeze the tree or mutate the original capability. |

### Bounded reads

Result fields: `content`, `line_start`, `line_end`, `total_lines`, `output_bytes`,
`truncated`, `truncated_bytes`. Lines are 1-based and inclusive; line terminators
are preserved. An empty file has zero lines. A trailing newline does not add an
extra empty line. Out-of-range starts produce an empty selection.

The reported line range describes the selection **before** the byte cap; an empty
selection may have `line_start > line_end`. The byte cap can split UTF-8 sequences.
`truncated_bytes` counts bytes omitted by that cap, not lines outside the selection.
Bounds control returned output, not current internal memory use: the MVP first
reads the whole file.

### Search

```python
workspace.search(pattern, regex=False, glob=None,
                 max_matches=1000, output_limit=1048576)
```

`pattern` accepts a string or list/tuple of strings (OR). `glob` accepts a string or
list/tuple of path patterns. `regex=True` uses Go's regular-expression syntax.
Traversal is deterministic and sorted. Each matching line produces one result
at its earliest matching byte column. Search skips symlinks, NUL-containing files,
and files with a line over 8 MiB.

Result fields: `matches`, `match_count`, `files_searched`, `output_bytes`,
`skipped_large_files`, `truncated`. Each match has `path`, `line`, `column`, `text`.
Lines and byte columns are 1-based. Match text omits the newline. `output_bytes`
counts path plus line-text bytes, not the serialized result's structural overhead.
Search stops on the first additional match that would exceed either limit and
reports `truncated=True`. Zero limits are accepted.

**Compatibility gap:** ignore rules and the `ignore` argument are not implemented.
All files in the exposed tree are candidates, including native files matching a
`.gitignore`. Unsupported keywords fail rather than being silently ignored.
Search is not a snapshot-wide transaction when a Go client concurrently mutates
the same live workspace. Use an independent fork for a stable view.

## Native extensions

| Method | Behavior |
| --- | --- |
| `stat(path)` | Struct with `kind` (`file`, `dir`, `symlink`), `mode`, and `size`. |
| `symlink(path, target)` | Creates/stores a link without resolving its target. |
| `readlink(path)` | Returns the stored target, without dereferencing. |
| `snapshot()` | Syncs an immutable snapshot; returns its ID. Does not move a name. |
| `fork()` | Durably snapshots current state and returns an independent writable workspace sharing immutable metadata; O(1) when already snapshotted. |
| `publish(name)` | Syncs objects and publishes a named root; returns snapshot ID. |

Repeated `snapshot()` calls on unchanged, already durable state perform no I/O.
After edits, snapshots serialize changed metadata and bounded directory pages,
then sync before returning. Directory pages split/collapse deterministically,
so snapshot IDs within v2 are independent of insertion/deletion order.
A dirty `fork()` pays the same persistence cost; its returned handle then shares
the snapshot root. Edits in either handle path-copy metadata without changing the
other. Cold host-side `Repository.Fork(id)`/`Checkout(name)` still load and validate
the tree; they do not use this in-memory fast path.

Read-only capabilities deny *all* operations that mutate repository state,
including snapshot/fork/publication. They do not expose the enclosing repository
or allow loading arbitrary snapshot IDs. The trusted host/CLI selects the initial
workspace and can recover snapshots by ID.

Publication names contain 1–255 ASCII letters, digits, `.`, `_`, or `-`, excluding
`.` and `..`. An unnamed fork can publish only a new name. A checked-out workspace
can update its own name only if the current ID matches the ID it checked out or
last published. Publishing under a new name rebinds that handle to the new name.
This is content-ID compare-and-swap, not a monotonic revision counter.

## Deliberate omissions and execution limits

No `path()` method exists because there is no host mount path yet. `format()` and
`read_image()` are not implemented. `AGENTS.star` inside an imported tree is just
data; this runtime does not evaluate agent configuration or confer host powers.

Starlark permits top-level control flow, while loops, global reassignment, sets,
and recursion. Default execution budget is 10 million interpreter steps. The CLI
adds a one-minute context timeout; both limits are configurable. There is no `load`
function configured, no process tool, and no host filesystem API.

Step budgets do not meter native I/O or native allocation, and cancellation is
cooperative. Large strings, full-file reads, or expensive native operations can
exceed intended resource budgets. Use trusted scripts for this MVP; enforce OS
resource limits and sandboxing before accepting adversarial workloads.

## Repository format compatibility

This build creates/opens `SCSREPO2`. The previous `SCSREPO1` format is rejected
without modification. Re-import Git into a new file, or use a v1 build to recover
native edits from old files. There is no automatic conversion or cross-format
snapshot-ID compatibility; workspace method signatures are unchanged.
