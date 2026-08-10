package lgit

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"filippo.io/age"
	"github.com/pelletier/go-toml/v2"
)

type StorageBackend string

const (
	StoragePlain StorageBackend = "plain"
	StorageAge   StorageBackend = "age"
)

type StorageEncryption struct {
	Mode string `toml:"mode"`
}
type StorageConfig struct {
	Version    int                       `toml:"version"`
	Default    StorageBackend            `toml:"default"`
	Encryption StorageEncryption         `toml:"encryption"`
	Files      map[string]StorageBackend `toml:"files,omitempty"`
}

func validBackend(v StorageBackend) bool { return v == StoragePlain || v == StorageAge }
func normalizeLogical(path string) (string, error) {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == "" || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid logical path %q", path)
	}
	return path, nil
}
func storageConfigPath(root string) string { return filepath.Join(root, ".lgit", "storage.toml") }

func loadStorageConfig(root string) (StorageConfig, error) {
	var c StorageConfig
	b, err := os.ReadFile(storageConfigPath(root))
	if err == nil {
		if err := toml.Unmarshal(b, &c); err != nil {
			return c, err
		}
		if c.Version != 1 {
			return c, fmt.Errorf("unsupported storage config version %d", c.Version)
		}
		if !validBackend(c.Default) {
			return c, fmt.Errorf("unsupported default storage backend %q", c.Default)
		}
		if c.Encryption.Mode != "identity" && c.Encryption.Mode != "password" {
			return c, fmt.Errorf("unsupported encryption mode %q", c.Encryption.Mode)
		}
		if c.Files == nil {
			c.Files = map[string]StorageBackend{}
		}
		for path, backend := range c.Files {
			if _, err := normalizeLogical(path); err != nil {
				return c, err
			}
			if !validBackend(backend) {
				return c, fmt.Errorf("unsupported storage backend %q for %s", backend, path)
			}
		}
		return c, nil
	}
	if !os.IsNotExist(err) {
		return c, err
	}
	// Legacy repositories were entirely age-backed and stored the encryption mode in format.json.
	f, ferr := readLegacyAgeFormat(root)
	if ferr != nil {
		return c, ferr
	}
	mode := "identity"
	if f.Encryption == agePasswordFormat {
		mode = "password"
	}
	return StorageConfig{Version: 1, Default: StorageAge, Encryption: StorageEncryption{Mode: mode}, Files: map[string]StorageBackend{}}, nil
}

