# Agent guidance for lgit

When configuring `lgit`, preserve Git as the version-control engine and use lgit roots to model real configuration ownership boundaries.

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
lgit attach --root "$env:LOCALAPPDATA\nvim" --env windows --project <neovim-project>
```

Do not try to make a home-level lgit root translate `.config/nvim` into `AppData/Local/nvim`. Avoid adding platform path aliases, path rewriting, or mapping rules unless the product explicitly gains that feature later.

## Home roots are for genuinely home-relative files

A standalone `$HOME` root is appropriate for files whose logical location is actually rooted at home, for example `.gitconfig` or other directly home-relative configuration. Applications with their own platform-specific config directory should generally get their own lgit root.

## Nested roots are intentional

A home-level lgit root and application/project-specific roots may coexist. lgit resolves the nearest registered root, so a nested application or project root should own its own files. Do not work around this by flattening everything into a single dotfiles root.

When deciding between one broad root and several smaller roots, prefer the smaller root when it corresponds to a clear application/configuration boundary, especially when that boundary improves cross-platform portability.