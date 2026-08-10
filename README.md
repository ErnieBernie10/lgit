# lgit

`lgit` is a Git-backed companion repository for files that should not live in a project's normal Git repository. It keeps the real files in their normal locations while storing version history in an external Git directory.

It supports both ordinary Git storage and age-encrypted storage on a per-path basis, and it can also manage standalone roots such as your home directory for dotfiles.

## Model

- Git remains authoritative for tracked files, commits, branches, refs, history and remotes.
- One external local companion Git repository exists per lgit root.
- One optional shared remote can contain many projects.
- Project environments use namespaced remote branches such as `projects/booking-a43f91d2/envs/pcx`.
- `lgit` translates logical paths to their physical Git representation only when a storage backend requires it.

## Install

```bash
go install github.com/ErnieBernie10/lgit/cmd/lgit@latest
```

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

For a normal project clone:

```bash
git clone git@github.com:company/booking.git
cd Booking
lgit remote set git@github.com:you/lgit-data.git
lgit attach --env PCX
```

`lgit` discovers the remote project from the repository folder name. When multiple projects match, select one explicitly:

```bash
lgit attach --project booking-a43f91d2 --env PCX
```

For a standalone root, select the root explicitly:

```bash
lgit --root ~ remote set git@github.com:you/lgit-data.git
lgit --root ~ attach --project arne-a43f91d2 --env desktop
```

Attachment refuses differing existing files by default. Use `--keep-local` to preserve local contents as modifications or `--use-remote` to replace them after backing them up under the lgit data directory.

## Encryption

Password mode uses age scrypt encryption and never stores the password:

```bash
lgit init --env PCX --encryption password
```

For controlled non-interactive automation, `LGIT_PASSWORD` can provide the password.

Identity mode uses an age X25519 identity stored outside repositories:

```bash
lgit key generate
lgit key show
lgit key export identity.txt
lgit key import identity.txt
```

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

Raw Git operates on physical companion paths, so encrypted files appear under `.lgit/store`.

## Safety and current limits

- A project-local lgit repository refuses paths already tracked by the normal Git repository.
- Parent and child lgit roots cannot both own the same logical path.
- Recursive add stops at nested Git and lgit roots.
- Age storage currently supports regular files; symlink storage is rejected rather than silently changing semantics.
- Companion repositories set `core.autocrlf=false` so plain and encrypted backends preserve bytes consistently across platforms.
- `git clean -x` or `git clean -X` in a normal project can still delete ignored plaintext files managed by lgit. Committed state can be restored; uncommitted edits cannot.

## Storage location

```bash
lgit data-dir
lgit list
```

Set `LGIT_DATA_DIR` to override the platform user configuration directory.

## License

MIT
