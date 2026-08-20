package lgit

// RunUX is kept only as a source-level compatibility wrapper during pre-stable
// development. The production CLI and new tests use RunCLI directly.
func (a App) RunUX(cwd string, args []string) int {
	return a.RunCLI(cwd, args)
}
