from pathlib import Path
import re

p = Path('internal/lgit/sync.go')
s = p.read_text()

# Receive-only sync needs a clean tree; --push intentionally accepts tracked changes.
old = '''\tclean, err := a.mixedClean(root, p)\n\tif err != nil {\n\t\treturn a.fail(fmt.Errorf("cannot sync: determine working tree state: %w", err))\n\t}\n\tif !o.Push && !clean {\n\t\treturn a.fail(fmt.Errorf("cannot sync: local tracked files have uncommitted changes; use 'lgit sync --push' to commit and publish them"))\n\t}\n'''
new = '''\tif !o.Push {\n\t\tclean, err := a.mixedClean(root, p)\n\t\tif err != nil {\n\t\t\treturn a.fail(fmt.Errorf("cannot sync: determine working tree state: %w", err))\n\t\t}\n\t\tif !clean {\n\t\t\treturn a.fail(fmt.Errorf("cannot sync: local tracked files have uncommitted changes; use 'lgit sync --push' to commit and publish them"))\n\t\t}\n\t}\n'''
if old not in s:
    raise SystemExit('syncCommand clean block not found')
s = s.replace(old, new)

# Replace graph inspection so a new environment can use sync --push for its first commit.
start = s.index('func inspectSyncGraph(')
end = s.index('\nfunc runSyncGitQuiet', start)
replacement = r'''func inspectSyncGraph(root string, p Project, remote string) (syncGraph, error) {
	var g syncGraph
	ref := "refs/heads/" + remoteBranch(p, p.Environment)
	ls := exec.Command("git", "ls-remote", "--heads", remote, ref)
	out, err := ls.CombinedOutput()
	if err != nil {
		return g, fmt.Errorf("inspect remote environment: %v: %s", err, out)
	}
	g.remoteExists = strings.TrimSpace(string(out)) != ""
	if !g.remoteExists {
		return g, nil
	}
	if _, err := gitOutput(root, p.GitDir, "rev-parse", "--verify", "HEAD"); err != nil {
		return g, fmt.Errorf("remote environment already exists but local environment has no commits; attach the remote environment instead")
	}

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
'''
s = s[:start] + replacement + s[end:]

# Snapshot/rollback must tolerate an unborn branch.
s = s.replace('''type syncSnapshot struct {\n\thead    string\n\tindex   []byte''', '''type syncSnapshot struct {\n\thead    string\n\tunborn  bool\n\tindex   []byte''')
s = s.replace('''\thead, err := gitOutput(root, p.GitDir, "rev-parse", "HEAD")\n\tif err != nil {\n\t\treturn s, err\n\t}\n\ts.head = strings.TrimSpace(head)''', '''\thead, err := gitOutput(root, p.GitDir, "rev-parse", "--verify", "HEAD")\n\tif err != nil {\n\t\ts.unborn = true\n\t} else {\n\t\ts.head = strings.TrimSpace(head)\n\t}''')
s = s.replace('''func (a App) restoreSyncSnapshot(root string, p Project, s syncSnapshot) {\n\t_ = a.run(root, p.GitDir, "reset", "--hard", s.head)\n\t_ = a.mixedMaterialize(root, p)''', '''func (a App) restoreSyncSnapshot(root string, p Project, s syncSnapshot) {\n\tif s.unborn {\n\t\tif ref, err := gitOutput(root, p.GitDir, "symbolic-ref", "-q", "HEAD"); err == nil {\n\t\t\t_ = a.run(root, p.GitDir, "update-ref", "-d", strings.TrimSpace(ref))\n\t\t}\n\t} else {\n\t\t_ = a.run(root, p.GitDir, "reset", "--hard", s.head)\n\t\t_ = a.mixedMaterialize(root, p)\n\t}''')
p.write_text(s)

