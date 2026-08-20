package lgit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a App) executeSyncView(root string, p Project, o syncV2Options, view SyncView) error {
	snapshot, err := captureSyncSnapshot(root, p, view.LocalChanges)
	if err != nil {
		return err
	}
	rollback := true
	defer func() {
		if rollback {
			a.restoreSyncSnapshot(root, p, snapshot)
		}
	}()

	if o.Push && view.WouldCommit {
		_, tracked, err := a.trackedWorkingChanges(root, p)
		if err != nil {
			return err
		}
		paths := make([]string, 0, len(tracked))
		for path := range tracked {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		c, err := loadStorageConfig(root)
		if err != nil {
			return err
		}
		if err := a.mixedAddOptimized(root, p, c, paths); err != nil {
			return err
		}
		if companionIndexDirty(root, p) {
			if err := runGitQuiet(root, p.GitDir, "commit", "-m", "lgit sync"); err != nil {
				return fmt.Errorf("commit local sync changes: %w", err)
			}
		}
	}

	if view.RemoteExists {
		if err := runGitQuiet(root, p.GitDir, "fetch", "origin", remoteBranch(p, p.Environment)); err != nil {
			return fmt.Errorf("fetch shared remote: %w", err)
		}
		remoteHead, err := gitOutput(root, p.GitDir, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return err
		}
		remoteHead = strings.TrimSpace(remoteHead)
		localHead, err := gitOutput(root, p.GitDir, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		localHead = strings.TrimSpace(localHead)
		if localHead != remoteHead {
			ancestorRemote := isAncestor(root, p.GitDir, localHead, remoteHead)
			ancestorLocal := isAncestor(root, p.GitDir, remoteHead, localHead)
			if ancestorRemote || (!ancestorLocal && !ancestorRemote) {
				removed, err := a.checkoutPrepare(root, p, "FETCH_HEAD")
				if err != nil {
					return err
				}
				if err := runGitQuiet(root, p.GitDir, "merge", "--no-edit", "FETCH_HEAD"); err != nil {
					restorePrepared(root, p, removed)
					return fmt.Errorf("integrate remote history: %w", err)
				}
				if err := a.mixedMaterialize(root, p); err != nil {
					return err
				}
				_ = os.RemoveAll(filepath.Join(p.GitDir, "transition"))
			}
		}
	}

	// A failed publication should not undo a successful local commit/merge. The
	// next sync --push can safely retry the push.
	rollback = false
	if o.Push && view.WouldPush {
		ref := "HEAD:refs/heads/" + remoteBranch(p, p.Environment)
		if err := runGitQuiet(root, p.GitDir, "push", "-u", "origin", ref); err != nil {
			return fmt.Errorf("publish synchronized environment: %w", err)
		}
	}
	return nil
}
