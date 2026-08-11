package lgit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAttachUXAcceptsRemoteAndMachineFlags(t *testing.T) {
	o, err := parseAttachUX([]string{"git@example.com:private.git", "--env", "Windows", "--project", "profile-abcd1234", "--dry-run", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Remote != "git@example.com:private.git" || o.Env != "windows" || o.Project != "profile-abcd1234" || !o.DryRun || !o.JSON {
		t.Fatalf("unexpected options: %#v", o)
	}
}

func TestStructuralConflictsDetectParentFileAndLeafDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "leaf"), 0700); err != nil {
		t.Fatal(err)
	}
	logical := map[string]StorageBackend{
		"config/app/settings.json": StoragePlain,
		"leaf":                     StorageAge,
	}
	got, err := structuralConflicts(root, logical)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "config") || !strings.Contains(joined, "leaf") {
		t.Fatalf("structural conflicts=%v", got)
	}
}

func TestAttachDryRunWithRemoteDoesNotRegister(t *testing.T) {
	remote, second, app := fixtureRemote(t)
	var out, stderr bytes.Buffer
	app.Stdout = &out
	app.Stderr = &stderr
	code := app.attachUX(second, []string{remote, "--env", "PCX", "--dry-run", "--json"})
	if code != 0 {
		t.Fatalf("dry-run failed (%d): %s", code, stderr.String())
	}
	var plan attachPlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if plan.Project == "" || plan.Environment != "pcx" || plan.Remote != remote {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	r, err := app.registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Projects) != 0 {
		t.Fatalf("dry-run registered project: %#v", r.Projects)
	}
}

func TestAttachUseRemoteHandlesStructuralConflictAndBacksItUp(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "shared.git")
	mustRun(t, t.TempDir(), "git", "init", "--bare", remote)

	data1 := filepath.Join(t.TempDir(), "data1")
	original := filepath.Join(t.TempDir(), "Profile")
	if err := os.MkdirAll(filepath.Join(original, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LGIT_DATA_DIR", data1)
	appRun(t, App{}, original, "init", "--root", original, "--env", "windows", "--default", "plain")
	appRun(t, App{}, original, "git", "config", "user.name", "Test")
	appRun(t, App{}, original, "git", "config", "user.email", "test@example.com")
	appRun(t, App{}, original, "remote", "set", remote)
	if err := os.WriteFile(filepath.Join(original, "config", "settings.json"), []byte("remote\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appRun(t, App{}, original, "add", "config/settings.json")
	appRun(t, App{}, original, "commit", "-m", "profile")
	appRun(t, App{}, original, "push")

	data2 := filepath.Join(t.TempDir(), "data2")
	second := filepath.Join(t.TempDir(), "Profile")
	if err := os.MkdirAll(second, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "config"), []byte("local blocker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LGIT_DATA_DIR", data2)

	var out, stderr bytes.Buffer
	app := App{Stdout: &out, Stderr: &stderr}
	if code := app.attachUX(second, []string{remote, "--env", "windows", "--use-remote"}); code != 0 {
		t.Fatalf("attach failed (%d): %s", code, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(second, "config", "settings.json"))
	if err != nil || string(got) != "remote\n" {
		t.Fatalf("remote materialization=%q err=%v", got, err)
	}

	entries, err := os.ReadDir(filepath.Join(data2, "backups"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("missing project backup dir: %v", err)
	}
	projectBackup := filepath.Join(data2, "backups", entries[0].Name())
	timestamps, err := os.ReadDir(projectBackup)
	if err != nil || len(timestamps) == 0 {
		t.Fatalf("missing timestamp backup: %v", err)
	}
	backed, err := os.ReadFile(filepath.Join(projectBackup, timestamps[0].Name(), "config"))
	if err != nil || string(backed) != "local blocker\n" {
		t.Fatalf("backup=%q err=%v", backed, err)
	}
}

func TestKeyPathAndStatusAreSelfDescribing(t *testing.T) {
	data := filepath.Join(t.TempDir(), "lgit")
	t.Setenv("LGIT_DATA_DIR", data)
	var out bytes.Buffer
	app := App{Stdout: &out, Stderr: &bytes.Buffer{}}
	if code := app.keyUX(t.TempDir(), []string{"path"}); code != 0 {
		t.Fatalf("key path failed")
	}
	if strings.TrimSpace(out.String()) != filepath.Join(data, "age-identity.txt") {
		t.Fatalf("path=%q", out.String())
	}
	out.Reset()
	if code := app.keyUX(t.TempDir(), []string{"status", "--json"}); code != 0 {
		t.Fatalf("key status failed")
	}
	if !strings.Contains(out.String(), `"status": "missing"`) || !strings.Contains(out.String(), "age-identity.txt") {
		t.Fatalf("status=%q", out.String())
	}
}
