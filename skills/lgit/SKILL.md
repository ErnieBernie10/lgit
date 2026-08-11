---
name: lgit
description: Operate and configure the lgit CLI for project-local files, dotfiles, standalone roots, encrypted storage, remote attachment, and environment management. Use when an agent needs to initialize, attach, inspect, synchronize, or troubleshoot an lgit-managed root.
license: MIT
compatibility: Requires the lgit CLI and Git. Some workflows also require age identity material or an lgit password.
metadata:
  author: ErnieBernie10
  version: "1.0"
---

# lgit usage

Use `lgit` as the user-facing interface for lgit concepts. Git remains the version-control engine underneath, but raw companion-repository internals are an expert debugging surface, not the normal operating interface.

## Prefer intent-level lgit commands

For a fresh machine, prefer a single attach operation:

```sh
lgit --root PATH attach REMOTE --env ENV
```

Do not manually inspect `refs/heads/projects/.../envs/...`, `projects.json`, companion Git directories, or lgit source code when the CLI exposes the information directly.

To inspect a remote before attaching:

```sh
lgit remote list REMOTE
lgit remote list REMOTE --json
```

Before a potentially conflicting attach, use the built-in preflight:

```sh
lgit --root PATH attach REMOTE --env ENV --dry-run
lgit --root PATH attach REMOTE --env ENV --dry-run --json
```

`attach` validates selection, encryption requirements, content conflicts, and filesystem-structure conflicts before modifying the root.

If `--use-remote` is chosen, let lgit own backup and replacement of conflicting entries. Do not manually move files into lgit's backup directory to work around checkout conflicts. If attach cannot execute the plan it reported during preflight, treat that as an lgit bug.

For current state, use:

```sh
lgit --root PATH info
lgit --root PATH info --json
```

For age identity discovery, use:

```sh
lgit key path
lgit key status
lgit key status --json
```

Do not guess platform-specific lgit data or identity paths.

When machine-readable output is available, prefer `--json` instead of parsing human-oriented output.

## Choose roots by configuration ownership

Use a home-directory root for files that are genuinely home-relative, such as `.gitconfig`:

```sh
lgit init --root ~ --env desktop --default plain
```

Use a smaller application-specific standalone root when an application's configuration lives in different physical locations across operating systems.

For example, Neovim commonly uses different config roots:

- Linux: `~/.config/nvim`
- Windows: `%LOCALAPPDATA%\nvim`

Treat those as different local roots for the same logical lgit project. The files inside the roots remain portable (`init.lua`, `lua/...`, `after/...`) while the absolute filesystem location is machine-specific.

Linux example:

```sh
lgit init --root ~/.config/nvim --env linux --default plain
```

Windows example:

```powershell
lgit --root "$env:LOCALAPPDATA\nvim" attach REMOTE --env windows --project <neovim-project>
```

Do not introduce path remapping inside a broad home-directory root to translate `.config/nvim` into `AppData/Local/nvim`. Prefer an application-specific root unless lgit explicitly gains a path-mapping feature.

## Nested roots are intentional

A home-level lgit root can coexist with application- or project-specific lgit roots below it. lgit resolves the nearest registered root.

If `$HOME` is an lgit root and `~/code/Booking` is another lgit root, commands inside `Booking` operate on the Booking root. To explicitly target the parent root:

```sh
lgit --root ~ status
```

Recursive `lgit add` stops at nested lgit roots and nested normal Git repositories. Do not flatten unrelated projects into one broad dotfiles root merely to avoid nesting.

## Storage backend guidance

Use `plain` when the file can be stored as an ordinary Git blob. Use `age` when its contents should be encrypted in the companion repository.

Inspect or change a path's storage backend with:

```sh
lgit storage show PATH
lgit storage set PATH plain
lgit storage set PATH age
lgit storage unset PATH
lgit storage default
```

Changing the default does not reinterpret or bulk-migrate existing tracked paths.

## Attach conflict handling

Default attach behavior refuses conflicting local entries.

Use `--keep-local` only for regular-file content conflicts where the local contents should remain as modifications after attach.

Use `--use-remote` when remote contents should win. lgit backs up conflicting local entries before replacement.

Use `--dry-run` first when the root contains important existing configuration:

```sh
lgit --root PATH attach REMOTE --env ENV --dry-run --json
```

Structural conflicts are different from content conflicts. A local file, symlink, junction, or directory can block a path hierarchy required by the remote. `--keep-local` cannot preserve incompatible filesystem structures; use `--use-remote` if replacement is intended.

## Git usage

Use lgit-aware commands for worktree operations:

```sh
lgit add PATH
lgit status
lgit diff PATH
lgit restore PATH
lgit pull
lgit env switch ENV
```

Normal version-control commands remain Git-backed:

```sh
lgit commit -m "Update configuration"
lgit log
lgit push
```

Use raw companion Git only as a debugging escape hatch:

```sh
lgit git status
lgit git ls-files
```

Raw Git exposes physical companion paths such as `.lgit/store/...` for encrypted files, so do not use it as the normal discovery or automation interface.

## Troubleshooting sequence

When an lgit command behaves unexpectedly:

1. Run `lgit --root PATH info --json`.
2. For remote selection problems, run `lgit remote list REMOTE --json`.
3. For attach problems, run the same attach command with `--dry-run --json`.
4. For identity-mode encryption problems, run `lgit key status --json` and `lgit key path`.
5. Use `lgit git ...` or inspect internals only after the lgit-facing interfaces are insufficient.

Prefer reporting a reproducible lgit bug over manually modifying companion Git internals.