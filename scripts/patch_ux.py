from pathlib import Path

ux = Path('internal/lgit/ux.go')
s = ux.read_text()
old = '''\tfor rel := range logical {
\t\tparts := strings.Split(filepath.ToSlash(rel), "/")
\t\tfor i := 1; i < len(parts); i++ {
\t\t\tancestor := strings.Join(parts[:i], "/")
\t\t\tinfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(ancestor)))
\t\t\tif os.IsNotExist(err) {
\t\t\t\tbreak
\t\t\t}
\t\t\tif err != nil {
\t\t\t\treturn nil, err
\t\t\t}
\t\t\tif !info.IsDir() {
\t\t\t\tout = addUniqueOuter(out, ancestor)
\t\t\t\tbreak
\t\t\t}
\t\t}
\t\tinfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
'''
new = '''\tfor rel := range logical {
\t\tparts := strings.Split(filepath.ToSlash(rel), "/")
\t\tblocked := false
\t\tfor i := 1; i < len(parts); i++ {
\t\t\tancestor := strings.Join(parts[:i], "/")
\t\t\tinfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(ancestor)))
\t\t\tif os.IsNotExist(err) {
\t\t\t\tbreak
\t\t\t}
\t\t\tif err != nil {
\t\t\t\treturn nil, err
\t\t\t}
\t\t\tif !info.IsDir() {
\t\t\t\tout = addUniqueOuter(out, ancestor)
\t\t\t\tblocked = true
\t\t\t\tbreak
\t\t\t}
\t\t}
\t\tif blocked {
\t\t\tcontinue
\t\t}
\t\tinfo, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
'''
if old not in s and new not in s:
    raise SystemExit('structural conflict anchor not found')
if old in s:
    s = s.replace(old, new, 1)

old = '''\tcontent, err := contentConflictsAt(root, p, ref, logical, config, id)
\tif err != nil {
\t\treturn a.fail(err)
\t}
\tstructural, err := structuralConflicts(root, logical)
\tif err != nil {
\t\treturn a.fail(err)
\t}
'''
new = '''\tstructural, err := structuralConflicts(root, logical)
\tif err != nil {
\t\treturn a.fail(err)
\t}
\tcontentLogical := logicalWithoutStructural(logical, structural)
\tcontent, err := contentConflictsAt(root, p, ref, contentLogical, config, id)
\tif err != nil {
\t\treturn a.fail(err)
\t}
'''
if old not in s and new not in s:
    raise SystemExit('attach preflight ordering anchor not found')
if old in s:
    s = s.replace(old, new, 1)
ux.write_text(s)

test = Path('internal/lgit/ux_test.go')
s = test.read_text()
old = '''\tappRun(t, App{}, original, "init", "--root", original, "--env", "windows", "--default", "plain")
\tappRun(t, App{}, original, "remote", "set", remote)
'''
new = '''\tappRun(t, App{}, original, "init", "--root", original, "--env", "windows", "--default", "plain")
\tappRun(t, App{}, original, "git", "config", "user.name", "Test")
\tappRun(t, App{}, original, "git", "config", "user.email", "test@example.com")
\tappRun(t, App{}, original, "remote", "set", remote)
'''
if old not in s and new not in s:
    raise SystemExit('test identity anchor not found')
if old in s:
    s = s.replace(old, new, 1)
test.write_text(s)
