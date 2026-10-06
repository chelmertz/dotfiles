// git-freshen fetches every clone under the work roots, fast-forwards what can
// be fast-forwarded, and removes linked worktrees that are finished with, so
// entering a repo never starts with `git fetch` and a decision, and finished
// worktrees do not pile up on disk.
//
// It never rewrites history. No rebase, no merge commit, no push, no stash, no
// reset: every branch write is either `merge --ff-only` or an update-ref that
// has been proven a fast-forward first. Anything that would need judgement - a
// diverged branch, a dirty tree, an interrupted rebase - is reported and left
// alone. That rule is the whole design. A background job that rebases can leave
// a repo mid-conflict, and a repo you find mid-conflict is worse than one you
// find three commits behind.
//
// Three separate writes, because they solve different problems:
//
//  1. the checked-out branch, when it is clean and strictly behind upstream
//  2. local main/master, when it is *not* checked out anywhere - advanced by
//     update-ref, which touches no working tree at all, so a feature branch
//     you are sitting on still gets a fresh main to rebase onto later
//  3. a linked worktree that is clean, merged, idle and unoccupied - removed
//     with `git worktree remove`, which keeps its branch; see prune.go
//
// Auth is non-interactive by construction (BatchMode, no terminal prompt, a
// timeout per fetch): a timer must never hang on a passphrase nobody can type.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const usage = `git-freshen [--dry-run] [ROOT...]

Fetch every clone under the roots, fast-forward what is provably a
fast-forward, and remove finished linked worktrees. Nothing else is written.

--dry-run still fetches. Fetching writes only remote-tracking refs and can
change no working tree, so a dry run that skipped it would report stale
behind-counts, which is the one thing the report is for. What --dry-run
withholds is every local-branch write and every worktree removal. It is the
only flag; the two machine-dependent knobs are environment variables
(GIT_FRESHEN_JOBS, default 8, and GIT_FRESHEN_TIMEOUT in seconds, default 45).

Stdout is one tab-separated record per checkout and nothing else; stderr
carries the header and the tally.

  status  path  branch  ahead  behind  upstream  fetch  moved  note

Every field is present on every line; "-" means not applicable. path is
relative to $HOME. moved names the refs this run advanced (or would, under
--dry-run). Pipe through column -t to read it.

status is one of:
  fast-forwarded  the checked-out branch was advanced to its upstream
  would-ff        it could have been, and --dry-run held the write back
  current         nothing to do
  unpushed        local commits are not on the remote yet
  diverged        ahead *and* behind; needs a rebase you must do yourself
  blocked         behind, but dirty / mid-rebase / someone is working in it
  upstream-gone   tracks a branch deleted from the remote, usually a merged PR
  no-upstream     tracks nothing
  detached        no branch checked out
  pruned          a finished linked worktree was removed (its branch is kept)
  would-prune     it would have been, and --dry-run held the removal back
  failed          the write was attempted and git refused it
  unknown         git could not answer; the note says what was asked
  skip            not a usable checkout
