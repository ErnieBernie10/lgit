package lgit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := Registry{Remote: "x", Projects: map[string]Project{"/work/app": {ID: "abc", GitDir: filepath.Join(dir, "repos", "abc"), Slug: "app-abc", Environment: "PCX"}}}
	path := filepath.Join(dir, "projects.json")
	if err := SaveRegistry(path, r); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Remote != "x" || got.Projects["/work/app"].Environment != "PCX" {
		t.Fatalf("unexpected registry: %#v", got)
	}
}

func TestLoadMissingRegistryReturnsEmpty(t *testing.T) {
	got, err := LoadRegistry(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects == nil || len(got.Projects) != 0 {
		t.Fatalf("expected empty registry, got %#v", got)
	}
}

func TestDataDirUsesOverride(t *testing.T) {
	t.Setenv("LGIT_DATA_DIR", filepath.Join(t.TempDir(), "custom"))
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != os.Getenv("LGIT_DATA_DIR") {
		t.Fatalf("got %q", got)
	}
}
