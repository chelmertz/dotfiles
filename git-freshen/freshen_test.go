package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate keeps the machine's git configuration out of the fixtures: a global
// setting such as fetch.prune or a rewritten URL would otherwise decide what
// these tests see.
func isolate(t *testing.T) {
	t.Helper()
	// A failure, not a skip: these are git-freshen's only tests, and a build
	// that skipped them would pass having proved nothing.
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := gitq(dir, args...)
	return out
}

// world is one origin and a clone per case. Origin moves on after the clones
// are made, so "behind" is the default state and each case differs only in
// what its clone did locally. Every remote is a path on disk, so the real
// fetch path runs without a network.
type world struct {
	home, roots, origin string
}

func (w world) repo(name string) string { return filepath.Join(w.roots, name) }

func newWorld(t *testing.T) world {
	t.Helper()
	isolate(t)
	home := t.TempDir()
	w := world{home: home, roots: filepath.Join(home, "roots"), origin: filepath.Join(home, "origin.git")}
	if err := os.MkdirAll(w.roots, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, home, "init", "-q", "--bare", w.origin)
	seed := filepath.Join(home, "seed")
	gitT(t, home, "clone", "-q", w.origin, seed)
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "base")
	gitT(t, seed, "push", "-q", "origin", "main")

	early := []string{"behind", "dirty", "diverged", "detached", "noupstream", "onfeature", "mainahead", "inrebase", "busy"}
	for _, name := range early {
		gitT(t, home, "clone", "-q", w.origin, w.repo(name))
	}
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "upstream-1")
	gitT(t, seed, "push", "-q", "origin", "main")
	for _, name := range early {
		gitT(t, w.repo(name), "fetch", "-q", "origin")
	}

	// Cloned after origin moved, so it is level with upstream and its one
	// local commit makes it purely ahead - reported, never pushed.
	gitT(t, home, "clone", "-q", w.origin, w.repo("ahead"))
	gitT(t, w.repo("ahead"), "commit", "-q", "--allow-empty", "-m", "local-only")

	// Staged but uncommitted.
	write(t, filepath.Join(w.repo("dirty"), "tracked.txt"))
	gitT(t, w.repo("dirty"), "add", "tracked.txt")
	gitT(t, w.repo("dirty"), "commit", "-q", "-m", "tracked")
	gitT(t, w.repo("dirty"), "reset", "-q", "--soft", "HEAD~1")

	gitT(t, w.repo("diverged"), "commit", "-q", "--allow-empty", "-m", "local-only")
	gitT(t, w.repo("detached"), "checkout", "-q", "--detach", "HEAD")
	gitT(t, w.repo("noupstream"), "checkout", "-q", "-b", "orphan-branch")
	gitT(t, w.repo("onfeature"), "checkout", "-q", "-b", "feature")
	gitT(t, w.repo("onfeature"), "commit", "-q", "--allow-empty", "-m", "feature-work")

	// Local main has a commit origin does not, and is not checked out. This is
	// the case that separates a fast-forward from a reset: update-ref would
	// happily move main back onto origin/main and drop the commit, so the
	// ancestry proof is all that stands between this repo and data loss.
	gitT(t, w.repo("mainahead"), "commit", "-q", "--allow-empty", "-m", "main-only")
	gitT(t, w.repo("mainahead"), "checkout", "-q", "-b", "feature")

	// A branch whose remote counterpart was deleted - what every merged PR
	// leaves behind. git reports it only on stderr while still printing
	// "@{u}" on stdout, so it is easy to read as a working upstream.
	gitT(t, home, "clone", "-q", w.origin, w.repo("gonebranch"))
	gitT(t, w.repo("gonebranch"), "checkout", "-q", "-b", "feature-merged")
	gitT(t, w.repo("gonebranch"), "push", "-q", "-u", "origin", "feature-merged")
	gitT(t, w.origin, "update-ref", "-d", "refs/heads/feature-merged")

	// An interrupted rebase leaves rebase-merge behind; making the directory
	// is how git itself detects one, and far steadier than racing a conflict.
	if err := os.Mkdir(filepath.Join(w.repo("inrebase"), ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w world) run(t *testing.T, dryRun bool) map[string]record {
	t.Helper()
	cfg := config{dryRun: dryRun, home: w.home, now: time.Now(), timeout: 30 * time.Second}
	return cfg.all(discover([]string{w.roots}), 4)
}

func (w world) rec(t *testing.T, recs map[string]record, name string) record {
	t.Helper()
	r, ok := recs["roots/"+name]
	if !ok {
		t.Fatalf("no record for %s in %v", name, recs)
	}
	return r
}

// The rule git-freshen exists to keep is "no write that is not a proven
// fast-forward", so most of this asserts what it declines to do. Every
// comparison is against state captured before the run, so none can pass by
// comparing a value to itself.
func TestFreshen(t *testing.T) {
	w := newWorld(t)
	beforeBehind := gitOut(t, w.repo("behind"), "rev-parse", "HEAD")
	beforeFeatureMain := gitOut(t, w.repo("onfeature"), "rev-parse", "main")
	beforeMainAhead := gitOut(t, w.repo("mainahead"), "rev-parse", "main")
	originMain := gitOut(t, w.origin, "rev-parse", "main")

	t.Run("dry run", func(t *testing.T) {
		recs := w.run(t, true)
		for _, r := range recs {
			if n := len(strings.Split(r.String(), "\t")); n != 9 {
				t.Errorf("%d fields: %q", n, r.String())
			}
		}
		if r := w.rec(t, recs, "behind"); r.status != "would-ff" {
			t.Errorf("behind: %+v", r)
		}
		if r := w.rec(t, recs, "onfeature"); r.moved != "main" {
			t.Errorf("dry run should name the ref it would move: %+v", r)
		}
		if got := gitOut(t, w.repo("behind"), "rev-parse", "HEAD"); got != beforeBehind {
			t.Error("dry run moved a checkout")
		}
		if got := gitOut(t, w.repo("onfeature"), "rev-parse", "main"); got != beforeFeatureMain {
			t.Error("dry run wrote a ref")
		}
	})

	// A process whose cwd is inside busy: the occupancy gate must see it.
	sleeper := exec.Command("sleep", "30")
	sleeper.Dir = w.repo("busy")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	recs := w.run(t, false)
	_ = sleeper.Process.Kill()
	_ = sleeper.Wait()

	for _, c := range []struct{ name, status, note, moved string }{
		{"behind", "fast-forwarded", "", "main"},
		{"dirty", "blocked", "uncommitted", "-"},
		{"inrebase", "blocked", "in progress", "-"},
		{"busy", "blocked", "working in it", "-"},
		{"diverged", "diverged", "", "-"},
		{"ahead", "unpushed", "", "-"},
		{"gonebranch", "upstream-gone", "", "-"},
		{"noupstream", "no-upstream", "", "main"},
		{"detached", "detached", "", "main"},
		// main is ahead of origin/main and not checked out: update-ref is
		// otherwise free to run, and would reset it.
		{"mainahead", "no-upstream", "", "-"},
	} {
		r := w.rec(t, recs, c.name)
		if r.status != c.status || !strings.Contains(r.note, c.note) || r.moved != c.moved {
			t.Errorf("%s: got %+v, want status %s, note containing %q, moved %s", c.name, r, c.status, c.note, c.moved)
		}
	}
	if r := w.rec(t, recs, "diverged"); r.ahead != "1" || r.behind != "1" {
		t.Errorf("diverged should count both directions: %+v", r)
	}
	if r := w.rec(t, recs, "detached"); r.branch != "-" {
		t.Errorf("a detached HEAD has no branch: %+v", r)
	}
	if r := w.rec(t, recs, "behind"); r.fetch != "ok" {
		t.Errorf("a local fetch should be ok: %+v", r)
	}

	// The writes that must not have happened.
	if !strings.Contains(gitOut(t, w.repo("dirty"), "status", "--porcelain"), "tracked.txt") {
		t.Error("dirty tree was cleaned")
	}
	if got := gitOut(t, w.repo("diverged"), "log", "-1", "--format=%s"); got != "local-only" {
		t.Errorf("diverged head moved to %q", got)
	}
	if got := gitOut(t, w.repo("detached"), "symbolic-ref", "--quiet", "--short", "HEAD"); got != "" {
		t.Errorf("detached HEAD now on %q", got)
	}
	if got := gitOut(t, w.repo("gonebranch"), "status", "--porcelain"); got != "" {
		t.Errorf("a gone upstream changed the tree: %q", got)
	}
	if got := gitOut(t, w.origin, "rev-parse", "main"); got != originMain {
		t.Error("something was pushed")
	}

	// The update-ref half: on a feature branch, local main advances to
	// origin's tip without the checkout moving.
	if got, want := gitOut(t, w.repo("onfeature"), "rev-parse", "main"), gitOut(t, w.repo("onfeature"), "rev-parse", "origin/main"); got != want || got == beforeFeatureMain {
		t.Errorf("local main %s, origin/main %s, before %s", got, want, beforeFeatureMain)
	}
	if got := gitOut(t, w.repo("onfeature"), "log", "-1", "--format=%s"); got != "feature-work" {
		t.Errorf("the feature branch moved to %q", got)
	}
	if got := gitOut(t, w.repo("mainahead"), "rev-parse", "main"); got != beforeMainAhead {
		t.Error("a local main ahead of origin was reset")
	}
}

// The first real run put a GTK "Username for 'https://git.heroku.com'" dialog
// on screen, because GIT_TERMINAL_PROMPT=0 shuts only the terminal door and
// git prefers an askpass helper for HTTPS credentials. A timer has nobody to
// answer that. Asserting it end to end would need a server that returns 401,
// so this asserts one level down, through run() so the wiring is covered too:
// a `git` shim first on PATH records the environment it was handed. The
// ambient askpass values are what a desktop session supplies, and must not
// survive.
func TestEveryGitIsNonInteractive(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim, log := filepath.Join(tmp, "shim"), filepath.Join(tmp, "env")
	if err := os.Mkdir(shim, 0o755); err != nil {
		t.Fatal(err)
	}
	// /bin/sh, not /usr/bin/env: the nix sandbox has the former only.
	script := "#!/bin/sh\n{\n" +
		"  echo \"GIT_ASKPASS=${GIT_ASKPASS-unset}\"\n" +
		"  echo \"GIT_TERMINAL_PROMPT=${GIT_TERMINAL_PROMPT-unset}\"\n" +
		"  echo \"SSH_ASKPASS_REQUIRE=${SSH_ASKPASS_REQUIRE-unset}\"\n" +
		"  echo \"SSH_ASKPASS=${SSH_ASKPASS-unset}\"\n" +
		"  echo \"GIT_SSH_COMMAND=${GIT_SSH_COMMAND-unset}\"\n" +
		"} > " + log + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// t.Setenv on every key run() touches, so each is restored afterwards.
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_ASKPASS", "/desktop/gui-askpass")
	t.Setenv("SSH_ASKPASS", "/desktop/ssh-askpass")
	t.Setenv("GIT_TERMINAL_PROMPT", "")
	t.Setenv("SSH_ASKPASS_REQUIRE", "")
	t.Setenv("GIT_SSH_COMMAND", "")

	run([]string{repo})

	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("git was never run: %v", err)
	}
	seen := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		seen[k] = v
	}
	if v := seen["GIT_ASKPASS"]; v == "/desktop/gui-askpass" || !strings.HasSuffix(v, "false") {
		t.Errorf("GIT_ASKPASS=%q, want a helper that refuses", v)
	}
	for k, want := range map[string]string{"GIT_TERMINAL_PROMPT": "0", "SSH_ASKPASS_REQUIRE": "never", "SSH_ASKPASS": "unset"} {
		if seen[k] != want {
			t.Errorf("%s=%q, want %q", k, seen[k], want)
		}
	}
	if !strings.Contains(seen["GIT_SSH_COMMAND"], "-oBatchMode=yes") {
		t.Errorf("GIT_SSH_COMMAND=%q lets ssh prompt", seen["GIT_SSH_COMMAND"])
	}
}
