from pathlib import Path

# Wire sync into the dispatcher/help.
p = Path('internal/lgit/app.go')
s = p.read_text()
s = s.replace('''\tcase "pull":\n\t\treturn a.pullMixed(root, args[1:])\n\tcase "add":''', '''\tcase "pull":\n\t\treturn a.pullMixed(root, args[1:])\n\tcase "sync":\n\t\treturn a.syncCommand(root, args[1:])\n\tcase "add":''')
s = s.replace('''  lgit push | lgit pull\n  lgit git <raw git command>''', '''  lgit push | lgit pull | lgit sync [--push] [--dry-run] [--json]\n  lgit git <raw git command>''')
p.write_text(s)

# Tighten sync.go after review.
p = Path('internal/lgit/sync.go')
s = p.read_text()
s = s.replace('\t"errors"\n', '')
s = s.replace('''\tchanges, tracked, err := a.trackedWorkingChanges(root, p)\n\tif err != nil {''', '''\tchanges, _, err := a.trackedWorkingChanges(root, p)\n\tif err != nil {''')
s = s.replace('''\tgraph, err := inspectSyncGraph(root, p, remote)\n\tif err != nil {\n\t\treturn plan, err\n\t}\n''', '''\tgraph, err := inspectSyncGraph(root, p, remote)\n\tif err != nil {\n\t\treturn plan, err\n\t}\n\tif graph.dir != "" {\n\t\tdefer os.RemoveAll(graph.dir)\n\t}\n''')
s = s.replace('syncLogicalConflicts(root, p, graph, tracked, changes)', 'syncLogicalConflicts(root, p, graph, changes)')
s = s.replace('func syncLogicalConflicts(root string, p Project, g syncGraph, tracked map[string]StorageBackend, working SyncChanges) ([]string, error) {\n\tdefer os.RemoveAll(g.dir)\n', 'func syncLogicalConflicts(root string, p Project, g syncGraph, working SyncChanges) ([]string, error) {\n')
s = s.replace('''\tdefer func() {\n\t\t// buildSyncPlan owns a temporary graph directory when a remote exists.\n\t\t// It is safe to remove here after planning is complete.\n\t}()\n\n''', '')
s = s.replace('\nvar errSyncNoHead = errors.New("sync requires an initial commit")\n', '\n')
s = s.replace('runGitQuiet(d, "init", "--bare", d)', 'runSyncGitQuiet(d, "init", "--bare", d)')
s = s.replace('runGitQuiet(d, "--git-dir="+d, "fetch", p.GitDir, "HEAD:refs/heads/local")', 'runSyncGitQuiet(d, "--git-dir="+d, "fetch", p.GitDir, "HEAD:refs/heads/local")')
s = s.replace('runGitQuiet(d, "--git-dir="+d, "fetch", remote, ref+":refs/heads/remote")', 'runSyncGitQuiet(d, "--git-dir="+d, "fetch", remote, ref+":refs/heads/remote")')
s = s.replace('func runGitQuiet(dir string, args ...string) error {', 'func runSyncGitQuiet(dir string, args ...string) error {')
p.write_text(s)

# README: add sync to the normal workflow before safety/current limits.
p = Path('README.md')
s = p.read_text()
marker = '## Safety and current limits\n'
section = '''## Synchronizing an environment\n\nUse `sync` for the normal cross-machine workflow instead of manually sequencing add/commit/pull/push.\n\n```bash\n# Receive and reconcile committed remote changes.\nlgit sync\n\n# Preview what a bidirectional sync would do.\nlgit sync --push --dry-run\n\n# Reconcile already-tracked local modifications/deletions, create an\n# `lgit sync` commit when needed, integrate remote changes, and push.\nlgit sync --push\n```\n\n`sync --push` deliberately operates only on logical paths that are already tracked. New untracked files are never captured implicitly; add them explicitly with `lgit add PATH` first. Removing a tracked directory is supported: sync discovers the missing tracked descendants and commits their deletions.\n\nDry-run is non-mutating and can be consumed by automation:\n\n```bash\nlgit sync --dry-run\nlgit sync --push --dry-run --json\n```\n\nIf local and remote histories changed the same logical path independently, sync reports the logical conflict before mutating the worktree rather than exposing `.lgit/store` ciphertext conflicts.\n\n'''
if marker not in s:
    raise SystemExit('README marker not found')
if '## Synchronizing an environment' not in s:
    s = s.replace(marker, section + marker)
p.write_text(s)

# Skill: teach agents the high-level sync workflow.
p = Path('skills/lgit/SKILL.md')
s = p.read_text()
append = '''\n## Routine synchronization\n\nPrefer the first-class sync workflow over manually sequencing raw Git operations:\n\n```sh\nlgit sync --dry-run\nlgit sync\nlgit sync --push --dry-run\nlgit sync --push\n```\n\n`lgit sync` receives/reconciles committed remote changes and refuses dirty tracked state. `lgit sync --push` treats modifications and deletions of already-tracked logical files as local intent, stages them, creates an `lgit sync` commit when needed, integrates remote history, and pushes the current environment. It does not implicitly add new untracked files.\n\nUse `--dry-run --json` when an agent needs a machine-readable plan. Treat the logical paths in `conflicts` as user-facing conflicts; do not inspect `.lgit/store` or attempt to resolve encrypted physical blobs with raw Git.\n'''
if '## Routine synchronization' not in s:
    s = s.rstrip() + '\n' + append
p.write_text(s)

# AGENTS: record the architectural invariant.
p = Path('AGENTS.md')
s = p.read_text()
marker = '## Filesystem safety\n'
section = '''## Sync architecture\n\n`lgit sync` is the high-level synchronization boundary. Keep it plan-first and logical-path-aware. Dry-run and execution must share the same planner so that preview behavior cannot drift from real behavior.\n\nDefault `sync` is receive-only. `sync --push` may automatically stage and commit modifications/deletions of already-tracked logical files, but it must never implicitly add new untracked files. Directory deletion is represented by deletion of its tracked logical descendants.\n\nSync conflict reporting must use logical paths. Never expose encrypted `.lgit/store/*.age` paths as merge conflicts. If a conflict cannot be resolved safely at the logical layer, fail before entering a partial Git conflict state.\n\nA dry-run must not persistently mutate the worktree, companion index, current branch, registry, commits, or remote refs. Remote inspection should use disposable state.\n\n'''
if marker not in s:
    raise SystemExit('AGENTS marker not found')
if '## Sync architecture' not in s:
    s = s.replace(marker, section + marker)
p.write_text(s)