func writeStorageConfig(root string, c StorageConfig) error {
	if c.Files == nil {
		c.Files = map[string]StorageBackend{}
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	path := storageConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
func configuredBackend(c StorageConfig, path string) (StorageBackend, string) {
	if b, ok := c.Files[path]; ok {
		return b, "explicit"
	}
	return c.Default, "default"
}

func (a App) initStorage(root string, p Project, backend StorageBackend, encryption string) error {
	c := StorageConfig{Version: 1, Default: backend, Encryption: StorageEncryption{Mode: encryption}, Files: map[string]StorageBackend{}}
	if encryption == "password" {
		if _, err := a.readPassword(true); err != nil {
			return err
		}
	} else {
		id, err := a.ensureIdentity()
		if err != nil {
			return err
		}
		meta := filepath.Join(root, ".lgit")
		if err := os.MkdirAll(meta, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(meta, "recipients.txt"), []byte(id.Recipient().String()+"\n"), 0600); err != nil {
			return err
		}
	}
	if err := writeStorageConfig(root, c); err != nil {
		return err
	}
	if !p.Standalone {
		if err := excludeLgitV2(root); err != nil {
			return err
		}
	}
	files := []string{".lgit/storage.toml"}
	if encryption == "identity" {
		files = append(files, ".lgit/recipients.txt")
	}
	args := append([]string{"add", "--force", "--"}, files...)
	if code := a.run(root, p.GitDir, args...); code != 0 {
		return fmt.Errorf("failed to stage storage metadata")
	}
	return nil
}

func readLegacyAgeFormat(root string) (ageFormatFile, error) {
	return readAgeFormatLegacyOnly(root)
}

func (a App) storageRecipients(root string, c StorageConfig) ([]age.Recipient, error) {
	if c.Encryption.Mode == "password" {
		password, err := a.readPassword(false)
		if err != nil {
			return nil, err
		}
		r, err := age.NewScryptRecipient(password)
		if err != nil {
			return nil, err
		}
		return []age.Recipient{r}, nil
	}
	return readRecipients(root)
}
func (a App) storageIdentity(root string, c StorageConfig) (age.Identity, error) {
	if c.Encryption.Mode == "password" {
		password, err := a.readPassword(false)
		if err != nil {
			return nil, err
		}
		return age.NewScryptIdentity(password)
	}
	return a.loadIdentity()
}

func plainTrackedAt(root string, p Project, ref string) ([]string, error) {
	out, err := gitOutput(root, p.GitDir, "ls-tree", "-r", "--name-only", ref)
	if err != nil {
		return nil, err
	}
	var xs []string
	for _, x := range strings.Split(strings.TrimSpace(out), "\n") {
		x = filepath.ToSlash(strings.TrimSpace(x))
		if x == "" || strings.HasPrefix(x, ".lgit/") {
			continue
		}
		xs = append(xs, x)
	}
	sort.Strings(xs)
	return xs, nil
}
func logicalTrackedAt(root string, p Project, ref string) (map[string]StorageBackend, error) {
	m := map[string]StorageBackend{}
	plain, err := plainTrackedAt(root, p, ref)
	if err != nil {
		return nil, err
	}
	for _, x := range plain {
		m[x] = StoragePlain
	}
	stores, err := trackedStore(root, p, ref)
	if err != nil {
		return nil, err
	}
	for _, sp := range stores {
		m[plainPath(sp)] = StorageAge
	}
	return m, nil
}
func currentBackend(root string, p Project, path string) (StorageBackend, bool, error) {
	if _, err := gitOutput(root, p.GitDir, "ls-files", "--error-unmatch", "--", path); err == nil {
		return StoragePlain, true, nil
	}
	sp := filepath.ToSlash(storePath(path))
	if _, err := gitOutput(root, p.GitDir, "ls-files", "--error-unmatch", "--", sp); err == nil {
		return StorageAge, true, nil
	}
	return "", false, nil
}

func (a App) mainOwns(root string, p Project, path string) bool {
	if p.Standalone {
		return false
	}
	c := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", path)
	return c.Run() == nil
}

func (a App) expandPaths(root string, p Project, args []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) error {
		rel, err := normalizeLogical(rel)
		if err != nil {
			return err
		}
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
		return nil
	}
	for _, raw := range args {
		abs := raw
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, raw)
		}
		abs, err := canonicalPath(abs)
		if err != nil {
			return nil, err
		}
		if !containsPath(root, abs) {
			return nil, fmt.Errorf("path outside lgit root: %s", raw)
		}
		if child, ok, err := a.childRoot(root, abs); err != nil {
			return nil, err
		} else if ok {
			return nil, fmt.Errorf("path belongs to nested lgit project: %s", child)
		}
		if nested, ok := nestedGitRoot(root, abs); ok {
			return nil, fmt.Errorf("path belongs to nested Git repository: %s", nested)
		}
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			rel, _ := filepath.Rel(root, abs)
			if err := add(rel); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("symlink storage is not supported yet: %s", raw)
			}
			rel, _ := filepath.Rel(root, abs)
			if err := add(rel); err != nil {
				return nil, err
			}
			continue
		}
		err = filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == abs {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == ".lgit" {
					return filepath.SkipDir
				}
				if child, ok, _ := a.childRoot(root, path); ok && pathKey(child) == pathKey(path) {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			return add(rel)
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func (a App) addOne(root string, p Project, c StorageConfig, path string, backend StorageBackend, rs []age.Recipient) error {
	if a.mainOwns(root, p, path) {
		return fmt.Errorf("main repository already tracks %s", path)
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("only regular files are supported: %s", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	sp := filepath.ToSlash(storePath(path))
	if os.IsNotExist(err) {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(sp)))
		_ = a.run(root, p.GitDir, "rm", "--cached", "--ignore-unmatch", "--", path, sp)
		return nil
	}
	if backend == StoragePlain {
		if code := a.run(root, p.GitDir, "add", "--force", "--", path); code != 0 {
			return fmt.Errorf("failed to stage %s", path)
		}
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(sp)))
		if code := a.run(root, p.GitDir, "rm", "--cached", "--ignore-unmatch", "--", sp); code != 0 {
			return fmt.Errorf("failed to remove encrypted representation of %s", path)
		}
		return nil
	}
	plain, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	// Avoid randomized ciphertext churn if the currently staged age representation already matches.
	if cipher, err := gitBlob(root, p.GitDir, ":"+sp); err == nil {
		id, derr := a.storageIdentity(root, c)
		if derr == nil {
			if old, derr := decryptBytes(cipher, id); derr == nil && bytes.Equal(old, plain) {
				return nil
			}
		}
	}
	if rs == nil {
		var rerr error
		rs, rerr = a.storageRecipients(root, c)
		if rerr != nil {
			return rerr
		}
	}
	cipher, err := encryptBytes(plain, rs)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(sp))
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(dst, cipher, 0600); err != nil {
		return err
	}
	if code := a.run(root, p.GitDir, "add", "--force", "--", sp); code != 0 {
		return fmt.Errorf("failed to stage encrypted %s", path)
	}
	if code := a.run(root, p.GitDir, "rm", "--cached", "--ignore-unmatch", "--", path); code != 0 {
		return fmt.Errorf("failed to remove plain representation of %s", path)
	}
	return nil
}

