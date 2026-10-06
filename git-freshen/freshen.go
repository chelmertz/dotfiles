package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// record is the one line a checkout produces: nine tab-separated fields, every
// one present, "-" for not-applicable, status first so a caller can tally with
// `cut -f1 | sort | uniq -c`.
type record struct {
	status, path, branch, ahead, behind, upstream, fetch, moved, note string
}

func (r record) String() string {
	return strings.Join([]string{r.status, r.path, r.branch, r.ahead, r.behind, r.upstream, r.fetch, r.moved, r.note}, "\t")
}

// addMoved accumulates the refs this run advanced. Keeping it a field rather
// than prose in the note is what lets a later run be diffed against this one.
func (r *record) addMoved(ref string) {
	if r.moved == "-" {
		r.moved = ref
	} else {
		r.moved += "," + ref
	}
}

// gitq runs git in repo and returns its trimmed stdout and whether it
// succeeded. Every call here is a question whose unanswerable case is "treat
// it as absent".
func gitq(repo string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return strings.TrimSpace(string(out)), err == nil
}

func git(repo string, args ...string) string {
	out, _ := gitq(repo, args...)
	return out
}

func (c config) freshen(repo string) record {
	r := record{path: c.rel(repo), branch: "-", ahead: "-", behind: "-", upstream: "-", fetch: "ok", moved: "-", note: "-"}
	emit := func(status, note string) record { r.status, r.note = status, note; return r }

	if _, ok := gitq(repo, "rev-parse", "--is-inside-work-tree"); !ok {
		return emit("skip", "not a usable checkout")
	}

	// --prune drops remote-tracking refs for branches deleted upstream, which
	// is what keeps a merged PR's branch from lingering as a live-looking ref,
	// and what lets prune.go tell a merged worktree branch by its [gone]
	// upstream. --no-tags: tag traffic is the bulk of a fetch on a
	// release-heavy repo and nothing here reads tags.
	//
	// The origin retry is not belt-and-braces. `--all` fails if any one remote
	// fails, and a long-lived clone collects dead remotes - a decommissioned
	// heroku, a deleted fork. One of those marked nine repositories "fetch
	// failed" on the first real run while origin had fetched cleanly, which
	// would only have taught the reader to ignore the warning.
	if !c.fetch(repo, "--all") && !c.fetch(repo, "origin") {
		r.fetch = "failed"
	}

	c.advanceUncheckedMain(repo, &r)

	branch, ok := gitq(repo, "symbolic-ref", "--quiet", "--short", "HEAD")
	if !ok || branch == "" {
		return emit("detached", "no branch checked out")
	}
	r.branch = branch

	// `rev-parse --abbrev-ref @{u}` is not a clean test for "has an upstream".
	// When the branch it tracks has been deleted on the remote it prints the
	// literal string "@{u}" on stdout and reports the failure on stderr only,
	// so taking it as an upstream name is how 25 repositories landed in an
	// "unknown" bucket on the first real run. The resolvable-commit check is
	// the honest question, and branch.<name>.merge is what tells "tracked
	// something that is gone" - a merged PR, nearly always - from "never
	// tracked anything".
	r.upstream = git(repo, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if git(repo, "rev-parse", "--verify", "--quiet", "@{u}^{commit}") == "" {
		if merge := git(repo, "config", "--get", "branch."+branch+".merge"); merge != "" {
			r.upstream = merge
			return emit("upstream-gone", "remote branch deleted, usually a merged PR")
		}
		r.upstream = "-"
		return emit("no-upstream", "tracks nothing")
	}

	counts := strings.Fields(git(repo, "rev-list", "--left-right", "--count", "HEAD...@{u}"))
	if len(counts) != 2 {
		return emit("unknown", "rev-list could not count against "+r.upstream)
	}
	r.ahead, r.behind = counts[0], counts[1]
	switch {
	case r.ahead != "0" && r.behind != "0":
		return emit("diverged", "rebase by hand")
	case r.behind == "0" && r.ahead != "0":
		return emit("unpushed", "local commits are not on the remote")
	case r.behind == "0":
		return emit("current", "-")
	}

	// Behind and not ahead: the fast-forward case. Untracked files are not a
	// reason to refuse - git itself aborts if a fast-forward would overwrite
	// one, and treating them as dirt would permanently exclude every repo with
	// a stray build artifact.
	switch {
	case inProgress(repo):
		return emit("blocked", "a rebase or merge is in progress")
	case git(repo, "status", "--porcelain", "--untracked-files=no") != "":
		return emit("blocked", "uncommitted changes")
	case occupied(repo):
		return emit("blocked", "a process is working in it")
	case c.dryRun:
		r.addMoved(branch)
		return emit("would-ff", "-")
	}
	if _, ok := gitq(repo, "merge", "--ff-only", "@{u}"); !ok {
		return emit("failed", "merge --ff-only refused it")
	}
	r.addMoved(branch)
	return emit("fast-forwarded", "-")
}

func (c config) fetch(repo, remote string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "fetch", remote, "--prune", "--no-tags", "--quiet")
	if remote == "--all" {
		cmd = exec.CommandContext(ctx, "git", "-C", repo, "fetch", "--all", "--prune", "--no-tags", "--quiet")
	}
	return cmd.Run() == nil
}

