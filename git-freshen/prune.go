package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// idleAfter is how long a linked worktree must sit untouched before it may be
// removed. A worktree created a moment ago is clean and level with main, which
// is indistinguishable from a finished one, so without this the next hourly run
// would remove it before anyone had used it.
const idleAfter = 7 * 24 * time.Hour

// prune removes the linked worktrees of repo that are finished with: idle for
// idleAfter, no process inside, nothing in progress, clean (untracked files
// included), and merged - tracking a branch the remote has deleted, or wholly
// contained in the remote's default branch. Only a main clone prunes; a linked
// worktree found by discover is visited through its clone instead.
//
// `git worktree remove` without --force is the write: git itself refuses a
// worktree with modifications or untracked files, and it keeps the branch, so
// the only things that go are the checkout and its ignored files - build
// output and node_modules, which is where the disk went. A detached worktree
// is removed only when its commit is on the default branch, since its reflog
// goes with it.
func (c config) prune(repo string) []record {
	if info, err := os.Stat(filepath.Join(repo, ".git")); err != nil || !info.IsDir() {
		return nil
	}
	wts := worktrees(repo)
	if len(wts) < 2 {
		return nil
	}
	def := defaultBranch(repo)
	var out []record
	for _, wt := range wts[1:] {
		if wt.locked || wt.prunable {
			continue
		}
		idle, ok := idleFor(wt.path, c.now)
		if !ok || idle < idleAfter || occupied(wt.path) || inProgress(wt.path) {
			continue
		}
		if s, ok := gitq(wt.path, "status", "--porcelain"); !ok || s != "" {
			continue
		}
		why := merged(repo, wt, def)
		if why == "" {
			continue
		}
		r := record{path: c.rel(wt.path), branch: "-", ahead: "-", behind: "-", upstream: "-", fetch: "-", moved: "-",
			note: fmt.Sprintf("%s, idle %dd", why, int(idle.Hours()/24))}
		if wt.branch != "" {
			r.branch = wt.branch
		}
		if c.dryRun {
			r.status = "would-prune"
		} else if _, ok := gitq(repo, "worktree", "remove", wt.path); ok {
			r.status = "pruned"
		} else {
			r.status, r.note = "failed", "worktree remove refused it"
		}
		out = append(out, r)
	}
	return out
}

// merged says why a worktree's commit is safe to drop from disk, or "" when it
// is not. A [gone] upstream comes first because a squash merge - the usual way
// a PR lands - leaves the branch's commits on no remote at all.
func merged(repo string, wt worktree, def string) string {
	if wt.branch != "" && git(repo, "for-each-ref", "--format=%(upstream:track)", "refs/heads/"+wt.branch) == "[gone]" {
		return "upstream gone"
	}
	if def == "" || wt.head == "" {
		return ""
	}
	if _, ok := gitq(repo, "merge-base", "--is-ancestor", wt.head, def); ok {
		return "in " + def
	}
	return ""
}

// defaultBranch is the remote branch work merges into: origin/HEAD when the
// clone recorded it, else origin/main or origin/master.
func defaultBranch(repo string) string {
	if ref := git(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ref != "" {
		return ref
	}
	for _, b := range []string{"origin/main", "origin/master"} {
		if _, ok := gitq(repo, "rev-parse", "--verify", "--quiet", b+"^{commit}"); ok {
			return b
		}
	}
	return ""
}

// idleFor is the time since a worktree was last touched: the newest mtime of
// the checkout's top directory and of its HEAD, index and HEAD reflog, which
// between them move on a checkout, a commit, a stage or a file created at the
// top. The index also moves when git refreshes stat data, so a session that
// only ran `git status` counts as use.
func idleFor(path string, now time.Time) (time.Duration, bool) {
	admin := git(path, "rev-parse", "--absolute-git-dir")
	if admin == "" {
		return 0, false
	}
	var newest time.Time
	for _, p := range []string{path, filepath.Join(admin, "HEAD"), filepath.Join(admin, "index"), filepath.Join(admin, "logs", "HEAD")} {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		return 0, false
	}
	return now.Sub(newest), true
}