func (a App) mixedAdd(root string, args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit add PATH..."))
	}
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	c, err := loadStorageConfig(root)
	if err != nil {
		return a.fail(err)
	}
	paths, err := a.expandPaths(root, p, args)
	if err != nil {
		return a.fail(err)
	}
	var rs []age.Recipient
	for _, path := range paths {
		backend, _ := configuredBackend(c, path)
		if backend == StorageAge && rs == nil {
			rs, err = a.storageRecipients(root, c)
			if err != nil {
				return a.fail(err)
			}
		}
		if err := a.addOne(root, p, c, path, backend, rs); err != nil {
			return a.fail(err)
		}
	}
	return 0
}

func (a App) mixedMaterialize(root string, p Project) error {
	c, err := loadStorageConfig(root)
	if err != nil {
		return err
	}
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return err
	}
	var id age.Identity
	if len(stores) > 0 {
		id, err = a.storageIdentity(root, c)
		if err != nil {
			return err
		}
	}
	oldFile := filepath.Join(p.GitDir, "lgit-materialized.txt")
	oldb, _ := os.ReadFile(oldFile)
	old := map[string]bool{}
	for _, x := range strings.Split(string(oldb), "\n") {
		if x != "" {
			old[x] = true
		}
	}
	now := map[string]bool{}
	for _, sp := range stores {
		cipher, err := gitBlob(root, p.GitDir, "HEAD:"+sp)
		if err != nil {
			return err
		}
		plain, err := decryptBytes(cipher, id)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", plainPath(sp), err)
		}
		rel := plainPath(sp)
		now[rel] = true
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if m, err := gitOutput(root, p.GitDir, "ls-tree", "HEAD", sp); err == nil && strings.HasPrefix(m, "100755") {
			mode = 0700
		}
		if err := os.WriteFile(dst, plain, mode); err != nil {
			return err
		}
	}
	for rel := range old {
		if !now[rel] {
			if backend, ok, _ := currentBackend(root, p, rel); !ok || backend != StoragePlain {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
			}
		}
	}
	lines := make([]string, 0, len(now))
	for rel := range now {
		lines = append(lines, rel)
	}
	sort.Strings(lines)
	return os.WriteFile(oldFile, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func (a App) mixedClean(root string, p Project) bool {
	if out, err := gitOutput(root, p.GitDir, "status", "--porcelain", "--untracked-files=no"); err != nil || strings.TrimSpace(out) != "" {
		return false
	}
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return false
	}
	if len(stores) == 0 {
		return true
	}
	c, err := loadStorageConfig(root)
	if err != nil {
		return false
	}
	id, err := a.storageIdentity(root, c)
	if err != nil {
		return false
	}
	for _, sp := range stores {
		cipher, err := gitBlob(root, p.GitDir, "HEAD:"+sp)
		if err != nil {
			return false
		}
		want, err := decryptBytes(cipher, id)
		if err != nil {
			return false
		}
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(plainPath(sp))))
		if err != nil || !bytes.Equal(got, want) {
			return false
		}
	}
	return true
}

