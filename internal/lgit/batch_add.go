package lgit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// gitPathBatchBudget keeps command lines comfortably below the Windows
// CreateProcess limit while still collapsing thousands of per-file Git calls
// into a small number of batched operations.
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

// mixedAddOptimized handles the common plain-storage case in batches. Age
// files still go through addOne because they require per-file encryption and
// comparison against the staged ciphertext.
func (a App) mixedAddOptimized(root string, p Project, c StorageConfig, paths []string) error {
	mainTracked, err := mainTrackedSet(root, p.Standalone)
	if err != nil {
		return err
	}

	var plainAdd []string
	var plainRemove []string
	var agePaths []string

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

	var recipients []age.Recipient
	for i, path := range agePaths {
		if recipients == nil {
			recipients, err = a.storageRecipients(root, c)
			if err != nil {
				return err
			}
		}
		if len(paths) >= 100 && (i+1)%100 == 0 {
			fmt.Fprintf(a.Stderr, "lgit: encrypted %d/%d age-backed files\n", i+1, len(agePaths))
		}
		if err := a.addOne(root, p, c, path, StorageAge, recipients); err != nil {
			return err
		}
	}
	return nil
}
