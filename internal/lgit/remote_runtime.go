package lgit

import (
	"fmt"
	"sort"
)

func (a App) sharedRemote() (string, error) {
	r, err := a.registry()
	if err != nil {
		return "", err
	}
	if r.Remote == "" {
		return "", fmt.Errorf("shared remote is not configured")
	}
	return r.Remote, nil
}

func (a App) ensureProjectRemoteQuiet(root string, p Project, remote string) error {
	return configureRemoteQuiet(root, p, remote)
}

func (a App) remoteSetAll(args []string) int {
	if len(args) != 1 {
		return a.fail(fmt.Errorf("usage: lgit remote set URL"))
	}
	_, rp, err := a.paths()
	if err != nil {
		return a.fail(err)
	}
	r, err := LoadRegistry(rp)
	if err != nil {
		return a.fail(err)
	}
	r.Remote = args[0]
	if err := SaveRegistry(rp, r); err != nil {
		return a.fail(err)
	}
	roots := make([]string, 0, len(r.Projects))
	for root := range r.Projects {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		p := r.Projects[root]
		if err := a.ensureProjectRemoteQuiet(root, p, r.Remote); err != nil {
			return a.fail(fmt.Errorf("configure remote for %s: %w", p.Slug, err))
		}
	}
	fmt.Fprintln(a.Stdout, "shared remote configured")
	return 0
}