func (a App) mixedStatus(root string, args []string) int {
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	out, _ := gitOutput(root, p.GitDir, "status", "--porcelain", "--untracked-files=no")
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		path := filepath.ToSlash(strings.TrimSpace(line[3:]))
		if strings.HasPrefix(path, ".lgit/store/") {
			fmt.Fprintf(a.Stdout, "%s %s\n", line[:2], plainPath(path))
			continue
		}
		if strings.HasPrefix(path, ".lgit/") {
			continue
		}
		fmt.Fprintln(a.Stdout, line)
	}
	if _, headErr := gitOutput(root, p.GitDir, "rev-parse", "--verify", "HEAD"); headErr != nil {
		return 0
	}
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return a.fail(err)
	}
	if len(stores) == 0 {
		return 0
	}
	c, err := loadStorageConfig(root)
	if err != nil {
		return a.fail(err)
	}
	id, err := a.storageIdentity(root, c)
	if err != nil {
		return a.fail(err)
	}
	for _, sp := range stores {
		cipher, _ := gitBlob(root, p.GitDir, "HEAD:"+sp)
		want, _ := decryptBytes(cipher, id)
		rel := plainPath(sp)
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			fmt.Fprintf(a.Stdout, " D %s\n", rel)
		} else if !bytes.Equal(got, want) {
			fmt.Fprintf(a.Stdout, " M %s\n", rel)
		}
	}
	return 0
}

func (a App) mixedRestore(root string, args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit restore PATH..."))
	}
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	c, err := loadStorageConfig(root)
	if err != nil {
		return a.fail(err)
	}
	var id age.Identity
	for _, raw := range args {
		rel, err := filepath.Rel(root, filepath.Join(root, raw))
		if err != nil {
			return a.fail(err)
		}
		rel, err = normalizeLogical(rel)
		if err != nil {
			return a.fail(err)
		}
		backend, ok, err := currentBackend(root, p, rel)
		if err != nil {
			return a.fail(err)
		}
		if !ok {
			return a.fail(fmt.Errorf("path is not tracked: %s", rel))
		}
		if backend == StoragePlain {
			if code := a.run(root, p.GitDir, "restore", "--", rel); code != 0 {
				return code
			}
			continue
		}
		if id == nil {
			id, err = a.storageIdentity(root, c)
			if err != nil {
				return a.fail(err)
			}
		}
		sp := filepath.ToSlash(storePath(rel))
		cipher, err := gitBlob(root, p.GitDir, "HEAD:"+sp)
		if err != nil {
			return a.fail(err)
		}
		plain, err := decryptBytes(cipher, id)
		if err != nil {
			return a.fail(err)
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return a.fail(err)
		}
		if err := os.WriteFile(dst, plain, 0600); err != nil {
			return a.fail(err)
		}
	}
	return 0
}

