package lgit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func canonicalPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = filepath.Clean(resolved)
	}
	return p, nil
}

func pathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (a App) nearestRegisteredRoot(cwd string) (string, bool, error) {
	cwd, err := canonicalPath(cwd)
	if err != nil {
		return "", false, err
	}
	r, err := a.registry()
	if err != nil {
		return "", false, err
	}
	best := ""
	for candidate := range r.Projects {
		canonicalCandidate, err := canonicalPath(candidate)
		if err != nil {
			canonicalCandidate = candidate
		}
		if containsPath(canonicalCandidate, cwd) && len(pathKey(canonicalCandidate)) > len(pathKey(best)) {
			best = candidate
		}
	}
	return best, best != "", nil
}

func (a App) resolveRoot(cwd, explicit string, allowUnregistered bool) (string, error) {
	if explicit != "" {
		root, err := canonicalPath(explicit)
		if err != nil {
			return "", err
		}
		if allowUnregistered {
			return root, nil
		}
		r, err := a.registry()
		if err != nil {
			return "", err
		}
		for candidate := range r.Projects {
			canonicalCandidate, err := canonicalPath(candidate)
			if err == nil && pathKey(canonicalCandidate) == pathKey(root) {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("lgit root is not initialized: %s", root)
	}
	if root, ok, err := a.nearestRegisteredRoot(cwd); err != nil {
		return "", err
	} else if ok {
		return root, nil
	}
	if allowUnregistered {
		return gitRoot(cwd)
	}
	return "", fmt.Errorf("no initialized lgit root contains %s", cwd)
}

func isGitWorkTreeRoot(root string) bool {
	c := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	b, err := c.Output()
	if err != nil {
		return false
	}
	got, err := canonicalPath(strings.TrimSpace(string(b)))
	want, werr := canonicalPath(root)
	return err == nil && werr == nil && pathKey(got) == pathKey(want)
}

func (a App) childRoot(root, path string) (string, bool, error) {
	r, err := a.registry()
	if err != nil {
		return "", false, err
	}
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return "", false, err
	}
	path, err = canonicalPath(path)
	if err != nil {
		return "", false, err
	}
	best := ""
	for candidate := range r.Projects {
		canonicalCandidate, err := canonicalPath(candidate)
		if err != nil {
			continue
		}
		if pathKey(canonicalCandidate) == pathKey(canonicalRoot) {
			continue
		}
		if containsPath(canonicalRoot, canonicalCandidate) && containsPath(canonicalCandidate, path) {
			if best == "" || len(canonicalCandidate) > len(best) {
				best = candidate
			}
		}
	}
	return best, best != "", nil
}

func nestedGitRoot(root, path string) (string, bool) {
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		canonicalRoot = filepath.Clean(root)
	}
	cur, err := canonicalPath(path)
	if err != nil {
		cur = filepath.Clean(path)
	}
	if info, err := os.Stat(cur); err == nil && !info.IsDir() {
		cur = filepath.Dir(cur)
	}
	for containsPath(canonicalRoot, cur) && pathKey(cur) != pathKey(canonicalRoot) {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, true
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return "", false
}
