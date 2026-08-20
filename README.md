# lgit

`lgit` is a Git-backed companion repository for files that should not live in a project's normal Git repository. It keeps the real files in their normal locations while storing version history in an external Git directory.

It supports both ordinary Git storage and age-encrypted storage on a per-path basis, and it can also manage standalone roots such as your home directory for dotfiles.

## Model

- Git remains authoritative for tracked files, commits, branches, refs, history and remotes.
- One external local companion Git repository exists per lgit root.
- One optional shared remote can contain many projects.
- Project environments use namespaced remote branches such as `projects/booking-a43f91d2/envs/pcx`.
- `lgit` translates logical paths to their physical Git representation only when a storage backend requires it.
- User-facing commands report logical lgit concepts. Raw Git ref names, temporary Git repositories, and checkout mechanics are implementation details.

## Install

```bash
go install github.com/ErnieBernie10/lgit/cmd/lgit@latest
```

## Agent skill

The repository ships a reusable Agent Skill at [`skills/lgit/SKILL.md`](skills/lgit/SKILL.md). It describes how an agent should operate lgit, choose roots, handle cross-platform configuration, inspect state, and troubleshoot attach/encryption issues using the user-facing CLI rather than repository internals.

Install or copy the `skills/lgit` directory using your agent client's normal skill mechanism. The root of this repository intentionally does not contain usage-oriented `AGENTS.md` instructions; repository-level agent instructions should be reserved for contributing to lgit itself.

## Project-local files

The default storage backend is `age`, preserving the original lgit use case:

```bash
cd Booking
lgit init --env PCX --encryption password
lgit remote set git@github.com:you/lgit-data.git

lgit add .env docker-compose.override.yml
lgit commit -m "Add PCX environment"
lgit push
```

The normal project contains the plaintext `.env`; the companion Git tree stores `.lgit/store/.env.age`.

Identity encryption remains available and is the default encryption mode:

```bash
lgit init --env PCX --encryption identity
```

## Storage backends

There are currently two storage backends:

- `plain`: the logical path is stored as a normal Git blob.
- `age`: the logical plaintext stays in the work tree while Git stores an age-encrypted blob under `.lgit/store`.

The storage configuration is intentionally small:

```toml
version = 1
default = "plain"

[encryption]
mode = "password"

[files]
".npmrc" = "age"
".ssh/config" = "age"
```

Git remains the source of truth for what is tracked. `storage.toml` only controls how a path is represented.

Inspect or change storage with:

```bash
lgit storage show .npmrc
lgit storage set .npmrc age
lgit storage set .gitconfig plain
lgit storage unset .npmrc
lgit storage default
lgit storage default plain
```

`storage set` migrates an already-tracked path atomically between the plain and age representations. Changing the repository default does not bulk-migrate existing tracked paths.

## Dotfiles / standalone roots

A normal Git repository is not required. To use your home directory as an lgit root:

```bash
lgit init --root ~ --env desktop --default plain --encryption password

lgit add ~/.gitconfig
lgit add ~/.config/helix
lgit storage set ~/.npmrc age
lgit add ~/.npmrc
```

No `.git` directory is created in your home directory. The companion Git directory stays under the lgit data directory.

Nested lgit projects are supported. If `$HOME` is an lgit root and `~/code/Booking` has its own lgit project, commands inside Booking automatically use the nearest registered root. To explicitly operate on the home-level repository from inside a nested project:

```bash
lgit --root ~ status
```

Recursive adds stop at nested lgit roots and nested normal Git repositories, so `lgit add .` from a home-directory root does not ingest source repositories below it.

## Attach on another computer

The simplest fresh-machine workflow is to give `attach` the shared remote directly:

```bash
git clone git@github.com:company/booking.git
cd Booking
lgit attach git@github.com:you/lgit-data.git --env PCX
```

For a standalone root:

```bash
lgit --root ~ attach git@github.com:you/lgit-data.git --env desktop
```

`REMOTE` is optional if a shared remote was configured earlier with `lgit remote set URL`. On a successful attach, a directly supplied remote becomes the configured shared remote.

lgit discovers project/environment refs itself. It first prefers a project whose name matches the local root. If selection is ambiguous, it reports the matching projects and asks for an explicit project:

```bash
lgit --root ~ attach git@github.com:you/lgit-data.git \
  --project arne-a43f91d2 --env desktop
```

You can inspect a remote without knowing lgit's Git ref layout:

```bash
lgit remote list git@github.com:you/lgit-data.git
lgit remote list git@github.com:you/lgit-data.git --json
```

### Attach preflight and conflicts

`attach` completes discovery and preflight before changing the user root. Preflight validates:

- the selected project/environment;
- storage and encryption metadata;
- availability of an age identity or password;
- conflicts where a local regular file differs from the remote;
- structural conflicts where the remote needs a path hierarchy that an existing file, symlink, junction, or directory entry blocks.

Preview the complete plan without changing anything:

