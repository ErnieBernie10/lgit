package lgit

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	gitCommandsOnce sync.Once
	gitCommandsSet  map[string]bool
)

func knownGitCommand(name string) bool {
	gitCommandsOnce.Do(func() {
		gitCommandsSet = map[string]bool{}
		cmd := exec.Command("git", "--list-cmds=main,others,alias,nohelpers")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				gitCommandsSet[line] = true
			}
		}
	})
	return gitCommandsSet[name]
}

func capitalizeSentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (a App) RunCLI(cwd string, args []string) int {
	if a.Stdout == nil {
		a.Stdout = os.Stdout
	}
	if a.Stderr == nil {
		a.Stderr = os.Stderr
	}

	explicit := ""
	if len(args) >= 2 && args[0] == "--root" {
		explicit = args[1]
		args = args[2:]
	}
	if len(args) == 0 {
		return a.helpCLI()
	}

	verb := args[0]
	switch verb {
	case "help", "--help", "-h":
		return a.helpCLI()
	case "version", "--version", "-v":
		fmt.Fprintln(a.Stdout, versionString())
		return 0
	case "key":
		root, _ := canonicalPath(cwd)
		return a.keyUX(root, args[1:])
	case "info":
		return a.infoUX(cwd, explicit, args[1:])
	case "remote":
		if len(args) > 1 && args[1] == "list" {
			return a.remoteListUX(args[2:])
		}
		if len(args) > 1 && args[1] == "set" {
			return a.remoteSetAll(args[2:])
		}
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(a.Stdout, "Usage: lgit remote set URL | lgit remote list [REMOTE] [--json]")
			return 0
		}
	case "attach":
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			return a.attachHelp()
		}
		root, err := a.resolveRoot(cwd, explicit, true)
		if err != nil {
			return a.fail(err)
		}
		return a.attachUX(root, args[1:])
	case "sync":
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprintln(a.Stdout, syncHelpText())
			return 0
		}
		o, err := parseSyncV2Options(args[1:])
		if err != nil {
			return a.fail(fmt.Errorf("usage: lgit sync [--all] [--push] [--dry-run] [--json]: %w", err))
		}
		if o.All {
			if explicit != "" {
				return a.fail(fmt.Errorf("--root and sync --all cannot be used together"))
			}
			return a.syncAllV2(o)
		}
		root, err := a.resolveRoot(cwd, explicit, false)
		if err != nil {
			return a.fail(err)
		}
		p, err := a.lookup(root)
		if err != nil {
			return a.fail(err)
		}
		view, err := a.syncOneV2(root, p, o)
		if err != nil {
			return a.fail(err)
		}
		if o.DryRun {
			return a.renderSyncView(view, o.JSON)
		}
		fmt.Fprintln(a.Stdout, capitalizeSentence(syncSummary(view, o.Push))+".")
		if len(view.Untracked) > 0 {
			fmt.Fprintln(a.Stdout, "\nUntracked drift:")
			for _, path := range view.Untracked {
				fmt.Fprintf(a.Stdout, "  ? %s\n", path)
			}
			fmt.Fprintln(a.Stdout, "\nNot included in sync; use 'lgit add PATH' to track it.")
		}
		return 0
	case "status":
		root, err := a.resolveRoot(cwd, explicit, false)
		if err != nil {
			return a.fail(err)
		}
		if code := a.mixedStatus(root, args[1:]); code != 0 {
			return code
		}
		p, err := a.lookup(root)
		if err != nil {
			return a.fail(err)
		}
		drift, err := a.scopedUntrackedDrift(root, p)
		if err != nil {
			return a.fail(err)
		}
		for _, path := range drift {
			fmt.Fprintf(a.Stdout, "?? %s\n", path)
		}
		return 0
	}

	knownLgit := map[string]bool{
		"init": true, "data-dir": true, "list": true, "remove": true, "env": true,
		"storage": true, "push": true, "pull": true, "add": true, "diff": true,
		"restore": true, "git": true,
	}
	if !knownLgit[verb] && !knownGitCommand(verb) {
		return a.fail(fmt.Errorf("unknown command %q; run 'lgit --help' for available commands", verb))
	}

	needsRemote := verb == "push" || verb == "pull" || (verb == "env" && len(args) > 1 && (args[1] == "list" || args[1] == "switch"))
	if needsRemote {
		root, err := a.resolveRoot(cwd, explicit, false)
		if err != nil {
			return a.fail(err)
		}
		p, err := a.lookup(root)
		if err != nil {
			return a.fail(err)
		}
		remote, err := a.sharedRemote()
		if err != nil {
			return a.fail(err)
		}
		if err := a.ensureProjectRemoteQuiet(root, p, remote); err != nil {
			return a.fail(fmt.Errorf("cannot configure project remote: %w", err))
		}
	}

	forward := append([]string(nil), args...)
	if explicit != "" {
		forward = append([]string{"--root", explicit}, forward...)
	}
	return a.Run(cwd, forward)
}

func (a App) helpCLI() int {
	lines := []string{
		"lgit - Git-backed storage for local project files and dotfiles", "", "Usage:",
		"  lgit init [--root PATH] [--env NAME] [--new-project] [--default plain|age] [--encryption identity|password]",
		"  lgit [--root PATH] attach [REMOTE] --env NAME [--project KEY] [--keep-local|--use-remote] [--dry-run] [--json]",
		"  lgit [--root PATH] info [--json]",
		"  lgit storage show PATH | set PATH plain|age | unset PATH | default [plain|age]",
		"  lgit remote set URL | lgit remote list [REMOTE] [--json]",
		"  lgit env current|branch|list|create NAME|switch NAME",
		"  lgit key generate|show|path|status|export FILE|import FILE",
		"  lgit add PATH... | lgit status | lgit diff [PATH...] | lgit restore PATH...",
		"  lgit sync [--all] [--push] [--dry-run] [--json]",
		"  lgit push | lgit pull",
		"  lgit git <raw git command>",
		"  lgit <git command>", "",
		"Run 'lgit sync --help', 'lgit attach --help', 'lgit key --help', or 'lgit remote --help' for focused help.",
	}
	fmt.Fprintln(a.Stdout, strings.Join(lines, "\n"))
	return 0
}
