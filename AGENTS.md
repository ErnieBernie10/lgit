# Agent guidance for lgit

When configuring or operating `lgit`, preserve Git as the version-control engine and use lgit's user-facing commands for lgit concepts. Do not inspect raw remote refs, `projects.json`, companion Git internals, or source code when the CLI exposes the information directly.

## Prefer intent-level lgit commands

For a fresh machine, prefer one attach operation:

```sh
lgit --root PATH attach REMOTE --env ENV
```

Do not manually run `git ls-remote` to discover lgit's `refs/heads/projects/.../envs/...` namespace. If you need to inspect a remote first, use:

```sh
lgit remote list REMOTE
lgit remote list REMOTE --json
```

Before a potentially conflicting attach, use the built-in preflight rather than manually cloning or inspecting the remote Git tree:

```sh
lgit --root PATH attach REMOTE --env ENV --dry-run
lgit --root PATH attach REMOTE --env ENV --dry-run --json
```

`attach` validates encryption and both content and filesystem-structure conflicts before changing the root. If `--use-remote` is chosen, lgit owns backup and replacement of conflicting entries. Do not manually move files into lgit's backup directory to work around checkout conflicts; report an lgit bug if attach cannot execute its own preflight plan.

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

Do not guess Linux/macOS/Windows config-directory locations or read lgit source to find the identity path.

When machine-readable output is available, agents should prefer `--json` rather than parse human-oriented prose. Raw `lgit git ...` remains an expert debugging escape hatch, not the normal discovery API.

## Prefer application-specific standalone roots for cross-platform config

If an application's configuration lives at different physical paths on different operating systems, prefer a dedicated standalone lgit root for that application rather than introducing path remapping inside a broad home-directory root.

Example: Neovim uses different config locations across platforms:

- Linux: `~/.config/nvim`
- Windows: `%LOCALAPPDATA%\nvim`

Treat these as two local roots for the same logical lgit project. The files inside the root remain portable (`init.lua`, `lua/...`, `after/...`) while the absolute filesystem location is machine-specific.

Typical setup:

```sh
# Linux
lgit init --root ~/.config/nvim --env linux --default plain
```

```powershell
# Windows
lgit --root "$env:LOCALAPPDATA\nvim" attach REMOTE --env windows --project <neovim-project>
```

Do not try to make a home-level lgit root translate `.config/nvim` into `AppData/Local/nvim`. Avoid adding platform path aliases, path rewriting, or mapping rules unless the product explicitly gains that feature later.

## Home roots are for genuinely home-relative files

A standalone `$HOME` root is appropriate for files whose logical location is actually rooted at home, for example `.gitconfig` or other directly home-relative configuration. Applications with their own platform-specific config directory should generally get their own lgit root.

## Nested roots are intentional

A home-level lgit root and application/project-specific roots may coexist. lgit resolves the nearest registered root, so a nested application or project root should own its own files. Do not work around this by flattening everything into a single dotfiles root.

When deciding between one broad root and several smaller roots, prefer the smaller root when it corresponds to a clear application/configuration boundary, especially when that boundary improves cross-platform portability.