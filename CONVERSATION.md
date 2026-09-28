If you were writing Git from scratch what would you fix and what would you keep the same?

If I were rebuilding Git from scratch today, I’d keep its core data model almost intact and redesign most of the human-facing surface area.

Keep

The content-addressed object model is still the strongest part of Git:

blobs for file contents
trees for directory snapshots
commits pointing to trees + parents
refs as movable names pointing into the graph

That model is simple, composable, and extremely durable. I’d also keep commits as snapshots rather than deltas. Storage can still use delta compression internally, but the semantic model should remain snapshot-based.

I’d keep the distributed architecture too. A repository being fully useful offline, with branches and history being local first-class data, remains a major advantage.

The DAG-based history model also stays. Multiple parents for merges, immutable commits, and hashes identifying history are all good abstractions.

I’d preserve the distinction between plumbing and porcelain conceptually. Having a small set of graph/object primitives underneath higher-level workflows is excellent. What I would change is how much of that plumbing leaks through the CLI.

The staging area/index is worth keeping, despite its reputation. Being able to construct a commit independently from both HEAD and the working tree is genuinely powerful. I might present it differently, but I would not remove the capability.

Fix

The biggest rewrite would be the CLI semantics.

Git accumulated commands according to implementation history rather than a coherent user model. The classic examples are:

git checkout
git reset
git revert
git restore
git switch
git clean

These manipulate several different dimensions:

HEAD
branch refs
index
working tree
commit graph

but their names don't clearly express which dimensions they affect.

I'd make those dimensions explicit. Something more like:

git branch switch feature
git index add file.c
git index restore file.c
git worktree restore file.c
git history undo <commit>
git ref move main <commit>

The exact syntax isn't important; the important part is eliminating commands whose meaning depends heavily on argument shape and flags.

git reset in particular probably wouldn't exist.

I'd make the ref model more uniform.

Today users encounter:

