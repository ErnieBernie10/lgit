package lgit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, e, b)
	}
	return string(b)
}
func appRun(t *testing.T, app App, dir string, args ...string) string {
	t.Helper()
	var out, err bytes.Buffer
	app.Stdout = &out
	app.Stderr = &err
	if code := app.Run(dir, args); code != 0 {
		t.Fatalf("lgit %v failed (%d): %s", args, code, err.String())
	}
	return out.String()
}
func initMain(t *testing.T, dir string) {
	t.Helper()
	mustRun(t, dir, "git", "init")
	mustRun(t, dir, "git", "config", "user.name", "Test")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0600)
	mustRun(t, dir, "git", "add", ".gitignore")
	mustRun(t, dir, "git", "commit", "-m", "init")
}

func TestSlugifyRepositoryFolder(t *testing.T) {
	cases := map[string]string{"Booking": "booking", "Customer Portal": "customer-portal", "my_project": "my-project", "AMEND.Web": "amend-web"}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Fatalf("slugify(%q)=%q want %q", in, got, want)
		}
	}
}

func TestAttachDiscoversProjectByFolderNameAcrossPaths(t *testing.T) {
	data1 := filepath.Join(t.TempDir(), "data1")
	remote := filepath.Join(t.TempDir(), "shared.git")
	mustRun(t, t.TempDir(), "git", "init", "--bare", remote)
	original := filepath.Join(t.TempDir(), "Booking")
	os.MkdirAll(original, 0700)
	initMain(t, original)
	t.Setenv("LGIT_DATA_DIR", data1)
	appRun(t, App{}, original, "init", "--env", "PCX")
	appRun(t, App{}, original, "remote", "set", remote)
	os.WriteFile(filepath.Join(original, ".env"), []byte("MACHINE=PCX\n"), 0600)
	appRun(t, App{}, original, "add", ".env")
	appRun(t, App{}, original, "commit", "-m", "pcx")
	appRun(t, App{}, original, "push")

	data2 := filepath.Join(t.TempDir(), "data2")
	second := filepath.Join(t.TempDir(), "Booking")
	os.MkdirAll(second, 0700)
	initMain(t, second)
	t.Setenv("LGIT_DATA_DIR", data2)
	appRun(t, App{}, second, "remote", "set", remote)
	appRun(t, App{}, filepath.Join(second, "."), "attach", "--env", "PCX")
	b, e := os.ReadFile(filepath.Join(second, ".env"))
	if e != nil || strings.TrimSpace(string(b)) != "MACHINE=PCX" {
		t.Fatalf("attached file=%q err=%v", b, e)
	}
	out := appRun(t, App{}, second, "status", "--short")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("status not clean: %q", out)
	}
}

func TestAttachRejectsDifferentExistingFileByDefault(t *testing.T) {
	remote, second, app := fixtureRemote(t)
	_ = remote
	os.WriteFile(filepath.Join(second, ".env"), []byte("LOCAL=1\n"), 0600)
	var out, err bytes.Buffer
	app.Stdout = &out
	app.Stderr = &err
	if code := app.Run(second, []string{"attach", "--env", "PCX"}); code == 0 || !strings.Contains(err.String(), "local files differ") {
		t.Fatalf("code=%d stderr=%q", code, err.String())
	}
	r, _ := app.registry()
	if len(r.Projects) != 0 {
		t.Fatalf("failed attach registered project: %#v", r)
	}
}

func TestAttachKeepLocalLeavesModification(t *testing.T) {
	_, second, app := fixtureRemote(t)
	os.WriteFile(filepath.Join(second, ".env"), []byte("LOCAL=1\n"), 0600)
	appRun(t, app, second, "attach", "--env", "PCX", "--keep-local")
	b, _ := os.ReadFile(filepath.Join(second, ".env"))
	if string(b) != "LOCAL=1\n" {
		t.Fatalf("local file overwritten: %q", b)
	}
	out := appRun(t, app, second, "status", "--short")
	if !strings.Contains(out, ".env") {
		t.Fatalf("expected modification: %q", out)
	}
}

func fixtureRemote(t *testing.T) (string, string, App) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "shared.git")
	mustRun(t, t.TempDir(), "git", "init", "--bare", remote)
	d1 := filepath.Join(t.TempDir(), "d1")
	original := filepath.Join(t.TempDir(), "Booking")
	os.MkdirAll(original, 0700)
	initMain(t, original)
	t.Setenv("LGIT_DATA_DIR", d1)
	appRun(t, App{}, original, "init", "--env", "PCX")
	appRun(t, App{}, original, "remote", "set", remote)
	os.WriteFile(filepath.Join(original, ".env"), []byte("REMOTE=1\n"), 0600)
	appRun(t, App{}, original, "add", ".env")
	appRun(t, App{}, original, "commit", "-m", "env")
	appRun(t, App{}, original, "push")
	d2 := filepath.Join(t.TempDir(), "d2")
	second := filepath.Join(t.TempDir(), "Booking")
	os.MkdirAll(second, 0700)
	initMain(t, second)
	t.Setenv("LGIT_DATA_DIR", d2)
	app := App{}
	appRun(t, app, second, "remote", "set", remote)
	return remote, second, app
}
