# Development guidance for lgit

This file is for agents and contributors **developing lgit itself**. It records architectural decisions, product invariants, performance lessons, compatibility requirements, and testing expectations that should survive across future changes.

For guidance on **using the installed lgit CLI**, use `skills/lgit/SKILL.md`. Do not turn this file into an end-user usage guide.

## Product identity

`lgit` is a Git-backed companion repository for files that should not live in a project's normal Git repository. It adds logical-path handling, external worktree roots, storage transformations, environment namespacing, and safety around Git; it is **not a replacement version-control system**.

The core rule is:

> Git remains authoritative for what is tracked, the index, commits, history, branches, refs, remotes, fetch, push, and object storage.

Do not introduce a second inventory, commit graph, index, or bespoke history database inside lgit.

## Git tree is the inventory

`.lgit/storage.toml` is a **storage policy**, not a manifest of tracked files.

Responsibilities are intentionally split:

- Git tree/index: what is tracked and the current physical representation.
- `.lgit/storage.toml`: default/explicit storage policy for logical paths.
- project filesystem: the materialized user-facing plaintext view.

For `plain` storage:

```text
logical path == Git path
```

For `age` storage:

```text
logical path: .npmrc
Git path:     .lgit/store/.npmrc.age
```

The Git representation of an already-tracked file determines its **current backend**. Changing the storage default must not reinterpret or silently migrate existing tracked files.

## Storage model

There are currently only two real storage backends:

```text
plain
age
```

Keep this simple. Do not build a plugin/backend framework until a real third backend exists.

Storage configuration uses exact logical paths. Do not add globbing, pattern precedence, recursive rule semantics, or sensitivity guessing without an explicit product decision.

Backend resolution for a newly tracked path is:

```text
explicit [files] entry, otherwise default
```

Unknown backend values and unsupported config versions must fail explicitly.

Storage migrations such as `plain -> age`, `age -> plain`, and `storage unset` must be atomic from the user's perspective. Never leave configuration and Git representation disagreeing because an operation failed halfway through.

## Encryption architecture

`age` is the content-encryption implementation. Storage backend and encryption credential mechanism are separate concepts:

```text
backend:          plain | age
encryption mode: identity | password
```

Do not collapse these into pseudo-backends such as `age-password` or `age-identity`.

### Identity mode

Identity mode uses an age X25519 identity stored outside repositories. Public recipients may be committed; private identities must not be committed.

### Password mode

Do **not** return to per-file scrypt encryption.

That design caused severe Windows performance problems because age's intentionally expensive scrypt KDF was run once for every file. A small 10-file add measured roughly 8 seconds in password mode.

Current password mode instead uses:

```text
password
  -> scrypt once to unlock a wrapped project X25519 identity
  -> fast X25519 age operations for repository files
```

The wrapped project identity lives at `.lgit/password-identity.age`. The password itself is never stored.

Legacy per-file-scrypt repositories remain readable through lazy compatibility. Do not remove that compatibility casually; migration should be incremental rather than forcing users to rewrite all history.

A password KDF once per command can still impose a noticeable fixed cost. If this is optimized later, prefer OS credential/session-key caching or another explicit design over weakening scrypt security parameters.

## Performance is a product requirement

Do not assume slow behavior is an unavoidable Git limitation. Measure the command path first.

Two performance bugs have already occurred:

1. `add` launched Git subprocesses per file, causing thousands of processes for large directory adds and heavy Microsoft Defender activity.
2. recursive path expansion reloaded the registry and canonicalized/resolved paths for every visited directory.

The fixes establish these rules:

- batch Git path operations;
- precompute nested-root boundaries once per command;
- do not reload the registry inside a recursive walk;
- do not resolve symlinks/canonical paths once per visited directory;
- avoid unnecessary file reads, encryption, and Git subprocesses;
- acquire/decrypt password identity once per command and reuse it across files.