p = Path('internal/lgit/sync_test.go')
s = p.read_text()
append = r'''

func cloneRemoteEnv(t *testing.T, remote, branch string) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "clone", "--branch", branch, "--single-branch", remote, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone remote env: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "sync-test@example.invalid").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Sync Test").Run()
	return dir
}

func commitRemoteFile(t *testing.T, clone, remote, branch, path, content, message string) {
	t.Helper()
	full := filepath.Join(clone, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-C", clone, "add", "--", path}, {"-C", clone, "commit", "-m", message}, {"-C", clone, "push", "origin", "HEAD:" + branch}} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestSyncPushCreatesInitialCommitAndRemoteEnvironment(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "first.txt"), []byte("first\n"), 0600)
	appRun(t, App{}, dir, "add", "first.txt")
	appRun(t, App{}, dir, "sync", "--push")
	p, _ := App{}.lookup(dir)
	if _, err := gitOutput(dir, p.GitDir, "rev-parse", "--verify", "HEAD"); err != nil {
		t.Fatalf("initial sync did not create HEAD: %v", err)
	}
	cmd := exec.Command("git", "--git-dir="+remote, "rev-parse", "refs/heads/"+remoteBranch(p, p.Environment))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("initial sync did not publish environment: %v: %s", err, out)
	}
}

func TestSyncDefaultFastForwardsRemoteChange(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "remote.txt"), []byte("one\n"), 0600)
	appRun(t, App{}, dir, "add", "remote.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := App{}.lookup(dir)
	branch := remoteBranch(p, p.Environment)
	clone := cloneRemoteEnv(t, remote, branch)
	commitRemoteFile(t, clone, remote, branch, "remote.txt", "two\n", "remote change")
	appRun(t, App{}, dir, "sync")
	got, err := os.ReadFile(filepath.Join(dir, "remote.txt"))
	if err != nil || string(got) != "two\n" {
		t.Fatalf("materialized remote change=%q err=%v", got, err)
	}
}

func TestSyncMergesNonOverlappingDivergence(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0600)
	appRun(t, App{}, dir, "add", "base.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := App{}.lookup(dir)
	branch := remoteBranch(p, p.Environment)
	clone := cloneRemoteEnv(t, remote, branch)

	os.WriteFile(filepath.Join(dir, "local.txt"), []byte("local\n"), 0600)
	appRun(t, App{}, dir, "add", "local.txt")
	appRun(t, App{}, dir, "commit", "-m", "local")
	commitRemoteFile(t, clone, remote, branch, "remote.txt", "remote\n", "remote")

	appRun(t, App{}, dir, "sync")
	if _, err := os.Stat(filepath.Join(dir, "local.txt")); err != nil {
		t.Fatal("local file lost after merge:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "remote.txt")); err != nil {
		t.Fatal("remote file not materialized after merge:", err)
	}
	parents, err := gitOutput(dir, p.GitDir, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil || len(strings.Fields(parents)) != 3 {
		t.Fatalf("expected merge commit, got %q err=%v", parents, err)
	}
}

func TestSyncDryRunReportsLogicalConflictBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "same.txt"), []byte("base\n"), 0600)
	appRun(t, App{}, dir, "add", "same.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := App{}.lookup(dir)
	branch := remoteBranch(p, p.Environment)
	clone := cloneRemoteEnv(t, remote, branch)
	commitRemoteFile(t, clone, remote, branch, "same.txt", "remote\n", "remote")

	os.WriteFile(filepath.Join(dir, "same.txt"), []byte("local\n"), 0600)
	beforeHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")
	out := appRun(t, App{}, dir, "sync", "--push", "--dry-run", "--json")
	var plan SyncPlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != "same.txt" {
		t.Fatalf("conflicts=%v", plan.Conflicts)
	}
	afterHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")
	if beforeHead != afterHead {
		t.Fatal("dry-run changed HEAD")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "same.txt"))
	if string(got) != "local\n" {
		t.Fatalf("dry-run changed worktree: %q", got)
	}
}

func TestChangedLogicalPathsMapsAgeStoreToLogicalPath(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	exec.Command("git", "-C", dir, "config", "user.email", "sync-test@example.invalid").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Sync Test").Run()
	path := filepath.Join(dir, ".lgit", "store", "secret.txt.age")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("one"), 0600)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "one").Run()
	base, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	os.WriteFile(path, []byte("two"), 0600)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "two").Run()
	m, err := changedLogicalPaths(filepath.Join(dir, ".git"), strings.TrimSpace(string(base)), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !m["secret.txt"] || m[".lgit/store/secret.txt.age"] {
		t.Fatalf("logical mapping=%v", m)
	}
}
'''
if 'TestSyncPushCreatesInitialCommitAndRemoteEnvironment' not in s:
    s = s.rstrip() + append + '\n'
p.write_text(s)
