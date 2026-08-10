package lgit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type App struct{ Stdout, Stderr io.Writer }

func (a App) Run(cwd string, args []string) int {
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
		return a.help()
	}
	switch args[0] {
	case "help", "--help", "-h":
		return a.help()
	case "version", "--version", "-v":
		fmt.Fprintln(a.Stdout, "lgit dev")
		return 0
	case "data-dir":
		d, e := DataDir()
		if e != nil {
			return a.fail(e)
		}
		fmt.Fprintln(a.Stdout, d)
		return 0
	case "list":
		return a.list()
	case "key":
		root, _ := canonicalPath(cwd)
		return a.key(root, args[1:])
	case "init":
		return a.initV2(cwd, explicit, args[1:])
	}
	allowUnregistered := args[0] == "attach" || (args[0] == "remote" && len(args) > 1 && args[1] == "set")
	root, err := a.resolveRoot(cwd, explicit, allowUnregistered)
	if err != nil {
		return a.fail(err)
	}
	switch args[0] {
	case "attach":
		return a.attachMixed(root, args[1:])
	case "remove":
		return a.remove(root)
	case "env":
		return a.env(root, args[1:])
	case "storage":
		return a.storageCommand(root, args[1:])
	case "remote":
		if len(args) > 1 && args[1] == "set" {
			return a.remoteSet(root, args[2:])
		}
	case "push":
		return a.push(root, args[1:])
	case "pull":
		return a.pullMixed(root, args[1:])
	case "add":
		return a.mixedAdd(root, args[1:])
	case "status":
		return a.mixedStatus(root, args[1:])
	case "diff":
		return a.mixedDiff(root, args[1:])
	case "restore":
		return a.mixedRestore(root, args[1:])
	case "git":
		if len(args) == 1 {
			return a.fail(fmt.Errorf("usage: lgit git <git command>"))
		}
		return a.delegate(root, args[1:])
	}
	return a.delegate(root, args)
}

func (a App) help() int {
	fmt.Fprintln(a.Stdout, `lgit - Git-backed storage for local project files and dotfiles

Usage:
  lgit init [--root PATH] [--env NAME] [--new-project] [--default plain|age] [--encryption identity|password]
  lgit [--root PATH] attach --env NAME [--project KEY] [--keep-local|--use-remote]
  lgit storage show PATH | set PATH plain|age | unset PATH | default [plain|age]
  lgit remote set URL
  lgit env current|branch|list|create NAME|switch NAME
  lgit key generate|show|export FILE|import FILE
  lgit add PATH... | lgit status | lgit diff [PATH...] | lgit restore PATH...
  lgit push | lgit pull
  lgit git <raw git command>
  lgit <git command>`)
	return 0
}

func (a App) paths() (string, string, error) {
	d, e := DataDir()
	return d, filepath.Join(d, "projects.json"), e
}
func (a App) registry() (Registry, error) {
	_, p, e := a.paths()
	if e != nil {
		return Registry{}, e
	}
	return LoadRegistry(p)
}

func (a App) init(root string, args []string) int {
	env, newProject, encryption, err := parseInit(args)
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
	if p, ok := r.Projects[root]; ok {
		fmt.Fprintf(a.Stdout, "already initialized: %s (%s)\n", p.ID, p.Environment)
		return 0
	}
	base := slugify(filepath.Base(root))
	if base == "" {
		return a.fail(fmt.Errorf("repository folder name cannot be converted to a project slug"))
	}
	if r.Remote != "" && !newProject {
		matches, err := discover(r.Remote, base, "")
		if err != nil {
			return a.fail(err)
		}
		if len(uniqueProjects(matches)) > 0 {
			return a.fail(fmt.Errorf("remote project %q already exists; use 'lgit attach --env %s' or 'lgit init --new-project --env %s'", base, env, env))
		}
	}
	id, err := newID()
	if err != nil {
		return a.fail(err)
	}
	p := Project{ID: id, Slug: base + "-" + id[:8], Environment: env, GitDir: filepath.Join(d, "repos", id)}
	if err := os.MkdirAll(p.GitDir, 0700); err != nil {
		return a.fail(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.GitDir)
		}
	}()
	if c := a.exec(root, "git", "init", "--bare", p.GitDir); c != 0 {
		return c
	}
	if c := a.run(root, p.GitDir, "symbolic-ref", "HEAD", "refs/heads/env/"+env); c != 0 {
		return c
	}
	_ = a.run(root, p.GitDir, "config", "status.showUntrackedFiles", "no")
	copyIdentity(root, a, p)
	if r.Remote != "" {
		if c := a.configureRemote(root, p, r.Remote); c != 0 {
			return c
		}
	}
	if err := a.initEncryption(root, p, encryption); err != nil {
		return a.fail(err)
	}
	r.Projects[root] = p
	if err := SaveRegistry(rp, r); err != nil {
		return a.fail(err)
	}
	cleanup = false
	fmt.Fprintf(a.Stdout, "initialized %s environment %s\n", p.Slug, env)
	return 0
}