A small plain `lgit add` should feel close to `git add`, not take multiple seconds. Multi-second latency for a few ordinary files is a performance regression that should be investigated, not explained away.

Successful `lgit add` is intentionally quiet, matching Git. Do not print internal messages such as "scanning", "found N files", or "adding in batches" during ordinary successful operation. Diagnostic verbosity should be opt-in.

## Roots and ownership

lgit supports project-local roots and standalone roots such as `$HOME` without creating a normal `.git` directory in the standalone root.

Once initialized, registered lgit roots are authoritative for command routing.

When several registered roots contain the current directory:

> The nearest/deepest registered root owns the path.

Example:

```text
/home/arne                  home/dotfiles root
/home/arne/code/Booking     Booking root
```

A command under `Booking` resolves to the Booking root. `--root` is the explicit override when the caller intentionally wants another registered root.

A logical path may belong to only one lgit root. Parent and nested lgit projects must not overlap ownership.

Recursive operations must stop at:

- another registered lgit root;
- another normal Git repository;
- `.git` and `.lgit` internal directories.

Do not let `lgit add .` from a home root ingest nested source repositories.

## Cross-platform configuration roots

Do not solve application-specific OS path differences by adding implicit path remapping to a broad home root.

For applications whose config root changes by platform, prefer independent standalone roots that attach to the same logical lgit project. For example, Neovim may use `~/.config/nvim` on Linux and `%LOCALAPPDATA%\nvim` on Windows.

The physical local root is machine-specific; the logical contents inside that root are portable.

User/agent workflow details for this convention belong in `skills/lgit/SKILL.md`, not here.

## Attach is an intent-level transaction

`attach` must be a complete user-facing operation. Users and agents should not need to know remote ref namespaces, temporary Git repositories, checkout mechanics, registry layout, or age identity filesystem conventions.

The intended flow is conceptually:

```text
discover remote/project/environment
  -> inspect target tree and storage metadata
  -> verify encryption requirements
  -> compute content conflicts
  -> compute structural filesystem conflicts
  -> determine backup/replacement plan
  -> apply atomically
  -> register root
```

All meaningful preflight work should happen **before mutating the user's root**.

### Structural conflicts matter

Do not only compare leaf-file contents. If the remote needs `config/app/settings.json`, existing `config` or `config/app` entries may themselves block materialization because they are files, symlinks, junctions, or incompatible filesystem objects.

Preflight must discover these blockers before Git checkout does.

`--use-remote` means the remote representation wins. lgit must back up all local entries that block that representation and then complete the operation itself.

`--keep-local` may preserve regular-file content conflicts, but it cannot preserve structurally incompatible representations. Fail with an lgit-level explanation instead of leaking a later Git checkout error.

### Attach atomicity

A failed attach should leave the user's filesystem as it was before attach and leave the root unregistered.

Rollback/snapshot logic must reason in ownership units. If a structural blocker is `config`, do not separately try to snapshot `config/settings.json` beneath it; that previously caused `ENOTDIR` failures.

Internal `git init`, fetch, and checkout chatter should stay quiet during normal attach. Expose lgit's own actionable errors instead.

## Human and agent introspection

The CLI should expose intent-level state rather than forcing callers to inspect implementation details.

Current examples include:

```text
lgit info [--json]
lgit remote list REMOTE [--json]
lgit key path
lgit key status [--json]
lgit attach ... --dry-run [--json]
```

When adding automation-relevant state, prefer a stable structured output mode instead of encouraging agents to parse prose or raw Git refs.

`lgit git ...` remains an expert/debugging escape hatch. Do not make raw companion Git commands the normal discovery or recovery workflow.

## Filesystem safety

Project-local lgit must not manage a path already tracked by the main Git repository.

Run ownership checks before operations that materialize or take control of logical files, not only when initially adding them; the main repository may start tracking a path later.

Age-backed storage currently supports regular files. Do not silently follow or reinterpret symlinks. If symlink support is added, define an explicit portable metadata representation for file type/target/mode first.