```bash
lgit --root ~ attach git@github.com:you/lgit-data.git \
  --env desktop --dry-run

lgit --root ~ attach git@github.com:you/lgit-data.git \
  --env desktop --dry-run --json
```

Attachment refuses conflicts by default. `--keep-local` preserves differing regular-file contents as local modifications. It cannot preserve incompatible filesystem structures. `--use-remote` backs up every conflicting local entry and materializes the remote representation:

```bash
lgit --root ~ attach git@github.com:you/lgit-data.git \
  --env desktop --use-remote
```

Backups are stored under the lgit data directory and the successful command prints their location. Attach is transactional: if the apply phase fails, lgit restores the worktree snapshot and leaves the root unregistered.

Internal Git initialization/fetch/checkout output is quiet during attach. Use lgit's own errors and preflight output instead of interpreting raw Git setup chatter.

## Introspection

Use `info` when you or an automation need to understand lgit state without opening `projects.json` or inspecting the companion Git repository:

```bash
lgit info
lgit --root ~ info
lgit --root ~ info --json
```

For an attached root it reports the project, environment, remote, data directory, encryption/storage state, identity availability, and companion Git directory. For an unattached explicit root it reports that state without treating it as an error.

Focused help is available directly from the installed binary:

```bash
lgit attach --help
lgit key --help
lgit remote --help
```

## Encryption

Password mode uses age encryption and never stores the password:

```bash
lgit init --env PCX --encryption password
```

For controlled non-interactive automation, `LGIT_PASSWORD` can provide the password.

Identity mode uses an age X25519 identity stored outside repositories:

```bash
lgit key generate
lgit key show
lgit key path
lgit key status
lgit key status --json
lgit key export identity.txt
lgit key import identity.txt
```

`lgit key path` reports the exact platform-specific identity path, and `lgit key status` reports whether it is available. Agents and scripts should use these commands rather than guessing platform config directories or inspecting lgit source code.

The remote can still see branch names, commit metadata, logical path names encoded in encrypted store paths, and approximate object sizes. Age protects file contents, not repository metadata.

## Environments

```bash
lgit env current
lgit env branch
lgit env list
lgit env create PCY
lgit env switch PCX
```

Environment switching checks logical worktree cleanliness, handles plain/age representation changes, then materializes age-backed files after the Git branch switch.

## Git commands

Git remains the version-control engine:

```bash
lgit add .env
lgit status
lgit diff .env
lgit commit -m "Update local configuration"
lgit log
lgit push
lgit pull
```

Commands that need logical-path translation are handled by lgit. Other commands continue to delegate to the companion Git repository.

For an explicit raw Git escape hatch:

```bash
lgit git status
lgit git ls-files
```

Raw Git operates on physical companion paths, so encrypted files appear under `.lgit/store`. Raw Git is an expert escape hatch, not the normal way to discover lgit projects or diagnose attach state.

## Synchronizing an environment

Use `sync` for the normal cross-machine workflow instead of manually sequencing add/commit/pull/push.

```bash
# Receive and reconcile committed remote changes.
lgit sync

# Preview what a bidirectional sync would do.
lgit sync --push --dry-run

# Reconcile already-tracked local modifications/deletions, create an
# `lgit sync` commit when needed, integrate remote changes, and push.
lgit sync --push
```

`sync --push` deliberately operates only on logical paths that are already tracked. New untracked files are never captured implicitly; add them explicitly with `lgit add PATH` first. Removing a tracked directory is supported: sync discovers the missing tracked descendants and commits their deletions.

Dry-run is non-mutating and can be consumed by automation:

```bash
lgit sync --dry-run
lgit sync --push --dry-run --json
```

If local and remote histories changed the same logical path independently, sync reports the logical conflict before mutating the worktree rather than exposing `.lgit/store` ciphertext conflicts.

## Safety and current limits

- A project-local lgit repository refuses paths already tracked by the normal Git repository.
- Parent and child lgit roots cannot both own the same logical path.
- Recursive add stops at nested Git and lgit roots.
- Age storage currently supports regular files; symlink storage is rejected rather than silently changing semantics.
- Attach detects structural blockers before checkout and `--use-remote` backs them up before replacing them.
- Companion repositories set `core.autocrlf=false` so plain and encrypted backends preserve bytes consistently across platforms.
- `git clean -x` or `git clean -X` in a normal project can still delete ignored plaintext files managed by lgit. Committed state can be restored; uncommitted edits cannot.

## Storage location

```bash
lgit data-dir
lgit list
```

Set `LGIT_DATA_DIR` to override the platform user configuration directory.

## Machine-readable output

State/discovery operations intended for automation expose JSON where useful:

```bash
lgit info --json
lgit key status --json
lgit remote list REMOTE --json
lgit attach REMOTE --env NAME --dry-run --json
```

Prefer these interfaces in agents and scripts over parsing human-oriented prose or raw Git refs.

## License

MIT