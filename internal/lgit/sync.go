package lgit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"filippo.io/age"
)

type SyncOptions struct {
	Push   bool
	DryRun bool
	JSON   bool
}

type SyncChanges struct {
	Modified []string `json:"modified"`
	Deleted  []string `json:"deleted"`
}

type SyncPlan struct {
	Environment        string      `json:"environment"`
	LocalChanges       SyncChanges `json:"local_changes"`
	LocalCommitsAhead  int         `json:"local_commits_ahead"`
	RemoteCommitsAhead int         `json:"remote_commits_ahead"`
	Diverged           bool        `json:"diverged"`
	RemoteExists       bool        `json:"remote_exists"`
	WouldCommit        bool        `json:"would_commit"`
	WouldPull          bool        `json:"would_pull"`
	WouldPush          bool        `json:"would_push"`
	Conflicts          []string    `json:"conflicts"`
}

func parseSyncOptions(args []string) (SyncOptions, error) {
	var o SyncOptions
	for _, arg := range args {
		switch arg {
		case "--push":
			o.Push = true
		case "--dry-run":
			o.DryRun = true
		case "--json":
			o.JSON = true
		default:
			return o, fmt.Errorf("unknown sync option %q", arg)
		}
	}
	if o.JSON && !o.DryRun {
		return o, fmt.Errorf("--json currently requires --dry-run")
	}
	return o, nil
}

func (a App) syncCommand(root string, args []string) int {
	o, err := parseSyncOptions(args)
	if err != nil {
		return a.fail(fmt.Errorf("usage: lgit sync [--push] [--dry-run] [--json]: %w", err))
	}
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}

	clean, err := a.mixedClean(root, p)
	if err != nil {
		return a.fail(fmt.Errorf("cannot sync: determine working tree state: %w", err))
	}
	if !o.Push && !clean {
		return a.fail(fmt.Errorf("cannot sync: local tracked files have uncommitted changes; use 'lgit sync --push' to commit and publish them"))
	}

	plan, err := a.buildSyncPlan(root, p, o)
	if err != nil {
		return a.fail(err)
	}
	if o.DryRun {
		return a.renderSyncPlan(plan, o.JSON)
	}
	if len(plan.Conflicts) > 0 {
		return a.fail(fmt.Errorf("sync conflict: local and remote both changed: %s", strings.Join(plan.Conflicts, ", ")))
	}
	return a.executeSyncPlan(root, p, o, plan)
}

func (a App) buildSyncPlan(root string, p Project, o SyncOptions) (SyncPlan, error) {
	plan := SyncPlan{Environment: p.Environment}
	changes, _, err := a.trackedWorkingChanges(root, p)
	if err != nil {
		return plan, err
	}
	plan.LocalChanges = changes
	plan.WouldCommit = o.Push && (len(changes.Modified) > 0 || len(changes.Deleted) > 0 || companionIndexDirty(root, p))

	remote, err := gitOutput(root, p.GitDir, "remote", "get-url", "origin")
	if err != nil {
		return plan, fmt.Errorf("cannot sync: shared remote is not configured")
	}
	remote = strings.TrimSpace(remote)

	graph, err := inspectSyncGraph(root, p, remote)
	if err != nil {
		return plan, err
	}
	if graph.dir != "" {
		defer os.RemoveAll(graph.dir)
	}
	plan.RemoteExists = graph.remoteExists
	plan.LocalCommitsAhead = graph.localAhead
	plan.RemoteCommitsAhead = graph.remoteAhead
	plan.Diverged = graph.localAhead > 0 && graph.remoteAhead > 0
	plan.WouldPull = graph.remoteExists && graph.remoteAhead > 0
	plan.WouldPush = o.Push && (!graph.remoteExists || graph.localAhead > 0 || plan.WouldCommit || plan.Diverged)

	if graph.remoteExists && graph.remoteAhead > 0 && (graph.localAhead > 0 || plan.WouldCommit) {
		conflicts, err := syncLogicalConflicts(root, p, graph, changes)
		if err != nil {
			return plan, err
		}
		plan.Conflicts = conflicts
	}
	return plan, nil
}

type syncGraph struct {
	dir          string
	remoteExists bool
	localAhead   int
	remoteAhead  int
	mergeBase    string
}