Preserve bytes across storage backends. Companion repositories set:

```text
core.autocrlf=false
```

Do not allow `plain <-> age` migration to change CRLF/LF bytes.

Executable-mode and other Unix metadata changes must be intentional and tested across platforms.

## Environments and remote namespace

Environments remain Git branches under the lgit remote namespace. Git refs/remotes are implementation details for users but are still the actual version-control mechanism.

Environment switching must reason in logical paths and storage backends, then materialize the correct plaintext view after the Git state changes.

Do not implement a separate environment state database that competes with Git branches.

Encryption mode is effectively project-level for now. Do not make password/identity mode migration happen merely because someone edited TOML. A future change between encryption modes should be an explicit migration operation.

## Backward compatibility

Existing repositories matter.

Current compatibility expectations include:

- legacy age-only repositories without `storage.toml` continue to load as age-backed;
- legacy identity encryption continues to work;
- legacy password/scrypt ciphertext remains decryptable while selected files can migrate lazily to the wrapped-X25519 model;
- introducing metadata/config changes should avoid decrypting and re-encrypting blobs unnecessarily;
- changes to project/ref discovery should consider older naming conventions and existing remote data.

Prefer explicit migrations and compatibility readers over silently breaking existing repositories.

## Code organization landmarks

The implementation is intentionally still small. Before creating new abstractions, inspect the existing responsibilities:

- `internal/lgit/app.go`: command routing and core Git-backed operations.
- `internal/lgit/storage_policy.go`: `plain`/`age` policy, logical/physical representation, mixed storage behavior.
- `internal/lgit/fast_password.go`: wrapped password identity and legacy password compatibility.
- `internal/lgit/walk_fast.go`: efficient recursive expansion and root boundaries.
- `internal/lgit/root.go`: canonical roots and nearest-root resolution.
- `internal/lgit/ux.go` / `ux_run.go`: attach/introspection UX and transactional preflight behavior.
- `skills/lgit/SKILL.md`: end-user/agent operational guidance, not development instructions.

Keep abstractions proportional to real requirements. Do not introduce a generalized framework when a small enum/helper is enough.

## CLI behavior principles

Match Git's mental model where possible.

Commands whose semantics depend on logical-path/storage translation must be lgit-aware. Commands that naturally operate on refs/commits may delegate to Git.

If a storage-aware operation is not implemented safely, return a clear explicit error rather than silently doing the wrong thing.

Prefer concise success behavior and actionable failures. Do not expose internal progress or Git setup details unless requested through a verbose/debug mechanism.

## Development workflow and verification

The project targets **Go 1.26.5**. Keep `go.mod` and CI aligned with that exact version unless intentionally changing the project toolchain.

Before considering a change complete, run:

```sh
go test ./...
go vet ./...
go build ./cmd/lgit
```

Normal CI runs all three on:

```text
ubuntu-latest
macos-latest
windows-latest
```

Cross-platform behavior is not verified until the relevant matrix jobs have actually completed successfully. Do not claim Windows/macOS support from Linux-only tests.

For filesystem, path, CRLF, symlink/junction, encryption, or process-performance changes, add focused regression tests for the platform-sensitive behavior.

When a bug comes from a real user/agent workflow, preserve that shape in a regression test where practical. Examples already include large directory add performance and attach structural blockers.

Temporary migration/profiling workflows or scripts may be useful during development, but **must not remain in the final repository tree**. The normal CI workflow is the persistent workflow.

## Repository-specific development constraints

Do not add Superpowers-specific artifacts, workflows, plans, `docs/superpowers`, or references. Previous Superpowers references were explicitly removed from history and should not be reintroduced.

Use neutral, product-focused commit messages.

Do not rewrite history unless there is a deliberate reason and the consequences are understood. Prefer normal forward fixes.

When modifying behavior, update the README for humans and `skills/lgit/SKILL.md` when operational guidance for agents changes. Keep this `AGENTS.md` focused on implementation decisions and contributor guardrails.
