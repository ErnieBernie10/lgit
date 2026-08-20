package lgit

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type SyncView struct {
	SyncPlan
	Untracked []string `json:"untracked"`
}

type SyncAllEntry struct {
	Root        string    `json:"root"`
	Project     string    `json:"project"`
	Environment string    `json:"environment"`
	Plan        *SyncView `json:"plan,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type SyncAllResult struct {
	Roots  []SyncAllEntry `json:"roots"`
	Failed int            `json:"failed"`
}

type syncV2Options struct {
	Push, DryRun, JSON, All bool
}

func parseSyncV2Options(args []string) (syncV2Options, error) {
	var o syncV2Options
	for _, arg := range args {
		switch arg {
		case "--push":
			o.Push = true
		case "--dry-run":
			o.DryRun = true
		case "--json":
			o.JSON = true
		case "--all":
			o.All = true
		case "--help", "-h":
			return o, nil
		default:
			return o, fmt.Errorf("unknown sync option %q", arg)
		}
	}
	if o.JSON && !o.DryRun {
		return o, fmt.Errorf("--json currently requires --dry-run")
	}
	return o, nil
}

func syncHelpText() string {
	return `Usage: lgit sync [--all] [--push] [--dry-run] [--json]

  --all      synchronize every locally registered root
  --push     commit tracked local modifications/deletions and publish them
  --dry-run  show the synchronization plan without persistent changes
  --json     machine-readable dry-run output

Untracked drift is reported but never added implicitly. Use 'lgit add PATH' to track new files.`
}

func (a App) buildSyncView(root string, p Project, remote string, o syncV2Options) (SyncView, error) {
	plan := SyncPlan{Environment: p.Environment}
	changes, _, err := a.trackedWorkingChanges(root, p)
	if err != nil {
		return SyncView{}, err
	}
	plan.LocalChanges = changes
	plan.WouldCommit = o.Push && (len(changes.Modified) > 0 || len(changes.Deleted) > 0 || companionIndexDirty(root, p))
	graph, err := inspectSyncGraph(root, p, remote)
	if err != nil {
		return SyncView{}, err
	}
	if graph.dir != "" {
		defer os.RemoveAll(graph.dir)
	}
	plan.RemoteExists = graph.remoteExists
	plan.LocalCommitsAhead = graph.localAhead
	plan.RemoteCommitsAhead = graph.remoteAhead
	plan.Diverged = graph.localAhead > 0 && graph.remoteAhead > 0
	plan.WouldPull = graph.remoteExists && graph.remoteAhead > 0
	plan.WouldPush = o.Push && (!graph.remoteExists || graph.localAhead > 0 || plan.WouldCommit || plan.Diverged)
	if graph.remoteExists && graph.remoteAhead > 0 && (graph.localAhead > 0 || plan.WouldCommit) {
		plan.Conflicts, err = syncLogicalConflicts(root, p, graph, changes)
		if err != nil {
			return SyncView{}, err
		}
	}
	drift, err := a.scopedUntrackedDrift(root, p)
	if err != nil {
		return SyncView{}, err
	}
	if graph.remoteExists && graph.remoteAhead > 0 && graph.mergeBase != "" && len(drift) > 0 {
		remoteChanged, err := changedLogicalPaths(graph.dir, graph.mergeBase, "refs/heads/remote")
		if err != nil {
			return SyncView{}, err
		}
		for _, u := range drift {
			for remotePath := range remoteChanged {
				if structuralPathOverlap(u, remotePath) {
					plan.Conflicts = append(plan.Conflicts, u)
					break
				}
			}
		}
		sort.Strings(plan.Conflicts)
		plan.Conflicts = uniqueStrings(plan.Conflicts)
	}
	return SyncView{SyncPlan: plan, Untracked: drift}, nil
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

func (a App) renderSyncView(view SyncView, asJSON bool) int {
	if asJSON {
		b, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprintln(a.Stdout, string(b))
		return 0
	}
	plan := view.SyncPlan
	if len(plan.LocalChanges.Modified) == 0 && len(plan.LocalChanges.Deleted) == 0 && !plan.WouldCommit && !plan.WouldPull && !plan.WouldPush && !plan.Diverged && plan.LocalCommitsAhead == 0 && len(view.Untracked) == 0 {
		fmt.Fprintln(a.Stdout, "Already synchronized.")
		return 0
	}
	if len(plan.LocalChanges.Modified) > 0 || len(plan.LocalChanges.Deleted) > 0 {
		fmt.Fprintln(a.Stdout, "Local changes:")
		for _, p := range plan.LocalChanges.Modified {
			fmt.Fprintf(a.Stdout, "  M %s\n", p)
		}
		for _, p := range plan.LocalChanges.Deleted {
			fmt.Fprintf(a.Stdout, "  D %s\n", p)
		}
		fmt.Fprintln(a.Stdout)
	}
	if len(view.Untracked) > 0 {
		fmt.Fprintln(a.Stdout, "Untracked drift:")
		for _, p := range view.Untracked {
			fmt.Fprintf(a.Stdout, "  ? %s\n", p)
		}
		fmt.Fprintln(a.Stdout, "\nNot included in sync; use 'lgit add PATH' to track it.")
	}
	if len(plan.Conflicts) > 0 {
		fmt.Fprintln(a.Stdout, "\nConflicts:")
		for _, p := range plan.Conflicts {
			fmt.Fprintf(a.Stdout, "  %s\n", p)
		}
	}
	fmt.Fprintln(a.Stdout, "\nWould:")
	if plan.WouldCommit {
		fmt.Fprintf(a.Stdout, "  commit %d local changes\n", len(plan.LocalChanges.Modified)+len(plan.LocalChanges.Deleted))
	}
	if plan.WouldPull {
		fmt.Fprintf(a.Stdout, "  integrate %d remote commits\n", plan.RemoteCommitsAhead)
	}
	if plan.LocalCommitsAhead > 0 && !plan.WouldPush {
		fmt.Fprintf(a.Stdout, "  keep %d local commits unpushed\n", plan.LocalCommitsAhead)
	}
	if plan.WouldPush {
		fmt.Fprintf(a.Stdout, "  push environment %s\n", plan.Environment)
	}
	if !plan.WouldCommit && !plan.WouldPull && !plan.WouldPush && plan.LocalCommitsAhead == 0 {
		fmt.Fprintln(a.Stdout, "  no repository changes")
	}
	fmt.Fprintln(a.Stdout, "\nNo changes made.")
	return 0
}

func (a App) syncOneV2(root string, p Project, o syncV2Options) (SyncView, error) {
	remote, err := a.sharedRemote()
	if err != nil {
		return SyncView{}, err
	}
	if !o.Push {
		clean, err := a.mixedClean(root, p)
		if err != nil {
			return SyncView{}, fmt.Errorf("cannot sync: determine working tree state: %w", err)
		}
		if !clean {
			return SyncView{}, fmt.Errorf("cannot sync: local tracked files have uncommitted changes; use 'lgit sync --push' to commit and publish them")
		}
	}
	view, err := a.buildSyncView(root, p, remote, o)
	if err != nil {
		return SyncView{}, err
	}
	if o.DryRun {
		return view, nil
	}
	if len(view.Conflicts) > 0 {
		return view, fmt.Errorf("sync conflict: local and remote paths overlap: %s", strings.Join(view.Conflicts, ", "))
	}
	if err := a.ensureProjectRemoteQuiet(root, p, remote); err != nil {
		return view, fmt.Errorf("cannot configure project remote: %w", err)
	}
	if err := a.executeSyncView(root, p, o, view); err != nil {
		return view, err
	}
	return view, nil
}

func syncSummary(view SyncView, pushed bool) string {
	if len(view.LocalChanges.Modified) == 0 && len(view.LocalChanges.Deleted) == 0 && !view.WouldPull && !view.WouldPush {
		parts := []string{}
		if view.LocalCommitsAhead > 0 {
			parts = append(parts, fmt.Sprintf("%d local commits unpushed", view.LocalCommitsAhead))
		}
		if len(view.Untracked) > 0 {
			parts = append(parts, fmt.Sprintf("%d untracked", len(view.Untracked)))
		}
		if len(parts) > 0 {
			return "synchronized; " + strings.Join(parts, "; ")
		}
		return "already synchronized"
	}
	parts := []string{"synchronized"}
	if view.WouldPull {
		parts = append(parts, fmt.Sprintf("pulled %d", view.RemoteCommitsAhead))
	}
	if pushed && view.WouldPush {
		parts = append(parts, "pushed")
	}
	if !pushed && view.LocalCommitsAhead > 0 {
		parts = append(parts, fmt.Sprintf("%d local commits unpushed", view.LocalCommitsAhead))
	}
	if len(view.Untracked) > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", len(view.Untracked)))
	}
	return strings.Join(parts, "; ")
}

func (a App) syncAllV2(o syncV2Options) int {
	r, err := a.registry()
	if err != nil {
		return a.fail(err)
	}
	roots := make([]string, 0, len(r.Projects))
	for root := range r.Projects {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		pi, pj := r.Projects[roots[i]], r.Projects[roots[j]]
		if pi.Slug == pj.Slug {
			return roots[i] < roots[j]
		}
		return pi.Slug < pj.Slug
	})
	result := SyncAllResult{}
	for _, root := range roots {
		p := r.Projects[root]
		view, e := a.syncOneV2(root, p, o)
		entry := SyncAllEntry{Root: root, Project: p.Slug, Environment: p.Environment}
		if e != nil {
			entry.Error = e.Error()
			result.Failed++
		} else {
			entry.Plan = &view
		}
		result.Roots = append(result.Roots, entry)
		if !o.JSON {
			status := ""
			if e != nil {
				status = "error: " + e.Error()
			} else {
				status = syncSummary(view, o.Push)
			}
			fmt.Fprintf(a.Stdout, "%-28s %-12s %s\n", p.Slug, p.Environment, status)
		}
	}
	if o.JSON {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(a.Stdout, string(b))
	}
	if result.Failed > 0 {
		return 1
	}
	return 0
}
