package lgit

import "strings"

func logicalWithoutStructural(logical map[string]StorageBackend, blockers []string) map[string]StorageBackend {
	out := make(map[string]StorageBackend, len(logical))
	for rel, backend := range logical {
		blocked := false
		for _, blocker := range blockers {
			if rel == blocker || strings.HasPrefix(rel, blocker+"/") {
				blocked = true
				break
			}
		}
		if !blocked {
			out[rel] = backend
		}
	}
	return out
}
