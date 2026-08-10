from pathlib import Path

root = Path('.')

# Extend registry projects with standalone-root ownership.
p = root/'internal/lgit/store.go'
s = p.read_text()
s = s.replace('Environment string `json:"environment"`\n}', 'Environment string `json:"environment"`\n\tStandalone  bool   `json:"standalone,omitempty"`\n}')
p.write_text(s)

# New root resolution and boundary helpers.
(root/'internal/lgit/root.go').write_text(r'''package lgit

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
)

func canonicalPath(path string) (string, error) {
    if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\\`) {
        home, err := os.UserHomeDir()
        if err != nil { return "", err }
        if path == "~" { path = home } else { path = filepath.Join(home, path[2:]) }
    }
    p, err := filepath.Abs(path)
    if err != nil { return "", err }
    return filepath.Clean(p), nil
}

func pathKey(path string) string {
    path = filepath.Clean(path)
    if runtime.GOOS == "windows" { return strings.ToLower(path) }
    return path
}

func containsPath(parent, child string) bool {
    rel, err := filepath.Rel(parent, child)
    if err != nil { return false }
    return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (a App) nearestRegisteredRoot(cwd string) (string, bool, error) {
    cwd, err := canonicalPath(cwd)
    if err != nil { return "", false, err }
    r, err := a.registry()
    if err != nil { return "", false, err }
    best := ""
    for candidate := range r.Projects {
        if containsPath(candidate, cwd) && len(pathKey(candidate)) > len(pathKey(best)) { best = candidate }
    }
    return best, best != "", nil
}

func (a App) resolveRoot(cwd, explicit string, allowUnregistered bool) (string, error) {
    if explicit != "" {
        root, err := canonicalPath(explicit)
        if err != nil { return "", err }
        if allowUnregistered { return root, nil }
        r, err := a.registry()
        if err != nil { return "", err }
        for candidate := range r.Projects {
            if pathKey(candidate) == pathKey(root) { return candidate, nil }
        }
        return "", fmt.Errorf("lgit root is not initialized: %s", root)
    }
    if root, ok, err := a.nearestRegisteredRoot(cwd); err != nil { return "", err } else if ok { return root, nil }
    if allowUnregistered { return gitRoot(cwd) }
    return "", fmt.Errorf("no initialized lgit root contains %s", cwd)
}

func isGitWorkTreeRoot(root string) bool {
    c := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
    b, err := c.Output()
    if err != nil { return false }
    got, err := canonicalPath(strings.TrimSpace(string(b)))
    return err == nil && pathKey(got) == pathKey(root)
}

func (a App) childRoot(root, path string) (string, bool, error) {
    r, err := a.registry()
    if err != nil { return "", false, err }
    path, err = canonicalPath(path)
    if err != nil { return "", false, err }
    best := ""
    for candidate := range r.Projects {
        if pathKey(candidate) == pathKey(root) { continue }
        if containsPath(root, candidate) && containsPath(candidate, path) {
            if best == "" || len(candidate) > len(best) { best = candidate }
        }
    }
    return best, best != "", nil
}

func nestedGitRoot(root, path string) (string, bool) {
    cur := filepath.Clean(path)
    if info, err := os.Stat(cur); err == nil && !info.IsDir() { cur = filepath.Dir(cur) }
    for containsPath(root, cur) && pathKey(cur) != pathKey(root) {
        if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil { return cur, true }
        next := filepath.Dir(cur)
        if next == cur { break }
        cur = next
    }
    return "", false
}
''')

