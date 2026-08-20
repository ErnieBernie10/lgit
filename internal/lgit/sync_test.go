package lgit

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initBareRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	cmd := exec.Command("git", "init", "--bare", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	return remote
}

func configureLGitRemote(t *testing.T, dir, remote string) {
	t.Helper()
	appRun(t, App{}, dir, "remote", "set", remote)
}

func TestSyncPushCommitsTrackedModificationAndPushes(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	if err := os.WriteFile(filepath.Join(dir, "settings.txt"), []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "add", "settings.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")

	if err := os.WriteFile(filepath.Join(dir, "settings.txt"), []byte("two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "sync", "--push")
	p, _ := App{}.lookup(dir)
	got, err := gitBlob(dir, p.GitDir, "HEAD:settings.txt")
	if err != nil || string(got) != "two\n" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	msg, err := gitOutput(dir, p.GitDir, "log", "-1", "--format=%s")
	if err != nil || strings.TrimSpace(msg) != "lgit sync" {
		t.Fatalf("message=%q err=%v", msg, err)
	}
	remoteHead := exec.Command("git", "--git-dir="+remote, "rev-parse", "refs/heads/"+remoteBranch(p, p.Environment))
	out, err := remoteHead.Output()
	if err != nil {
		t.Fatal(err)
	}
	localHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")
	if strings.TrimSpace(string(out)) != strings.TrimSpace(localHead) {
		t.Fatalf("remote=%q local=%q", out, localHead)
	}
}

func TestSyncPushStagesDeletedTrackedDirectory(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	if err := os.MkdirAll(filepath.Join(dir, "skills", "one"), 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "skills", "one", "a.txt"), []byte("a"), 0600)
	os.WriteFile(filepath.Join(dir, "skills", "one", "b.txt"), []byte("b"), 0600)
	appRun(t, App{}, dir, "add", "skills")
	appRun(t, App{}, dir, "commit", "-m", "skills")
	appRun(t, App{}, dir, "push")
	if err := os.RemoveAll(filepath.Join(dir, "skills")); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "sync", "--push")
	p, _ := App{}.lookup(dir)
	out, _ := gitOutput(dir, p.GitDir, "ls-tree", "-r", "--name-only", "HEAD")
	if strings.Contains(out, "skills/") {
		t.Fatalf("deleted directory descendants remain tracked: %q", out)
	}
}

func TestSyncPushDoesNotAddUntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one"), 0600)
	appRun(t, App{}, dir, "add", "tracked.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two"), 0600)
	os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("no"), 0600)
	appRun(t, App{}, dir, "sync", "--push")
	p, _ := App{}.lookup(dir)
	out, _ := gitOutput(dir, p.GitDir, "ls-files")
	if strings.Contains(out, "untracked.txt") {
		t.Fatalf("sync implicitly added untracked file: %q", out)
	}
}

func TestSyncDryRunIsNonMutatingAndReportsDeletion(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0600)
	appRun(t, App{}, dir, "add", "a.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := App{}.lookup(dir)
	beforeHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")
	beforeIndex, _ := os.ReadFile(filepath.Join(p.GitDir, "index"))
	os.Remove(filepath.Join(dir, "a.txt"))
	out := appRun(t, App{}, dir, "sync", "--push", "--dry-run", "--json")
	var plan SyncPlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("json: %v: %s", err, out)
	}
	if len(plan.LocalChanges.Deleted) != 1 || plan.LocalChanges.Deleted[0] != "a.txt" || !plan.WouldCommit || !plan.WouldPush {
		t.Fatalf("plan=%+v", plan)
	}
	afterHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")
	afterIndex, _ := os.ReadFile(filepath.Join(p.GitDir, "index"))
	if beforeHead != afterHead || !bytes.Equal(beforeIndex, afterIndex) {
		t.Fatal("dry-run mutated HEAD or index")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("dry-run restored or changed deleted file: %v", err)
	}
}

func TestSyncDefaultRefusesDirtyTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0600)
	appRun(t, App{}, dir, "add", "a.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0600)
	var stdout, stderr bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &stderr}).Run(dir, []string{"sync"})
	if code == 0 || !strings.Contains(stderr.String(), "sync --push") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestSyncPushAgeModification(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("LGIT_PASSWORD", "sync-test-password")
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "age", "--encryption", "password")
	configureLGitRemote(t, dir, remote)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("one"), 0600)
	appRun(t, App{}, dir, "add", "secret.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("two"), 0600)
	appRun(t, App{}, dir, "sync", "--push")
	p, _ := App{}.lookup(dir)
	cipher, err := gitBlob(dir, p.GitDir, "HEAD:.lgit/store/secret.txt.age")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := loadStorageConfig(dir)
	id, err := (App{}).storageIdentity(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptBytes(cipher, id)
	if err != nil || string(plain) != "two" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
}

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
