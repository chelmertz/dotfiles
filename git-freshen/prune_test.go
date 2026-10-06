package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// fixture is an origin with one commit on main and a clone of it whose
// worktrees live where Claude Code puts them, under .claude/worktrees.
func fixture(t *testing.T) (origin, clone string) {
	t.Helper()
	isolate(t)
	tmp := t.TempDir()
	origin, clone = filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	if err := os.Mkdir(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, origin, "init", "-q")
	gitT(t, origin, "commit", "-q", "--allow-empty", "-m", "init")
	gitT(t, tmp, "clone", "-q", origin, clone)
	if err := os.WriteFile(filepath.Join(clone, ".git", "info", "exclude"), []byte(".claude/\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return origin, clone
}

func wtPath(clone, name string) string { return filepath.Join(clone, ".claude", "worktrees", name) }

func addWorktree(t *testing.T, clone, name string, extra ...string) string {
	t.Helper()
	gitT(t, clone, append([]string{"worktree", "add", "-q"}, append(extra, wtPath(clone, name))...)...)
	return wtPath(clone, name)
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func pruned(recs []record) map[string]string {
	out := map[string]string{}
	for _, r := range recs {
		out[filepath.Base(r.path)] = r.status
	}
	return out
}

// The point of pruning is to free the disk a finished worktree holds, so most
// of this asserts the worktrees it must leave alone: anything unfinished,
// unsaved, in use or just created.
func TestPruneRemovesOnlyFinishedWorktrees(t *testing.T) {
	origin, clone := fixture(t)

	merged := addWorktree(t, clone, "merged")
	write(t, filepath.Join(merged, "node_modules", "big")) // ignored: must not keep it alive

	addWorktree(t, clone, "atmain", "--detach")

	dirty := addWorktree(t, clone, "dirty")
	write(t, filepath.Join(dirty, "untracked.txt"))

	unmerged := addWorktree(t, clone, "unmerged")
	gitT(t, unmerged, "commit", "-q", "--allow-empty", "-m", "only here")

	detached := addWorktree(t, clone, "detached", "--detach")
	gitT(t, detached, "commit", "-q", "--allow-empty", "-m", "only here")

	// The squash-merge case: the branch's commit is on no remote, but the PR
	// branch it tracked has been deleted, so the work landed.
	gone := addWorktree(t, clone, "gone")
	gitT(t, gone, "commit", "-q", "--allow-empty", "-m", "squashed upstream")
	gitT(t, gone, "push", "-q", "-u", "origin", "HEAD")
	gitT(t, origin, "branch", "-q", "-D", "gone")
	gitT(t, clone, "fetch", "-q", "--prune")

	// Pushed and open: on a remote, but not merged into main yet.
	open := addWorktree(t, clone, "open")
	gitT(t, open, "commit", "-q", "--allow-empty", "-m", "in review")
	gitT(t, open, "push", "-q", "-u", "origin", "HEAD")

	addWorktree(t, clone, "locked", "--lock")

	busy := addWorktree(t, clone, "busy")
	sleeper := exec.Command("sleep", "30")
	sleeper.Dir = busy
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })

	later := config{now: time.Now().Add(idleAfter + time.Hour)}

	got := pruned(later.prune(clone))
	want := map[string]string{"merged": "pruned", "atmain": "pruned", "gone": "pruned"}
	if len(got) != len(want) {
		t.Fatalf("pruned %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("pruned %v, want %v", got, want)
		}
	}
	for _, name := range []string{"merged", "atmain", "gone"} {
		if exists(wtPath(clone, name)) {
			t.Fatalf("%s still on disk", name)
		}
	}
	for _, name := range []string{"dirty", "unmerged", "detached", "open", "locked", "busy"} {
		if !exists(wtPath(clone, name)) {
			t.Fatalf("%s was removed", name)
		}
	}
	// The branch outlives its worktree, so even a wrong call loses no commit.
	if _, ok := gitq(clone, "rev-parse", "--verify", "--quiet", "refs/heads/gone"); !ok {
		t.Fatal("the branch went with the worktree")
	}
}

// A worktree created a moment ago is clean and level with main - exactly what
// a finished one looks like. Only the idle gate tells them apart.
func TestPruneLeavesAFreshWorktree(t *testing.T) {
	_, clone := fixture(t)
	fresh := addWorktree(t, clone, "fresh")
	if recs := (config{now: time.Now()}).prune(clone); len(recs) != 0 {
		t.Fatalf("pruned %v", recs)
	}
	almost := config{now: time.Now().Add(idleAfter - time.Hour)}
	if recs := almost.prune(clone); len(recs) != 0 {
		t.Fatalf("pruned before idleAfter: %v", recs)
	}
	if !exists(fresh) {
		t.Fatal("fresh worktree removed")
	}
}

func TestPruneDryRunRemovesNothing(t *testing.T) {
	_, clone := fixture(t)
	wt := addWorktree(t, clone, "merged")
	recs := (config{dryRun: true, now: time.Now().Add(idleAfter + time.Hour)}).prune(clone)
	if len(recs) != 1 || recs[0].status != "would-prune" {
		t.Fatalf("%+v", recs)
	}
	if !exists(wt) {
		t.Fatal("dry run removed the worktree")
	}
}

// discover must reach a worktree's clone at the ~/p depth and stop at
// maxDepth, the bound the find(1) it replaced had.
func TestDiscoverDepth(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/.git", "p/ns/proj/repo/.git", "p/ns/proj/repo/nested/.git", "1/2/3/4/5/.git"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "p/ns/proj/wt/.git")) // a linked worktree's .git is a file
	got := discover([]string{root})
	for i := range got {
		got[i], _ = filepath.Rel(root, got[i])
	}
	// nested/.git is at depth 6, one past the bound.
	want := []string{"a", "p/ns/proj/repo", "p/ns/proj/wt"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// mergedRemotely is a clone with an idle-looking worktree whose PR branch was
// squash-merged and deleted on origin - after the clone last fetched, so only
// a run that fetches first can see that it is finished.
func mergedRemotely(t *testing.T) (origin, clone, wt string) {
	t.Helper()
	origin, clone = fixture(t)
	wt = addWorktree(t, clone, "pr")
	gitT(t, wt, "commit", "-q", "--allow-empty", "-m", "squashed upstream")
	gitT(t, wt, "push", "-q", "-u", "origin", "HEAD")
	gitT(t, origin, "branch", "-q", "-D", "pr")
	if got := git(clone, "for-each-ref", "--format=%(upstream:track)", "refs/heads/pr"); got == "[gone]" {
		t.Fatal("fixture already knows the branch is gone; the test would prove nothing")
	}
	return origin, clone, wt
}

func TestRunFetchesBeforeJudgingMerged(t *testing.T) {
	_, clone, wt := mergedRemotely(t)
	recs := config{now: time.Now().Add(idleAfter + time.Hour), timeout: 30 * time.Second}.all([]string{clone}, 2)
	if r := recs[strings.TrimPrefix(wt, "/")]; r.status != "pruned" {
		t.Fatalf("worktree record %+v, all %+v", r, recs)
	}
	if exists(wt) {
		t.Fatal("worktree still on disk")
	}
}

// A failed fetch leaves refs as stale as they were, and merged-ness read from
// stale refs is a guess, so that clone prunes nothing until a fetch succeeds.
func TestRunPrunesNothingAfterAFailedFetch(t *testing.T) {
	_, clone, wt := mergedRemotely(t)
	gitT(t, clone, "fetch", "-q", "--prune") // it now knows the branch is gone...
	gitT(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
	recs := config{now: time.Now().Add(idleAfter + time.Hour), timeout: 30 * time.Second}.all([]string{clone}, 2)
	if r := recs[strings.TrimPrefix(clone, "/")]; r.fetch != "failed" {
		t.Fatalf("clone record %+v", r)
	}
	if !exists(wt) { // ...but this run could not confirm it
		t.Fatal("pruned on stale refs")
	}
}
