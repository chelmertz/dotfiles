// claude-review starts, or resumes, a Claude Code review session for a
// GitHub pull request in its local clone. See README.md.
package main

import "os"

// exit is os.Exit, replaced in tests.
var exit = os.Exit

func main() {
	exit(run(os.Args[1:], realDeps()))
}
