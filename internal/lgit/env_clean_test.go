package lgit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvCreateUsesMixedCleanWithoutLegacyFormatPlain(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "one", "--default", "plain")
	if _, err := os.Stat(filepath.Join(dir, ".lgit", "format.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy format.json unexpectedly exists: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.txt"), []byte("ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "add", "settings.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	out := appRun(t, App{}, dir, "env", "create", "two")
	if !strings.Contains(out, "environment two") {
		t.Fatalf("unexpected output: %q", out)
	}
	p, err := App{}.lookup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Environment != "two" {
		t.Fatalf("environment=%q", p.Environment)
	}
}

func TestEnvCreateUsesMixedCleanWithoutLegacyFormatAge(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("LGIT_PASSWORD", "test-password")
	appRun(t, App{}, dir, "init", "--env", "one", "--default", "age", "--encryption", "password")
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "add", "secret.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	if _, err := os.Stat(filepath.Join(dir, ".lgit", "format.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy format.json unexpectedly exists: %v", err)
	}
	appRun(t, App{}, dir, "env", "create", "two")
}

func TestEnvCreateReportsCleanCheckErrorsInsteadOfDirtyTree(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "one", "--default", "age", "--encryption", "identity")
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "add", "secret.txt")
	appRun(t, App{}, dir, "commit", "-m", "initial")
	identity, err := (App{}).identityPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(identity); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := (App{Stdout: &stdout, Stderr: &stderr}).Run(dir, []string{"env", "create", "two"})
	if code == 0 {
		t.Fatal("env create unexpectedly succeeded without identity")
	}
	if strings.Contains(stderr.String(), "uncommitted changes") {
		t.Fatalf("operational error was misreported as dirty tree: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "age identity not found") {
		t.Fatalf("missing identity error not preserved: %q", stderr.String())
	}
}
