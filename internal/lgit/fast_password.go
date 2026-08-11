package lgit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"filippo.io/age"
)

const wrappedPasswordIdentityRel = ".lgit/password-identity.age"

type chainedIdentity []age.Identity

func (ids chainedIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	var last error
	for _, id := range ids {
		key, err := id.Unwrap(stanzas)
		if err == nil {
			return key, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("no age identity matched")
	}
	return nil, last
}

func wrappedPasswordIdentityPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(wrappedPasswordIdentityRel))
}

func hasWrappedPasswordIdentity(root string) bool {
	_, err := os.Stat(wrappedPasswordIdentityPath(root))
	return err == nil
}

func isScryptAgeCipher(cipher []byte) bool {
	return bytes.Contains(cipher, []byte("-> scrypt "))
}

func (a App) createWrappedPasswordIdentity(root string, confirm bool) (*age.X25519Identity, error) {
	password, err := a.readPassword(confirm)
	if err != nil {
		return nil, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	scryptRecipient, err := age.NewScryptRecipient(password)
	if err != nil {
		return nil, err
	}
	wrapped, err := encryptBytes([]byte(id.String()+"\n"), []age.Recipient{scryptRecipient})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, ".lgit"), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(wrappedPasswordIdentityPath(root), wrapped, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, ".lgit", "recipients.txt"), []byte(id.Recipient().String()+"\n"), 0600); err != nil {
		return nil, err
	}
	return id, nil
}

// loadWrappedPasswordIdentity unlocks the project identity once. The
// returned chained identity also retains the legacy scrypt identity so
// repositories can gradually migrate old per-file password ciphertext.
func (a App) loadWrappedPasswordIdentity(root string) (age.Identity, error) {
	password, err := a.readPassword(false)
	if err != nil {
		return nil, err
	}
	scryptIdentity, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, err
	}
	wrapped, err := os.ReadFile(wrappedPasswordIdentityPath(root))
	if err != nil {
		return nil, err
	}
	plain, err := decryptBytes(wrapped, scryptIdentity)
	if err != nil {
		return nil, fmt.Errorf("unlock password identity: %w", err)
	}
	xid, err := age.ParseX25519Identity(string(bytes.TrimSpace(plain)))
	if err != nil {
		return nil, err
	}
	return chainedIdentity{xid, scryptIdentity}, nil
}

// ensureWrappedPasswordIdentity upgrades legacy password repositories
// lazily. It stages only encryption metadata; selected age files are
// converted to X25519 storage by the normal add operation.
func (a App) ensureWrappedPasswordIdentity(root string, p Project, c StorageConfig) error {
	if c.Encryption.Mode != "password" || hasWrappedPasswordIdentity(root) {
		return nil
	}
	if _, err := a.createWrappedPasswordIdentity(root, false); err != nil {
		return err
	}
	return a.runPathBatches(root, p.GitDir, []string{"add", "--force", "--"}, []string{wrappedPasswordIdentityRel, ".lgit/recipients.txt"})
}