# Storage policy and mixed logical worktree implementation.
(root/'internal/lgit/storage_policy.go').write_text(r'''package lgit

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
    StorageAge StorageBackend = "age"
)

type StorageEncryption struct { Mode string `toml:"mode"` }
type StorageConfig struct {
    Version int `toml:"version"`
    Default StorageBackend `toml:"default"`
    Encryption StorageEncryption `toml:"encryption"`
    Files map[string]StorageBackend `toml:"files,omitempty"`
}

func validBackend(v StorageBackend) bool { return v == StoragePlain || v == StorageAge }
func normalizeLogical(path string) (string, error) {
    path = filepath.ToSlash(filepath.Clean(path))
    if path == "." || path == "" || strings.HasPrefix(path, "../") || filepath.IsAbs(path) { return "", fmt.Errorf("invalid logical path %q", path) }
    return path, nil
}
func storageConfigPath(root string) string { return filepath.Join(root, ".lgit", "storage.toml") }

func loadStorageConfig(root string) (StorageConfig, error) {
    var c StorageConfig
    b, err := os.ReadFile(storageConfigPath(root))
    if err == nil {
        if err := toml.Unmarshal(b, &c); err != nil { return c, err }
        if c.Version != 1 { return c, fmt.Errorf("unsupported storage config version %d", c.Version) }
        if !validBackend(c.Default) { return c, fmt.Errorf("unsupported default storage backend %q", c.Default) }
        if c.Encryption.Mode != "identity" && c.Encryption.Mode != "password" { return c, fmt.Errorf("unsupported encryption mode %q", c.Encryption.Mode) }
        if c.Files == nil { c.Files = map[string]StorageBackend{} }
        for path, backend := range c.Files {
            if _, err := normalizeLogical(path); err != nil { return c, err }
            if !validBackend(backend) { return c, fmt.Errorf("unsupported storage backend %q for %s", backend, path) }
        }
        return c, nil
    }
    if !os.IsNotExist(err) { return c, err }
    // Legacy repositories were entirely age-backed and stored the encryption mode in format.json.
    f, ferr := readLegacyAgeFormat(root)
    if ferr != nil { return c, ferr }
    mode := "identity"
    if f.Encryption == agePasswordFormat { mode = "password" }
    return StorageConfig{Version:1, Default:StorageAge, Encryption:StorageEncryption{Mode:mode}, Files:map[string]StorageBackend{}}, nil
}

func writeStorageConfig(root string, c StorageConfig) error {
    if c.Files == nil { c.Files = map[string]StorageBackend{} }
    b, err := toml.Marshal(c)
    if err != nil { return err }
    path := storageConfigPath(root)
    if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { return err }
    return os.WriteFile(path, b, 0600)
}
func configuredBackend(c StorageConfig, path string) (StorageBackend, string) {
    if b, ok := c.Files[path]; ok { return b, "explicit" }
    return c.Default, "default"
}

func (a App) initStorage(root string, p Project, backend StorageBackend, encryption string) error {
    c := StorageConfig{Version:1, Default:backend, Encryption:StorageEncryption{Mode:encryption}, Files:map[string]StorageBackend{}}
    if encryption == "password" {
        if _, err := a.readPassword(true); err != nil { return err }
    } else {
        id, err := a.ensureIdentity(); if err != nil { return err }
        meta := filepath.Join(root, ".lgit")
        if err := os.MkdirAll(meta,0700); err != nil { return err }
        if err := os.WriteFile(filepath.Join(meta,"recipients.txt"), []byte(id.Recipient().String()+"\n"),0600); err != nil { return err }
    }
    if err := writeStorageConfig(root,c); err != nil { return err }
    if !p.Standalone { if err := excludeLgitV2(root); err != nil { return err } }
    files := []string{".lgit/storage.toml"}
    if encryption == "identity" { files = append(files,".lgit/recipients.txt") }
    args := append([]string{"add","--force","--"},files...)
    if code := a.run(root,p.GitDir,args...); code != 0 { return fmt.Errorf("failed to stage storage metadata") }
    return nil
}

func readLegacyAgeFormat(root string) (ageFormatFile,error) {
    return readAgeFormatLegacyOnly(root)
}

func (a App) storageRecipients(root string, c StorageConfig) ([]age.Recipient,error) {
    if c.Encryption.Mode == "password" {
        password,err:=a.readPassword(false); if err!=nil{return nil,err}
        r,err:=age.NewScryptRecipient(password); if err!=nil{return nil,err}
        return []age.Recipient{r},nil
    }
    return readRecipients(root)
}
func (a App) storageIdentity(root string, c StorageConfig) (age.Identity,error) {
    if c.Encryption.Mode == "password" {
        password,err:=a.readPassword(false); if err!=nil{return nil,err}
        return age.NewScryptIdentity(password)
    }
    return a.loadIdentity()
}

func plainTrackedAt(root string,p Project,ref string) ([]string,error) {
    out,err:=gitOutput(root,p.GitDir,"ls-tree","-r","--name-only",ref)
    if err!=nil{return nil,err}
    var xs []string
    for _,x:=range strings.Split(strings.TrimSpace(out),"\n") {
        x=filepath.ToSlash(strings.TrimSpace(x)); if x==""||strings.HasPrefix(x,".lgit/"){continue}
        xs=append(xs,x)
    }
    sort.Strings(xs); return xs,nil
}
func logicalTrackedAt(root string,p Project,ref string) (map[string]StorageBackend,error) {
    m:=map[string]StorageBackend{}
    plain,err:=plainTrackedAt(root,p,ref); if err!=nil{return nil,err}
    for _,x:=range plain {m[x]=StoragePlain}
    stores,err:=trackedStore(root,p,ref); if err!=nil{return nil,err}
    for _,sp:=range stores {m[plainPath(sp)]=StorageAge}
    return m,nil
}
func currentBackend(root string,p Project,path string) (StorageBackend,bool,error) {
    if _,err:=gitOutput(root,p.GitDir,"ls-files","--error-unmatch","--",path); err==nil{return StoragePlain,true,nil}
    sp:=filepath.ToSlash(storePath(path))
    if _,err:=gitOutput(root,p.GitDir,"ls-files","--error-unmatch","--",sp); err==nil{return StorageAge,true,nil}
    return "",false,nil
}

func (a App) mainOwns(root string,p Project,path string) bool {
    if p.Standalone{return false}
    c:=exec.Command("git","-C",root,"ls-files","--error-unmatch","--",path)
    return c.Run()==nil
}

func (a App) expandPaths(root string,p Project,args []string) ([]string,error) {
    seen:=map[string]bool{}
    var out []string
    add:=func(rel string) error { rel,err:=normalizeLogical(rel); if err!=nil{return err}; if !seen[rel]{seen[rel]=true;out=append(out,rel)}; return nil }
    for _,raw:=range args {
        abs:=raw
        if !filepath.IsAbs(abs){abs=filepath.Join(root,raw)}
        abs,err:=canonicalPath(abs); if err!=nil{return nil,err}
        if !containsPath(root,abs){return nil,fmt.Errorf("path outside lgit root: %s",raw)}
        if child,ok,err:=a.childRoot(root,abs); err!=nil{return nil,err}else if ok{return nil,fmt.Errorf("path belongs to nested lgit project: %s",child)}
        if nested,ok:=nestedGitRoot(root,abs);ok{return nil,fmt.Errorf("path belongs to nested Git repository: %s",nested)}
        info,err:=os.Lstat(abs)
        if os.IsNotExist(err) { rel,_:=filepath.Rel(root,abs); if err:=add(rel);err!=nil{return nil,err}; continue }
        if err!=nil{return nil,err}
        if !info.IsDir(){if info.Mode()&os.ModeSymlink!=0{return nil,fmt.Errorf("symlink storage is not supported yet: %s",raw)};rel,_:=filepath.Rel(root,abs);if err:=add(rel);err!=nil{return nil,err};continue}
        err=filepath.WalkDir(abs,func(path string,d os.DirEntry,walkErr error) error{
            if walkErr!=nil{return walkErr}
            if path==abs{return nil}
            if d.IsDir(){
                if d.Name()==".git"||d.Name()==".lgit"{return filepath.SkipDir}
                if child,ok,_:=a.childRoot(root,path);ok&&pathKey(child)==pathKey(path){return filepath.SkipDir}
                if _,err:=os.Stat(filepath.Join(path,".git"));err==nil{return filepath.SkipDir}
                return nil
            }
            info,err:=d.Info();if err!=nil{return err};if !info.Mode().IsRegular(){return nil}
            rel,_:=filepath.Rel(root,path);return add(rel)
        })
        if err!=nil{return nil,err}
    }
    sort.Strings(out);return out,nil
}

func (a App) addOne(root string,p Project,c StorageConfig,path string,backend StorageBackend,rs []age.Recipient) error {
    if a.mainOwns(root,p,path){return fmt.Errorf("main repository already tracks %s",path)}
    full:=filepath.Join(root,filepath.FromSlash(path)); info,err:=os.Lstat(full)
    if err==nil && !info.Mode().IsRegular(){return fmt.Errorf("only regular files are supported: %s",path)}
    if err!=nil && !os.IsNotExist(err){return err}
    sp:=filepath.ToSlash(storePath(path))
    if os.IsNotExist(err) {
        _=os.Remove(filepath.Join(root,filepath.FromSlash(sp)))
        _=a.run(root,p.GitDir,"rm","--cached","--ignore-unmatch","--",path,sp)
        return nil
    }
    if backend==StoragePlain {
        if code:=a.run(root,p.GitDir,"add","--force","--",path);code!=0{return fmt.Errorf("failed to stage %s",path)}
        _=os.Remove(filepath.Join(root,filepath.FromSlash(sp)))
        if code:=a.run(root,p.GitDir,"rm","--cached","--ignore-unmatch","--",sp);code!=0{return fmt.Errorf("failed to remove encrypted representation of %s",path)}
        return nil
    }
    plain,err:=os.ReadFile(full);if err!=nil{return err}
    // Avoid randomized ciphertext churn if the currently staged age representation already matches.
    if cipher,err:=gitBlob(root,p.GitDir,":"+sp);err==nil {
        id,derr:=a.storageIdentity(root,c);if derr==nil { if old,derr:=decryptBytes(cipher,id);derr==nil&&bytes.Equal(old,plain){return nil} }
    }
    if rs==nil { var rerr error; rs,rerr=a.storageRecipients(root,c);if rerr!=nil{return rerr} }
    cipher,err:=encryptBytes(plain,rs);if err!=nil{return err}
    dst:=filepath.Join(root,filepath.FromSlash(sp));if err:=os.MkdirAll(filepath.Dir(dst),0700);err!=nil{return err}
    if err:=os.WriteFile(dst,cipher,0600);err!=nil{return err}
    if code:=a.run(root,p.GitDir,"add","--force","--",sp);code!=0{return fmt.Errorf("failed to stage encrypted %s",path)}
    if code:=a.run(root,p.GitDir,"rm","--cached","--ignore-unmatch","--",path);code!=0{return fmt.Errorf("failed to remove plain representation of %s",path)}
    return nil
}

func (a App) mixedAdd(root string,args []string) int {
    if len(args)==0{return a.fail(fmt.Errorf("usage: lgit add PATH..."))}
    p,err:=a.lookup(root);if err!=nil{return a.fail(err)}
    c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)}
    paths,err:=a.expandPaths(root,p,args);if err!=nil{return a.fail(err)}
    var rs []age.Recipient
    for _,path:=range paths {
        backend,_:=configuredBackend(c,path)
        if backend==StorageAge && rs==nil {rs,err=a.storageRecipients(root,c);if err!=nil{return a.fail(err)}}
        if err:=a.addOne(root,p,c,path,backend,rs);err!=nil{return a.fail(err)}
    }
    return 0
}

func (a App) mixedMaterialize(root string,p Project) error {
    c,err:=loadStorageConfig(root);if err!=nil{return err}
    stores,err:=trackedStore(root,p,"HEAD");if err!=nil{return err}
    var id age.Identity
    if len(stores)>0{id,err=a.storageIdentity(root,c);if err!=nil{return err}}
    oldFile:=filepath.Join(p.GitDir,"lgit-materialized.txt")
    oldb,_:=os.ReadFile(oldFile);old:=map[string]bool{}
    for _,x:=range strings.Split(string(oldb),"\n"){if x!=""{old[x]=true}}
    now:=map[string]bool{}
    for _,sp:=range stores {
        cipher,err:=gitBlob(root,p.GitDir,"HEAD:"+sp);if err!=nil{return err}
        plain,err:=decryptBytes(cipher,id);if err!=nil{return fmt.Errorf("decrypt %s: %w",plainPath(sp),err)}
        rel:=plainPath(sp);now[rel]=true;dst:=filepath.Join(root,filepath.FromSlash(rel))
        if err:=os.MkdirAll(filepath.Dir(dst),0700);err!=nil{return err}
        mode:=os.FileMode(0600)
        if m,err:=gitOutput(root,p.GitDir,"ls-tree","HEAD",sp);err==nil&&strings.HasPrefix(m,"100755"){mode=0700}
        if err:=os.WriteFile(dst,plain,mode);err!=nil{return err}
    }
    for rel:=range old {if !now[rel]{ if backend,ok,_:=currentBackend(root,p,rel);!ok||backend!=StoragePlain{_ = os.Remove(filepath.Join(root,filepath.FromSlash(rel)))}}}
    lines:=make([]string,0,len(now));for rel:=range now{lines=append(lines,rel)};sort.Strings(lines)
    return os.WriteFile(oldFile,[]byte(strings.Join(lines,"\n")+"\n"),0600)
}

func (a App) mixedClean(root string,p Project) bool {
    if out,err:=gitOutput(root,p.GitDir,"status","--porcelain","--untracked-files=no");err!=nil||strings.TrimSpace(out)!=""{return false}
    stores,err:=trackedStore(root,p,"HEAD");if err!=nil{return false}
    if len(stores)==0{return true}
    c,err:=loadStorageConfig(root);if err!=nil{return false};id,err:=a.storageIdentity(root,c);if err!=nil{return false}
    for _,sp:=range stores {cipher,err:=gitBlob(root,p.GitDir,"HEAD:"+sp);if err!=nil{return false};want,err:=decryptBytes(cipher,id);if err!=nil{return false};got,err:=os.ReadFile(filepath.Join(root,filepath.FromSlash(plainPath(sp))));if err!=nil||!bytes.Equal(got,want){return false}}
    return true
}

func (a App) mixedStatus(root string,args []string) int {
    p,err:=a.lookup(root);if err!=nil{return a.fail(err)}
    out,_:=gitOutput(root,p.GitDir,"status","--porcelain","--untracked-files=no")
    for _,line:=range strings.Split(strings.TrimRight(out,"\n"),"\n") {
        if line==""{continue}; path:=filepath.ToSlash(strings.TrimSpace(line[3:])); if strings.HasPrefix(path,".lgit/store/"){fmt.Fprintf(a.Stdout,"%s %s\n",line[:2],plainPath(path));continue};if strings.HasPrefix(path,".lgit/"){continue};fmt.Fprintln(a.Stdout,line)
    }
    stores,err:=trackedStore(root,p,"HEAD");if err!=nil{return a.fail(err)};if len(stores)==0{return 0}
    c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)};id,err:=a.storageIdentity(root,c);if err!=nil{return a.fail(err)}
    for _,sp:=range stores {cipher,_:=gitBlob(root,p.GitDir,"HEAD:"+sp);want,_:=decryptBytes(cipher,id);rel:=plainPath(sp);got,err:=os.ReadFile(filepath.Join(root,filepath.FromSlash(rel)));if err!=nil{fmt.Fprintf(a.Stdout," D %s\n",rel)}else if !bytes.Equal(got,want){fmt.Fprintf(a.Stdout," M %s\n",rel)}}
    return 0
}

func (a App) mixedRestore(root string,args []string) int {
    if len(args)==0{return a.fail(fmt.Errorf("usage: lgit restore PATH..."))}
    p,err:=a.lookup(root);if err!=nil{return a.fail(err)};c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)}
    var id age.Identity
    for _,raw:=range args {rel,err:=filepath.Rel(root,filepath.Join(root,raw));if err!=nil{return a.fail(err)};rel,err=normalizeLogical(rel);if err!=nil{return a.fail(err)};backend,ok,err:=currentBackend(root,p,rel);if err!=nil{return a.fail(err)};if !ok{return a.fail(fmt.Errorf("path is not tracked: %s",rel))};if backend==StoragePlain{if code:=a.run(root,p.GitDir,"restore","--",rel);code!=0{return code};continue};if id==nil{id,err=a.storageIdentity(root,c);if err!=nil{return a.fail(err)}};sp:=filepath.ToSlash(storePath(rel));cipher,err:=gitBlob(root,p.GitDir,"HEAD:"+sp);if err!=nil{return a.fail(err)};plain,err:=decryptBytes(cipher,id);if err!=nil{return a.fail(err)};dst:=filepath.Join(root,filepath.FromSlash(rel));if err:=os.MkdirAll(filepath.Dir(dst),0700);err!=nil{return a.fail(err)};if err:=os.WriteFile(dst,plain,0600);err!=nil{return a.fail(err)}}
    return 0
}

func logicalDiffAge(root string,p Project,c StorageConfig,rel string,id age.Identity) (string,error) {
    sp:=filepath.ToSlash(storePath(rel));cipher,err:=gitBlob(root,p.GitDir,"HEAD:"+sp);if err!=nil{return "",err};old,err:=decryptBytes(cipher,id);if err!=nil{return "",err};now,err:=os.ReadFile(filepath.Join(root,filepath.FromSlash(rel)));if os.IsNotExist(err){now=[]byte{}}else if err!=nil{return "",err};if bytes.Equal(old,now){return "",nil}
    d,err:=os.MkdirTemp("","lgit-diff-");if err!=nil{return "",err};defer os.RemoveAll(d);a:=filepath.Join(d,"old");b:=filepath.Join(d,"new");_ = os.WriteFile(a,old,0600);_ = os.WriteFile(b,now,0600)
    cmd:=exec.Command("git","diff","--no-index","--text","--",a,b);out,_:=cmd.CombinedOutput();text:=string(out);text=strings.ReplaceAll(text,filepath.ToSlash(a),"a/"+rel);text=strings.ReplaceAll(text,filepath.ToSlash(b),"b/"+rel);text=strings.ReplaceAll(text,a,"a/"+rel);text=strings.ReplaceAll(text,b,"b/"+rel);return text,nil
}
func (a App) mixedDiff(root string,args []string) int {
    p,err:=a.lookup(root);if err!=nil{return a.fail(err)};tracked,err:=logicalTrackedAt(root,p,"HEAD");if err!=nil{return a.fail(err)};var paths []string
    if len(args)==0{for x:=range tracked{paths=append(paths,x)};sort.Strings(paths)}else{for _,raw:=range args{if strings.HasPrefix(raw,"-"){return a.fail(fmt.Errorf("diff options are not supported for logical storage yet"))};rel,_:=filepath.Rel(root,filepath.Join(root,raw));rel,err=normalizeLogical(rel);if err!=nil{return a.fail(err)};paths=append(paths,rel)}}
    c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)};var id age.Identity
    for _,rel:=range paths{backend,ok:=tracked[rel];if !ok{continue};if backend==StoragePlain{out,err:=gitOutput(root,p.GitDir,"diff","--",rel);if err!=nil{return a.fail(err)};fmt.Fprint(a.Stdout,out);continue};if id==nil{id,err=a.storageIdentity(root,c);if err!=nil{return a.fail(err)}};out,err:=logicalDiffAge(root,p,c,rel,id);if err!=nil{return a.fail(err)};fmt.Fprint(a.Stdout,out)}
    return 0
}

func (a App) storageCommand(root string,args []string) int {
    p,err:=a.lookup(root);if err!=nil{return a.fail(err)};c,err:=loadStorageConfig(root);if err!=nil{return a.fail(err)}
    if len(args)==0{return a.fail(fmt.Errorf("usage: lgit storage show|set|unset|default"))}
    switch args[0] {
    case "default":
        if len(args)==1{fmt.Fprintln(a.Stdout,c.Default);return 0};if len(args)!=2{return a.fail(fmt.Errorf("usage: lgit storage default [plain|age]"))};b:=StorageBackend(strings.ToLower(args[1]));if !validBackend(b){return a.fail(fmt.Errorf("unsupported storage backend %q",b))};c.Default=b;if err:=writeStorageConfig(root,c);err!=nil{return a.fail(err)};if code:=a.run(root,p.GitDir,"add","--force","--",".lgit/storage.toml");code!=0{return code};return 0
    case "show":
        if len(args)!=2{return a.fail(fmt.Errorf("usage: lgit storage show PATH"))};rel,_:=filepath.Rel(root,filepath.Join(root,args[1]));rel,err=normalizeLogical(rel);if err!=nil{return a.fail(err)};configured,source:=configuredBackend(c,rel);current,ok,_:=currentBackend(root,p,rel);if !ok{current="untracked"};fmt.Fprintf(a.Stdout,"%s\ncurrent: %s\nconfigured: %s\nsource: %s\n",rel,current,configured,source);if configured==StorageAge{fmt.Fprintf(a.Stdout,"encryption: %s\n",c.Encryption.Mode)};return 0
    case "set":
        if len(args)!=3{return a.fail(fmt.Errorf("usage: lgit storage set PATH plain|age"))};return a.setStorage(root,p,c,args[1],StorageBackend(strings.ToLower(args[2])),false)
    case "unset":
        if len(args)!=2{return a.fail(fmt.Errorf("usage: lgit storage unset PATH"))};return a.setStorage(root,p,c,args[1],c.Default,true)
    default:return a.fail(fmt.Errorf("unknown storage command %q",args[0]))
    }
}

func (a App) setStorage(root string,p Project,c StorageConfig,raw string,backend StorageBackend,unset bool) int {
    if !validBackend(backend){return a.fail(fmt.Errorf("unsupported storage backend %q",backend))};rel,_:=filepath.Rel(root,filepath.Join(root,raw));rel,err:=normalizeLogical(rel);if err!=nil{return a.fail(err)}
    oldConfig,oldConfigErr:=os.ReadFile(storageConfigPath(root));indexPath:=filepath.Join(p.GitDir,"index");oldIndex,oldIndexErr:=os.ReadFile(indexPath);spFull:=filepath.Join(root,filepath.FromSlash(storePath(rel)));oldStore,oldStoreErr:=os.ReadFile(spFull)
    rollback:=func(){if oldConfigErr==nil{_ = os.WriteFile(storageConfigPath(root),oldConfig,0600)}else{_ = os.Remove(storageConfigPath(root))};if oldIndexErr==nil{_ = os.WriteFile(indexPath,oldIndex,0600)};if oldStoreErr==nil{_ = os.MkdirAll(filepath.Dir(spFull),0700);_ = os.WriteFile(spFull,oldStore,0600)}else{_ = os.Remove(spFull)}}
    if unset{delete(c.Files,rel)}else{if c.Files==nil{c.Files=map[string]StorageBackend{}};c.Files[rel]=backend}
    if err:=writeStorageConfig(root,c);err!=nil{rollback();return a.fail(err)}
    if code:=a.run(root,p.GitDir,"add","--force","--",".lgit/storage.toml");code!=0{rollback();return code}
    if _,tracked,_:=currentBackend(root,p,rel);tracked {var rs []age.Recipient;if backend==StorageAge{rs,err=a.storageRecipients(root,c);if err!=nil{rollback();return a.fail(err)}};if err:=a.addOne(root,p,c,rel,backend,rs);err!=nil{rollback();return a.fail(err)}}
    return 0
}

func targetConfigAt(root string,p Project,ref string) (StorageConfig,error) {
    b,err:=gitBlob(root,p.GitDir,ref+":.lgit/storage.toml")
    if err!=nil{return loadStorageConfig(root)}
    var c StorageConfig;if err:=toml.Unmarshal(b,&c);err!=nil{return c,err};if c.Files==nil{c.Files=map[string]StorageBackend{}};return c,nil
}

func (a App) conflictsAt(root string,p Project,ref string) ([]string,error) {
    tracked,err:=logicalTrackedAt(root,p,ref);if err!=nil{return nil,err};c,err:=targetConfigAt(root,p,ref);if err!=nil{return nil,err};var id age.Identity;var out []string
    for rel,b:=range tracked {local,err:=os.ReadFile(filepath.Join(root,filepath.FromSlash(rel)));if os.IsNotExist(err){continue};if err!=nil{return nil,err};var remote []byte;if b==StoragePlain{remote,err=gitBlob(root,p.GitDir,ref+":"+rel)}else{if id==nil{id,err=a.storageIdentity(root,c);if err!=nil{return nil,err}};cipher,e:=gitBlob(root,p.GitDir,ref+":"+filepath.ToSlash(storePath(rel)));if e!=nil{return nil,e};remote,err=decryptBytes(cipher,id)};if err!=nil{return nil,err};if !bytes.Equal(local,remote){out=append(out,rel)}}
    sort.Strings(out);return out,nil
}

func (a App) checkoutPrepare(root string,p Project,targetRef string) ([]string,error) {
    current,err:=logicalTrackedAt(root,p,"HEAD");if err!=nil{return nil,err};target,err:=logicalTrackedAt(root,p,targetRef);if err!=nil{return nil,err};var removed []string
    for rel,b:=range current {if b==StorageAge&&target[rel]==StoragePlain{if data,err:=os.ReadFile(filepath.Join(root,filepath.FromSlash(rel)));err==nil{backup:=filepath.Join(p.GitDir,"transition",filepath.FromSlash(rel));_ = os.MkdirAll(filepath.Dir(backup),0700);_ = os.WriteFile(backup,data,0600);_ = os.Remove(filepath.Join(root,filepath.FromSlash(rel)));removed=append(removed,rel)}}}
    return removed,nil
}
func restorePrepared(root string,p Project,paths []string){for _,rel:=range paths{backup:=filepath.Join(p.GitDir,"transition",filepath.FromSlash(rel));if b,err:=os.ReadFile(backup);err==nil{dst:=filepath.Join(root,filepath.FromSlash(rel));_ = os.MkdirAll(filepath.Dir(dst),0700);_ = os.WriteFile(dst,b,0600);_ = os.Remove(backup)}}}

func (a App) envSwitchMixed(root string,p Project,name string) int {
    name,err:=validateName(name);if err!=nil{return a.fail(err)};if name==p.Environment{return 0};if !a.mixedClean(root,p){return a.fail(fmt.Errorf("cannot switch environment: uncommitted changes"))}
    target:="refs/heads/env/"+name;if _,err:=gitOutput(root,p.GitDir,"rev-parse","--verify",target);err!=nil{_ = a.run(root,p.GitDir,"fetch","origin");target="refs/remotes/origin/envs/"+name}
    removed,err:=a.checkoutPrepare(root,p,target);if err!=nil{return a.fail(err)};old:=p.Environment
    if _,err:=gitOutput(root,p.GitDir,"rev-parse","--verify","refs/heads/env/"+name);err!=nil{if code:=a.run(root,p.GitDir,"switch","-c","env/"+name,target);code!=0{restorePrepared(root,p,removed);return code}}else if code:=a.run(root,p.GitDir,"checkout","env/"+name);code!=0{restorePrepared(root,p,removed);return code}
    if err:=a.mixedMaterialize(root,p);err!=nil{_ = a.run(root,p.GitDir,"checkout","env/"+old);restorePrepared(root,p,removed);_ = a.mixedMaterialize(root,p);return a.fail(err)};_ = os.RemoveAll(filepath.Join(p.GitDir,"transition"));return a.setEnvironment(root,p,name)
}

func (a App) pullMixed(root string,args []string) int {
    p,err:=a.lookup(root);if err!=nil{return a.fail(err)};if !a.mixedClean(root,p){return a.fail(fmt.Errorf("cannot pull: uncommitted changes"))};if code:=a.run(root,p.GitDir,"fetch","origin",remoteBranch(p,p.Environment));code!=0{return code};target:="FETCH_HEAD";removed,err:=a.checkoutPrepare(root,p,target);if err!=nil{return a.fail(err)};x:=append([]string{"merge","--ff-only",target},args...);if code:=a.run(root,p.GitDir,x...);code!=0{restorePrepared(root,p,removed);return code};if err:=a.mixedMaterialize(root,p);err!=nil{return a.fail(err)};_ = os.RemoveAll(filepath.Join(p.GitDir,"transition"));return 0
}
''')

