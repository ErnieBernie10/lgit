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
if old in s:
    s = s.replace(old, new, 1)
elif new not in s:
    raise SystemExit('structural conflict anchor not found')

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
if old in s:
    s = s.replace(old, new, 1)
elif new not in s:
    raise SystemExit('attach preflight ordering anchor not found')

old = '''\trollback := filepath.Join(p.GitDir, "attach-rollback")
\tvar mutationPaths []string
\tfor rel := range logical {
\t\tmutationPaths = append(mutationPaths, rel)
\t}
\tmutationPaths = mergePaths(mutationPaths, structural, []string{".lgit"})
'''
new = '''\trollback := filepath.Join(p.GitDir, "attach-rollback")
\tvar mutationPaths []string
\trollbackLogical := logicalWithoutStructural(logical, structural)
\tfor rel := range rollbackLogical {
\t\tmutationPaths = append(mutationPaths, rel)
\t}
\tmutationPaths = mergePaths(mutationPaths, structural, []string{".lgit"})
'''
if old in s:
    s = s.replace(old, new, 1)
elif new not in s:
    raise SystemExit('rollback snapshot anchor not found')

old = '''\tdefer func() {
\t\tif !applied {
\t\t\trestoreSnapshot(root, rollback, logical, structural)
\t\t}
\t}()
'''
new = '''\tdefer func() {
\t\tif !applied {
\t\t\trestoreSnapshot(root, rollback, rollbackLogical, structural)
\t\t}
\t}()
'''
if old in s:
    s = s.replace(old, new, 1)
elif new not in s:
    raise SystemExit('rollback restore anchor not found')
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
if old in s:
    s = s.replace(old, new, 1)
elif new not in s:
    raise SystemExit('test identity anchor not found')
test.write_text(s)
