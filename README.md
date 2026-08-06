# lgit

`lgit` tracks ignored, project-local files with Git while keeping its Git metadata outside the normal project repository.

## Model

- One external local companion repository per project checkout.
- One optional shared remote repository for all projects.
- One namespaced branch per project and environment.
- The current environment's files live directly in the normal project folder; there is no copied-file synchronization layer.

Remote branches look like:

```text
projects/booking-a43f91d2/envs/pcx
projects/booking-a43f91d2/envs/pcy
projects/amend-82d71c0a/envs/pcx
```

## Install

```bash
go install github.com/ErnieBernie10/lgit/cmd/lgit@latest
```

## First computer

```bash
cd Booking
lgit init --env PCX
lgit remote set git@github.com:you/lgit-data.git

lgit add .env docker-compose.override.yml
lgit commit -m "Add PCX environment"
lgit push
```

`lgit add` force-adds ignored files. Other untracked project files remain hidden from `lgit status` by default.

## Attach on another computer

The main repository can be cloned into any absolute path. `lgit` identifies the remote project from the main Git work-tree folder name.

```bash
git clone git@github.com:company/booking.git
cd Booking

lgit remote set git@github.com:you/lgit-data.git
lgit attach --env PCX
```

`Booking`, `/home/user/code/Booking`, and `D:\Work\Booking` all resolve to the slug `booking`.

When more than one remote project matches the same folder name, select one explicitly:

```bash
lgit attach --project booking-a43f91d2 --env PCX
```

### Existing local files

Attachment aborts when a remote-tracked file already exists locally with different contents.

Preserve the local versions as uncommitted companion changes:

```bash
lgit attach --env PCX --keep-local
```

Replace them with the remote versions and back up the local files under the lgit data directory:

```bash
lgit attach --env PCX --use-remote
```

The two flags are mutually exclusive.

## Environments

```bash
lgit env current
lgit env branch
lgit env list
lgit env create PCY
lgit env switch PCX
```

Environment names are normalized to lowercase branch-safe names. Environment switches are refused while the companion work tree has uncommitted changes.

Remote-only environments are fetched and turned into local environment branches when switched to.

## Shared remote

The remote configuration is global to the local lgit installation, so it can be set before a project is attached:

```bash
lgit remote set git@github.com:you/lgit-data.git
```

Each local companion repository fetches only branches for its own project namespace.

## Normal Git commands

Commands not owned by `lgit` are passed to Git using the external Git directory and the real project folder as the work tree:

```bash
lgit status
lgit diff
lgit log
lgit restore .env
lgit commit -m "Update local configuration"
lgit push
lgit pull
```

## Safety

- The main repository and lgit must not track the same path. Attachment rejects such collisions.
- `git clean -x` and `git clean -X` in the main repository can delete ignored files managed by lgit. Committed files are recoverable with `lgit restore`; uncommitted edits are not.
- The shared remote is not an access-control boundary. Encryption can hide contents, but branch names, filenames, commit messages, and object metadata remain visible to users with repository access.

## Storage

```bash
lgit data-dir
lgit list
```

Set `LGIT_DATA_DIR` to override the platform user configuration directory.

## License

MIT
