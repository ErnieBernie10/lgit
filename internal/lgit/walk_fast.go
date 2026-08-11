package lgit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func (a App) nestedRootBoundaries(root string) ([]string, error) {
	r, err := a.registry()
	if err != nil {
		return nil, err
	}
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return nil, err
	}
	var boundaries []string
	for candidate := range r.Projects {
		c, err := canonicalPath(candidate)
		if err != nil || pathKey(c) == pathKey(canonicalRoot) {
			continue
		}
		if containsPath(canonicalRoot, c) {
			boundaries = append(boundaries, c)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return len(boundaries[i]) > len(boundaries[j]) })
	return boundaries, nil
}

func containingBoundary(boundaries []string, path string) (string, bool) {
	for _, boundary := range boundaries {
		if containsPath(boundary, path) {
			return boundary, true
		}
	}
	return "", false
}

func exactBoundary(boundaries []string, path string) bool {
	key := pathKey(path)
	for _, boundary := range boundaries {
		if pathKey(boundary) == key {
			return true
		}
	}
	return false
}

// expandPathsFast performs one registry/canonicalization pass per add
// command. In particular, it never reloads the registry or resolves
// symlinks once per visited directory.
func (a App) expandPathsFast(root string, p Project, args []string) ([]string, error) {
	boundaries, err := a.nestedRootBoundaries(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(rel string) error {
		rel, err := normalizeLogical(rel)
		if err != nil {
			return err
		}
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
		return nil
	}

	for _, raw := range args {
		abs := raw
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, raw)
		}
		abs, err = canonicalPath(abs)
		if err != nil {
			return nil, err
		}
		if !containsPath(root, abs) {
			return nil, fmt.Errorf("path outside lgit root: %s", raw)
		}
		if child, ok := containingBoundary(boundaries, abs); ok {
			return nil, fmt.Errorf("path belongs to nested lgit project: %s", child)
		}
		if nested, ok := nestedGitRoot(root, abs); ok {
			return nil, fmt.Errorf("path belongs to nested Git repository: %s", nested)
		}
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			rel, _ := filepath.Rel(root, abs)
			if err := add(rel); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("symlink storage is not supported yet: %s", raw)
			}
			rel, _ := filepath.Rel(root, abs)
			if err := add(rel); err != nil {
				return nil, err
			}
			continue
		}

		err = filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == abs {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".lgit" || exactBoundary(boundaries, path) {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			return add(rel)
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
