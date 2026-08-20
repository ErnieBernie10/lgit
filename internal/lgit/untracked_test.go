package lgit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopedUntrackedDriftStaysInsideManagedParents(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")

	managed := filepath.Join(dir, ".config", "opencode")
	if err := os.MkdirAll(managed, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(managed, "settings.json"), []byte("{}"), 0600); err != nil { t.Fatal(err) }
	appRun(t, App{}, dir, "add", ".config/opencode/settings.json")
	appRun(t, App{}, dir, "commit", "-m", "track settings")

	if err := os.MkdirAll(filepath.Join(managed, "plugins"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(managed, "plugins", "new.js"), []byte("plugin"), 0600); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(filepath.Join(dir, "unrelated", "deep"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "unrelated", "deep", "ignore.txt"), []byte("ignore"), 0600); err != nil { t.Fatal(err) }

	p, err := (App{}).lookup(dir)
	if err != nil { t.Fatal(err) }
	drift, err := (App{}).scopedUntrackedDrift(dir, p)
	if err != nil { t.Fatal(err) }
	if !containsString(drift, ".config/opencode/plugins/new.js") {
		t.Fatalf("managed drift missing: %v", drift)
	}
	if containsString(drift, "unrelated/deep/ignore.txt") {
		t.Fatalf("scanner escaped managed scope: %v", drift)
	}
}

func TestScopedUntrackedDriftRootFilesDoNotOpenRootDirectories(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	if err := os.WriteFile(filepath.Join(dir, ".gitconfig"), []byte("[user]\n"), 0600); err != nil { t.Fatal(err) }
	appRun(t, App{}, dir, "add", ".gitconfig")
	appRun(t, App{}, dir, "commit", "-m", "track root file")

	if err := os.WriteFile(filepath.Join(dir, "root-new.txt"), []byte("new"), 0600); err != nil { t.Fatal(err) }
	if err := os.MkdirAll(filepath.Join(dir, "huge", "nested"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "huge", "nested", "not-scanned.txt"), []byte("no"), 0600); err != nil { t.Fatal(err) }

	p, _ := (App{}).lookup(dir)
	drift, err := (App{}).scopedUntrackedDrift(dir, p)
	if err != nil { t.Fatal(err) }
	if !containsString(drift, "root-new.txt") {
		t.Fatalf("root-level drift missing: %v", drift)
	}
	if containsString(drift, "huge/nested/not-scanned.txt") {
		t.Fatalf("root-level scan descended into directory: %v", drift)
	}
}

func TestScopedUntrackedDriftDoesNotReportAgePlaintext(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("LGIT_PASSWORD", "drift-password")
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "age", "--encryption", "password")
	if err := os.MkdirAll(filepath.Join(dir, ".config", "secret"), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, ".config", "secret", "token.txt"), []byte("secret"), 0600); err != nil { t.Fatal(err) }
	appRun(t, App{}, dir, "add", ".config/secret/token.txt")
	appRun(t, App{}, dir, "commit", "-m", "track encrypted")

	p, _ := (App{}).lookup(dir)
	drift, err := (App{}).scopedUntrackedDrift(dir, p)
	if err != nil { t.Fatal(err) }
	if containsString(drift, ".config/secret/token.txt") {
		t.Fatalf("age plaintext falsely reported as drift: %v", drift)
	}
}