func inspectSyncGraph(root string, p Project, remote string) (syncGraph, error) {
	var g syncGraph
	d, err := os.MkdirTemp("", "lgit-sync-graph-")
	if err != nil {
		return g, err
	}
	g.dir = d
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(d)
		}
	}()
	if err := runSyncGitQuiet(d, "init", "--bare", d); err != nil {
		return g, err
	}
	if err := runSyncGitQuiet(d, "--git-dir="+d, "fetch", p.GitDir, "HEAD:refs/heads/local"); err != nil {
		return g, fmt.Errorf("inspect local sync history: %w", err)
	}
	ref := "refs/heads/" + remoteBranch(p, p.Environment)
	ls := exec.Command("git", "ls-remote", "--heads", remote, ref)
	out, err := ls.CombinedOutput()
	if err != nil {
		return g, fmt.Errorf("inspect remote environment: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		cleanupOnError = false
		return g, nil
	}
	g.remoteExists = true
	if err := runSyncGitQuiet(d, "--git-dir="+d, "fetch", remote, ref+":refs/heads/remote"); err != nil {
		return g, fmt.Errorf("fetch remote environment for sync plan: %w", err)
	}
	counts, err := gitCmdOutput(d, "--git-dir="+d, "rev-list", "--left-right", "--count", "refs/heads/local...refs/heads/remote")
	if err != nil {
		return g, err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return g, fmt.Errorf("unexpected git ahead/behind result %q", counts)
	}
	g.localAhead, _ = strconv.Atoi(fields[0])
	g.remoteAhead, _ = strconv.Atoi(fields[1])
	base, err := gitCmdOutput(d, "--git-dir="+d, "merge-base", "refs/heads/local", "refs/heads/remote")
	if err != nil {
		return g, fmt.Errorf("local and remote environment histories do not share a merge base")
	}
	g.mergeBase = strings.TrimSpace(base)
	cleanupOnError = false
	return g, nil
}