# Patch legacy age helpers to read storage.toml first and avoid standalone .git creation.
p = root/'internal/lgit/age_store.go'
s = p.read_text()
s = s.replace('func readAgeFormat(root string) (ageFormatFile, error) {', 'func readAgeFormatLegacyOnly(root string) (ageFormatFile, error) {')
s = s.replace('f, err := readAgeFormat(root)', 'c, cerr := loadStorageConfig(root)\n\tif cerr == nil {\n\t\tif c.Encryption.Mode == "password" { return []age.Recipient{}, fmt.Errorf("password recipients are handled by storage policy") }\n\t\treturn readRecipients(root)\n\t}\n\tf, err := readAgeFormatLegacyOnly(root)', 1)
# The previous replacement only affects the old encryptionRecipients, which is no longer used. Patch decryption old call similarly if present.
s = s.replace('f, err := readAgeFormat(root)', 'f, err := readAgeFormatLegacyOnly(root)')
p.write_text(s)

# Replace App.Run, add v2 init/attach and root-aware dispatch while leaving legacy functions available.
p = root/'internal/lgit/app.go'
s = p.read_text()
start=s.index('func (a App) Run('); end=s.index('\nfunc (a App) help()',start)
run=r'''func (a App) Run(cwd string, args []string) int {
    if a.Stdout == nil { a.Stdout = os.Stdout }
    if a.Stderr == nil { a.Stderr = os.Stderr }
    explicit := ""
    if len(args) >= 2 && args[0] == "--root" { explicit = args[1]; args = args[2:] }
    if len(args) == 0 { return a.help() }
    switch args[0] {
    case "help", "--help", "-h": return a.help()
    case "version", "--version", "-v": fmt.Fprintln(a.Stdout,"lgit dev"); return 0
    case "data-dir": d,e:=DataDir();if e!=nil{return a.fail(e)};fmt.Fprintln(a.Stdout,d);return 0
    case "list": return a.list()
    case "key": root,_:=canonicalPath(cwd);return a.key(root,args[1:])
    case "init": return a.initV2(cwd, explicit, args[1:])
    }
    allowUnregistered := args[0] == "attach" || (args[0] == "remote" && len(args)>1 && args[1]=="set")
    root,err:=a.resolveRoot(cwd,explicit,allowUnregistered);if err!=nil{return a.fail(err)}
    switch args[0] {
    case "attach": return a.attachMixed(root,args[1:])
    case "remove": return a.remove(root)
    case "env": return a.env(root,args[1:])
    case "storage": return a.storageCommand(root,args[1:])
    case "remote": if len(args)>1&&args[1]=="set"{return a.remoteSet(root,args[2:])}
    case "push": return a.push(root,args[1:])
    case "pull": return a.pullMixed(root,args[1:])
    case "add": return a.mixedAdd(root,args[1:])
    case "status": return a.mixedStatus(root,args[1:])
    case "diff": return a.mixedDiff(root,args[1:])
    case "restore": return a.mixedRestore(root,args[1:])
    case "git": if len(args)==1{return a.fail(fmt.Errorf("usage: lgit git <git command>"))};return a.delegate(root,args[1:])
    }
    return a.delegate(root,args)
}
'''
s=s[:start]+run+s[end:]
# Help text.
help_start=s.index('func (a App) help()'); help_end=s.index('\nfunc (a App) paths()',help_start)
help=r'''func (a App) help() int {
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
'''
s=s[:help_start]+help+s[help_end:]
# env switch dispatch to mixed.
s=s.replace('return a.envSwitch(root, p, args[1])','return a.envSwitchMixed(root, p, args[1])')
# core.autocrlf false in remote config/init paths via configureRemote and new init.
s=s.replace('func (a App) configureRemote(root string, p Project, url string) int {','func (a App) configureRemote(root string, p Project, url string) int {\n\t_ = a.run(root, p.GitDir, "config", "core.autocrlf", "false")')
# lookup should tolerate case-normalized registered roots.
old='''\tp, ok := r.Projects[root]\n\tif !ok {\n\t\treturn Project{}, fmt.Errorf("project is not initialized; run \'lgit init\' or \'lgit attach --env NAME\'")\n\t}\n\treturn p, nil'''
new='''\tif p, ok := r.Projects[root]; ok { return p, nil }\n\tfor candidate, p := range r.Projects { if pathKey(candidate) == pathKey(root) { return p, nil } }\n\treturn Project{}, fmt.Errorf("project is not initialized; run 'lgit init' or 'lgit attach --env NAME'")'''
if old in s:s=s.replace(old,new)
# Append new init and attach implementation.
s += r'''

func parseInitV2(args []string) (env string,newProject bool,encryption string,backend StorageBackend,rootOverride string,err error) {
    encryption="identity";backend=StorageAge
    for i:=0;i<len(args);i++ {switch args[i]{
    case "--env":i++;if i>=len(args){err=fmt.Errorf("--env requires a name");return};env=args[i]
    case "--new-project":newProject=true
    case "--encryption":i++;if i>=len(args){err=fmt.Errorf("--encryption requires identity or password");return};encryption=strings.ToLower(args[i]);if encryption!="identity"&&encryption!="password"{err=fmt.Errorf("--encryption must be identity or password");return}
    case "--default":i++;if i>=len(args){err=fmt.Errorf("--default requires plain or age");return};backend=StorageBackend(strings.ToLower(args[i]));if !validBackend(backend){err=fmt.Errorf("unsupported storage backend %q",backend);return}
    case "--root":i++;if i>=len(args){err=fmt.Errorf("--root requires a path");return};rootOverride=args[i]
    default:err=fmt.Errorf("usage: lgit init [--root PATH] [--env NAME] [--new-project] [--default plain|age] [--encryption identity|password]");return}}
    if env==""{h,e:=os.Hostname();if e!=nil{err=e;return};env=h};env,err=validateName(env);return
}

func (a App) initV2(cwd,explicit string,args []string) int {
    env,newProject,encryption,backend,rootArg,err:=parseInitV2(args);if err!=nil{return a.fail(err)}
    if explicit!=""&&rootArg!=""{return a.fail(fmt.Errorf("specify --root only once"))};if rootArg!=""{explicit=rootArg}
    var root string
    standalone:=explicit!=""
    if explicit!=""{root,err=canonicalPath(explicit)}else{root,err=gitRoot(cwd)};if err!=nil{return a.fail(err)}
    if !standalone{standalone=!isGitWorkTreeRoot(root)}
    d,rp,err:=a.paths();if err!=nil{return a.fail(err)};r,err:=LoadRegistry(rp);if err!=nil{return a.fail(err)}
    for candidate,p:=range r.Projects{if pathKey(candidate)==pathKey(root){fmt.Fprintf(a.Stdout,"already initialized: %s (%s)\n",p.ID,p.Environment);return 0}}
    base:=slugify(filepath.Base(root));if base==""{return a.fail(fmt.Errorf("root folder name cannot be converted to a project slug"))}
    if r.Remote!=""&&!newProject{matches,err:=discover(r.Remote,base,"");if err!=nil{return a.fail(err)};if len(uniqueProjects(matches))>0{return a.fail(fmt.Errorf("remote project %q already exists; attach it or use --new-project",base))}}
    id,err:=newID();if err!=nil{return a.fail(err)};p:=Project{ID:id,Slug:base+"-"+id[:8],Environment:env,GitDir:filepath.Join(d,"repos",id),Standalone:standalone}
    if err:=os.MkdirAll(p.GitDir,0700);err!=nil{return a.fail(err)};cleanup:=true;defer func(){if cleanup{_ = os.RemoveAll(p.GitDir)}}()
    if code:=a.exec(root,"git","init","--bare",p.GitDir);code!=0{return code};if code:=a.run(root,p.GitDir,"symbolic-ref","HEAD","refs/heads/env/"+env);code!=0{return code};_ = a.run(root,p.GitDir,"config","status.showUntrackedFiles","no");_ = a.run(root,p.GitDir,"config","core.autocrlf","false");copyIdentity(root,a,p)
    if r.Remote!=""{if code:=a.configureRemote(root,p,r.Remote);code!=0{return code}}
    if err:=a.initStorage(root,p,backend,encryption);err!=nil{return a.fail(err)};r.Projects[root]=p;if err:=SaveRegistry(rp,r);err!=nil{return a.fail(err)};cleanup=false;fmt.Fprintf(a.Stdout,"initialized %s environment %s (%s default)\n",p.Slug,env,backend);return 0
}

func (a App) attachMixed(root string,args []string) int {
    o,err:=parseAttach(args);if err!=nil{return a.fail(err)};d,rp,err:=a.paths();if err!=nil{return a.fail(err)};r,err:=LoadRegistry(rp);if err!=nil{return a.fail(err)}
    for candidate:=range r.Projects{if pathKey(candidate)==pathKey(root){return a.fail(fmt.Errorf("project is already initialized"))}}
    if r.Remote==""{return a.fail(fmt.Errorf("shared remote is not configured; run 'lgit remote set URL'"))};base:=slugify(filepath.Base(root));matches,err:=discover(r.Remote,base,o.env);if err!=nil{return a.fail(err)};key,err:=resolveProject(matches,o.project);if err!=nil{return a.fail(err)};idpart:=strings.TrimPrefix(key,base+"-");if idpart==key||idpart==""{idpart,_=newID()};p:=Project{ID:idpart,Slug:key,Environment:o.env,GitDir:filepath.Join(d,"repos",idpart+"-"+fmt.Sprint(time.Now().UnixNano())),Standalone:!isGitWorkTreeRoot(root)}
    if err:=os.MkdirAll(p.GitDir,0700);err!=nil{return a.fail(err)};cleanup:=true;defer func(){if cleanup{_ = os.RemoveAll(p.GitDir)}}();if code:=a.exec(root,"git","init","--bare",p.GitDir);code!=0{return code};_ = a.run(root,p.GitDir,"config","core.autocrlf","false");copyIdentity(root,a,p);if code:=a.configureRemote(root,p,r.Remote);code!=0{return code};if code:=a.run(root,p.GitDir,"fetch","origin");code!=0{return code};ref:="refs/remotes/origin/envs/"+o.env
    logical,err:=logicalTrackedAt(root,p,ref);if err!=nil{return a.fail(err)};if !p.Standalone{var owned []string;for path:=range logical{if a.mainOwns(root,p,path){owned=append(owned,path)}};if len(owned)>0{sort.Strings(owned);return a.fail(fmt.Errorf("main repository already tracks: %s",strings.Join(owned,", ")))}}
    conflicts,err:=a.conflictsAt(root,p,ref);if err!=nil{return a.fail(err)};if len(conflicts)>0&&!o.keepLocal&&!o.useRemote{return a.fail(fmt.Errorf("local files differ from remote: %s; use --keep-local or --use-remote",strings.Join(conflicts,", ")))}
    saved:=map[string][]byte{};for path:=range logical{full:=filepath.Join(root,filepath.FromSlash(path));if b,e:=os.ReadFile(full);e==nil{saved[path]=b}}
    if o.useRemote&&len(conflicts)>0{if err:=backupFiles(d,p.ID,root,conflicts);err!=nil{return a.fail(err)}}
    // Remove target plain files before checkout so ignored/untracked local files cannot be overwritten silently.
    for path,b:=range logical{if b==StoragePlain{_ = os.Remove(filepath.Join(root,filepath.FromSlash(path)))}}
    if code:=a.run(root,p.GitDir,"checkout","-b","env/"+o.env,ref);code!=0{for path,b:=range saved{dst:=filepath.Join(root,filepath.FromSlash(path));_ = os.MkdirAll(filepath.Dir(dst),0700);_ = os.WriteFile(dst,b,0600)};return code}
    if !p.Standalone{if err:=excludeLgitV2(root);err!=nil{return a.fail(err)}};if err:=a.mixedMaterialize(root,p);err!=nil{return a.fail(err)}
    if o.keepLocal{for path,b:=range saved{dst:=filepath.Join(root,filepath.FromSlash(path));_ = os.MkdirAll(filepath.Dir(dst),0700);if err:=os.WriteFile(dst,b,0600);err!=nil{return a.fail(err)}}}
    _ = a.run(root,p.GitDir,"config","status.showUntrackedFiles","no");r.Projects[root]=p;if err:=SaveRegistry(rp,r);err!=nil{return a.fail(err)};cleanup=false;fmt.Fprintf(a.Stdout,"attached %s environment %s\n",key,o.env);return 0
}

func excludeLgitV2(root string) error {
    p:=filepath.Join(root,".git","info","exclude");b,_:=os.ReadFile(p);if strings.Contains(string(b),"\n.lgit/\n")||strings.HasSuffix(string(b),".lgit/\n"){return nil};f,err:=os.OpenFile(p,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{return err};defer f.Close();_,err=io.WriteString(f,"\n.lgit/\n");return err
}
'''
p.write_text(s)