refs/heads/*
refs/remotes/*
refs/tags/*
HEAD
FETCH_HEAD
ORIG_HEAD
MERGE_HEAD

The underlying mechanisms make sense, but the exposed namespace feels historical. I'd define one clean typed-reference system:

branch/main
remote/origin/main
tag/v1.2
operation/merge
operation/rebase

with explicit lifecycle rules.

I'd redesign remotes and tracking branches.

origin/main, refs/remotes/origin/main, upstream branches, fetch refspecs, push refspecs, and remote HEAD are individually reasonable but collectively too complicated.

I'd separate three concepts clearly:

remote repository
remote-tracking snapshot
publish destination

A local branch shouldn't implicitly acquire surprising push/fetch behavior because of several loosely related config variables.

I'd make rewriting history transactional.

Rebase, cherry-pick, merge, revert, and similar operations should expose a common transaction abstraction:

transaction start
transaction status
transaction continue
transaction abort
transaction inspect

Internally Git already has operation state, but every subsystem currently exposes it slightly differently.

That same transaction mechanism would make interrupted operations much easier to reason about.

I'd replace the current reflog/recovery experience with something much more deliberate.

Git is unusually difficult to permanently destroy data in, yet users frequently believe they've lost work because recovery mechanisms are obscure.

Every destructive operation should produce an obvious recovery handle:

Moved branch main:
  old: a91f0c2
  new: b024d77

Recover with:
  git undo 4f9321

Then maintain a local operation journal.

git reflog could remain as low-level plumbing, but ordinary recovery shouldn't require understanding it.

I'd formalize undo as a core operation.

One reason Git feels inconsistent is that "undo" means several fundamentally different things:

undo working-tree changes
undo staged changes
undo a commit locally
reverse a published commit
undo a ref movement
undo a merge/rebase operation

I'd make:

git undo

inspect the last repository mutation and reverse it when possible, while commands such as:

git history revert

would explicitly create inverse commits for published history.

I'd rethink submodules completely.

They expose too many implementation details and have awkward recursive behavior. A repository dependency should effectively be:

path
repository identity
commit
fetch policy

and cloning/updating it should be atomic and predictable.

I'd probably make something closer to today's subtree/submodule concepts but implemented directly in the object model instead of bolted on through .gitmodules plus special tree entries.

I'd make large-file handling native.

Git's object database is excellent for source trees but behaves poorly when repositories accumulate large binaries or frequently changing binary assets.

Rather than Git LFS being an external protocol layered on top, objects could have storage classes:

normal
large
promised
external

with partial-clone semantics being fundamental from the start.

I'd design partial clone and sparse checkout as first-class concepts.

Git originally assumes that obtaining a repository means obtaining essentially the whole object graph. Modern monorepos break that assumption.

I'd make repository views explicit:

git clone repo --paths src/compiler,libs/runtime
git view add docs
git view remove testdata

and ensure commands operate correctly even when parts of the graph haven't been materialized locally.

I'd replace .gitignore's collection of overlapping mechanisms:

.gitignore
.git/info/exclude
core.excludesFile
assume-unchanged
skip-worktree

with a clearer separation between:

repository ignore rules
developer-local ignore rules
sparse/materialization rules
index state

assume-unchanged and skip-worktree are particularly easy to misuse because they look like user-facing ignore mechanisms even though they aren't.

I'd make configuration typed and inspectable.

Instead of arbitrary strings spread over:

system
global
local
worktree
command line
environment

I'd want:

git config explain pull.strategy

to show:

value: rebase
type: enum
source: ~/.config/git/config:18
valid values:
  merge
  rebase
  ff-only

Git already has pieces of this, but I'd make schemas mandatory for core settings.

I'd probably change the hash/object format abstraction, but not because SHA-1 itself is the interesting issue.

Git has already had to evolve from SHA-1 toward SHA-256. A new design would treat the object-ID algorithm as an explicit repository-format parameter from day one, with protocol-level algorithm negotiation and clean migration support.

Object identity should not conceptually mean "40 hex characters."

One architectural change I'd strongly consider is separating commit identity from metadata that doesn't need to affect identity.

Currently author, committer, timestamps, message, tree, and parents all contribute to the commit hash. That gives strong immutability semantics, but it means changing innocuous metadata rewrites every descendant.

I would at least investigate something like:

commit core:
    tree
    parents
    author identity
    message

attestations:
    signatures
    CI results
    review metadata
    timestamps

I would not move parents or tree references outside the hashed commit structure—the cryptographic history relationship is too valuable. But signatures and evolving metadata belong much more naturally in separate attestations.

The part I would resist "simplifying"

I wouldn't redesign Git around files and diffs.

Its unusual strength is that the fundamental abstraction is:

immutable objects plus references over a Merkle DAG.

Diffs, branches, merges, tags, worktrees, and synchronization are interpretations or manipulations of that graph.

A lot of proposed "simpler Git" designs improve the UI by weakening that model. I'd do the opposite: preserve the model almost exactly, then build a much more coherent transactional interface around it.

So if I had to summarize the rewrite:

Git's database is roughly what I would design today. Git's command language is largely what I wouldn't.

Yes. That gives you a very clean separation between repository storage format and wire format, while letting them share almost all of the same primitives.

If the object index is append-only, replication can be approximately:

1. Exchange repository/index generations.
2. Determine which object IDs are missing on each side.
3. Sender streams:
       type
       hash
       payload
4. Compress the stream as a whole.
5. Receiver decompresses sequentially.
6. Receiver verifies H(type || payload) == hash.
7. Receiver appends payload to free pages.
8. Receiver appends hash -> location records to its index log.
9. Atomically publish the new repository state.

That is substantially simpler than Git's pack negotiation + delta selection + pack indexes + object reconstruction machinery.

The whole-stream compression is especially attractive. You don't need objects themselves compressed because the transport compressor gets a long stream containing structurally similar blocks:

block A
block B
block C
indirect block
tree block
...

Zstd or similar gets plenty of opportunity to exploit redundancy across adjacent transmitted objects. You retain uncompressed random-access storage locally without paying much of a network penalty.

And if the sender chooses sensible ordering, compression should improve further. For example:

related data blocks
indirect blocks
trees
commits

or perhaps sort blocks according to traversal order from the transferred roots.

The receiver doesn't care about that ordering because identity is by hash.

Append-only index

I like the append-only index idea particularly because it makes replication itself transactional.

Conceptually:

OBJECT_LOG

(hash A, DATA, page 100)
(hash B, DATA, page 101)
(hash C, INDIRECT, page 102)
...

You never mutate an existing mapping under ordinary operation.

If compaction moves objects, you could append newer mappings:

(hash A, page 100)
...
(hash A, page 91823)

with "latest mapping wins."

That starts looking like an LSM-style store, though, so you'd eventually want checkpoints/index snapshots to avoid replaying a multi-terabyte log on startup.

Something like:

checkpoint
    object-index root
    log position 8,391,022

append log
    entries after checkpoint

Startup becomes:

load checkpoint
replay small log tail

and checkpoint generation can happen asynchronously from the logical repository state.

Reconciliation is the interesting bit

If both sides literally exchange every hash in their indexes:

I have:
A
B
C
D
...

then it works, but repositories with hundreds of millions of blocks make that expensive.

There are several progressively smarter options.

For repositories that share ancestry, your commit/tree graph already drastically restricts what needs consideration. The receiver says:

I have commit X
I want commit Y

and the sender walks objects reachable from Y but not X.

That's essentially the normal VCS case and requires little fancy set reconciliation.

But arbitrary repository synchronization is more interesting. You could exchange compact set summaries:

Bloom filter
XOR filter
IBLT
Merkleized index ranges

An IBLT-style reconciliation scheme is particularly appealing when the sets are enormous but differ only slightly: communication can scale roughly with the size of the difference rather than the size of the repository.

I wouldn't necessarily put that in v1, though.

A hierarchical Merkle summary of the object index would fit the rest of your architecture nicely.

For example, partition object IDs by prefix:

00...
01...
02...
...
ff...

and maintain hashes/counts for those buckets.

Then reconciliation does:

ours bucket 7a hash = X
theirs bucket 7a hash = X
    → identical, skip

ours bucket 91 hash != theirs
    → descend

Eventually you reach individual differing object IDs.

That's deterministic, cacheable, and cryptographically verifiable.

Integrity checking becomes almost embarrassingly simple

For each received object:

expected = transmitted hash
actual   = SHA256(type || payload)

expected == actual

If it matches, you know you received exactly the object identified by that hash.

Then the higher-level graph verifies itself transitively because indirect blocks and trees contain hashes of their children.

You could therefore validate a received commit incrementally:

commit verified
  ↓
tree hash verified
  ↓
indirect hashes verified
  ↓
data blocks verified

No separate transport checksum is really required for correctness, although a stream checksum can still be useful for detecting truncated/corrupt transport before you've processed everything.

The receiver can write essentially at line rate

Because the destination representation is uncompressed fixed-size pages:

network
   ↓
decompressor
   ↓
SHA-256
   ↓
write(4096)
   ↓
append index record

There's no:

inflate Git object
reconstruct delta base
apply delta chain
serialize loose object
later repack it

The bytes received after decompression are basically already in their final representation.

That is a major architectural win.

It also allows very bounded memory use. You don't need to materialize an entire pack:

read compressed chunk
decompress block
hash block
write block
forget block

Even a multi-terabyte repository transfer can operate with a relatively tiny working set.

Deduplication happens naturally on reception

Before writing:

if object_index.contains(hash):
    discard payload
else:
    allocate page
    write payload
    append index entry

So the sender doesn't even have to negotiate perfectly.

False positives in the transfer set cost bandwidth, not correctness.

That suggests an interesting protocol optimization: bias reconciliation toward simplicity even if it occasionally sends redundant blocks.

For example, a Bloom filter supplied by the receiver can cheaply tell the sender:

probably already have it
definitely don't have it

Bloom false positives are problematic because they could cause the sender to omit required blocks, but you can reverse how you use it or perform a final missing-object pass from the requested roots.

A safer pattern is:

initial approximate transfer
receiver walks requested roots
receiver reports missing hashes
sender supplies missing blocks

That can make protocol negotiation considerably simpler.

There's also a nice resumability property

Since each object is individually authenticated, interrupted transfers don't really need special partial-pack machinery.

Suppose the connection dies after 80 GB.

Everything already committed to the receiver's object store remains valid.

Reconnect:

receiver advertises updated object set
sender reconciles again
transfer continues

There is no need to resume at byte offset 83,272,193,819 in some particular archive.

You're resuming semantically rather than physically.

One thing I'd separate

I would distinguish the object-location log from the replication/object-existence log.

Physical mappings like:

hash -> page 18292

are local implementation details. Compaction changes them even though the repository's object set hasn't changed.

For synchronization you really care about:

object hash exists

So perhaps:

OBJECT ADD LOG
    generation
    hash
    type

and independently:

PHYSICAL INDEX
    hash -> current page

Then a compaction doesn't make the repository appear to have millions of "new" entries to a peer.

Or the append log could simply contain immutable object introductions, with physical relocation represented elsewhere.

That also gives you useful generations:

peer says:
    last object generation I saw from you = 928173

sender can immediately stream:
    additions after 928173

when the peer is known to descend from a previous synchronized state.

For unrelated peers, fall back to set reconciliation.

So the protocol could have a very cheap fast path:

same repository lineage + known generation
    → stream append-log suffix

and a general path:

unknown/diverged lineage
    → reconcile object sets

That seems exceptionally efficient.

The emerging design has a pleasing property: clone, fetch, push, backup, mirroring, and repository repair are all basically variations of "reconcile immutable block sets, then publish roots."

Git has separate machinery for several of those operations because its physical and logical representations evolved separately. Your representation makes them converge on essentially the same primitive.

Makes sense. I really like this design. It's backwards compatible with git and probbaly supports a transformation to push to a existing repo.

Yes. The compatibility story is one of the stronger aspects because your model changes the physical representation, not the essential Git semantics.

A Git blob can map to a canonical file root built from 4 KiB blocks. A Git tree maps to your tree representation. A Git commit can preserve the same parent graph, author/committer metadata, message, and referenced tree. Tags can similarly map directly.

That gives you two useful boundaries:

Git object model
    ↕ canonical transform
your block/Merkle representation

and:

your repository
    ↕ Git protocol/object adapter
existing Git remote

For pushing to an ordinary Git server, you'd reconstruct Git objects on demand:

your file root
    → stream logical file bytes
    → SHA1/SHA256 Git blob framing
    → Git blob object

your tree
    → Git tree encoding

your commit
    → Git commit encoding

Then feed those objects into the normal Git wire protocol or produce a packfile.

The important caveat is object IDs won't generally match. Your blob ID might be:

SHA256(FILE_ROOT || block hashes...)

whereas Git's blob ID is:

SHA1("blob " || length || NUL || file bytes)

or SHA-256 in newer-format repositories.

So you'd need a mapping cache:

native_object_id -> git_object_id

and probably the reverse as well for imported repositories.

That cache is completely derivable, though, so it doesn't need to be authoritative state.

Importing an existing Git repository is similarly straightforward:

Git blob
    → stream bytes
    → split into 4096-byte blocks
    → construct native file representation

Git tree
    → native tree

Git commit
    → native commit

Because commits reference Git object IDs, you'd translate those references as you import them.

The only place I'd be careful with the phrase backwards compatible with Git is that compatibility exists at the semantic/protocol layer, not necessarily at the hash namespace. Existing tools won't be able to directly open your single-file repository as though it were .git/objects.

But you could provide a compatibility shim:

git-compatible CLI/protocol frontend
        ↓
translation layer
        ↓
native repository

That could support normal operations such as:

git clone
git fetch
git push

against your implementation.

There's also an interesting possibility of preserving the original Git object ID as optional metadata during import:

native object
git-id: abc123...

Then round-tripping an untouched imported repository back to Git becomes very cheap because you already know the correct Git IDs. Modified objects get Git IDs lazily when needed.

I would probably avoid making Git IDs part of the canonical native object itself, though. Otherwise compatibility metadata starts affecting your native identity.

So I'd structure it as:

authoritative:
    native object graph

derived compatibility index:
    native ID <-> Git ID

That keeps the core format independent.

The design also gives you a nice path for gradual adoption: a user could clone from GitHub into the native format, work entirely in the native repository, and push back to the same ordinary Git remote without the remote knowing anything changed locally.

That is much easier to deploy than a VCS that requires the whole ecosystem to switch storage formats at once.

Why not match git's IDs exactly?

You can, and for compatibility I think there’s a strong case for doing exactly that.

The key is to distinguish Git object identity from internal block identity.

For a blob, keep:

git_blob_id =
    SHA1("blob " || decimal_size || "\0" || file_bytes)

for a SHA-1 Git repository, even though internally the bytes are represented as:

file
  size
  direct blocks
  indirect blocks

The Git ID does not need to be the Merkle root of your storage representation. It can be a property of the logical object.

Internally you'd therefore have two kinds of hashes:

Git object ID
    identifies the logical Git object

block hash
    identifies a physical/content-addressed 4096-byte block

For example:

Git blob
id = 8f3e...

Native representation
size = 18293
blocks =
    91ac...
    e831...
    72dd...
    ...

The mapping:

8f3e... -> native file descriptor

lives in the repository index.

That would let an imported Git repository preserve every object ID exactly.

More importantly, commits and trees can preserve their IDs too, provided you retain their exact canonical Git serialization.

A Git commit is just bytes such as:

tree <id>
parent <id>
author ...
committer ...

message

Hash those exact bytes using Git's framing and you get the original commit ID.

Likewise Git trees have their canonical binary representation. You don't need to store that representation physically; you only need to be able to reproduce it exactly.

So your architecture could be:

                 logical Git namespace
                         |
                 Git SHA-1/SHA-256 IDs
                         |
               native object index
                         |
             native structural objects
                         |
              4096-byte block hashes

That has a major advantage over the scheme I described previously: pushing back to Git becomes almost trivial.

If you have commit:

abc123

and the remote says it doesn't have abc123, you already know exactly what Git object must be emitted. No ID translation is necessary.

It also makes fetch much nicer

When receiving:

blob 9bc...

you stream its bytes into 4 KiB blocks while simultaneously verifying:

GitHash(blob framing || bytes) == 9bc...

Then store:

9bc... -> native representation

The external Git namespace survives untouched.

There is one cost

You lose the property that the logical file ID itself gives you a Merkle root over your blocks.

Suppose a blob is 100 GB. Its Git ID is:

H(entire 100 GB logical byte stream)

while internally you have:

block hashes -> indirect hashes -> ...

The latter permits cheap incremental verification and structural sharing, but the Git blob ID doesn't encode that tree.

I don't think that's a serious problem.

You already have the native block hashes for integrity and storage. The Git ID serves a different purpose: interoperability and logical identity.

In fact, separating the namespaces may be cleaner:

external/logical identity:
    Git object ID

internal/storage identity:
    SHA-256(type || 4096-byte payload)

Trying to make one hash perform both roles forces unnecessary coupling.

SHA-1 versus SHA-256

This is the part I'd design carefully.

If you want exact compatibility with ordinary existing Git repositories, many of them still have SHA-1 object IDs. You therefore need to support those IDs.

But I wouldn't use SHA-1 internally.

Something like:

ObjectKey {
    algorithm: SHA1 | SHA256
    digest: ...
}

for Git-facing objects.

Internally:

BlockID = SHA256(type || block)

always.

So SHA-1 becomes merely a compatibility identifier, not something you're relying upon for internal storage integrity.

You could even verify imported SHA-1 Git objects additionally with your own SHA-256-based block graph.

That is a meaningful improvement over ordinary Git: the Git-visible ID remains exactly the same, but internally you've got stronger integrity checking.

And there is an unexpectedly nice consequence

Your repository could potentially host Git histories using either hash algorithm simultaneously.

For example:

git-sha1:   91abc...
git-sha256: d38f...
native:     object record 48291

Both logical IDs could refer to the same underlying native object.

For blobs especially, calculating both is straightforward while streaming the content.

That potentially gives you a migration bridge:

SHA-1 Git repository
       ↓
native repository
       ↓
SHA-256 Git repository

without duplicating the file storage.

The commits and trees would need rewriting because their serialized representations contain object IDs, so their SHA-256 history would naturally differ, but the native block data stays shared.

So I'd revise my earlier position: I would preserve Git IDs exactly whenever the object originated from or is representable as a Git object.

I'd make the native Merkle/block hashes an entirely separate lower-level namespace.

That gives you both:

perfect Git interoperability

and

a substantially better physical object store

without forcing either representation to compromise the other.
