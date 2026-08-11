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

type attachUXOptions struct {
	Remote    string
	Env       string
	Project   string
	KeepLocal bool
	UseRemote bool
	DryRun    bool
	JSON      bool
}

type remoteEnvironment struct {
	Project string `json:"project"`
	Env     string `json:"environment"`
}

type attachPlan struct {
	Project             string   `json:"project"`
	Environment         string   `json:"environment"`
	Root                string   `json:"root"`
	Remote              string   `json:"remote"`
	Encryption          string   `json:"encryption"`
	Identity            string   `json:"identity"`
	ContentConflicts    []string `json:"content_conflicts"`
	StructuralConflicts []string `json:"structural_conflicts"`
	BackupPaths         []string `json:"backup_paths"`
}

func parseAttachUX(args []string) (attachUXOptions, error) {
	var o attachUXOptions
	for i := 0; i < len(args); i++ {
		x := args[i]
		switch x {
		case "--env":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--env requires a name")
			}
			o.Env = args[i]
		case "--project":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--project requires a key")
			}
			o.Project = args[i]
		case "--keep-local":
			o.KeepLocal = true
		case "--use-remote":
			o.UseRemote = true
		case "--dry-run":
			o.DryRun = true
		case "--json":
			o.JSON = true
		case "--help", "-h":
			return o, fmt.Errorf("help")
		default:
			if strings.HasPrefix(x, "-") {
				return o, fmt.Errorf("unknown attach option %q", x)
			}
			if o.Remote != "" {
				return o, fmt.Errorf("attach accepts at most one remote URL")
			}
			o.Remote = x
		}
	}
	if o.KeepLocal && o.UseRemote {
		return o, fmt.Errorf("--keep-local and --use-remote are mutually exclusive")
	}
	if o.Env == "" {
		return o, fmt.Errorf("usage: lgit attach [REMOTE] --env NAME [--project KEY] [--keep-local|--use-remote] [--dry-run] [--json]")
	}
	var err error
	o.Env, err = validateName(o.Env)
	return o, err
}

func (a App) attachHelp() int {
	fmt.Fprintln(a.Stdout, `Usage:
  lgit [--root PATH] attach [REMOTE] --env NAME [--project KEY]
       [--keep-local|--use-remote] [--dry-run] [--json]

Attach an lgit project/environment to a local root. REMOTE is optional when a
shared remote is already configured. lgit discovers projects itself, validates
encryption requirements, and computes all content and filesystem-structure
conflicts before changing the root.

  --keep-local  keep differing regular files as local modifications
  --use-remote  back up conflicting local entries and use the remote versions
  --dry-run     show the complete attach plan without changing the root
  --json        emit machine-readable plan/output

A failed attach restores the root to its pre-attach state.`)
	return 0
}

func discoverRemote(remote string) ([]remoteEnvironment, error) {
	c := exec.Command("git", "ls-remote", "--heads", remote, "refs/heads/projects/*/envs/*")
	b, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("inspect remote: %v: %s", err, strings.TrimSpace(string(b)))
	}
	var out []remoteEnvironment
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ref := strings.TrimPrefix(f[1], "refs/heads/projects/")
		parts := strings.SplitN(ref, "/envs/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		key := parts[0] + "\x00" + parts[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, remoteEnvironment{Project: parts[0], Env: parts[1]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project == out[j].Project {
			return out[i].Env < out[j].Env
		}
		return out[i].Project < out[j].Project
	})
	return out, nil
}