func logicalDiffAge(root string, p Project, c StorageConfig, rel string, id age.Identity) (string, error) {
	sp := filepath.ToSlash(storePath(rel))
	cipher, err := gitBlob(root, p.GitDir, "HEAD:"+sp)
	if err != nil {
		return "", err
	}
	old, err := decryptBytes(cipher, id)
	if err != nil {
		return "", err
	}
	now, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		now = []byte{}
	} else if err != nil {
		return "", err
	}
	if bytes.Equal(old, now) {
		return "", nil
	}
	d, err := os.MkdirTemp("", "lgit-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(d)
	a := filepath.Join(d, "old")
	b := filepath.Join(d, "new")
	_ = os.WriteFile(a, old, 0600)
	_ = os.WriteFile(b, now, 0600)
	cmd := exec.Command("git", "diff", "--no-index", "--text", "--", a, b)
	out, _ := cmd.CombinedOutput()
	text := string(out)
	text = strings.ReplaceAll(text, filepath.ToSlash(a), "a/"+rel)
	text = strings.ReplaceAll(text, filepath.ToSlash(b), "b/"+rel)
	text = strings.ReplaceAll(text, a, "a/"+rel)
	text = strings.ReplaceAll(text, b, "b/"+rel)
	return text, nil
}
func (a App) mixedDiff(root string, args []string) int {
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	tracked, err := logicalTrackedAt(root, p, "HEAD")
	if err != nil {
		return a.fail(err)
	}
	var paths []string
	if len(args) == 0 {
		for x := range tracked {
			paths = append(paths, x)
		}
		sort.Strings(paths)
	} else {
		for _, raw := range args {
			if strings.HasPrefix(raw, "-") {
				return a.fail(fmt.Errorf("diff options are not supported for logical storage yet"))
			}
			rel, _ := filepath.Rel(root, filepath.Join(root, raw))
			rel, err = normalizeLogical(rel)
			if err != nil {
				return a.fail(err)
			}
			paths = append(paths, rel)
		}
	}
	c, err := loadStorageConfig(root)
	if err != nil {
		return a.fail(err)
	}
	var id age.Identity
	for _, rel := range paths {
		backend, ok := tracked[rel]
		if !ok {
			continue
		}
		if backend == StoragePlain {
			out, err := gitOutput(root, p.GitDir, "diff", "--", rel)
			if err != nil {
				return a.fail(err)
			}
			fmt.Fprint(a.Stdout, out)
			continue
		}
		if id == nil {
			id, err = a.storageIdentity(root, c)
			if err != nil {
				return a.fail(err)
			}
		}
		out, err := logicalDiffAge(root, p, c, rel, id)
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprint(a.Stdout, out)
	}
	return 0
}

func (a App) storageCommand(root string, args []string) int {
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	c, err := loadStorageConfig(root)
	if err != nil {
		return a.fail(err)
	}
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit storage show|set|unset|default"))
	}
	switch args[0] {
	case "default":
		if len(args) == 1 {
			fmt.Fprintln(a.Stdout, c.Default)
			return 0
		}
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit storage default [plain|age]"))
		}
		b := StorageBackend(strings.ToLower(args[1]))
		if !validBackend(b) {
			return a.fail(fmt.Errorf("unsupported storage backend %q", b))
		}
		c.Default = b
		if err := writeStorageConfig(root, c); err != nil {
			return a.fail(err)
		}
		if code := a.run(root, p.GitDir, "add", "--force", "--", ".lgit/storage.toml"); code != 0 {
			return code
		}
		return 0
	case "show":
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit storage show PATH"))
		}
		rel, _ := filepath.Rel(root, filepath.Join(root, args[1]))
		rel, err = normalizeLogical(rel)
		if err != nil {
			return a.fail(err)
		}
		configured, source := configuredBackend(c, rel)
		current, ok, _ := currentBackend(root, p, rel)
		if !ok {
			current = "untracked"
		}
		fmt.Fprintf(a.Stdout, "%s\ncurrent: %s\nconfigured: %s\nsource: %s\n", rel, current, configured, source)
		if configured == StorageAge {
			fmt.Fprintf(a.Stdout, "encryption: %s\n", c.Encryption.Mode)
		}
		return 0
	case "set":
		if len(args) != 3 {
			return a.fail(fmt.Errorf("usage: lgit storage set PATH plain|age"))
		}
		return a.setStorage(root, p, c, args[1], StorageBackend(strings.ToLower(args[2])), false)
	case "unset":
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit storage unset PATH"))
		}
		return a.setStorage(root, p, c, args[1], c.Default, true)
	default:
		return a.fail(fmt.Errorf("unknown storage command %q", args[0]))
	}
}

