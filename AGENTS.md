# Development guidance for lgit

This file is for agents and contributors **developing lgit itself**. It records architectural decisions, product invariants, performance lessons, stability-stage policy, and testing expectations that should survive across future changes.

For guidance on **using the installed lgit CLI**, use `skills/lgit/SKILL.md`. Do not turn this file into an end-user usage guide.

## Product model

`lgit` is a Git-backed companion repository for files that should not live in a project's normal Git repository, plus standalone roots such as dotfiles.

The source project/root remains the actual worktree. Companion Git metadata lives externally under the lgit data directory.

The core rule is:

> lgit remains Git, not a separate VCS.

Git owns:

- tracked physical representations and inventory;
- index state;
- commits and history;
- branches/environments;
- refs/remotes;
- fetch/push/object storage.

lgit adds:

- a logical worktree abstraction where a logical path may map to another physical representation in the companion repository;
- external standalone roots;
- storage/encryption policy;
- root/project/environment discovery and safety.

Do not create parallel state that competes with Git when Git already models it correctly.

## Storage policy is not inventory

`.lgit/storage.toml` is policy/configuration. It is **not** the tracked-file manifest.

The split is:

```text
Git                 -> what is tracked + commits/history/index/refs/remotes
storage.toml        -> how a logical path should be stored / first-add policy
project filesystem  -> materialized user view
```

Git trees and the index are authoritative for whether a path is tracked.

Do not redesign `storage.toml` into a second inventory database.

## Logical paths and physical representations

Supported storage backends are:

```text
plain
age
```

Plain paths are stored directly in the companion Git tree:

```text
logical: .gitconfig
Git:     .gitconfig
```

Age-backed paths are stored under the private physical store:

```text
logical: .npmrc
Git:     .lgit/store/.npmrc.age
```

Nested example:

```text
logical: .ssh/config
Git:     .lgit/store/.ssh/config.age
```

User-facing commands should speak in **logical paths**. `.lgit/store/...` is an implementation detail except for explicit expert/debug/raw-Git operations.

When a command mutates tracked file state, determine whether it needs logical-path handling before delegating to raw Git.

## Storage migration

Changing a tracked path between `plain` and `age` is a real physical-representation migration, not merely a config edit.

Expected direction:

- `plain -> age`: encrypt logical source, stage store representation, remove direct companion representation, update/stage storage policy;
- `age -> plain`: stage logical plaintext representation, remove encrypted companion representation, update/stage storage policy.

The source logical file remains the user's usable plaintext file.

Storage mutation must be treated transactionally enough to avoid leaving config/index/store in contradictory states when an operation fails.

Changing the default storage backend does **not** implicitly migrate every tracked file. The default is first-add policy unless a deliberate bulk migration feature is implemented.

## Encryption architecture

Encryption uses the native Go `filippo.io/age` library. Do not add a dependency on an external `age` executable.

Current modes:

```text
identity
password
```

Identity mode uses an age identity stored outside the managed repository data and public recipients in project metadata where appropriate.

Password mode must **not** use an expensive password/scrypt recipient independently for every tracked file.

The intended model is:

```text
password
   |
   | scrypt / password KDF once when unlocking
   v
wrapped project identity
   |
   v
X25519 project key
   |
   +-- file 1
   +-- file 2
   +-- file 3
```

The wrapped project identity lives at `.lgit/password-identity.age`. The password itself is never stored.

The current code can still read the earlier per-file-scrypt representation, but **that compatibility is not a product requirement during early development**. Do not preserve it if doing so meaningfully complicates a cleaner design, format change, or refactor. If a breaking encryption-format change is the best design, prefer the break and update tests/docs accordingly.

A password KDF once per command can still impose a noticeable fixed cost. If this is optimized later, prefer OS credential/session-key caching or another explicit design over weakening scrypt security parameters.

## Performance requirements

Directory operations must scale with the amount of real work, not with avoidable subprocess or registry overhead.

Lessons already learned:

- do not launch Git once or twice per ordinary plain file when operations can be batched;
- respect Windows command-line length limits when batching paths;
- do not reload the lgit registry or recanonicalize every ancestor for every visited directory;
- precompute nested lgit boundaries once per command where possible;
- do not run password KDF work once per encrypted file;
- avoid randomized ciphertext churn when staged/committed encrypted plaintext has not changed;
- successful `lgit add` should be quiet like `git add` rather than printing scanning/batching implementation details.

When performance is reported as poor, measure the actual operation shape and separate:

```text
filesystem traversal
Git subprocess overhead
Git index work
encryption/decryption
password KDF
antivirus/endpoint scanning amplification
```

Do not attribute multi-second small-directory operations to "Git being slow" without evidence.

Windows matters. Process creation and large numbers of small filesystem operations can be substantially more expensive there and can amplify Microsoft Defender/other antivirus work.

## Root model

Roots may be:

- normal Git-project-local lgit roots;
- standalone roots such as `$HOME`;
- nested application-specific roots.

Root resolution uses the **nearest/deepest registered root** containing the current path.

Example:

```text
/home/user                 standalone home root
/home/user/.config/nvim    optional standalone/app-specific root
/home/user/code/project    project-local lgit root
```

The deepest applicable root owns the operation.

A parent root must not recursively ingest a registered child lgit root.

A parent root must also stop at nested normal Git repositories/worktrees.

Directly adding content owned by a nested root should fail clearly rather than silently crossing ownership boundaries.

Canonical-path handling must account for platform aliases/symlinks such as macOS `/var` versus `/private/var`. Windows registry/root matching is case-insensitive.

## Cross-platform application roots

Do not invent a generalized cross-platform path-remapping layer just to make broad home roots appear portable.

When an application's physical configuration root differs by platform, prefer a standalone lgit root at the application's configuration directory on each machine.

Example Neovim:

```text
Linux:   ~/.config/nvim
Windows: %LOCALAPPDATA%\nvim
```

Both can represent the same logical project tree:

```text
init.lua
lua/
after/
ftplugin/
```

This is cleaner than teaching one `$HOME` project aliases such as `.config/nvim <-> AppData/Local/nvim`.

A broad `$HOME` root remains appropriate for paths that are genuinely home-relative across the intended machines.

## Main Git ownership

For project-local lgit roots, the normal project Git repository retains ownership of files it already tracks.

lgit must refuse to manage paths tracked by the main repository.

Standalone roots do not have a main Git ownership check because no normal repository is implied.

Do not make lgit silently steal ownership from a main repository.

## Symlinks and file types

Age-backed storage currently supports regular files only.

Do not encrypt symlink targets as if they were regular files.

Recursive traversal should not follow symlink loops or platform equivalents such as junction/reparse cycles.

If symlink support is introduced, define the logical semantics explicitly and add Windows/macOS/Linux tests rather than relying on incidental `filepath` behavior.

Executable bits must be preserved through encrypted representation/materialization where supported.

`core.autocrlf=false` is deliberate for companion Git so lgit preserves exact bytes such as CRLF content.

## Environments

Environments are Git branches conceptually and must remain grounded in Git history.

Remote namespace:

```text
projects/<project-slug>-<project-id>/envs/<environment>
```

Local environment branch:

```text
env/<environment>
```

Switch/pull behavior must operate at the logical worktree level when mixed storage is involved.

Before changing representation between environments (for example an age-backed path becoming plain), prepare/remove the incompatible logical materialization so Git checkout does not fail because of an untracked-file collision.

Do not implement a separate environment state database that competes with Git branches.

## Attach is a high-level transaction

`attach` should be a complete intent-level operation. Users and agents should not need to understand lgit's internal remote ref namespace, companion GitDir layout, checkout mechanics, or encryption store to attach a machine.

Attach design order:

```text
discover/select project and environment
inspect encryption requirements
validate credentials/identity
construct complete filesystem conflict plan
resolve according to policy
checkout/materialize
register root
```

Do not mutate the real worktree until preflight has identified all blockers it reasonably can.

