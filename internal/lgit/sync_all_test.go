package lgit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncAllDryRunReportsAllRootsInDeterministicOrder(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)

	rootB := filepath.Join(base, "zeta")
	rootA := filepath.Join(base, "alpha")
	for _, root := range []string{rootB, rootA} {
		if err := os.MkdirAll(root, 0700); err != nil { t.Fatal(err) }
		initMain(t, root)
		appRun(t, App{}, root, "init", "--env", "pc", "--default", "plain")
	}
	code, _, errOut := runCLIForTest(t, rootA, "remote", "set", remote)
	if code != 0 { t.Fatalf("remote set: %s", errOut) }

	for _, root := range []string{rootA, rootB} {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("x"), 0600); err != nil { t.Fatal(err) }
		appRun(t, App{}, root, "add", "tracked.txt")
		appRun(t, App{}, root, "commit", "-m", "initial")
		appRun(t, App{}, root, "push")
	}

	code, out, errOut := runCLIForTest(t, base, "sync", "--all", "--dry-run", "--json")
	if code != 0 || errOut != "" { t.Fatalf("code=%d out=%q err=%q", code, out, errOut) }
	var got SyncAllResult
	if err := json.Unmarshal([]byte(out), &got); err != nil { t.Fatalf("json: %v: %s", err, out) }
	if len(got.Roots) != 2 || got.Failed != 0 { t.Fatalf("result=%+v", got) }
	if got.Roots[0].Project > got.Roots[1].Project { t.Fatalf("not sorted: %+v", got.Roots) }
}

func TestSyncAllContinuesAfterOneRootFails(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	remote := initBareRemote(t)

	bad := filepath.Join(base, "a-bad")
	good := filepath.Join(base, "z-good")
	for _, root := range []string{bad, good} {
		if err := os.MkdirAll(root, 0700); err != nil { t.Fatal(err) }
		initMain(t, root)
		appRun(t, App{}, root, "init", "--env", "pc", "--default", "plain")
	}
	code, _, errOut := runCLIForTest(t, good, "remote", "set", remote)
	if code != 0 { t.Fatalf("remote set: %s", errOut) }
	for _, root := range []string{bad, good} {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("x"), 0600); err != nil { t.Fatal(err) }
		appRun(t, App{}, root, "add", "tracked.txt")
		appRun(t, App{}, root, "commit", "-m", "initial")
		appRun(t, App{}, root, "push")
	}

	if err := os.RemoveAll(bad); err != nil { t.Fatal(err) }
	code, out, _ := runCLIForTest(t, base, "sync", "--all", "--dry-run", "--json")
	if code == 0 { t.Fatalf("expected aggregate failure: %s", out) }
	var got SyncAllResult
	if err := json.Unmarshal([]byte(out), &got); err != nil { t.Fatalf("json: %v: %s", err, out) }
	if len(got.Roots) != 2 || got.Failed != 1 { t.Fatalf("result=%+v", got) }
	var sawGood bool
	for _, entry := range got.Roots {
		if strings.Contains(entry.Project, "z-good") && entry.Error == "" && entry.Plan != nil { sawGood = true }
	}
	if !sawGood { t.Fatalf("good root was not processed after failure: %+v", got.Roots) }
}

func TestSyncAllRejectsExplicitRoot(t *testing.T) {
	code, _, errOut := runCLIForTest(t, t.TempDir(), "--root", t.TempDir(), "sync", "--all")
	if code == 0 || !strings.Contains(errOut, "--root and sync --all") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}