func (a App) setStorage(root string, p Project, c StorageConfig, raw string, backend StorageBackend, unset bool) int {
	if !validBackend(backend) {
		return a.fail(fmt.Errorf("unsupported storage backend %q", backend))
	}
	rel, _ := filepath.Rel(root, filepath.Join(root, raw))
	rel, err := normalizeLogical(rel)
	if err != nil {
		return a.fail(err)
	}
	oldConfig, oldConfigErr := os.ReadFile(storageConfigPath(root))
	indexPath := filepath.Join(p.GitDir, "index")
	oldIndex, oldIndexErr := os.ReadFile(indexPath)
	spFull := filepath.Join(root, filepath.FromSlash(storePath(rel)))
	oldStore, oldStoreErr := os.ReadFile(spFull)
	rollback := func() {
		if oldConfigErr == nil {
			_ = os.WriteFile(storageConfigPath(root), oldConfig, 0600)
		} else {
			_ = os.Remove(storageConfigPath(root))
		}
		if oldIndexErr == nil {
			_ = os.WriteFile(indexPath, oldIndex, 0600)
		}
		if oldStoreErr == nil {
			_ = os.MkdirAll(filepath.Dir(spFull), 0700)
			_ = os.WriteFile(spFull, oldStore, 0600)
		} else {
			_ = os.Remove(spFull)
		}
	}
	if unset {
		delete(c.Files, rel)
	} else {
		if c.Files == nil {
			c.Files = map[string]StorageBackend{}
		}
		c.Files[rel] = backend
	}
	if err := writeStorageConfig(root, c); err != nil {
		rollback()
		return a.fail(err)
	}
	if code := a.run(root, p.GitDir, "add", "--force", "--", ".lgit/storage.toml"); code != 0 {
		rollback()
		return code
	}
	if _, tracked, _ := currentBackend(root, p, rel); tracked {
		var rs []age.Recipient
		if backend == StorageAge {
			rs, err = a.storageRecipients(root, c)
			if err != nil {
				rollback()
				return a.fail(err)
			}
		}
		if err := a.addOne(root, p, c, rel, backend, rs); err != nil {
			rollback()
			return a.fail(err)
		}
	}
	return 0
}

func targetConfigAt(root string, p Project, ref string) (StorageConfig, error) {
	b, err := gitBlob(root, p.GitDir, ref+":.lgit/storage.toml")
	if err != nil {
		return loadStorageConfig(root)
	}
	var c StorageConfig
	if err := toml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Files == nil {
		c.Files = map[string]StorageBackend{}
	}
	return c, nil
}