func parseInit(args []string) (string, bool, string, error) {
	env := ""
	newProject := false
	encryption := "identity"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--env":
			i++
			if i >= len(args) {
				return "", false, "", fmt.Errorf("--env requires a name")
			}
			env = args[i]
		case "--new-project":
			newProject = true
		case "--encryption":
			i++
			if i >= len(args) {
				return "", false, "", fmt.Errorf("--encryption requires identity or password")
			}
			encryption = strings.ToLower(args[i])
			if encryption != "identity" && encryption != "password" {
				return "", false, "", fmt.Errorf("--encryption must be identity or password")
			}
		default:
			return "", false, "", fmt.Errorf("usage: lgit init [--env NAME] [--new-project] [--encryption identity|password]")
		}
	}
	if env == "" {
		h, e := os.Hostname()
		if e != nil {
			return "", false, "", e
		}
		env = h
	}
	env, e := validateName(env)
	return env, newProject, encryption, e
}

type attachOptions struct {
	env, project         string
	keepLocal, useRemote bool
}

func parseAttach(args []string) (attachOptions, error) {
	var o attachOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--env":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--env requires a name")
			}
			o.env = args[i]
		case "--project":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--project requires a key")
			}
			o.project = args[i]
		case "--keep-local":
			o.keepLocal = true
		case "--use-remote":
			o.useRemote = true
		default:
			return o, fmt.Errorf("unknown attach option %q", args[i])
		}
	}
	if o.env == "" {
		return o, fmt.Errorf("usage: lgit attach --env NAME [--project KEY] [--keep-local|--use-remote]")
	}
	if o.keepLocal && o.useRemote {
		return o, fmt.Errorf("--keep-local and --use-remote are mutually exclusive")
	}
	var e error
	o.env, e = validateName(o.env)
	return o, e
}

