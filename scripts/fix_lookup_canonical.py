from pathlib import Path

p = Path('internal/lgit/app.go')
s = p.read_text()
old = '''func (a App) lookup(root string) (Project, error) {
\tr, e := a.registry()
\tif e != nil {
\t\treturn Project{}, e
\t}
\tif p, ok := r.Projects[root]; ok {
\t\treturn p, nil
\t}
\tfor candidate, p := range r.Projects {
\t\tif pathKey(candidate) == pathKey(root) {
\t\t\treturn p, nil
\t\t}
\t}
\treturn Project{}, fmt.Errorf("project is not initialized; run 'lgit init' or 'lgit attach --env NAME'")
}
'''
new = '''func (a App) lookup(root string) (Project, error) {
\tr, e := a.registry()
\tif e != nil {
\t\treturn Project{}, e
\t}
\tif p, ok := r.Projects[root]; ok {
\t\treturn p, nil
\t}
\tcanonicalRoot, err := canonicalPath(root)
\tif err != nil {
\t\tcanonicalRoot = filepath.Clean(root)
\t}
\tfor candidate, p := range r.Projects {
\t\tcanonicalCandidate, err := canonicalPath(candidate)
\t\tif err != nil {
\t\t\tcanonicalCandidate = filepath.Clean(candidate)
\t\t}
\t\tif pathKey(canonicalCandidate) == pathKey(canonicalRoot) {
\t\t\treturn p, nil
\t\t}
\t}
\treturn Project{}, fmt.Errorf("project is not initialized; run 'lgit init' or 'lgit attach --env NAME'")
}
'''
if old not in s:
    raise SystemExit('lookup target not found')
p.write_text(s.replace(old, new, 1))
Path('scripts/fix_lookup_canonical.py').unlink()
