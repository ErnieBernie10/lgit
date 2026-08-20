package lgit

import (
	"fmt"
	"runtime/debug"
	"strings"
)

func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "lgit dev"
	}
	version := strings.TrimSpace(info.Main.Version)
	if version != "" && version != "(devel)" {
		return "lgit " + version
	}
	var revision string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if len(revision) > 8 {
		revision = revision[:8]
	}
	if revision == "" {
		return "lgit dev"
	}
	if modified {
		return fmt.Sprintf("lgit dev (%s, dirty)", revision)
	}
	return fmt.Sprintf("lgit dev (%s)", revision)
}
