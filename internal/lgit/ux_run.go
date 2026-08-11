package lgit

import (
	"fmt"
	"os"
)

// RunUX is the user-facing CLI entrypoint. It keeps the existing Run behavior
// for Git/storage commands while handling the self-describing attach and
// introspection commands before generic root resolution/delegation.
func (a App) RunUX(cwd string, args []string) int {
	if a.Stdout == nil {
		a.Stdout = os.Stdout
	}
	if a.Stderr == nil {
		a.Stderr = os.Stderr
	}

	original := append([]string(nil), args...)
	explicit := ""
	if len(args) >= 2 && args[0] == "--root" {
		explicit = args[1]
		args = args[2:]
	}
	if len(args) == 0 {
		return a.helpUX()
	}

	switch args[0] {
	case "help", "--help", "-h":
		return a.helpUX()
	case "key":
		root, _ := canonicalPath(cwd)
		return a.keyUX(root, args[1:])
	case "info":
		return a.infoUX(cwd, explicit, args[1:])
	case "remote":
		if len(args) > 1 && args[1] == "list" {
			return a.remoteListUX(args[2:])
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
	}

	return a.Run(cwd, original)
}

func (a App) helpUX() int {
	fmt.Fprintln(a.Stdout, `lgit - Git-backed storage for local project files and dotfiles

Usage:
  lgit init [--root PATH] [--env NAME] [--new-project] [--default plain|age] [--encryption identity|password]
  lgit [--root PATH] attach [REMOTE] --env NAME [--project KEY] [--keep-local|--use-remote] [--dry-run] [--json]
  lgit [--root PATH] info [--json]
  lgit storage show PATH | set PATH plain|age | unset PATH | default [plain|age]
  lgit remote set URL | lgit remote list [REMOTE] [--json]
  lgit env current|branch|list|create NAME|switch NAME
  lgit key generate|show|path|status|export FILE|import FILE
  lgit add PATH... | lgit status | lgit diff [PATH...] | lgit restore PATH...
  lgit push | lgit pull
  lgit git <raw git command>
  lgit <git command>

Run 'lgit attach --help', 'lgit key --help', or 'lgit remote --help' for focused help.`)
	return 0
}