func (a App) conflictsAt(root string, p Project, ref string) ([]string, error) {
	tracked, err := logicalTrackedAt(root, p, ref)
	if err != nil {
		return nil, err
	}
	c, err := targetConfigAt(root, p, ref)
	if err != nil {
		return nil, err
	}
	var id age.Identity
	var out []string
	for rel, b := range tracked {
		local, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var remote []byte
		if b == StoragePlain {
			remote, err = gitBlob(root, p.GitDir, ref+":"+rel)
		} else {
			if id == nil {
				id, err = a.storageIdentity(root, c)
				if err != nil {
					return nil, err
				}
			}
			cipher, e := gitBlob(root, p.GitDir, ref+":"+filepath.ToSlash(storePath(rel)))
			if e != nil {
				return nil, e
			}
			remote, err = decryptBytes(cipher, id)
		}
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(local, remote) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (a App) checkoutPrepare(root string, p Project, targetRef string) ([]string, error) {
	current, err := logicalTrackedAt(root, p, "HEAD")
	if err != nil {
		return nil, err
	}
	target, err := logicalTrackedAt(root, p, targetRef)
	if err != nil {
		return nil, err
	}
	var removed []string
	for rel, b := range current {
		if b == StorageAge && target[rel] == StoragePlain {
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				backup := filepath.Join(p.GitDir, "transition", filepath.FromSlash(rel))
				_ = os.MkdirAll(filepath.Dir(backup), 0700)
				_ = os.WriteFile(backup, data, 0600)
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
				removed = append(removed, rel)
			}
		}
	}
	return removed, nil
}
func restorePrepared(root string, p Project, paths []string) {
	for _, rel := range paths {
		backup := filepath.Join(p.GitDir, "transition", filepath.FromSlash(rel))
		if b, err := os.ReadFile(backup); err == nil {
			dst := filepath.Join(root, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(dst), 0700)
			_ = os.WriteFile(dst, b, 0600)
			_ = os.Remove(backup)
		}
	}
}

func (a App) envSwitchMixed(root string, p Project, name string) int {
	name, err := validateName(name)
	if err != nil {
		return a.fail(err)
	}
	if name == p.Environment {
		return 0
	}
	if !a.mixedClean(root, p) {
		return a.fail(fmt.Errorf("cannot switch environment: uncommitted changes"))
	}
	target := "refs/heads/env/" + name
	if _, err := gitOutput(root, p.GitDir, "rev-parse", "--verify", target); err != nil {
		_ = a.run(root, p.GitDir, "fetch", "origin")
		target = "refs/remotes/origin/envs/" + name
	}
	removed, err := a.checkoutPrepare(root, p, target)
	if err != nil {
		return a.fail(err)
	}
	old := p.Environment
	if _, err := gitOutput(root, p.GitDir, "rev-parse", "--verify", "refs/heads/env/"+name); err != nil {
		if code := a.run(root, p.GitDir, "switch", "-c", "env/"+name, target); code != 0 {
			restorePrepared(root, p, removed)
			return code
		}
	} else if code := a.run(root, p.GitDir, "checkout", "env/"+name); code != 0 {
		restorePrepared(root, p, removed)
		return code
	}
	if err := a.mixedMaterialize(root, p); err != nil {
		_ = a.run(root, p.GitDir, "checkout", "env/"+old)
		restorePrepared(root, p, removed)
		_ = a.mixedMaterialize(root, p)
		return a.fail(err)
	}
	_ = os.RemoveAll(filepath.Join(p.GitDir, "transition"))
	return a.setEnvironment(root, p, name)
}

func (a App) pullMixed(root string, args []string) int {
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	if !a.mixedClean(root, p) {
		return a.fail(fmt.Errorf("cannot pull: uncommitted changes"))
	}
	if code := a.run(root, p.GitDir, "fetch", "origin", remoteBranch(p, p.Environment)); code != 0 {
		return code
	}
	target := "FETCH_HEAD"
	removed, err := a.checkoutPrepare(root, p, target)
	if err != nil {
		return a.fail(err)
	}
	x := append([]string{"merge", "--ff-only", target}, args...)
	if code := a.run(root, p.GitDir, x...); code != 0 {
		restorePrepared(root, p, removed)
		return code
	}
	if err := a.mixedMaterialize(root, p); err != nil {
		return a.fail(err)
	}
	_ = os.RemoveAll(filepath.Join(p.GitDir, "transition"))
	return 0
}
