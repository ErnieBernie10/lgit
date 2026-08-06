package lgit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

const ageFormat = "lgit-age-v1"

type ageFormatFile struct {
	Version    int    `json:"version"`
	Encryption string `json:"encryption"`
}

func (a App) identityPath() (string, error) {
	d, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "age-identity.txt"), nil
}

func (a App) ensureIdentity() (*age.X25519Identity, error) {
	p, err := a.identityPath()
	if err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(p); err == nil {
		return age.ParseX25519Identity(strings.TrimSpace(string(b)))
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(id.String()+"\n"), 0600); err != nil {
		return nil, err
	}
	return id, nil
}

func (a App) loadIdentity() (*age.X25519Identity, error) {
	p, err := a.identityPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("age identity not found; run 'lgit key import FILE' or 'lgit key generate'")
	}
	return age.ParseX25519Identity(strings.TrimSpace(string(b)))
}

func (a App) key(root string, args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit key generate|show|export FILE|import FILE"))
	}
	switch args[0] {
	case "generate":
		id, err := a.ensureIdentity()
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprintln(a.Stdout, id.Recipient().String())
		return 0
	case "show":
		id, err := a.loadIdentity()
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprintln(a.Stdout, id.Recipient().String())
		return 0
	case "export":
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit key export FILE"))
		}
		p, err := a.identityPath()
		if err != nil {
			return a.fail(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return a.fail(err)
		}
		if err := os.WriteFile(args[1], b, 0600); err != nil {
			return a.fail(err)
		}
		fmt.Fprintln(a.Stdout, args[1])
		return 0
	case "import":
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit key import FILE"))
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return a.fail(err)
		}
		if _, err := age.ParseX25519Identity(strings.TrimSpace(string(b))); err != nil {
			return a.fail(err)
		}
		p, err := a.identityPath()
		if err != nil {
			return a.fail(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return a.fail(err)
		}
		if err := os.WriteFile(p, []byte(strings.TrimSpace(string(b))+"\n"), 0600); err != nil {
			return a.fail(err)
		}
		fmt.Fprintln(a.Stdout, "identity imported")
		return 0
	default:
		return a.fail(fmt.Errorf("usage: lgit key generate|show|export FILE|import FILE"))
	}
}

func (a App) initEncryption(root string, p Project) error {
	id, err := a.ensureIdentity()
	if err != nil {
		return err
	}
	meta := filepath.Join(root, ".lgit")
	if err := os.MkdirAll(filepath.Join(meta, "store"), 0700); err != nil {
		return err
	}
	f, _ := json.MarshalIndent(ageFormatFile{Version: 1, Encryption: ageFormat}, "", "  ")
	if err := os.WriteFile(filepath.Join(meta, "format.json"), append(f, '\n'), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(meta, "recipients.txt"), []byte(id.Recipient().String()+"\n"), 0600); err != nil {
		return err
	}
	if err := excludeLgit(root); err != nil {
		return err
	}
	if c := a.run(root, p.GitDir, "add", "--force", ".lgit/format.json", ".lgit/recipients.txt"); c != 0 {
		return fmt.Errorf("failed to stage encryption metadata")
	}
	return nil
}

func excludeLgit(root string) error {
	p := filepath.Join(root, ".git", "info", "exclude")
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "\n.lgit/\n") || strings.HasSuffix(string(b), ".lgit/\n") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, "\n.lgit/\n")
	return err
}

func readRecipients(root string) ([]age.Recipient, error) {
	b, err := os.ReadFile(filepath.Join(root, ".lgit", "recipients.txt"))
	if err != nil {
		return nil, err
	}
	var rs []age.Recipient
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := age.ParseX25519Recipient(line)
		if err != nil {
			return nil, err
		}
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("no age recipients configured")
	}
	return rs, nil
}