func (a App) attachLegacy(root string, args []string) int {
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
	if base == "" {
		return a.fail(fmt.Errorf("invalid repository folder name"))
	}
	matches, err := discover(r.Remote, base, o.env)
	if err != nil {
		return a.fail(err)
	}
	key, err := resolveProject(matches, o.project)
	if err != nil {
		return a.fail(err)
	}
	id := strings.TrimPrefix(key, base+"-")
	if id == key || id == "" {
		id, err = newID()
		if err != nil {
			return a.fail(err)
		}
	}
	p := Project{ID: id, Slug: key, Environment: o.env, GitDir: filepath.Join(d, "repos", id+"-"+fmt.Sprint(time.Now().UnixNano()))}
	if err := os.MkdirAll(p.GitDir, 0700); err != nil {
		return a.fail(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.GitDir)
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
	paths, err := trackedPaths(root, p.GitDir, ref)
	if err != nil {
		return a.fail(err)
	}
	if owned := mainTracked(root, paths); len(owned) > 0 {
		return a.fail(fmt.Errorf("main repository already tracks: %s", strings.Join(owned, ", ")))
	}
	conflicts, err := differentExisting(root, p.GitDir, ref, paths)
	if err != nil {
		return a.fail(err)
	}
	if len(conflicts) > 0 && !o.keepLocal && !o.useRemote {
		return a.fail(fmt.Errorf("local files differ from remote: %s; use --keep-local or --use-remote", strings.Join(conflicts, ", ")))
	}
	saved := map[string][]byte{}
	for _, path := range conflicts {
		b, _ := os.ReadFile(filepath.Join(root, path))
		saved[path] = b
	}
	if o.useRemote && len(conflicts) > 0 {
		if err := backupFiles(d, p.ID, root, conflicts); err != nil {
			return a.fail(err)
		}
	}
	if c := a.run(root, p.GitDir, "checkout", "-b", "env/"+o.env, ref); c != 0 {
		return c
	}
	if o.keepLocal {
		for path, b := range saved {
			if err := os.WriteFile(filepath.Join(root, path), b, 0600); err != nil {
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

func discover(remote, slug, env string) ([]string, error) {
	pattern := "refs/heads/projects/" + slug + "-*/envs/*"
	if env != "" {
		pattern = "refs/heads/projects/" + slug + "-*/envs/" + env
	}
	c := exec.Command("git", "ls-remote", "--heads", remote, pattern)
	b, e := c.CombinedOutput()
	if e != nil {
		return nil, fmt.Errorf("git ls-remote: %v: %s", e, b)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ref := strings.TrimPrefix(f[1], "refs/heads/projects/")
		parts := strings.Split(ref, "/envs/")
		if len(parts) == 2 {
			out = append(out, parts[0]+"\t"+parts[1])
		}
	}
	return out, nil
}
func uniqueProjects(matches []string) []string {
	m := map[string]bool{}
	for _, x := range matches {
		m[strings.SplitN(x, "\t", 2)[0]] = true
	}
	var r []string
	for x := range m {
		r = append(r, x)
	}
	sort.Strings(r)
	return r
}
func resolveProject(matches []string, explicit string) (string, error) {
	ps := uniqueProjects(matches)
	if explicit != "" {
		for _, p := range ps {
			if p == explicit {
				return p, nil
			}
		}
		return "", fmt.Errorf("project %q with requested environment was not found", explicit)
	}
	if len(ps) == 0 {
		return "", fmt.Errorf("no matching lgit project/environment found")
	}
	if len(ps) > 1 {
		return "", fmt.Errorf("multiple projects match: %s; use --project", strings.Join(ps, ", "))
	}
	return ps[0], nil
}

func trackedPaths(root, gd, ref string) ([]string, error) {
	out, e := gitOutput(root, gd, "ls-tree", "-r", "--name-only", ref)
	if e != nil {
		return nil, e
	}
	var p []string
	for _, x := range strings.Split(strings.TrimSpace(out), "\n") {
		if x != "" {
			p = append(p, x)
		}
	}
	return p, nil
}
func mainTracked(root string, paths []string) []string {
	var out []string
	for _, p := range paths {
		c := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", p)
		if c.Run() == nil {
			out = append(out, p)
		}
	}
	return out
}
func differentExisting(root, gd, ref string, paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		local := filepath.Join(root, p)
		b, e := os.ReadFile(local)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		remote, e := gitBlob(root, gd, ref+":"+filepath.ToSlash(p))
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(b, remote) {
			out = append(out, p)
		}
	}
	return out, nil
}
func gitBlob(root, gd, spec string) ([]byte, error) {
	c := exec.Command("git", "--git-dir="+gd, "--work-tree="+root, "show", spec)
	c.Dir = root
	b, e := c.CombinedOutput()
	if e != nil {
		return nil, fmt.Errorf("git show %s: %v: %s", spec, e, b)
	}
	return b, nil
}
func backupFiles(data, id, root string, paths []string) error {
	dir := filepath.Join(data, "backups", id, time.Now().UTC().Format("20060102T150405Z"))
	for _, p := range paths {
		dst := filepath.Join(dir, p)
		if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		b, e := os.ReadFile(filepath.Join(root, p))
		if e != nil {
			return e
		}
		if e = os.WriteFile(dst, b, 0600); e != nil {
			return e
		}
	}
	return nil
}

func (a App) env(root string, args []string) int {
	p, e := a.lookup(root)
	if e != nil {
		return a.fail(e)
	}
	if len(args) == 0 {
		return a.fail(fmt.Errorf("usage: lgit env current|branch|list|create|switch"))
	}
	switch args[0] {
	case "current":
		fmt.Fprintln(a.Stdout, p.Environment)
		return 0
	case "branch":
		fmt.Fprintln(a.Stdout, remoteBranch(p, p.Environment))
		return 0
	case "list":
		return a.envList(root, p)
	case "create":
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit env create NAME"))
		}
		return a.envCreate(root, p, args[1])
	case "switch":
		if len(args) != 2 {
			return a.fail(fmt.Errorf("usage: lgit env switch NAME"))
		}
		return a.envSwitchMixed(root, p, args[1])
	}
	return a.fail(fmt.Errorf("unknown env command"))
}
func (a App) envList(root string, p Project) int {
	_ = a.run(root, p.GitDir, "fetch", "origin")
	out, _ := gitOutput(root, p.GitDir, "for-each-ref", "--format=%(refname:short)", "refs/heads/env", "refs/remotes/origin/envs")
	seen := map[string]bool{}
	for _, x := range strings.Fields(out) {
		n := strings.TrimPrefix(strings.TrimPrefix(x, "env/"), "origin/envs/")
		seen[n] = true
	}
	var xs []string
	for x := range seen {
		xs = append(xs, x)
	}
	sort.Strings(xs)
	for _, x := range xs {
		m := " "
		if x == p.Environment {
			m = "*"
		}
		fmt.Fprintf(a.Stdout, "%s %s\n", m, x)
	}
	return 0
}
func (a App) envCreate(root string, p Project, name string) int {
	name, e := validateName(name)
	if e != nil {
		return a.fail(e)
	}
	if !a.clean(root, p) {
		return a.fail(fmt.Errorf("cannot create environment: uncommitted changes"))
	}
	if c := a.run(root, p.GitDir, "switch", "-c", "env/"+name); c != 0 {
		return c
	}
	return a.setEnvironment(root, p, name)
}
func (a App) envSwitchLegacy(root string, p Project, name string) int {
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
	} else if c := a.run(root, p.GitDir, "checkout", "--no-overwrite-ignore", "env/"+name); c != 0 {
		return c
	}
	return a.setEnvironment(root, p, name)
}
func (a App) cleanLegacy(root string, p Project) bool {
	o, e := gitOutput(root, p.GitDir, "status", "--porcelain")
	return e == nil && strings.TrimSpace(o) == ""
}
func (a App) setEnvironment(root string, p Project, name string) int {
	_, rp, e := a.paths()
	if e != nil {
		return a.fail(e)
	}
	r, e := LoadRegistry(rp)
	if e != nil {
		return a.fail(e)
	}
	p.Environment = name
	r.Projects[root] = p
	if e = SaveRegistry(rp, r); e != nil {
		return a.fail(e)
	}
	fmt.Fprintf(a.Stdout, "environment %s\n", name)
	return 0
}
func remoteBranch(p Project, env string) string { return "projects/" + p.Slug + "/envs/" + env }
func (a App) remoteSet(root string, args []string) int {
	if len(args) != 1 {
		return a.fail(fmt.Errorf("usage: lgit remote set URL"))
	}
	_, rp, e := a.paths()
	if e != nil {
		return a.fail(e)
	}
	r, e := LoadRegistry(rp)
	if e != nil {
		return a.fail(e)
	}
	r.Remote = args[0]
	if e = SaveRegistry(rp, r); e != nil {
		return a.fail(e)
	}
	if p, ok := r.Projects[root]; ok {
		return a.configureRemote(root, p, args[0])
	}
	fmt.Fprintln(a.Stdout, "shared remote configured")
	return 0
}

func (a App) configureRemote(root string, p Project, url string) int {
	_ = a.run(root, p.GitDir, "config", "core.autocrlf", "false")
	if _, e := gitOutput(root, p.GitDir, "remote", "get-url", "origin"); e == nil {
		if c := a.run(root, p.GitDir, "remote", "set-url", "origin", url); c != 0 {
			return c
		}
	} else if c := a.run(root, p.GitDir, "remote", "add", "origin", url); c != 0 {
		return c
	}
	spec := "+refs/heads/projects/" + p.Slug + "/envs/*:refs/remotes/origin/envs/*"
	if c := a.run(root, p.GitDir, "config", "remote.origin.fetch", spec); c != 0 {
		return c
	}
	return a.run(root, p.GitDir, "config", "remote.origin.tagOpt", "--no-tags")
}
func (a App) push(root string, args []string) int {
	p, e := a.lookup(root)
	if e != nil {
		return a.fail(e)
	}
	r, e := a.registry()
	if e != nil {
		return a.fail(e)
	}
	if r.Remote == "" {
		return a.fail(fmt.Errorf("shared remote is not configured"))
	}
	x := append([]string{"push", "-u", "origin", "HEAD:refs/heads/" + remoteBranch(p, p.Environment)}, args...)
	return a.run(root, p.GitDir, x...)
}
func (a App) pullLegacy(root string, args []string) int {
	p, e := a.lookup(root)
	if e != nil {
		return a.fail(e)
	}
	x := append([]string{"pull", "origin", remoteBranch(p, p.Environment)}, args...)
	return a.run(root, p.GitDir, x...)
}
func (a App) delegate(root string, args []string) int {
	p, e := a.lookup(root)
	if e != nil {
		return a.fail(e)
	}
	return a.run(root, p.GitDir, args...)
}
func (a App) run(root, gd string, args ...string) int {
	x := append([]string{"--git-dir=" + gd, "--work-tree=" + root}, args...)
	return a.exec(root, "git", x...)
}
func (a App) exec(root, name string, args ...string) int {
	c := exec.Command(name, args...)
	c.Dir = root
	c.Stdin = os.Stdin
	c.Stdout = a.Stdout
	c.Stderr = a.Stderr
	if e := c.Run(); e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			return x.ExitCode()
		}
		fmt.Fprintln(a.Stderr, "lgit:", e)
		return 1
	}
	return 0
}
func gitOutput(root, gd string, args ...string) (string, error) {
	x := append([]string{"--git-dir=" + gd, "--work-tree=" + root}, args...)
	c := exec.Command("git", x...)
	c.Dir = root
	b, e := c.CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), e, b)
	}
	return string(b), nil
}
func (a App) lookup(root string) (Project, error) {
	r, e := a.registry()
	if e != nil {
		return Project{}, e
	}
	if p, ok := r.Projects[root]; ok {
		return p, nil
	}
	for candidate, p := range r.Projects {
		if pathKey(candidate) == pathKey(root) {
			return p, nil
		}
	}
	return Project{}, fmt.Errorf("project is not initialized; run 'lgit init' or 'lgit attach --env NAME'")
}
func (a App) list() int {
	r, e := a.registry()
	if e != nil {
		return a.fail(e)
	}
	var roots []string
	for x := range r.Projects {
		roots = append(roots, x)
	}
	sort.Strings(roots)
	for _, x := range roots {
		p := r.Projects[x]
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\n", p.Slug, p.Environment, x)
	}
	return 0
}
func (a App) remove(root string) int {
	_, rp, e := a.paths()
	if e != nil {
		return a.fail(e)
	}
	r, e := LoadRegistry(rp)
	if e != nil {
		return a.fail(e)
	}
	p, ok := r.Projects[root]
	if !ok {
		return a.fail(fmt.Errorf("project is not initialized"))
	}
	delete(r.Projects, root)
	if e = SaveRegistry(rp, r); e != nil {
		return a.fail(e)
	}
	fmt.Fprintf(a.Stdout, "unregistered %s; data preserved at %s\n", p.Slug, p.GitDir)
	return 0
}
func (a App) fail(e error) int { fmt.Fprintln(a.Stderr, "lgit:", e); return 1 }
func hasForceFlag(args []string) bool {
	for _, x := range args {
		if x == "-f" || x == "--force" {
			return true
		}
	}
	return false
}
func copyIdentity(root string, a App, p Project) {
	for _, k := range []string{"user.name", "user.email"} {
		if v, ok := mainGitConfig(root, k); ok {
			_ = a.run(root, p.GitDir, "config", k, v)
		}
	}
}
func mainGitConfig(root, key string) (string, bool) {
	c := exec.Command("git", "-C", root, "config", "--get", key)
	b, e := c.Output()
	return strings.TrimSpace(string(b)), e == nil
}
func gitRoot(cwd string) (string, error) {
	c := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	b, e := c.CombinedOutput()
	if e != nil {
		return "", fmt.Errorf("not inside a Git work tree: %s", strings.TrimSpace(string(b)))
	}
	return filepath.Clean(strings.TrimSpace(string(b))), nil
}

var invalidSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Trim(invalidSlug.ReplaceAllString(s, "-"), "-")
}
func validateName(s string) (string, error) {
	s = slugify(s)
	if s == "" {
		return "", fmt.Errorf("invalid environment name")
	}
	return s, nil
}

func parseInitV2(args []string) (env string, newProject bool, encryption string, backend StorageBackend, rootOverride string, err error) {
	encryption = "identity"
	backend = StorageAge
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--env":
			i++
			if i >= len(args) {
				err = fmt.Errorf("--env requires a name")
				return
			}
			env = args[i]
		case "--new-project":
			newProject = true
		case "--encryption":
			i++
			if i >= len(args) {
				err = fmt.Errorf("--encryption requires identity or password")
				return
			}
			encryption = strings.ToLower(args[i])
			if encryption != "identity" && encryption != "password" {
				err = fmt.Errorf("--encryption must be identity or password")
				return
			}
		case "--default":
			i++
			if i >= len(args) {
				err = fmt.Errorf("--default requires plain or age")
				return
			}
			backend = StorageBackend(strings.ToLower(args[i]))
			if !validBackend(backend) {
				err = fmt.Errorf("unsupported storage backend %q", backend)
				return
			}
		case "--root":
			i++
			if i >= len(args) {
				err = fmt.Errorf("--root requires a path")
				return
			}
			rootOverride = args[i]
		default:
			err = fmt.Errorf("usage: lgit init [--root PATH] [--env NAME] [--new-project] [--default plain|age] [--encryption identity|password]")
			return
		}
	}
	if env == "" {
		h, e := os.Hostname()
		if e != nil {
			err = e
			return
		}
		env = h
	}
	env, err = validateName(env)
	return
}