func runSyncGitQuiet(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func gitCmdOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func companionIndexDirty(root string, p Project) bool {
	cmd := exec.Command("git", "--git-dir="+p.GitDir, "--work-tree="+root, "diff", "--cached", "--quiet")
	cmd.Dir = root
	return cmd.Run() != nil
}

func indexLogicalTracked(root string, p Project) (map[string]StorageBackend, error) {
	out, err := gitOutput(root, p.GitDir, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	m := map[string]StorageBackend{}
	for _, raw := range strings.Split(out, "\x00") {
		path := filepath.ToSlash(raw)
		if path == "" || strings.HasPrefix(path, ".lgit/") && !strings.HasPrefix(path, ".lgit/store/") {
			continue
		}
		if strings.HasPrefix(path, ".lgit/store/") {
			m[plainPath(path)] = StorageAge
		} else {
			m[path] = StoragePlain
		}
	}
	return m, nil
}

func (a App) trackedWorkingChanges(root string, p Project) (SyncChanges, map[string]StorageBackend, error) {
	var changes SyncChanges
	tracked := map[string]StorageBackend{}
	head, headErr := logicalTrackedAt(root, p, "HEAD")
	if headErr == nil {
		for path, backend := range head {
			tracked[path] = backend
		}
	}
	idx, err := indexLogicalTracked(root, p)
	if err != nil {
		return changes, nil, err
	}
	for path, backend := range idx {
		tracked[path] = backend
	}
	if len(tracked) == 0 {
		return changes, tracked, nil
	}

	var c StorageConfig
	var id age.Identity
	configLoaded := false
	for path, backend := range tracked {
		full := filepath.Join(root, filepath.FromSlash(path))
		got, err := os.ReadFile(full)
		if os.IsNotExist(err) {
			changes.Deleted = append(changes.Deleted, path)
			continue
		}
		if err != nil {
			return changes, nil, err
		}
		if headErr != nil {
			changes.Modified = append(changes.Modified, path)
			continue
		}
		headBackend, inHead := head[path]
		if !inHead {
			changes.Modified = append(changes.Modified, path)
			continue
		}
		var want []byte
		if headBackend == StoragePlain {
			want, err = gitBlob(root, p.GitDir, "HEAD:"+path)
		} else {
			if !configLoaded {
				c, err = loadStorageConfig(root)
				if err != nil {
					return changes, nil, err
				}
				configLoaded = true
			}
			if id == nil {
				id, err = a.storageIdentity(root, c)
				if err != nil {
					return changes, nil, err
				}
			}
			cipher, e := gitBlob(root, p.GitDir, "HEAD:"+filepath.ToSlash(storePath(path)))
			if e != nil {
				return changes, nil, e
			}
			want, err = decryptBytes(cipher, id)
		}
		if err != nil {
			return changes, nil, err
		}
		if backend != headBackend || !bytes.Equal(got, want) {
			changes.Modified = append(changes.Modified, path)
		}
	}
	sort.Strings(changes.Modified)
	sort.Strings(changes.Deleted)
	return changes, tracked, nil
}

func syncLogicalConflicts(root string, p Project, g syncGraph, working SyncChanges) ([]string, error) {
	localChanged, err := changedLogicalPaths(g.dir, g.mergeBase, "refs/heads/local")
	if err != nil {
		return nil, err
	}
	remoteChanged, err := changedLogicalPaths(g.dir, g.mergeBase, "refs/heads/remote")
	if err != nil {
		return nil, err
	}
	for _, path := range working.Modified {
		localChanged[path] = true
	}
	for _, path := range working.Deleted {
		localChanged[path] = true
	}
	var conflicts []string
	for path := range localChanged {
		if remoteChanged[path] {
			// Conservative by design: encrypted blobs cannot be merged safely at the
			// physical layer, and sync must not enter a partial Git conflict state.
			// Plain-path auto-merging can be relaxed later with a preflight merge-tree.
			conflicts = append(conflicts, path)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func changedLogicalPaths(gitDir, base, ref string) (map[string]bool, error) {
	out, err := gitCmdOutput(gitDir, "--git-dir="+gitDir, "diff", "--name-only", base+".."+ref)
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, raw := range strings.Split(strings.TrimSpace(out), "\n") {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if path == "" {
			continue
		}
		if strings.HasPrefix(path, ".lgit/store/") {
			m[plainPath(path)] = true
			continue
		}
		if strings.HasPrefix(path, ".lgit/") {
			continue
		}
		m[path] = true
	}
	return m, nil
}

func (a App) renderSyncPlan(plan SyncPlan, asJSON bool) int {
	if asJSON {
		b, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprintln(a.Stdout, string(b))
		return 0
	}
	if len(plan.LocalChanges.Modified) == 0 && len(plan.LocalChanges.Deleted) == 0 && !plan.WouldCommit && !plan.WouldPull && !plan.WouldPush && !plan.Diverged {
		fmt.Fprintln(a.Stdout, "Already synchronized.")
		return 0
	}
	if len(plan.LocalChanges.Modified) > 0 || len(plan.LocalChanges.Deleted) > 0 {
		fmt.Fprintln(a.Stdout, "Local changes:")
		for _, p := range plan.LocalChanges.Modified {
			fmt.Fprintf(a.Stdout, "  M %s\n", p)
		}
		for _, p := range plan.LocalChanges.Deleted {
			fmt.Fprintf(a.Stdout, "  D %s\n", p)
		}
		fmt.Fprintln(a.Stdout)
	}
	if len(plan.Conflicts) > 0 {
		fmt.Fprintln(a.Stdout, "Conflicts:")
		for _, p := range plan.Conflicts {
			fmt.Fprintf(a.Stdout, "  %s\n", p)
		}
		fmt.Fprintln(a.Stdout)
	}
	fmt.Fprintln(a.Stdout, "Would:")
	if plan.WouldCommit {
		fmt.Fprintf(a.Stdout, "  commit %d local changes\n", len(plan.LocalChanges.Modified)+len(plan.LocalChanges.Deleted))
	}
	if plan.WouldPull {
		fmt.Fprintf(a.Stdout, "  integrate %d remote commits\n", plan.RemoteCommitsAhead)
	}
	if plan.WouldPush {
		fmt.Fprintf(a.Stdout, "  push environment %s\n", plan.Environment)
	}
	if !plan.WouldCommit && !plan.WouldPull && !plan.WouldPush {
		fmt.Fprintln(a.Stdout, "  no changes")
	}
	fmt.Fprintln(a.Stdout, "\nNo changes made.")
	return 0
}

type syncSnapshot struct {
	head    string
	index   []byte
	indexOK bool
	files   map[string][]byte
	missing map[string]bool
}

func captureSyncSnapshot(root string, p Project, changes SyncChanges) (syncSnapshot, error) {
	s := syncSnapshot{files: map[string][]byte{}, missing: map[string]bool{}}
	head, err := gitOutput(root, p.GitDir, "rev-parse", "HEAD")
	if err != nil {
		return s, err
	}
	s.head = strings.TrimSpace(head)
	if b, err := os.ReadFile(filepath.Join(p.GitDir, "index")); err == nil {
		s.index = b
		s.indexOK = true
	}
	paths := append(append([]string{}, changes.Modified...), changes.Deleted...)
	for _, path := range paths {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if os.IsNotExist(err) {
			s.missing[path] = true
			continue
		}
		if err != nil {
			return s, err
		}
		s.files[path] = b
	}
	return s, nil
}

func (a App) restoreSyncSnapshot(root string, p Project, s syncSnapshot) {
	_ = a.run(root, p.GitDir, "reset", "--hard", s.head)
	_ = a.mixedMaterialize(root, p)
	for path, b := range s.files {
		dst := filepath.Join(root, filepath.FromSlash(path))
		_ = os.MkdirAll(filepath.Dir(dst), 0700)
		_ = os.WriteFile(dst, b, 0600)
	}
	for path := range s.missing {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(path)))
	}
	if s.indexOK {
		_ = os.WriteFile(filepath.Join(p.GitDir, "index"), s.index, 0600)
	}
}

func (a App) executeSyncPlan(root string, p Project, o SyncOptions, plan SyncPlan) int {
	snapshot, err := captureSyncSnapshot(root, p, plan.LocalChanges)
	if err != nil {
		return a.fail(err)
	}
	rollback := true
	defer func() {
		if rollback {
			a.restoreSyncSnapshot(root, p, snapshot)
		}
	}()

	if o.Push && plan.WouldCommit {
		_, tracked, err := a.trackedWorkingChanges(root, p)
		if err != nil {
			return a.fail(err)
		}
		paths := make([]string, 0, len(tracked))
		for path := range tracked {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		c, err := loadStorageConfig(root)
		if err != nil {
			return a.fail(err)
		}
		if err := a.mixedAddOptimized(root, p, c, paths); err != nil {
			return a.fail(err)
		}
		if companionIndexDirty(root, p) {
			if code := a.run(root, p.GitDir, "commit", "-m", "lgit sync"); code != 0 {
				return code
			}
		}
	}

	if plan.RemoteExists {
		if code := a.run(root, p.GitDir, "fetch", "origin", remoteBranch(p, p.Environment)); code != 0 {
			return code
		}
		remoteHead, err := gitOutput(root, p.GitDir, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return a.fail(err)
		}
		remoteHead = strings.TrimSpace(remoteHead)
		localHead, err := gitOutput(root, p.GitDir, "rev-parse", "HEAD")
		if err != nil {
			return a.fail(err)
		}
		localHead = strings.TrimSpace(localHead)
		if localHead != remoteHead {
			ancestorRemote := isAncestor(root, p.GitDir, localHead, remoteHead)
			ancestorLocal := isAncestor(root, p.GitDir, remoteHead, localHead)
			if ancestorRemote || (!ancestorLocal && !ancestorRemote) {
				removed, err := a.checkoutPrepare(root, p, "FETCH_HEAD")
				if err != nil {
					return a.fail(err)
				}
				if code := a.run(root, p.GitDir, "merge", "--no-edit", "FETCH_HEAD"); code != 0 {
					restorePrepared(root, p, removed)
					return code
				}
				if err := a.mixedMaterialize(root, p); err != nil {
					return a.fail(err)
				}
				_ = os.RemoveAll(filepath.Join(p.GitDir, "transition"))
			}
		}
	}

	rollback = false
	if o.Push && plan.WouldPush {
		if code := a.push(root, nil); code != 0 {
			// Local synchronization succeeded. A failed publication should not
			// undo the local commit/merge; the next sync --push can retry it.
			return code
		}
	}
	return 0
}

func isAncestor(root, gitDir, ancestor, descendant string) bool {
	cmd := exec.Command("git", "--git-dir="+gitDir, "--work-tree="+root, "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = root
	return cmd.Run() == nil
}
