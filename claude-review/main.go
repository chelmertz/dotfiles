// claude-review starts, or resumes, a Claude Code review session for a
// GitHub pull request in its local clone. See README.md.
package main

import (
	"os"
	"path/filepath"
	"time"
)

// exit is os.Exit, replaced in tests.
var exit = os.Exit

func main() {
	exit(run(os.Args[1:], realDeps()))
}

// realDeps wires Deps to the host. It is the one place that reads the
// environment (.golangci.yml's forbidigo rule holds everything else to
// that); every other function takes what it needs from Deps or arguments.
func realDeps() Deps {
	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	// "" on error, which cacheDir refuses: no usable cache directory.
	cacheBase, _ := os.UserCacheDir()
	dirs := resolveDirs(home, os.Getenv)
	h := osHost{environ: os.Environ(), cacheBase: cacheBase}
	return Deps{
		GOOS:           goos(),
		UserConfigDir:  cfgDir,
		Home:           home,
		ClaudeDir:      dirs.claude,
		StateDir:       dirs.state,
		RuntimeDir:     dirs.runtime,
		Environ:        h.environ,
		IsTTY:          stdinIsTerminal(),
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		FindRepo:       FindRepo,
		SessionExists:  SessionExists,
		SessionRunning: SessionRunning,
		Lock:           h.lockUnderCache,
		Sleep:          time.Sleep,
		Exec:           h.execAttached,
		Output:         h.execOutput,
		Capture:        h.execStdout,
		Spawn:          h.spawnDetached,
		WriteScript:    h.writeLauncherScript,
		Gh:             ghRun,
		Sessions:       realSessions(dirs.claude, goos(), h.execOutput),
		Trust:          func(dir string) error { return ensureTrusted(dirs.claudeJSON, dir) },
	}
}

// hostDirs are the directories realDeps takes from the environment.
type hostDirs struct{ claude, claudeJSON, state, runtime string }

// resolveDirs applies the fallbacks: an unset or empty variable means the
// default under home.
func resolveDirs(home string, getenv func(string) string) hostDirs {
	configEnv := getenv("CLAUDE_CONFIG_DIR")
	d := hostDirs{
		claude:     configEnv,
		claudeJSON: claudeJSONPath(home, configEnv),
		state:      getenv("XDG_STATE_HOME"),
		runtime:    getenv("XDG_RUNTIME_DIR"),
	}
	if d.claude == "" {
		d.claude = filepath.Join(home, ".claude")
	}
	if d.state == "" {
		d.state = filepath.Join(home, ".local", "state")
	}
	return d
}