func (a App) initV2(cwd, explicit string, args []string) int {
	env, newProject, encryption, backend, rootArg, err := parseInitV2(args)
	if err != nil {
		return a.fail(err)
	}
	if explicit != "" && rootArg != "" {
		return a.fail(fmt.Errorf("specify --root only once"))
	}
	if rootArg != "" {
		explicit = rootArg
	}
	var root string
	standalone := explicit != ""
	if explicit != "" {
		root, err = canonicalPath(explicit)
	} else {
		root, err = gitRoot(cwd)
	}
	if err != nil {
		return a.fail(err)
	}
	if !standalone {
		standalone = !isGitWorkTreeRoot(root)
	}
	d, rp, err := a.paths()
	if err != nil {
		return a.fail(err)
	}
	r, err := LoadRegistry(rp)
	if err != nil {
		return a.fail(err)
	}
	for candidate, p := range r.Projects {
		if pathKey(candidate) == pathKey(root) {
			fmt.Fprintf(a.Stdout, "already initialized: %s (%s)\n", p.ID, p.Environment)
			return 0
		}
	}
	base := slugify(filepath.Base(root))
	if base == "" {
		return a.fail(fmt.Errorf("root folder name cannot be converted to a project slug"))
	}
	if r.Remote != "" && !newProject {
		matches, err := discover(r.Remote, base, "")
		if err != nil {
			return a.fail(err)
		}
		if len(uniqueProjects(matches)) > 0 {
			return a.fail(fmt.Errorf("remote project %q already exists; attach it or use --new-project", base))
		}
	}
	id, err := newID()
	if err != nil {
		return a.fail(err)
	}
	p := Project{ID: id, Slug: base + "-" + id[:8], Environment: env, GitDir: filepath.Join(d, "repos", id), Standalone: standalone}
	if err := os.MkdirAll(p.GitDir, 0700); err != nil {
		return a.fail(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.GitDir)
		}
	}()
	if code := a.exec(root, "git", "init", "--bare", p.GitDir); code != 0 {
		return code
	}
	if code := a.run(root, p.GitDir, "symbolic-ref", "HEAD", "refs/heads/env/"+env); code != 0 {
		return code
	}
	_ = a.run(root, p.GitDir, "config", "status.showUntrackedFiles", "no")
	_ = a.run(root, p.GitDir, "config", "core.autocrlf", "false")
	copyIdentity(root, a, p)
	if r.Remote != "" {
		if code := a.configureRemote(root, p, r.Remote); code != 0 {
			return code
		}
	}
	if err := a.initStorage(root, p, backend, encryption); err != nil {
		return a.fail(err)
	}
	r.Projects[root] = p
	if err := SaveRegistry(rp, r); err != nil {
		return a.fail(err)
	}
	cleanup = false
	fmt.Fprintf(a.Stdout, "initialized %s environment %s (%s default)\n", p.Slug, env, backend)
	return 0
}

