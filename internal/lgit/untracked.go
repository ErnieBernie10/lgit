package lgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func managedLogicalPaths(root string, p Project) (map[string]StorageBackend, error) {
	out := map[string]StorageBackend{}
	if head, err := logicalTrackedAt(root, p, "HEAD"); err == nil {
		for path, backend := range head {
			out[path] = backend
		}
	}
	idx, err := indexLogicalTracked(root, p)
	if err != nil {
		return nil, err
	}
	for path, backend := range idx {
		out[path] = backend
	}
	return out, nil
}

func untrackedScopes(root string, tracked map[string]StorageBackend) ([]string, error) {
	dirs := map[string]bool{}
	hasRootFile := false
	for path := range tracked {
		d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
		if d == "." {
			hasRootFile = true
			continue
		}
		dirs[d] = true
	}
	var scopes []string
	for d := range dirs {
		scopes = append(scopes, d)
	}
	if hasRootFile {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Name() == ".git" || entry.Name() == ".lgit" {
				continue
			}
			scopes = append(scopes, filepath.ToSlash(entry.Name()))
		}
	}
	sort.Strings(scopes)
	return scopes, nil
}

func parsePorcelainUntracked(out []byte) []string {
	parts := strings.Split(string(out), "\x00")
	var paths []string
	for _, rec := range parts {
		if len(rec) < 4 || rec[:2] != "??" {
			continue
		}
		path := filepath.ToSlash(strings.TrimSpace(rec[3:]))
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func (a App) scopedUntrackedDrift(root string, p Project) ([]string, error) {
	tracked, err := managedLogicalPaths(root, p)
	if err != nil {
		return nil, err
	}
	if len(tracked) == 0 {
		return nil, nil
	}
	scopes, err := untrackedScopes(root, tracked)
	if err != nil || len(scopes) == 0 {
		return nil, err
	}
	boundaries, err := a.nestedRootBoundaries(root)
	if err != nil {
		return nil, err
	}
	mainOwned, err := mainTrackedSet(root, p.Standalone)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var drift []string
	base := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}
	for _, batch := range splitPathBatches(scopes, base) {
		args := append([]string{"--git-dir=" + p.GitDir, "--work-tree=" + root}, base...)
		args = append(args, batch...)
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		for _, path := range parsePorcelainUntracked(out) {
			if seen[path] || path == ".lgit" || strings.HasPrefix(path, ".lgit/") || path == ".git" || strings.HasPrefix(path, ".git/") {
				continue
			}
			if _, ok := tracked[path]; ok {
				continue
			}
			if _, ok := mainOwned[path]; ok {
				continue
			}
			abs := filepath.Join(root, filepath.FromSlash(path))
			if _, ok := containingBoundary(boundaries, abs); ok {
				continue
			}
			if _, ok := nestedGitRoot(root, abs); ok {
				continue
			}
			seen[path] = true
			drift = append(drift, path)
		}
	}
	sort.Strings(drift)
	return drift, nil
}

func structuralPathOverlap(aPath, bPath string) bool {
	aPath = strings.Trim(filepath.ToSlash(aPath), "/")
	bPath = strings.Trim(filepath.ToSlash(bPath), "/")
	return aPath == bPath || strings.HasPrefix(aPath, bPath+"/") || strings.HasPrefix(bPath, aPath+"/")
}