`

// maxDepth matches the `find -maxdepth 5` this replaced: deep enough for
// ~/p/<ns>/<project>/<clone>/.git, shallow enough not to crawl node_modules.
const maxDepth = 5

var defaultRoots = []string{"code/github", "code/gitlab", "code/matchi", "p"}

type config struct {
	dryRun  bool
	timeout time.Duration
	home    string
	now     time.Time
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	home, _ := os.UserHomeDir()
	cfg := config{home: home, now: time.Now(), timeout: 45 * time.Second}
	if n, err := strconv.Atoi(os.Getenv("GIT_FRESHEN_TIMEOUT")); err == nil && n > 0 {
		cfg.timeout = time.Duration(n) * time.Second
	}
	jobs := 8
	if n, err := strconv.Atoi(os.Getenv("GIT_FRESHEN_JOBS")); err == nil && n > 0 {
		jobs = n
	}

	var roots []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--dry-run":
			cfg.dryRun = true
		case a == "--help" || a == "-h":
			fmt.Print(usage)
			return 0
		case a == "--":
			roots = append(roots, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			return die("unknown flag %s; the only flag is --dry-run (see --help)", a)
		default:
			roots = append(roots, a)
		}
	}
	if len(roots) == 0 {
		for _, r := range defaultRoots {
			roots = append(roots, filepath.Join(home, r))
		}
	}
	var present []string
	for _, r := range roots {
		if info, err := os.Stat(r); err == nil && info.IsDir() {
			present = append(present, r)
		}
	}
	if len(present) == 0 {
		return die("none of the roots exist: %s", strings.Join(roots, " "))
	}
	repos := discover(present)
	if len(repos) == 0 {
		return die("no checkouts found under %s", strings.Join(present, " "))
	}

	noninteractiveAuth()
	var shown []string
	for _, r := range present {
		shown = append(shown, cfg.rel(r))
	}
	suffix := ""
	if cfg.dryRun {
		suffix = " - dry run, no local branch will move"
	}
	fmt.Fprintf(os.Stderr, "git-freshen: %d checkouts under %s%s\n", len(repos), strings.Join(shown, " "), suffix)

	recs := cfg.all(repos, jobs)

	// Sorted so two runs can be diffed, and so the statuses needing a human
	// group together above the "current" bulk.
	var lines []string
	tally := map[string]int{}
	fetchFailed := 0
	for _, r := range recs {
		lines = append(lines, r.String())
		tally[r.status]++
		if r.fetch == "failed" {
			fetchFailed++
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
	statuses := make([]string, 0, len(tally))
	for s := range tally {
		statuses = append(statuses, s)
	}
	sort.Slice(statuses, func(i, j int) bool {
		if tally[statuses[i]] != tally[statuses[j]] {
			return tally[statuses[i]] > tally[statuses[j]]
		}
		return statuses[i] < statuses[j]
	})
	fmt.Fprintln(os.Stderr)
	for _, s := range statuses {
		fmt.Fprintf(os.Stderr, "  %-15s %d\n", s, tally[s])
	}
	if fetchFailed > 0 {
		fmt.Fprintf(os.Stderr, "  %-15s %d\n", "fetch-failed", fetchFailed)
	}
	return 0
}

// all runs both passes. Pruning waits for every fetch, because "merged" is
// read from remote-tracking refs the first pass refreshes, and because a
// worktree must not be removed while another worker is fast-forwarding it. A
// clone whose fetch failed prunes nothing that run: its refs are stale, and
// merged-ness read from stale refs is a guess.
func (c config) all(repos []string, jobs int) map[string]record {
	recs := map[string]record{}
	var mu sync.Mutex
	add := func(r record) { mu.Lock(); recs[r.path] = r; mu.Unlock() }
	forEach(repos, jobs, func(repo string) { add(c.freshen(repo)) })
	fetched := map[string]bool{}
	for _, repo := range repos {
		fetched[repo] = recs[c.rel(repo)].fetch == "ok"
	}
	forEach(repos, jobs, func(repo string) {
		if !fetched[repo] {
			return
		}
		for _, r := range c.prune(repo) {
			add(r)
		}
	})
	return recs
}

func die(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "git-freshen: "+format+"\n", a...)
	return 2
}

func forEach(items []string, jobs int, f func(string)) {
	ch := make(chan string)
	var wg sync.WaitGroup
	for range jobs {
		wg.Go(func() {
			for it := range ch {
				f(it)
			}
		})
	}
	for _, it := range items {
		ch <- it
	}
	close(ch)
	wg.Wait()
}

// discover finds every checkout under the roots. A .git entry is a directory
// for a clone and a file for a linked worktree; both are checkouts worth
// visiting. The walk does not descend into .git itself but does continue into
// the checkout, so it reaches a clone nested inside a directory that is itself
// a repo, which is exactly the ~/p layout.
func discover(roots []string) []string {
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || path == root {
				return nil
			}
			depth := strings.Count(strings.TrimPrefix(path, root), string(filepath.Separator))
			if d.Name() == ".git" {
				seen[filepath.Dir(path)] = true
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() && depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		})
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// noninteractiveAuth makes it impossible for any git invocation here to ask a
// human for anything. A timer has no human, and on a desktop the question does
// not even reach a terminal.
//
// GIT_TERMINAL_PROMPT is not enough on its own, which is how the first real
// run put a GTK "Username for 'https://git.heroku.com'" dialog on screen: it
// suppresses only the *terminal* prompt, and for HTTPS credentials git prefers
// an askpass helper, which on a desktop session is a GUI. Four separate doors,
// so all four are shut - askpass, the ssh askpass the agent would use, ssh's
// own interactive fallback, and the terminal.
func noninteractiveAuth() {
	_ = os.Setenv("GIT_TERMINAL_PROMPT", "0")
	// A helper that exits non-zero makes git fail the fetch immediately
	// instead of retrying with an empty answer, which is what `true` would do.
	askpass := os.Getenv("GIT_FRESHEN_ASKPASS")
	if askpass == "" {
		askpass = "false"
		if p, err := exec.LookPath("false"); err == nil {
			askpass = p
		}
	}
	_ = os.Setenv("GIT_ASKPASS", askpass)
	_ = os.Setenv("SSH_ASKPASS_REQUIRE", "never")
	_ = os.Unsetenv("SSH_ASKPASS")
	ssh := os.Getenv("GIT_SSH_COMMAND")
	if ssh == "" {
		ssh = "ssh"
	}
	_ = os.Setenv("GIT_SSH_COMMAND", ssh+" -oBatchMode=yes -oConnectTimeout=10")
}

func (c config) rel(path string) string {
	return strings.TrimPrefix(path, c.home+"/")
}