func selectRemoteEnvironment(all []remoteEnvironment, base string, o attachUXOptions) (remoteEnvironment, error) {
	var envMatches []remoteEnvironment
	for _, x := range all {
		if x.Env == o.Env {
			envMatches = append(envMatches, x)
		}
	}
	if o.Project != "" {
		for _, x := range envMatches {
			if x.Project == o.Project {
				return x, nil
			}
		}
		return remoteEnvironment{}, fmt.Errorf("project %q does not provide environment %q", o.Project, o.Env)
	}
	var rootMatches []remoteEnvironment
	for _, x := range envMatches {
		if x.Project == base || strings.HasPrefix(x.Project, base+"-") {
			rootMatches = append(rootMatches, x)
		}
	}
	if len(rootMatches) == 1 {
		return rootMatches[0], nil
	}
	matches := rootMatches
	if len(matches) == 0 {
		matches = envMatches
	}
	if len(matches) == 0 {
		return remoteEnvironment{}, fmt.Errorf("no remote project provides environment %q", o.Env)
	}
	var names []string
	seen := map[string]bool{}
	for _, x := range matches {
		if !seen[x.Project] {
			seen[x.Project] = true
			names = append(names, x.Project)
		}
	}
	sort.Strings(names)
	return remoteEnvironment{}, fmt.Errorf("multiple projects provide environment %q: %s; rerun with --project PROJECT", o.Env, strings.Join(names, ", "))
}

func projectIDFromSlug(slug string) string {
	if i := strings.LastIndex(slug, "-"); i >= 0 && i+1 < len(slug) {
		return slug[i+1:]
	}
	id, _ := newID()
	return id
}

func runGitQuiet(root, gitDir string, args ...string) error {
	x := append([]string{"--git-dir=" + gitDir, "--work-tree=" + root}, args...)
	c := exec.Command("git", x...)
	c.Dir = root
	c.Stdin = os.Stdin
	b, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return nil
}

