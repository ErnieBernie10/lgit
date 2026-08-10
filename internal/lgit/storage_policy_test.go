package lgit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedPlainAndAgeStorage(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("LGIT_PASSWORD", "correct horse battery staple")
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain", "--encryption", "password")
	os.WriteFile(filepath.Join(dir, "local.txt"), []byte("public\n"), 0600)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=42\n"), 0600)
	appRun(t, App{}, dir, "storage", "set", ".env", "age")
	appRun(t, App{}, dir, "add", "local.txt", ".env")
	appRun(t, App{}, dir, "commit", "-m", "mixed")
	p, _ := App{}.lookup(dir)
	plain, _ := gitBlob(dir, p.GitDir, "HEAD:local.txt")
	if string(plain) != "public\n" {
		t.Fatalf("plain=%q", plain)
	}
	cipher, _ := gitBlob(dir, p.GitDir, "HEAD:.lgit/store/.env.age")
	if bytes.Contains(cipher, []byte("SECRET=42")) {
		t.Fatal("remote age blob contains plaintext")
	}
	out := appRun(t, App{}, dir, "status")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("dirty: %q", out)
	}
}

func TestStorageMigrationPlainAgePlain(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("LGIT_PASSWORD", "pw")
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain", "--encryption", "password")
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("hello\n"), 0600)
	appRun(t, App{}, dir, "add", "x.txt")
	appRun(t, App{}, dir, "commit", "-m", "plain")
	appRun(t, App{}, dir, "storage", "set", "x.txt", "age")
	appRun(t, App{}, dir, "commit", "-m", "age")
	p, _ := App{}.lookup(dir)
	if _, err := gitBlob(dir, p.GitDir, "HEAD:x.txt"); err == nil {
		t.Fatal("plain representation remained")
	}
	if _, err := gitBlob(dir, p.GitDir, "HEAD:.lgit/store/x.txt.age"); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, dir, "storage", "set", "x.txt", "plain")
	appRun(t, App{}, dir, "commit", "-m", "plain again")
	if b, err := gitBlob(dir, p.GitDir, "HEAD:x.txt"); err != nil || string(b) != "hello\n" {
		t.Fatalf("plain restore %q %v", b, err)
	}
}

func TestChangingDefaultDoesNotMigrateTrackedFile(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0600)
	appRun(t, App{}, dir, "add", "x")
	appRun(t, App{}, dir, "commit", "-m", "x")
	appRun(t, App{}, dir, "storage", "default", "age")
	p, _ := App{}.lookup(dir)
	if b, ok, _ := currentBackend(dir, p, "x"); !ok || b != StoragePlain {
		t.Fatalf("backend=%s tracked=%v", b, ok)
	}
}

func TestStandaloneRootAndNearestNestedProject(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("LGIT_DATA_DIR", data)
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0700)
	appRun(t, App{}, home, "init", "--root", home, "--env", "desk", "--default", "plain")
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n"), 0600)
	appRun(t, App{}, home, "add", ".gitconfig")
	child := filepath.Join(home, "code", "Booking")
	os.MkdirAll(child, 0700)
	initMain(t, child)
	appRun(t, App{}, child, "init", "--env", "pc")
	os.WriteFile(filepath.Join(child, ".env"), []byte("CHILD=1\n"), 0600)
	appRun(t, App{}, child, "add", ".env")
	out := appRun(t, App{}, filepath.Join(child, "."), "status")
	if !strings.Contains(out, ".env") {
		t.Fatalf("nearest root not child: %q", out)
	}
	var stdout, stderr bytes.Buffer
	a := App{Stdout: &stdout, Stderr: &stderr}
	if code := a.Run(home, []string{"add", "code/Booking/.env"}); code == 0 || !strings.Contains(stderr.String(), "nested lgit project") {
		t.Fatalf("code=%d err=%q", code, stderr.String())
	}
}

func TestStandaloneRecursiveAddStopsAtNestedGitRepo(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("LGIT_DATA_DIR", data)
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(filepath.Join(home, ".config"), 0700)
	os.WriteFile(filepath.Join(home, ".config", "a"), []byte("a"), 0600)
	nested := filepath.Join(home, "src", "repo")
	os.MkdirAll(nested, 0700)
	initMain(t, nested)
	os.WriteFile(filepath.Join(nested, "secret"), []byte("no"), 0600)
	appRun(t, App{}, home, "init", "--root", home, "--env", "desk", "--default", "plain")
	appRun(t, App{}, home, "add", ".")
	p, _ := App{}.lookup(home)
	out, _ := gitOutput(home, p.GitDir, "ls-files")
	if !strings.Contains(out, ".config/a") || strings.Contains(out, "src/repo/secret") {
		t.Fatalf("tracked=%q", out)
	}
}

func TestPlainStoragePreservesCRLFBytes(t *testing.T) {
	dir := t.TempDir()
	initMain(t, dir)
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	appRun(t, App{}, dir, "init", "--env", "pc", "--default", "plain")
	want := []byte("A=1\r\nB=2\r\n")
	os.WriteFile(filepath.Join(dir, "local.env"), want, 0600)
	appRun(t, App{}, dir, "add", "local.env")
	appRun(t, App{}, dir, "commit", "-m", "crlf")
	p, _ := App{}.lookup(dir)
	got, err := gitBlob(dir, p.GitDir, "HEAD:local.env")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
