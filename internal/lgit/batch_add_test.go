package lgit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitPathBatchesKeepsReasonableCommandSize(t *testing.T) {
	paths := make([]string, 5000)
	for i := range paths {
		paths[i] = fmt.Sprintf(".agents/skills/skill-%04d/some-long-config-file-name.toml", i)
	}
	batches := splitPathBatches(paths, []string{"add", "--force", "--"})
	if len(batches) <= 1 {
		t.Fatalf("expected multiple batches, got %d", len(batches))
	}
	count := 0
	for _, batch := range batches {
		count += len(batch)
		size := 0
		for _, p := range batch {
			size += len(p) + 3
		}
		if size > gitPathBatchBudget+256 {
			t.Fatalf("batch too large: %d", size)
		}
	}
	if count != len(paths) {
		t.Fatalf("batched %d paths, want %d", count, len(paths))
	}
}

func TestLargePlainDirectoryAdd(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("LGIT_DATA_DIR", data)
	appRun(t, App{}, root, "init", "--root", root, "--env", "desktop", "--default", "plain", "--encryption", "identity")

	dir := filepath.Join(root, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		p := filepath.Join(dir, fmt.Sprintf("skill-%04d.txt", i))
		if err := os.WriteFile(p, []byte("skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock := filepath.Join(root, ".agents", ".skill-lock.json")
	if err := os.WriteFile(lock, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	appRun(t, App{}, root, "--root", root, "add", ".agents/skills", ".agents/.skill-lock.json")
	p, err := App{}.lookup(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := gitOutput(root, p.GitDir, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(out)
	// init also stages .lgit/storage.toml and recipients.txt.
	if len(lines) < 603 {
		t.Fatalf("expected all files staged, got %d entries", len(lines))
	}
	if strings.Contains(out, ".lgit/store/.agents/skills") {
		t.Fatalf("plain files unexpectedly stored through age: %s", out)
	}
}
