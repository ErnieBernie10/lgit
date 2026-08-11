package lgit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordInitUsesWrappedProjectIdentity(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	t.Setenv("LGIT_DATA_DIR", data)
	t.Setenv("LGIT_PASSWORD", "test-password")
	appRun(t, App{}, root, "init", "--root", root, "--env", "desktop", "--default", "age", "--encryption", "password")
	if !hasWrappedPasswordIdentity(root) {
		t.Fatal("wrapped password identity was not created")
	}
	if _, err := os.Stat(filepath.Join(root, ".lgit", "recipients.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, root, "add", "secret.txt")
	p, err := App{}.lookup(root)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := gitBlob(root, p.GitDir, ":.lgit/store/secret.txt.age")
	if err != nil {
		t.Fatal(err)
	}
	if isScryptAgeCipher(cipher) {
		t.Fatal("file ciphertext still uses per-file scrypt")
	}
	if !bytes.Contains(cipher, []byte("-> X25519 ")) {
		t.Fatalf("expected X25519 recipient header, got %q", cipher[:min(len(cipher), 160)])
	}
}

func TestAddIsSilentOnSuccess(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	t.Setenv("LGIT_DATA_DIR", data)
	appRun(t, App{}, root, "init", "--root", root, "--env", "desktop", "--default", "plain")
	if err := os.MkdirAll(filepath.Join(root, "one"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "one", "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	app := App{Stdout: &out, Stderr: &stderr}
	if code := app.Run(root, []string{"--root", root, "add", "one"}); code != 0 {
		t.Fatalf("add failed: %s", stderr.String())
	}
	if strings.TrimSpace(out.String()) != "" || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("successful add should be silent, stdout=%q stderr=%q", out.String(), stderr.String())
	}
}