func gitInitBareQuiet(root, gitDir string) error {
	c := exec.Command("git", "init", "--bare", "--quiet", gitDir)
	c.Dir = root
	b, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

func configureRemoteQuiet(root string, p Project, remote string) error {
	if err := runGitQuiet(root, p.GitDir, "config", "core.autocrlf", "false"); err != nil {
		return err
	}
	if _, err := gitOutput(root, p.GitDir, "remote", "get-url", "origin"); err == nil {
		if err := runGitQuiet(root, p.GitDir, "remote", "set-url", "origin", remote); err != nil {
			return err
		}
	} else if err := runGitQuiet(root, p.GitDir, "remote", "add", "origin", remote); err != nil {
		return err
	}
	spec := "+refs/heads/projects/" + p.Slug + "/envs/*:refs/remotes/origin/envs/*"
	if err := runGitQuiet(root, p.GitDir, "config", "remote.origin.fetch", spec); err != nil {
		return err
	}
	return runGitQuiet(root, p.GitDir, "config", "remote.origin.tagOpt", "--no-tags")
}

func targetIdentity(a App, root string, p Project, ref string, c StorageConfig) (age.Identity, string, error) {
	if c.Encryption.Mode == "identity" {
		id, err := a.loadIdentity()
		if err != nil {
			path, _ := a.identityPath()
			return nil, "missing", fmt.Errorf("age identity is required but missing at %s; import it with 'lgit key import FILE'", path)
		}
		return id, "available", nil
	}
	password, err := a.readPassword(false)
	if err != nil {
		return nil, "password-required", err
	}
	sid, err := age.NewScryptIdentity(password)
	if err != nil {
		return nil, "password-required", err
	}
	wrapped, wrappedErr := gitBlob(root, p.GitDir, ref+":"+wrappedPasswordIdentityRel)
	if wrappedErr != nil {
		return sid, "password", nil
	}
	plain, err := decryptBytes(wrapped, sid)
	if err != nil {
		return nil, "password", fmt.Errorf("unlock project identity: %w", err)
	}
	xid, err := age.ParseX25519Identity(strings.TrimSpace(string(plain)))
	if err != nil {
		return nil, "password", err
	}
	return chainedIdentity{xid, sid}, "password", nil
}

func contentConflictsAt(root string, p Project, ref string, logical map[string]StorageBackend, c StorageConfig, id age.Identity) ([]string, error) {
	var out []string
	for rel, backend := range logical {
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		local, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		var remote []byte
		if backend == StoragePlain {
			remote, err = gitBlob(root, p.GitDir, ref+":"+rel)
		} else {
			if id == nil {
				return nil, fmt.Errorf("encrypted path %s requires an encryption identity", rel)
			}
			var cipher []byte
			cipher, err = gitBlob(root, p.GitDir, ref+":"+filepath.ToSlash(storePath(rel)))
			if err == nil {
				remote, err = decryptBytes(cipher, id)
			}
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

func addUniqueOuter(paths []string, candidate string) []string {
	candidate = filepath.ToSlash(candidate)
	for _, existing := range paths {
		if candidate == existing || strings.HasPrefix(candidate, existing+"/") {
			return paths
		}
	}
	var kept []string
	for _, existing := range paths {
		if !strings.HasPrefix(existing, candidate+"/") {
			kept = append(kept, existing)
		}
	}
	return append(kept, candidate)
}

func structuralConflicts(root string, logical map[string]StorageBackend) ([]string, error) {
	var out []string
	for rel := range logical {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		blocked := false
		for i := 1; i < len(parts); i++ {
			ancestor := strings.Join(parts[:i], "/")
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(ancestor)))
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				out = addUniqueOuter(out, ancestor)
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err == nil && !info.Mode().IsRegular() {
			out = addUniqueOuter(out, rel)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func mergePaths(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range groups {
		for _, p := range group {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func copyEntry(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyEntry(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cannot back up unsupported filesystem entry %s", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyExistingPaths(root, dstRoot string, paths []string) error {
	for _, rel := range paths {
		src := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Lstat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := copyEntry(src, filepath.Join(dstRoot, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return nil
}

func removePaths(root string, paths []string) error {
	for _, rel := range paths {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return nil
}

func restoreSnapshot(root, snapshot string, logical map[string]StorageBackend, blockers []string) {
	var leaves []string
	for rel := range logical {
		leaves = append(leaves, rel)
	}
	_ = removePaths(root, leaves)
	_ = os.RemoveAll(filepath.Join(root, ".lgit"))
	for _, rel := range mergePaths(blockers, leaves, []string{".lgit"}) {
		src := filepath.Join(snapshot, filepath.FromSlash(rel))
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.RemoveAll(dst)
		_ = copyEntry(src, dst)
	}
}

func createPermanentBackup(dataDir, projectID, root string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	backup := filepath.Join(dataDir, "backups", projectID, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(backup, 0700); err != nil {
		return "", err
	}
	if err := copyExistingPaths(root, backup, paths); err != nil {
		_ = os.RemoveAll(backup)
		return "", err
	}
	return backup, nil
}

func (a App) printAttachPlan(plan attachPlan, asJSON bool) int {
	if asJSON {
		b, _ := json.MarshalIndent(plan, "", "  ")
		fmt.Fprintln(a.Stdout, string(b))
		return 0
	}
	fmt.Fprintf(a.Stdout, "Project:     %s\nEnvironment: %s\nRoot:        %s\nEncryption:  %s\nIdentity:    %s\n", plan.Project, plan.Environment, plan.Root, plan.Encryption, plan.Identity)
	if len(plan.ContentConflicts) > 0 {
		fmt.Fprintln(a.Stdout, "\nContent conflicts:")
		for _, p := range plan.ContentConflicts {
			fmt.Fprintln(a.Stdout, "  "+p)
		}
	}
	if len(plan.StructuralConflicts) > 0 {
		fmt.Fprintln(a.Stdout, "\nStructural conflicts:")
		for _, p := range plan.StructuralConflicts {
			fmt.Fprintln(a.Stdout, "  "+p)
		}
	}
	if len(plan.BackupPaths) > 0 {
		fmt.Fprintln(a.Stdout, "\nWith --use-remote lgit will back up:")
		for _, p := range plan.BackupPaths {
			fmt.Fprintln(a.Stdout, "  "+p)
		}
	}
	fmt.Fprintln(a.Stdout, "\nNo changes made.")
	return 0
}

func (a App) attachUX(root string, args []string) int {
	o, err := parseAttachUX(args)
	if err != nil {
		if err.Error() == "help" {
			return a.attachHelp()
		}
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
		canonical, _ := canonicalPath(candidate)
		if pathKey(canonical) == pathKey(root) {
			return a.fail(fmt.Errorf("project is already initialized"))
		}
	}
	remote := o.Remote
	if remote == "" {
		remote = r.Remote
	}
	if remote == "" {
		return a.fail(fmt.Errorf("no remote configured; pass REMOTE to attach or run 'lgit remote set URL'"))
	}
	all, err := discoverRemote(remote)
	if err != nil {
		return a.fail(err)
	}
	selected, err := selectRemoteEnvironment(all, slugify(filepath.Base(root)), o)
	if err != nil {
		return a.fail(err)
	}
	idpart := projectIDFromSlug(selected.Project)
	p := Project{ID: idpart, Slug: selected.Project, Environment: selected.Env, GitDir: filepath.Join(d, "repos", idpart+"-"+fmt.Sprint(time.Now().UnixNano())), Standalone: !isGitWorkTreeRoot(root)}
	if err := os.MkdirAll(p.GitDir, 0700); err != nil {
		return a.fail(err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.GitDir)
		}
	}()
	if err := gitInitBareQuiet(root, p.GitDir); err != nil {
		return a.fail(err)
	}
	for _, key := range []string{"user.name", "user.email"} {
		if value, ok := mainGitConfig(root, key); ok {
			_ = runGitQuiet(root, p.GitDir, "config", key, value)
		}
	}
	if err := configureRemoteQuiet(root, p, remote); err != nil {
		return a.fail(err)
	}
	if err := runGitQuiet(root, p.GitDir, "fetch", "--quiet", "origin"); err != nil {
		return a.fail(err)
	}
	ref := "refs/remotes/origin/envs/" + selected.Env
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
	config, err := targetConfigAt(root, p, ref)
	if err != nil {
		return a.fail(err)
	}
	identityState := "not-required"
	var id age.Identity
	hasAge := false
	for _, backend := range logical {
		if backend == StorageAge {
			hasAge = true
			break
		}
	}
	if hasAge {
		id, identityState, err = targetIdentity(a, root, p, ref, config)
		if err != nil {
			return a.fail(fmt.Errorf("cannot attach %s/%s: %w; no files were changed", selected.Project, selected.Env, err))
		}
	}
	structural, err := structuralConflicts(root, logical)
	if err != nil {
		return a.fail(err)
	}
	contentLogical := logicalWithoutStructural(logical, structural)
	content, err := contentConflictsAt(root, p, ref, contentLogical, config, id)
	if err != nil {
		return a.fail(err)
	}
	backupPaths := mergePaths(content, structural)
	plan := attachPlan{Project: selected.Project, Environment: selected.Env, Root: root, Remote: remote, Encryption: config.Encryption.Mode, Identity: identityState, ContentConflicts: content, StructuralConflicts: structural, BackupPaths: backupPaths}
	if o.DryRun {
		return a.printAttachPlan(plan, o.JSON)
	}
	if len(structural) > 0 && o.KeepLocal {
		return a.fail(fmt.Errorf("--keep-local cannot preserve filesystem-structure conflicts: %s; use --use-remote to back them up", strings.Join(structural, ", ")))
	}
	if len(backupPaths) > 0 && !o.KeepLocal && !o.UseRemote {
		return a.fail(fmt.Errorf("local paths conflict with remote: %s; use --keep-local for regular-file content conflicts or --use-remote to back up local entries", strings.Join(backupPaths, ", ")))
	}

	var keepLocal map[string][]byte
	if o.KeepLocal {
		keepLocal = map[string][]byte{}
		for _, rel := range content {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return a.fail(err)
			}
			keepLocal[rel] = b
		}
	}
	backupPath := ""
	if o.UseRemote {
		backupPath, err = createPermanentBackup(d, p.ID, root, backupPaths)
		if err != nil {
			return a.fail(err)
		}
	}

	rollback := filepath.Join(p.GitDir, "attach-rollback")
	var mutationPaths []string
	rollbackLogical := logicalWithoutStructural(logical, structural)
	for rel := range rollbackLogical {
		mutationPaths = append(mutationPaths, rel)
	}
	mutationPaths = mergePaths(mutationPaths, structural, []string{".lgit"})
	if err := os.MkdirAll(rollback, 0700); err != nil {
		return a.fail(err)
	}
	if err := copyExistingPaths(root, rollback, mutationPaths); err != nil {
		return a.fail(err)
	}
	applied := false
	defer func() {
		if !applied {
			restoreSnapshot(root, rollback, rollbackLogical, structural)
		}
	}()

	if err := removePaths(root, structural); err != nil {
		return a.fail(err)
	}
	var plainLeaves []string
	for rel, backend := range logical {
		if backend == StoragePlain {
			plainLeaves = append(plainLeaves, rel)
		}
	}
	if err := removePaths(root, plainLeaves); err != nil {
		return a.fail(err)
	}
	if err := runGitQuiet(root, p.GitDir, "checkout", "-q", "-b", "env/"+selected.Env, ref); err != nil {
		return a.fail(err)
	}
	if !p.Standalone {
		if err := excludeLgitV2(root); err != nil {
			return a.fail(err)
		}
	}
	if err := a.mixedMaterialize(root, p); err != nil {
		return a.fail(err)
	}
	for rel, b := range keepLocal {
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return a.fail(err)
		}
		if err := os.WriteFile(dst, b, 0600); err != nil {
			return a.fail(err)
		}
	}
	_ = runGitQuiet(root, p.GitDir, "config", "status.showUntrackedFiles", "no")
	r.Remote = remote
	r.Projects[root] = p
	if err := SaveRegistry(rp, r); err != nil {
		return a.fail(err)
	}
	applied = true
	cleanup = false
	_ = os.RemoveAll(rollback)
	if o.JSON {
		result := map[string]any{"project": selected.Project, "environment": selected.Env, "root": root, "backup": backupPath, "attached": true}
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(a.Stdout, string(b))
		return 0
	}
	fmt.Fprintf(a.Stdout, "attached %s/%s\n", selected.Project, selected.Env)
	if backupPath != "" {
		fmt.Fprintln(a.Stdout, "backup:", backupPath)
	}
	return 0
}

func (a App) keyUX(root string, args []string) int {
	if len(args) == 0 {
		return a.key(root, args)
	}
	switch args[0] {
	case "path":
		path, err := a.identityPath()
		if err != nil {
			return a.fail(err)
		}
		if len(args) > 1 && args[1] == "--json" {
			b, _ := json.Marshal(map[string]string{"path": path})
			fmt.Fprintln(a.Stdout, string(b))
		} else {
			fmt.Fprintln(a.Stdout, path)
		}
		return 0
	case "status":
		path, err := a.identityPath()
		if err != nil {
			return a.fail(err)
		}
		status := "missing"
		recipient := ""
		if id, err := a.loadIdentity(); err == nil {
			status = "available"
			recipient = id.Recipient().String()
		}
		if len(args) > 1 && args[1] == "--json" {
			b, _ := json.MarshalIndent(map[string]string{"status": status, "path": path, "recipient": recipient}, "", "  ")
			fmt.Fprintln(a.Stdout, string(b))
		} else {
			fmt.Fprintln(a.Stdout, "identity:", status)
			fmt.Fprintln(a.Stdout, "path:", path)
			if recipient != "" {
				fmt.Fprintln(a.Stdout, "recipient:", recipient)
			}
		}
		return 0
	case "--help", "-h", "help":
		fmt.Fprintln(a.Stdout, "Usage: lgit key generate|show|path|status|export FILE|import FILE [--json]")
		return 0
	default:
		return a.key(root, args)
	}
}

func (a App) remoteListUX(args []string) int {
	remote := ""
	asJSON := false
	for _, arg := range args {
		if arg == "--json" {
			asJSON = true
		} else if arg == "--help" || arg == "-h" {
			fmt.Fprintln(a.Stdout, "Usage: lgit remote list [REMOTE] [--json]")
			return 0
		} else if strings.HasPrefix(arg, "-") {
			return a.fail(fmt.Errorf("unknown remote list option %q", arg))
		} else if remote == "" {
			remote = arg
		} else {
			return a.fail(fmt.Errorf("remote list accepts at most one remote URL"))
		}
	}
	if remote == "" {
		r, err := a.registry()
		if err != nil {
			return a.fail(err)
		}
		remote = r.Remote
	}
	if remote == "" {
		return a.fail(fmt.Errorf("no remote configured; pass REMOTE"))
	}
	items, err := discoverRemote(remote)
	if err != nil {
		return a.fail(err)
	}
	if asJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Fprintln(a.Stdout, string(b))
		return 0
	}
	grouped := map[string][]string{}
	var projects []string
	for _, item := range items {
		if _, ok := grouped[item.Project]; !ok {
			projects = append(projects, item.Project)
		}
		grouped[item.Project] = append(grouped[item.Project], item.Env)
	}
	sort.Strings(projects)
	for _, project := range projects {
		sort.Strings(grouped[project])
		fmt.Fprintf(a.Stdout, "%s\t%s\n", project, strings.Join(grouped[project], ","))
	}
	return 0
}

func (a App) infoUX(cwd, explicit string, args []string) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 0 && !asJSON {
		if args[0] == "--help" || args[0] == "-h" {
			fmt.Fprintln(a.Stdout, "Usage: lgit [--root PATH] info [--json]")
			return 0
		}
		return a.fail(fmt.Errorf("usage: lgit [--root PATH] info [--json]"))
	}
	r, err := a.registry()
	if err != nil {
		return a.fail(err)
	}
	root := ""
	if explicit != "" {
		root, err = canonicalPath(explicit)
	} else if resolved, resolveErr := a.resolveRoot(cwd, "", false); resolveErr == nil {
		root = resolved
	} else {
		root, err = canonicalPath(cwd)
	}
	if err != nil {
		return a.fail(err)
	}
	dataDir, _ := DataDir()
	identityPath, _ := a.identityPath()
	identity := "missing"
	if _, err := a.loadIdentity(); err == nil {
		identity = "available"
	}
	state := "not-attached"
	var project *Project
	for candidate, p := range r.Projects {
		canonical, _ := canonicalPath(candidate)
		if pathKey(canonical) == pathKey(root) {
			state = "attached"
			copy := p
			project = &copy
			break
		}
	}
	result := map[string]any{"root": root, "state": state, "remote": r.Remote, "data_dir": dataDir, "identity": identity, "identity_path": identityPath}
	if project != nil {
		result["project"] = project.Slug
		result["environment"] = project.Environment
		result["git_dir"] = project.GitDir
		if c, err := loadStorageConfig(root); err == nil {
			result["storage_default"] = c.Default
			result["encryption"] = c.Encryption.Mode
		}
	}
	if asJSON {
		b, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(a.Stdout, string(b))
		return 0
	}
	fmt.Fprintln(a.Stdout, "root:", root)
	fmt.Fprintln(a.Stdout, "state:", state)
	if project != nil {
		fmt.Fprintln(a.Stdout, "project:", project.Slug)
		fmt.Fprintln(a.Stdout, "environment:", project.Environment)
	}
	if r.Remote != "" {
		fmt.Fprintln(a.Stdout, "remote:", r.Remote)
	}
	fmt.Fprintln(a.Stdout, "data dir:", dataDir)
	fmt.Fprintln(a.Stdout, "identity:", identity)
	fmt.Fprintln(a.Stdout, "identity path:", identityPath)
	if project != nil {
		fmt.Fprintln(a.Stdout, "git dir:", project.GitDir)
	}
	return 0
}