Conflict detection must include **structural conflicts**, not only differing leaf file bytes.

Example:

```text
local:  .config/app/agents -> symlink
remote: .config/app/agents/ -> directory
```

That conflict must be detected before Git checkout.

`--use-remote` means the remote representation wins. Back up every local entry that blocks materializing the target remote tree, including structural blockers.

`--keep-local` may preserve ordinary file-content conflicts after checkout, but it cannot preserve incompatible simultaneous filesystem structures. Explain that case before mutation.

A failed attach should leave the user's filesystem and registration state as close as possible to the pre-attach state. Prefer explicit snapshots/backups/rollback over hoping Git checkout can unwind everything.

Internal Git setup/fetch/checkout chatter should normally be quiet. User-facing output should describe lgit-level intent and actionable problems.

## Introspection and agent usability

The CLI should be self-describing enough that an agent does not need to:

- inspect `projects.json`;
- fetch lgit source code just to find a key path;
- use `git ls-remote` to discover lgit projects/environments;
- manually inspect `.lgit/store`;
- bare-clone the remote to diagnose normal attach conflicts.

Prefer first-class lgit introspection such as:

```text
lgit info
lgit info --json
lgit key path
lgit key status
lgit key status --json
lgit remote list
lgit remote list --json
lgit attach ... --dry-run
lgit attach ... --dry-run --json
```

When adding automation-relevant state, prefer a stable structured output mode instead of requiring agents to scrape human prose.

`lgit git ...` remains an expert/debugging escape hatch. Do not make raw companion Git commands the normal discovery or recovery workflow.

## Sync architecture

`lgit sync` is the high-level synchronization boundary. Keep it plan-first and logical-path-aware. Dry-run and execution must share the same planner so preview behavior cannot drift from real behavior.

Default `sync` is receive-only. `sync --push` may automatically stage and commit modifications/deletions of already-tracked logical files, but it must never implicitly add new untracked files. Directory deletion is represented by deletion of its tracked logical descendants.

Sync conflict reporting must use logical paths. Never expose encrypted `.lgit/store/*.age` paths as merge conflicts. If a conflict cannot be resolved safely at the logical layer, fail before entering a partial Git conflict state.

A dry-run must not persistently mutate the worktree, companion index, current branch, registry, commits, or remote refs. Remote inspection should use disposable state.

## CLI behavior principles

Match Git's mental model where possible.

Commands whose semantics depend on logical-path/storage translation must be lgit-aware. Commands that naturally operate on refs/commits may delegate to Git.

If a storage-aware operation is not implemented safely, return a clear explicit error rather than silently doing the wrong thing.

Prefer concise success behavior and actionable failures. Do not expose internal progress or Git setup details unless requested through a verbose/debug mechanism.

Clean-state checks must distinguish a genuinely dirty worktree from an inability to determine cleanliness. Return `(bool, error)` (or equivalent) and propagate Git/config/identity/decryption errors instead of converting them into misleading "uncommitted changes" messages.

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

**GitHub Actions is validation-only.** Persistent CI workflows must use read-only repository permissions and must never author commits, amend commits, push branches, or rewrite refs. Do not use `github-actions[bot]` as a permanent implementation author/committer. If an automated environment is needed to run formatting, profiling, or platform-specific tests, publish the result as logs/artifacts only; apply the resulting repository changes through a user-attributed commit path and then let CI validate that commit.

Temporary write-capable workflows are not an acceptable implementation mechanism for lgit development. Do not add migration/profiling workflows that commit or push back into the repository, even temporarily.

## Repository-specific development constraints

Do not add Superpowers-specific artifacts, workflows, plans, `docs/superpowers`, or references. Previous Superpowers references were explicitly removed from history and should not be reintroduced.

Use neutral, product-focused commit messages.

Do not rewrite history unless there is a deliberate reason and the consequences are understood. Prefer normal forward fixes.

When modifying behavior, update the README for humans and `skills/lgit/SKILL.md` when operational guidance for agents changes. Keep this `AGENTS.md` focused on implementation decisions and contributor guardrails.
