# Security boundaries

SCS and cah are experimental. This repository does not promise production
hardening, a stable storage format, or security support for released versions.
Keep independent backups of important data.

## Trust and isolation

- The Starlark workspace API does not expose host filesystem, process, network,
  or module-loading operations. Its step budget and cancellation are not hard
  CPU or memory quotas. Use external OS isolation and resource limits for
  untrusted scripts, Git remotes, and repository files.
- A cah FUSE mount is **not a sandbox**. In particular, mounted symlinks are
  resolved by the host OS and can point outside the mount. Running a build or
  executable from a mount requires a separate sandbox if the input is untrusted.
  Do not run untrusted builds as root.
- `AGENTS.star` is trusted developer-tool configuration, not an SCS workspace
  script. Review it before enabling it: it grants workspace writes, Go tools,
  Git operations, and explicit mount/build helpers. Go builds and tests execute
  code; these capabilities are not all covered by the mount helper's sandbox.
  Its local test artifacts and host header grant are not prerequisites for
  ordinary SCS use. Keep per-machine overrides in ignored `AGENTS.local.star`.
- Native files and snapshots are not encrypted. Dropping a name does not erase
  the underlying objects; there is no garbage collection. Do not publish `.scs`
  files that have ever contained private data.
- Opening or checking a repository may repair an incomplete tail. Read-only
  workspace capabilities do not make the repository file physically read-only.
  Use a copy when inspecting evidence or unknown files.
- cah buffers whole dirty files and has no aggregate memory quota. Its file
  size limit is not a process memory limit. See `docs/CAH.md` for durability and
  filesystem limitations, including the need for fsync or orderly publication.

## Reporting

Do not put credentials, private repository contents, or sensitive exploit
artifacts in public issues. Use a maintainer-confirmed private channel for
sensitive reports. No dedicated private reporting channel or response-time
commitment is currently documented here.

## Publication hygiene

Keep credentials, local handoff notes, generated binaries, and repository/build
artifacts out of commits. Ignore rules are only an accidental-add safeguard;
review staged changes and history before publishing. If a credential is ever
published, treat it as exposed even after removing it from the current tree.