# Add focused tests for mixed storage, migrations, standalone roots, nested roots and CRLF byte preservation.
p = root/'internal/lgit/storage_policy_test.go'
p.write_text(r'''package lgit

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestMixedPlainAndAgeStorage(t *testing.T) {
    dir:=t.TempDir();initMain(t,dir);t.Setenv("LGIT_DATA_DIR",filepath.Join(t.TempDir(),"data"));t.Setenv("LGIT_PASSWORD","correct horse battery staple")
    appRun(t,App{},dir,"init","--env","pc","--default","plain","--encryption","password")
    os.WriteFile(filepath.Join(dir,"local.txt"),[]byte("public\n"),0600);os.WriteFile(filepath.Join(dir,".env"),[]byte("SECRET=42\n"),0600)
    appRun(t,App{},dir,"storage","set",".env","age");appRun(t,App{},dir,"add","local.txt",".env");appRun(t,App{},dir,"commit","-m","mixed")
    p,_:=App{}.lookup(dir);plain,_:=gitBlob(dir,p.GitDir,"HEAD:local.txt");if string(plain)!="public\n"{t.Fatalf("plain=%q",plain)};cipher,_:=gitBlob(dir,p.GitDir,"HEAD:.lgit/store/.env.age");if bytes.Contains(cipher,[]byte("SECRET=42")){t.Fatal("remote age blob contains plaintext")}
    out:=appRun(t,App{},dir,"status");if strings.TrimSpace(out)!=""{t.Fatalf("dirty: %q",out)}
}

func TestStorageMigrationPlainAgePlain(t *testing.T) {
    dir:=t.TempDir();initMain(t,dir);t.Setenv("LGIT_DATA_DIR",filepath.Join(t.TempDir(),"data"));t.Setenv("LGIT_PASSWORD","pw")
    appRun(t,App{},dir,"init","--env","pc","--default","plain","--encryption","password");os.WriteFile(filepath.Join(dir,"x.txt"),[]byte("hello\n"),0600);appRun(t,App{},dir,"add","x.txt");appRun(t,App{},dir,"commit","-m","plain")
    appRun(t,App{},dir,"storage","set","x.txt","age");appRun(t,App{},dir,"commit","-m","age");p,_:=App{}.lookup(dir);if _,err:=gitBlob(dir,p.GitDir,"HEAD:x.txt");err==nil{t.Fatal("plain representation remained")};if _,err:=gitBlob(dir,p.GitDir,"HEAD:.lgit/store/x.txt.age");err!=nil{t.Fatal(err)}
    appRun(t,App{},dir,"storage","set","x.txt","plain");appRun(t,App{},dir,"commit","-m","plain again");if b,err:=gitBlob(dir,p.GitDir,"HEAD:x.txt");err!=nil||string(b)!="hello\n"{t.Fatalf("plain restore %q %v",b,err)}
}

func TestChangingDefaultDoesNotMigrateTrackedFile(t *testing.T) {
    dir:=t.TempDir();initMain(t,dir);t.Setenv("LGIT_DATA_DIR",filepath.Join(t.TempDir(),"data"));appRun(t,App{},dir,"init","--env","pc","--default","plain");os.WriteFile(filepath.Join(dir,"x"),[]byte("x"),0600);appRun(t,App{},dir,"add","x");appRun(t,App{},dir,"commit","-m","x");appRun(t,App{},dir,"storage","default","age");p,_:=App{}.lookup(dir);if b,ok,_:=currentBackend(dir,p,"x");!ok||b!=StoragePlain{t.Fatalf("backend=%s tracked=%v",b,ok)}
}

func TestStandaloneRootAndNearestNestedProject(t *testing.T) {
    data:=filepath.Join(t.TempDir(),"data");t.Setenv("LGIT_DATA_DIR",data);home:=filepath.Join(t.TempDir(),"home");os.MkdirAll(home,0700);appRun(t,App{},home,"init","--root",home,"--env","desk","--default","plain")
    os.WriteFile(filepath.Join(home,".gitconfig"),[]byte("[user]\n"),0600);appRun(t,App{},home,"add",".gitconfig")
    child:=filepath.Join(home,"code","Booking");os.MkdirAll(child,0700);initMain(t,child);appRun(t,App{},child,"init","--env","pc")
    os.WriteFile(filepath.Join(child,".env"),[]byte("CHILD=1\n"),0600);appRun(t,App{},child,"add",".env")
    out:=appRun(t,App{},filepath.Join(child,"."),"status");if !strings.Contains(out,".env"){t.Fatalf("nearest root not child: %q",out)}
    var stdout,stderr bytes.Buffer;a:=App{Stdout:&stdout,Stderr:&stderr};if code:=a.Run(home,[]string{"add","code/Booking/.env"});code==0||!strings.Contains(stderr.String(),"nested lgit project"){t.Fatalf("code=%d err=%q",code,stderr.String())}
}

func TestStandaloneRecursiveAddStopsAtNestedGitRepo(t *testing.T) {
    data:=filepath.Join(t.TempDir(),"data");t.Setenv("LGIT_DATA_DIR",data);home:=filepath.Join(t.TempDir(),"home");os.MkdirAll(filepath.Join(home,".config"),0700);os.WriteFile(filepath.Join(home,".config","a"),[]byte("a"),0600);nested:=filepath.Join(home,"src","repo");os.MkdirAll(nested,0700);initMain(t,nested);os.WriteFile(filepath.Join(nested,"secret"),[]byte("no"),0600)
    appRun(t,App{},home,"init","--root",home,"--env","desk","--default","plain");appRun(t,App{},home,"add",".");p,_:=App{}.lookup(home);out,_:=gitOutput(home,p.GitDir,"ls-files");if !strings.Contains(out,".config/a")||strings.Contains(out,"src/repo/secret"){t.Fatalf("tracked=%q",out)}
}

func TestPlainStoragePreservesCRLFBytes(t *testing.T) {
    dir:=t.TempDir();initMain(t,dir);t.Setenv("LGIT_DATA_DIR",filepath.Join(t.TempDir(),"data"));appRun(t,App{},dir,"init","--env","pc","--default","plain");want:=[]byte("A=1\r\nB=2\r\n");os.WriteFile(filepath.Join(dir,"local.env"),want,0600);appRun(t,App{},dir,"add","local.env");appRun(t,App{},dir,"commit","-m","crlf");p,_:=App{}.lookup(dir);got,err:=gitBlob(dir,p.GitDir,"HEAD:local.env");if err!=nil||!bytes.Equal(got,want){t.Fatalf("got=%q err=%v",got,err)}
}
''')

# Remove one-off migration script itself after successful application.
Path('scripts/implement_storage_plan.py').unlink()
