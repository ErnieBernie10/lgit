package lgit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReportsScopedUntrackedDrift(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	if err := os.MkdirAll(filepath.Join(dir, ".config", "opencode"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, ".config", "opencode", "settings.json"), []byte("{}"), 0600); err != nil { t.Fatal(err) }
	appRun(t, App{}, dir, "add", ".config/opencode/settings.json")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, ".config", "opencode", "plugin.js"), []byte("x"), 0600); err != nil { t.Fatal(err) }

	code, out, errOut := runCLIForTest(t, dir, "status")
	if code != 0 || errOut != "" || !strings.Contains(out, "?? .config/opencode/plugin.js") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestSyncPushNeverAddsUntrackedDrift(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	code, _, errOut := runCLIForTest(t, dir, "remote", "set", remote)
	if code != 0 { t.Fatalf("remote set: %s", errOut) }
	if err := os.MkdirAll(filepath.Join(dir, ".config", "opencode"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, ".config", "opencode", "settings.json"), []byte("one"), 0600); err != nil { t.Fatal(err) }
	appRun(t, App{}, dir, "add", ".config/opencode/settings.json")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	if err := os.WriteFile(filepath.Join(dir, ".config", "opencode", "settings.json"), []byte("two"), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, ".config", "opencode", "plugin.js"), []byte("new"), 0600); err != nil { t.Fatal(err) }

	code, out, errOut := runCLIForTest(t, dir, "sync", "--push")
	if code != 0 || errOut != "" { t.Fatalf("code=%d out=%q err=%q", code, out, errOut) }
	p, _ := (App{}).lookup(dir)
	tracked, _ := gitOutput(dir, p.GitDir, "ls-files")
	if strings.Contains(tracked, "plugin.js") {
		t.Fatalf("sync --push implicitly tracked drift: %q", tracked)
	}
	if !strings.Contains(out, "Untracked drift") {
		t.Fatalf("drift not surfaced after sync: %q", out)
	}
}

func TestSyncRefusesRemoteOverwriteOfUntrackedDriftBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	code, _, errOut := runCLIForTest(t, dir, "remote", "set", remote)
	if code != 0 { t.Fatalf("remote set: %s", errOut) }
	if err := os.MkdirAll(filepath.Join(dir, ".config", "opencode"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, ".config", "opencode", "settings.json"), []byte("base"), 0600); err != nil { t.Fatal(err) }
	appRun(t, App{}, dir, "add", ".config/opencode/settings.json")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	appRun(t, App{}, dir, "push")
	p, _ := (App{}).lookup(dir)
	branch := remoteBranch(p, p.Environment)
	clone := cloneRemoteEnv(t, remote, branch)
	commitRemoteFile(t, clone, remote, branch, ".config/opencode/plugins/foo.js", "remote\n", "remote plugin")

	localPath := filepath.Join(dir, ".config", "opencode", "plugins", "foo.js")
	if err := os.MkdirAll(filepath.Dir(localPath), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(localPath, []byte("local\n"), 0600); err != nil { t.Fatal(err) }
	beforeHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")

	code, _, errOut = runCLIForTest(t, dir, "sync")
	if code == 0 || !strings.Contains(errOut, ".config/opencode/plugins/foo.js") {
		t.Fatalf("expected logical collision: code=%d err=%q", code, errOut)
	}
	afterHead, _ := gitOutput(dir, p.GitDir, "rev-parse", "HEAD")
	if beforeHead != afterHead { t.Fatalf("HEAD changed before collision refusal: %q -> %q", beforeHead, afterHead) }
	got, err := os.ReadFile(localPath)
	if err != nil || string(got) != "local\n" { t.Fatalf("local drift changed: %q err=%v", got, err) }
}