func (a App) attachMixed(root string, args []string) int {
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
	for candidate := range r.Projects {
		if pathKey(candidate) == pathKey(root) {
			return a.fail(fmt.Errorf("project is already initialized"))
		}
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
	p := Project{ID: idpart, Slug: key, Environment: o.env, GitDir: filepath.Join(d, "repos", idpart+"-"+fmt.Sprint(time.Now().UnixNano())), Standalone: !isGitWorkTreeRoot(root)}
	if err := os.MkdirAll(p.GitDir, 0700); err != nil {
		return a.fail(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.GitDir)
		}
	}()
	if code := a.exec(root, "git", "init", "--bare", p.GitDir); code != 0 {
		return code
	}
	_ = a.run(root, p.GitDir, "config", "core.autocrlf", "false")
	copyIdentity(root, a, p)
	if code := a.configureRemote(root, p, r.Remote); code != 0 {
		return code
	}
	if code := a.run(root, p.GitDir, "fetch", "origin"); code != 0 {
		return code
	}
	ref := "refs/remotes/origin/envs/" + o.env
	logical, err := logicalTrackedAt(root, p, ref)
	if err != nil {
		return a.fail(err)
	}
	if !p.Standalone {
		var owned []string
		for path := range logical {
			if a.mainOwns(root, p, path) {
				owned = append(owned, path)
			}
		}
		if len(owned) > 0 {
			sort.Strings(owned)
			return a.fail(fmt.Errorf("main repository already tracks: %s", strings.Join(owned, ", ")))
		}
	}
	conflicts, err := a.conflictsAt(root, p, ref)
	if err != nil {
		return a.fail(err)
	}
	if len(conflicts) > 0 && !o.keepLocal && !o.useRemote {
		return a.fail(fmt.Errorf("local files differ from remote: %s; use --keep-local or --use-remote", strings.Join(conflicts, ", ")))
	}
	saved := map[string][]byte{}
	for path := range logical {
		full := filepath.Join(root, filepath.FromSlash(path))
		if b, e := os.ReadFile(full); e == nil {
			saved[path] = b
		}
	}
	if o.useRemote && len(conflicts) > 0 {
		if err := backupFiles(d, p.ID, root, conflicts); err != nil {
			return a.fail(err)
		}
	}
	// Remove target plain files before checkout so ignored/untracked local files cannot be overwritten silently.
	for path, b := range logical {
		if b == StoragePlain {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(path)))
		}
	}
	if code := a.run(root, p.GitDir, "checkout", "-b", "env/"+o.env, ref); code != 0 {
		for path, b := range saved {
			dst := filepath.Join(root, filepath.FromSlash(path))
			_ = os.MkdirAll(filepath.Dir(dst), 0700)
			_ = os.WriteFile(dst, b, 0600)
		}
		return code
	}
	if !p.Standalone {
		if err := excludeLgitV2(root); err != nil {
			return a.fail(err)
		}
	}
	if err := a.mixedMaterialize(root, p); err != nil {
		return a.fail(err)
	}
	if o.keepLocal {
		for path, b := range saved {
			dst := filepath.Join(root, filepath.FromSlash(path))
			_ = os.MkdirAll(filepath.Dir(dst), 0700)
			if err := os.WriteFile(dst, b, 0600); err != nil {
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

func excludeLgitV2(root string) error {
	p := filepath.Join(root, ".git", "info", "exclude")
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "\n.lgit/\n") || strings.HasSuffix(string(b), ".lgit/\n") {
		return nil
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.WriteString(f, "\n.lgit/\n")
	return err
}