func encryptBytes(plain []byte, rs []age.Recipient) ([]byte, error) {
	var out bytes.Buffer
	w, err := age.Encrypt(&out, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func decryptBytes(cipher []byte, id age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(cipher), id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func storePath(path string) string {
	return filepath.Join(".lgit", "store", filepath.FromSlash(path)+".age")
}
func plainPath(store string) string {
	x := filepath.ToSlash(store)
	x = strings.TrimPrefix(x, ".lgit/store/")
	return strings.TrimSuffix(x, ".age")
}

func (a App) ageAdd(root string, args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit add PATH..."))
	}
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	rs, err := readRecipients(root)
	if err != nil {
		return a.fail(err)
	}
	for _, raw := range args {
		rel, err := filepath.Rel(root, filepath.Join(root, raw))
		if err != nil || strings.HasPrefix(rel, "..") {
			return a.fail(fmt.Errorf("path outside project: %s", raw))
		}
		rel = filepath.ToSlash(rel)
		if rel == ".lgit" || strings.HasPrefix(rel, ".lgit/") {
			return a.fail(fmt.Errorf("cannot add lgit metadata directly"))
		}
		if len(mainTracked(root, []string{rel})) > 0 {
			return a.fail(fmt.Errorf("main repository already tracks %s", rel))
		}
		plain, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		sp := storePath(rel)
		if os.IsNotExist(err) {
			_ = os.Remove(filepath.Join(root, sp))
			if c := a.run(root, p.GitDir, "rm", "--cached", "--ignore-unmatch", "--", filepath.ToSlash(sp)); c != 0 {
				return c
			}
			continue
		}
		if err != nil {
			return a.fail(err)
		}
		cipher, err := encryptBytes(plain, rs)
		if err != nil {
			return a.fail(err)
		}
		dst := filepath.Join(root, sp)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return a.fail(err)
		}
		if err := os.WriteFile(dst, cipher, 0600); err != nil {
			return a.fail(err)
		}
		if c := a.run(root, p.GitDir, "add", "--force", "--", filepath.ToSlash(sp)); c != 0 {
			return c
		}
	}
	return 0
}

func trackedStore(root string, p Project, ref string) ([]string, error) {
	out, err := gitOutput(root, p.GitDir, "ls-tree", "-r", "--name-only", ref, ".lgit/store")
	if err != nil {
		return nil, err
	}
	var xs []string
	for _, x := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasSuffix(x, ".age") {
			xs = append(xs, x)
		}
	}
	sort.Strings(xs)
	return xs, nil
}

func (a App) materialize(root string, p Project) error {
	id, err := a.loadIdentity()
	if err != nil {
		return err
	}
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return err
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
		if err := os.WriteFile(dst, plain, 0600); err != nil {
			return err
		}
	}
	for rel := range old {
		if !now[rel] {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
		}
	}
	var lines []string
	for rel := range now {
		lines = append(lines, rel)
	}
	sort.Strings(lines)
	return os.WriteFile(oldFile, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func (a App) plainConflicts(root string, p Project, ref string) ([]string, error) {
	id, err := a.loadIdentity()
	if err != nil {
		return nil, err
	}
	stores, err := trackedStore(root, p, ref)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sp := range stores {
		rel := plainPath(sp)
		local, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		cipher, err := gitBlob(root, p.GitDir, ref+":"+sp)
		if err != nil {
			return nil, err
		}
		remote, err := decryptBytes(cipher, id)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(local, remote) {
			out = append(out, rel)
		}
	}
	return out, nil
}

func (a App) attach(root string, args []string) int {
	o, err := parseAttach(args)
	if err != nil {
		return a.fail(err)
	}
	d, rp, err := a.paths()
	if err != nil {
		return a.fail(err)
	}
	r, err := LoadRegistry(rp)
	if err != nil {
		return a.fail(err)
	}
	if _, ok := r.Projects[root]; ok {
		return a.fail(fmt.Errorf("project is already initialized"))
	}
	if r.Remote == "" {
		return a.fail(fmt.Errorf("shared remote is not configured; run 'lgit remote set URL'"))
	}
	base := slugify(filepath.Base(root))
	matches, err := discover(r.Remote, base, o.env)
	if err != nil {
		return a.fail(err)
	}
	key, err := resolveProject(matches, o.project)
	if err != nil {
		return a.fail(err)
	}
	idpart := strings.TrimPrefix(key, base+"-")
	if idpart == key || idpart == "" {
		idpart, _ = newID()
	}
	p := Project{ID: idpart, Slug: key, Environment: o.env, GitDir: filepath.Join(d, "repos", idpart+"-"+fmt.Sprint(time.Now().UnixNano()))}
	if err := os.MkdirAll(p.GitDir, 0700); err != nil {
		return a.fail(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.GitDir)
			_ = os.RemoveAll(filepath.Join(root, ".lgit"))
		}
	}()
	if c := a.exec(root, "git", "init", "--bare", p.GitDir); c != 0 {
		return c
	}
	copyIdentity(root, a, p)
	if c := a.configureRemote(root, p, r.Remote); c != 0 {
		return c
	}
	if c := a.run(root, p.GitDir, "fetch", "origin"); c != 0 {
		return c
	}
	ref := "refs/remotes/origin/envs/" + o.env
	if c := a.run(root, p.GitDir, "checkout", "-b", "env/"+o.env, ref); c != 0 {
		return c
	}
	if err := excludeLgit(root); err != nil {
		return a.fail(err)
	}
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return a.fail(err)
	}
	var plains []string
	for _, sp := range stores {
		plains = append(plains, plainPath(sp))
	}
	if owned := mainTracked(root, plains); len(owned) > 0 {
		return a.fail(fmt.Errorf("main repository already tracks: %s", strings.Join(owned, ", ")))
	}
	conflicts, err := a.plainConflicts(root, p, "HEAD")
	if err != nil {
		return a.fail(err)
	}
	if len(conflicts) > 0 && !o.keepLocal && !o.useRemote {
		return a.fail(fmt.Errorf("local files differ from remote: %s; use --keep-local or --use-remote", strings.Join(conflicts, ", ")))
	}
	saved := map[string][]byte{}
	for _, x := range conflicts {
		b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(x)))
		saved[x] = b
	}
	if o.useRemote && len(conflicts) > 0 {
		if err := backupFiles(d, p.ID, root, conflicts); err != nil {
			return a.fail(err)
		}
	}
	if err := a.materialize(root, p); err != nil {
		return a.fail(err)
	}
	if o.keepLocal {
		for x, b := range saved {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(x)), b, 0600); err != nil {
				return a.fail(err)
			}
		}
	}
	_ = a.run(root, p.GitDir, "config", "status.showUntrackedFiles", "no")
	r.Projects[root] = p
	if err := SaveRegistry(rp, r); err != nil {
		return a.fail(err)
	}
	cleanup = false
	fmt.Fprintf(a.Stdout, "attached %s environment %s\n", key, o.env)
	return 0
}

