package lgit

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

const gitPathBatchBudget = 12 * 1024

func splitPathBatches(paths []string, baseArgs []string) [][]string {
	if len(paths) == 0 {
		return nil
	}
	baseLen := 0
	for _, arg := range baseArgs {
		baseLen += len(arg) + 3
	}
	var batches [][]string
	batch := make([]string, 0, 128)
	size := baseLen
	for _, path := range paths {
		cost := len(path) + 3
		if len(batch) > 0 && size+cost > gitPathBatchBudget {
			batches = append(batches, batch)
			batch = make([]string, 0, 128)
			size = baseLen
		}
		batch = append(batch, path)
		size += cost
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

func (a App) runPathBatches(root, gitDir string, baseArgs, paths []string) error {
	for _, batch := range splitPathBatches(paths, baseArgs) {
		args := append(append([]string{}, baseArgs...), batch...)
		if code := a.run(root, gitDir, args...); code != 0 {
			return fmt.Errorf("git %s failed", strings.Join(baseArgs, " "))
		}
	}
	return nil
}

func mainTrackedSet(root string, standalone bool) (map[string]struct{}, error) {
	tracked := map[string]struct{}{}
	if standalone {
		return tracked, nil
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	for _, raw := range strings.Split(string(out), "\x00") {
		if raw != "" {
			tracked[filepath.ToSlash(raw)] = struct{}{}
		}
	}
	return tracked, nil
}

func (a App) mixedAddOptimized(root string, p Project, c StorageConfig, paths []string) error {
	mainTracked, err := mainTrackedSet(root, p.Standalone)
	if err != nil {
		return err
	}

	var plainAdd, plainRemove, agePaths []string
	for _, path := range paths {
		backend, _ := configuredBackend(c, path)
		if backend == StorageAge {
			agePaths = append(agePaths, path)
			continue
		}
		if _, owned := mainTracked[path]; owned {
			return fmt.Errorf("main repository already tracks %s", path)
		}
		full := filepath.Join(root, filepath.FromSlash(path))
		info, statErr := os.Lstat(full)
		sp := filepath.ToSlash(storePath(path))
		switch {
		case statErr == nil:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("only regular files are supported: %s", path)
			}
			plainAdd = append(plainAdd, path)
			plainRemove = append(plainRemove, sp)
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(sp)))
		case os.IsNotExist(statErr):
			plainRemove = append(plainRemove, path, sp)
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(sp)))
		default:
			return statErr
		}
	}

	if err := a.runPathBatches(root, p.GitDir, []string{"add", "--force", "--"}, plainAdd); err != nil {
		return err
	}
	if err := a.runPathBatches(root, p.GitDir, []string{"rm", "--cached", "--ignore-unmatch", "--"}, plainRemove); err != nil {
		return err
	}
	if len(agePaths) == 0 {
		return nil
	}

	if err := a.ensureWrappedPasswordIdentity(root, p, c); err != nil {
		return err
	}
	recipients, err := a.storageRecipients(root, c)
	if err != nil {
		return err
	}
	wrappedPassword := c.Encryption.Mode == "password" && hasWrappedPasswordIdentity(root)
	var identity age.Identity
	var ageAdd, ageRemove []string

	for _, path := range agePaths {
		if _, owned := mainTracked[path]; owned {
			return fmt.Errorf("main repository already tracks %s", path)
		}
		full := filepath.Join(root, filepath.FromSlash(path))
		info, statErr := os.Lstat(full)
		sp := filepath.ToSlash(storePath(path))
		if os.IsNotExist(statErr) {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(sp)))
			ageRemove = append(ageRemove, path, sp)
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("only regular files are supported: %s", path)
		}
		plain, err := os.ReadFile(full)
		if err != nil {
			return err
		}

		if cipher, err := gitBlob(root, p.GitDir, ":"+sp); err == nil {
			// Old password ciphertext used an expensive scrypt recipient per
			// file. Once the wrapped project key exists, lgit add can migrate
			// the selected path directly from its plaintext worktree contents.
			if !(wrappedPassword && isScryptAgeCipher(cipher)) {
				if identity == nil {
					identity, err = a.storageIdentity(root, c)
					if err != nil {
						return err
					}
				}
				old, err := decryptBytes(cipher, identity)
				if err != nil {
					return fmt.Errorf("decrypt staged %s: %w", path, err)
				}
				if bytes.Equal(old, plain) {
					continue
				}
			}
		}

		cipher, err := encryptBytes(plain, recipients)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(sp))
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, cipher, 0600); err != nil {
			return err
		}
		ageAdd = append(ageAdd, sp)
		ageRemove = append(ageRemove, path)
	}

	if err := a.runPathBatches(root, p.GitDir, []string{"add", "--force", "--"}, ageAdd); err != nil {
		return err
	}
	return a.runPathBatches(root, p.GitDir, []string{"rm", "--cached", "--ignore-unmatch", "--"}, ageRemove)
}
