package lgit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCLIForTest(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &stderr}).RunCLI(cwd, args)
	return code, stdout.String(), stderr.String()
}

func TestCLIHelpIncludesSync(t *testing.T) {
	code, out, errOut := runCLIForTest(t, t.TempDir(), "--help")
	if code != 0 || errOut != "" || !strings.Contains(out, "lgit sync [--all] [--push] [--dry-run] [--json]") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestCLIUnknownCommandIsLGitError(t *testing.T) {
	code, _, errOut := runCLIForTest(t, t.TempDir(), "definitely-not-an-lgit-or-git-command")
	if code == 0 || !strings.Contains(errOut, "unknown command") || strings.Contains(errOut, "not a git command") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestUntrackedScopesNeverUseBroadRootPathspec(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".gitconfig"), []byte("x"), 0600)
	os.MkdirAll(filepath.Join(root, ".config", "opencode"), 0700)
	tracked := map[string]StorageBackend{
		".gitconfig": StoragePlain,
		".config/opencode/settings.json": StoragePlain,
	}
	scopes, err := untrackedScopes(root, tracked)
	if err != nil { t.Fatal(err) }
	for _, scope := range scopes {
		if scope == "." || scope == "*" || strings.Contains(scope, ":(glob)") {
			t.Fatalf("broad root scope leaked: %q in %v", scope, scopes)
		}
	}
	if !containsString(scopes, ".config/opencode") || !containsString(scopes, ".gitconfig") {
		t.Fatalf("unexpected scopes: %v", scopes)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs { if x == want { return true } }
	return false
}

func TestSyncDryRunUsesRegistryRemoteWhenOriginMissing(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	appRun(t, App{}, dir, "remote", "set", remote)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0600)
	appRun(t, App{}, dir, "add", "a.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := (App{}).lookup(dir)
	cmd := exec.Command("git", "--git-dir="+p.GitDir, "--work-tree="+dir, "remote", "remove", "origin")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("remove origin: %v: %s", err, out) }
	code, out, errOut := runCLIForTest(t, dir, "sync", "--dry-run", "--json")
	if code != 0 || errOut != "" || !strings.Contains(out, "\"remote_exists\": true") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	check := exec.Command("git", "--git-dir="+p.GitDir, "--work-tree="+dir, "remote", "get-url", "origin")
	check.Dir = dir
	if err := check.Run(); err == nil { t.Fatal("dry-run repaired origin and mutated companion config") }
}

func TestSyncRealRepairsMissingOriginQuietly(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	appRun(t, App{}, dir, "remote", "set", remote)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0600)
	appRun(t, App{}, dir, "add", "a.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := (App{}).lookup(dir)
	exec.Command("git", "--git-dir="+p.GitDir, "--work-tree="+dir, "remote", "remove", "origin").Run()
	code, out, errOut := runCLIForTest(t, dir, "sync")
	if code != 0 || errOut != "" { t.Fatalf("code=%d out=%q err=%q", code, out, errOut) }
	for _, noisy := range []string{"From ", "Fast-forward", "Enumerating objects", "Writing objects"} {
		if strings.Contains(out, noisy) || strings.Contains(errOut, noisy) { t.Fatalf("git chatter leaked: %q %q", out, errOut) }
	}
	check := exec.Command("git", "--git-dir="+p.GitDir, "--work-tree="+dir, "remote", "get-url", "origin")
	check.Dir = dir
	b, err := check.Output()
	if err != nil || strings.TrimSpace(string(b)) != remote { t.Fatalf("origin=%q err=%v", b, err) }
}