func (a App) encryptionClean(root string, p Project) bool {
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return false
	}
	id, err := a.loadIdentity()
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
	out, err := gitOutput(root, p.GitDir, "status", "--porcelain", "--", ".lgit")
	return err == nil && strings.TrimSpace(out) == ""
}
func (a App) clean(root string, p Project) bool { return a.encryptionClean(root, p) }

func (a App) ageStatus(root string, args []string) int {
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	stores, err := trackedStore(root, p, "HEAD")
	if err != nil {
		return a.fail(err)
	}
	id, err := a.loadIdentity()
	if err != nil {
		return a.fail(err)
	}
	staged, _ := gitOutput(root, p.GitDir, "diff", "--cached", "--name-status", "--", ".lgit/store")
	for _, line := range strings.Split(strings.TrimSpace(staged), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 {
			fmt.Fprintf(a.Stdout, "%s  %s\n", f[0], plainPath(f[len(f)-1]))
		}
	}
	for _, sp := range stores {
		cipher, _ := gitBlob(root, p.GitDir, "HEAD:"+sp)
		want, _ := decryptBytes(cipher, id)
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(plainPath(sp))))
		if err != nil {
			fmt.Fprintf(a.Stdout, " D %s\n", plainPath(sp))
		} else if !bytes.Equal(got, want) {
			fmt.Fprintf(a.Stdout, " M %s\n", plainPath(sp))
		}
	}
	return 0
}

func (a App) ageRestore(root string, args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit restore PATH..."))
	}
	p, err := a.lookup(root)
	if err != nil {
		return a.fail(err)
	}
	id, err := a.loadIdentity()
	if err != nil {
		return a.fail(err)
	}
	for _, rel := range args {
		sp := filepath.ToSlash(storePath(filepath.ToSlash(rel)))
		cipher, err := gitBlob(root, p.GitDir, "HEAD:"+sp)
		if err != nil {
			return a.fail(err)
		}
		plain, err := decryptBytes(cipher, id)
		if err != nil {
			return a.fail(err)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return a.fail(err)
		}
		if err := os.WriteFile(dst, plain, 0600); err != nil {
			return a.fail(err)
		}
	}
	return 0
}

func (a App) envSwitch(root string, p Project, name string) int {
	name, e := validateName(name)
	if e != nil {
		return a.fail(e)
	}
	if name == p.Environment {
		return 0
	}
	if !a.clean(root, p) {
		return a.fail(fmt.Errorf("cannot switch environment: uncommitted changes"))
	}
	if _, e := gitOutput(root, p.GitDir, "rev-parse", "--verify", "refs/heads/env/"+name); e != nil {
		_ = a.run(root, p.GitDir, "fetch", "origin")
		if c := a.run(root, p.GitDir, "switch", "-c", "env/"+name, "refs/remotes/origin/envs/"+name); c != 0 {
			return c
		}
	} else if c := a.run(root, p.GitDir, "checkout", "env/"+name); c != 0 {
		return c
	}
	if err := a.materialize(root, p); err != nil {
		return a.fail(err)
	}
	return a.setEnvironment(root, p, name)
}

func (a App) pull(root string, args []string) int {
	p, e := a.lookup(root)
	if e != nil {
		return a.fail(e)
	}
	if !a.clean(root, p) {
		return a.fail(fmt.Errorf("cannot pull: uncommitted changes"))
	}
	x := append([]string{"pull", "--ff-only", "origin", remoteBranch(p, p.Environment)}, args...)
	if c := a.run(root, p.GitDir, x...); c != 0 {
		return c
	}
	if err := a.materialize(root, p); err != nil {
		return a.fail(err)
	}
	return 0
}

func runNoOutput(dir string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, b)
	}
	return nil
}
