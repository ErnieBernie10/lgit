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
