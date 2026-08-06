package lgit

import (
	"bytes"

	"filippo.io/age"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgeRoundTrip(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("SECRET=value\n")
	cipher, err := encryptBytes(plain, []age.Recipient{id.Recipient()})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, []byte("SECRET=value")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := decryptBytes(cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
}

func TestKeyExportImport(t *testing.T) {
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "one"))
	var a App
	appRun(t, a, t.TempDir(), "key", "generate")
	export := filepath.Join(t.TempDir(), "identity.txt")
	appRun(t, a, t.TempDir(), "key", "export", export)
	b, err := os.ReadFile(export)
	if err != nil || !strings.Contains(string(b), "AGE-SECRET-KEY-") {
		t.Fatalf("bad export %q %v", b, err)
	}
}

func TestPasswordAgeRoundTrip(t *testing.T) {
	plain := []byte("SECRET=password-mode\n")
	r, err := age.NewScryptRecipient("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := encryptBytes(plain, []age.Recipient{r})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, plain) {
		t.Fatal("ciphertext contains plaintext")
	}
	id, err := age.NewScryptIdentity("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	got, err := decryptBytes(cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
}

func TestParseInitEncryptionModes(t *testing.T) {
	_, _, mode, err := parseInit([]string{"--env", "PCX", "--encryption", "password"})
	if err != nil || mode != "password" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	_, _, mode, err = parseInit([]string{"--env", "PCX"})
	if err != nil || mode != "identity" {
		t.Fatalf("default mode=%q err=%v", mode, err)
	}
}