// advanceUncheckedMain fast-forwards local main/master by writing the ref
// directly. Deliberately not `merge --ff-only`: that needs the branch checked
// out. update-ref moves it with no working tree involved, which is the point -
// the branch you are *on* stays exactly where it is.
//
// Guarded three ways: the branch must exist, must not be checked out in this
// or any sibling worktree, and origin/<branch> must be a strict descendant of
// it. The ancestry test is what makes this a fast-forward rather than a reset.
func (c config) advanceUncheckedMain(repo string, r *record) {
	checkedOut := map[string]bool{}
	for _, wt := range worktrees(repo) {
		checkedOut[wt.branch] = true
	}
	for _, b := range []string{"main", "master"} {
		if _, ok := gitq(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+b); !ok || checkedOut[b] {
			continue
		}
		if _, ok := gitq(repo, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+b); !ok {
			continue
		}
		local, remote := git(repo, "rev-parse", b), git(repo, "rev-parse", "origin/"+b)
		if local == "" || remote == "" || local == remote {
			continue
		}
		if _, ok := gitq(repo, "merge-base", "--is-ancestor", b, "origin/"+b); !ok {
			continue
		}
		if c.dryRun {
			r.addMoved(b)
		} else if _, ok := gitq(repo, "update-ref", "-m", "git-freshen: fast-forward", "refs/heads/"+b, remote, local); ok {
			r.addMoved(b)
		} else {
			r.addMoved(b + "(failed)")
		}
	}
}

// inProgress reports an interrupted rebase, merge, cherry-pick or bisect. Each
// leaves a marker in the *worktree's* git dir, not the common one, so a sibling
// worktree mid-rebase never blocks this one.
func inProgress(repo string) bool {
	d := git(repo, "rev-parse", "--absolute-git-dir")
	if d == "" {
		return false
	}
	for _, m := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "BISECT_LOG"} {
		if _, err := os.Stat(filepath.Join(d, m)); err == nil {
			return true
		}
	}
	return false
}

// occupied reports a process sitting inside dir. A clean fast-forward under an
// open session changes files it is reading, and removing a worktree under one
// pulls the floor out from under it. /proc is authoritative and costs
// milliseconds.
func occupied(dir string) bool {
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		cwd, err := os.Readlink(p + "/cwd")
		if err == nil && (cwd == dir || strings.HasPrefix(cwd, dir+"/")) {
			return true
		}
	}
	return false
}

type worktree struct {
	path, head, branch string // branch is the short name, "" when detached
	locked, prunable   bool
}

// worktrees parses `git worktree list --porcelain`. The first entry is the
// main working tree.
func worktrees(repo string) []worktree {
	var out []worktree
	for _, block := range strings.Split(git(repo, "worktree", "list", "--porcelain"), "\n\n") {
		var wt worktree
		for _, line := range strings.Split(block, "\n") {
			key, val, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				wt.path = val
			case "HEAD":
				wt.head = val
			case "branch":
				wt.branch = strings.TrimPrefix(val, "refs/heads/")
			case "locked":
				wt.locked = true
			case "prunable":
				wt.prunable = true
			}
		}
		if wt.path != "" {
			out = append(out, wt)
		}
	}
	return out
}
