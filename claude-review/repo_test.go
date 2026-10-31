package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo creates a real checkout at dir with the given remotes, so the
// default RemoteURLs (which shells out to git) is exercised for real.
func gitRepo(t *testing.T, dir string, remotes map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for name, url := range remotes {
		run("remote", "add", name, url)
	}
}

func TestSlugOf_NormalisesEveryGitHubShape(t *testing.T) {
	for _, in := range []string{
		originURL,
		"git@github.com:acme/widgets",
		"ssh://git@github.com/acme/widgets.git",
		httpsURL + ".git",
		httpsURL,
		httpsURL + "/",
		"  " + httpsURL + ".git\n",
	} {
		if got := slugOf(in); got != slug {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestSlugOf_IgnoresNonGitHub(t *testing.T) {
	for _, in := range []string{"", "https://gitlab.com/acme/widgets.git", "/srv/git/widgets", "git@github.com:widgets.git"} {
		if got := slugOf(in); got != "" {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestFindRepo_ExactNameAndRemote(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, filepath.Join(root, "acme", repoName), origin(originURL))
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "acme", repoName) {
		t.Errorf("got %q", got)
	}
}

func TestFindRepo_ShortestPathWins(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "widgets")
	short := filepath.Join(root, "widgets")
	gitRepo(t, deep, origin(originURL))
	gitRepo(t, short, origin(originURL))
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != short {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_SkipsSameNameWrongRemote(t *testing.T) {
	root := t.TempDir()
	fork := filepath.Join(root, "widgets")
	real := filepath.Join(root, "acme", repoName)
	gitRepo(t, fork, origin("git@github.com:someone/widgets.git"))
	gitRepo(t, real, map[string]string{upstream: httpsURL})
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != real {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_SkipsNonGitFolderAndNearNames(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "widgets"), 0o755); err != nil { // not a repo
		t.Fatal(err)
	}
	gitRepo(t, filepath.Join(root, "widgets-old"), origin(originURL))
	_, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err == nil {
		t.Fatal("expected not found")
	}
	for _, want := range []string{"acme/widgets is not cloned", "gh repo clone acme/widgets " + filepath.Join(root, "widgets")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err)
		}
	}
}

func TestFindRepo_RespectsDepthAndSkipsHidden(t *testing.T) {
	root := t.TempDir()
	tooDeep := filepath.Join(root, "a", "b", "c", "widgets")
	hidden := filepath.Join(root, ".cache", "widgets")
	gitRepo(t, tooDeep, origin(originURL))
	gitRepo(t, hidden, origin(originURL))
	if _, _, err := FindRepo([]string{root}, "", widgetsPR); err == nil {
		t.Fatal("expected not found")
	}
	okDepth := filepath.Join(root, "a", "b", "widgets")
	gitRepo(t, okDepth, origin(originURL))
	if got, _, err := FindRepo([]string{root}, "", widgetsPR); err != nil || got != okDepth {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_SearchesRootsInOrderButByLength(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	gitRepo(t, filepath.Join(r1, "x", "widgets"), origin(originURL))
	gitRepo(t, filepath.Join(r2, "widgets"), origin(originURL))
	got, _, err := FindRepo([]string{r1, r2}, "", widgetsPR)
	if err != nil || got != filepath.Join(r2, "widgets") {
		t.Errorf("shortest path should win across roots, got %q, %v", got, err)
	}
}

func TestFindRepo_MissingRootIsNotAnError(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, filepath.Join(root, "widgets"), origin(originURL))
	got, _, err := FindRepo([]string{"/does/not/exist", root}, "", widgetsPR)
	if err != nil || got != filepath.Join(root, "widgets") {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_WorktreeCheckoutMatches(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "elsewhere", "primary")
	gitRepo(t, mainDir, origin(originURL))
	c := exec.Command("git", "-C", mainDir, "commit", "-q", "--allow-empty", "-m", "init")
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wt := filepath.Join(root, "widgets")
	if out, err := exec.Command("git", "-C", mainDir, "worktree", "add", "-q", wt).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != wt {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestGitRemotes_NotARepo(t *testing.T) {
	if _, err := gitRemotes(t.TempDir()); err == nil {
		t.Fatal("expected an error outside a repo")
	}
}

func TestGitRemotes_NoRemotes(t *testing.T) {
	dir := t.TempDir()
	gitRepo(t, dir, nil)
	remotes, err := gitRemotes(dir)
	if len(remotes) != 0 {
		t.Errorf("got %v, %v", remotes, err)
	}
}

func TestFindRepo_ReturnsTheMatchingRemoteName(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, filepath.Join(root, repoName), map[string]string{
		"origin": "git@github.com:someone/widgets.git", // the fork
		upstream: originURL,
	})
	dir, remote, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || dir != filepath.Join(root, repoName) || remote != upstream {
		t.Errorf("got %q %q, %v", dir, remote, err)
	}
}

func TestFindRepo_EqualLengthPathsTieBreakAlphabetically(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "aa", "widgets")
	b := filepath.Join(root, "bb", "widgets")
	gitRepo(t, b, origin(originURL))
	gitRepo(t, a, origin(originURL))
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != a {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_NonGitShorterMatchDoesNotStopTheSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "widgets"), 0o755); err != nil { // plain folder, sorts first
		t.Fatal(err)
	}
	real := filepath.Join(root, "x", "widgets")
	gitRepo(t, real, origin(originURL))
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != real {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_SymlinkedRootAndClone(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-code")
	gitRepo(t, filepath.Join(realRoot, "elsewhere", "widgets-main"), origin(originURL))
	linkRoot := filepath.Join(base, "code") // ~/code -> real-code
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	linkClone := filepath.Join(realRoot, repoName) // code/widgets -> elsewhere/widgets-main
	if err := os.Symlink(filepath.Join(realRoot, "elsewhere", "widgets-main"), linkClone); err != nil {
		t.Fatal(err)
	}
	got, _, err := FindRepo([]string{linkRoot}, "", widgetsPR)
	if err != nil || got != linkClone {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_SymlinkToFileOrNowhereIsNotACandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "f"), filepath.Join(root, repoName)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FindRepo([]string{root}, "", widgetsPR); err == nil {
		t.Error("a symlink to a file must not match")
	}
	root = t.TempDir()
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, repoName)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FindRepo([]string{root}, "", widgetsPR); err == nil {
		t.Error("a dangling symlink must not match, and must not panic")
	}
}

func TestFindRepo_FolderCaseDoesNotMatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Widgets")
	gitRepo(t, dir, origin("git@github.com:Acme/Widgets.git"))
	pr, err := ParseTarget("https://github.com/Acme/Widgets/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := FindRepo([]string{root}, "", pr)
	if err != nil || got != dir {
		t.Errorf("got %q, %v", got, err)
	}
}

// WalkDir skips the rest of a directory when the callback returns SkipDir
// for a non-directory entry. A hidden symlink sorted before the clone, or a
// too-deep symlink, must therefore not hide its siblings.
func TestFindRepo_SymlinkEntriesDoNotHideSiblings(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".hidden-link")); err != nil { // sorts before "widgets"
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(deep, "0-link")); err != nil { // too deep, sorts first
		t.Fatal(err)
	}
	gitRepo(t, filepath.Join(deep, "widgets"), origin(originURL)) // too deep as well; must simply not match
	gitRepo(t, filepath.Join(root, repoName), origin(originURL))
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != filepath.Join(root, repoName) {
		t.Errorf("got %q, %v", got, err)
	}
	// A matching symlink followed by a sibling that also matches by name in a
	// deeper spot: both are candidates, the shorter wins.
	root = t.TempDir()
	gitRepo(t, filepath.Join(root, "zz", "real"), origin(originURL))
	if err := os.Symlink(filepath.Join(root, "zz", "real"), filepath.Join(root, repoName)); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, filepath.Join(root, "zz", repoName), origin(originURL))
	got, _, err = FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != filepath.Join(root, repoName) {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestParseRemotes(t *testing.T) {
	got := parseRemotes("remote.nourl.url\nremote.origin.url git@github.com:a/b.git\nbranch.main.remote origin\nremote.upstream.url https://github.com/c/d\n\n")
	want := []Remote{{originName, "git@github.com:a/b.git"}, {"upstream", "https://github.com/c/d"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %v want %v", i, got[i], want[i])
		}
	}
	if got := parseRemotes(""); len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
}

func TestFindRepo_OwnerNamedLikeTheRepo(t *testing.T) {
	root := t.TempDir()
	clone := filepath.Join(root, "github", "widgets", "widgets")
	gitRepo(t, clone, origin("git@github.com:widgets/widgets.git"))
	got, _, err := FindRepo([]string{root}, "", PR{Owner: "widgets", Repo: "widgets", Number: 1})
	if err != nil || got != clone {
		t.Errorf("got %q, %v", got, err)
	}
}

// A clone's tree can hold a folder shaped like a bare repository (HEAD,
// config, objects/, refs/ committed as plain files), with a config naming any
// remote. It must never pass for a clone: the session would run its config.
func TestFindRepo_IgnoresFoldersInsideOtherCheckouts(t *testing.T) {
	root := t.TempDir()
	evil := filepath.Join(root, "evil")
	gitRepo(t, evil, origin("git@github.com:someone/evil.git"))
	planted := filepath.Join(evil, "widgets")
	if out, err := exec.Command("git", "init", "-q", "--bare", planted).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", planted, "remote", "add", "origin", originURL).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	real := filepath.Join(root, "longer", "x", "widgets") // longer than the planted path
	gitRepo(t, real, origin(originURL))
	got, _, err := FindRepo([]string{root}, "", widgetsPR)
	if err != nil || got != real {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindRepo_NotFoundNamesClonePathAndNextStep(t *testing.T) {
	root := t.TempDir()
	_, _, err := FindRepo([]string{root}, filepath.Join(root, "github", "{owner}", "{repo}"), widgetsPR)
	if err == nil {
		t.Fatal("expected not found")
	}
	dir := filepath.Join(root, "github", "acme", "widgets")
	for _, want := range []string{
		"acme/widgets is not cloned at " + dir + "\n",
		"gh repo clone acme/widgets " + dir + "\n",
		"click review again",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, err)
		}
	}
}

func TestNotFound_CommandQuotesOnlyWhatNeedsIt(t *testing.T) {
	e := &notFoundError{pr: widgetsPR, target: "/home/a b/widgets"}
	if got := e.Command(); got != `gh repo clone acme/widgets '/home/a b/widgets'` {
		t.Errorf("got %s", got)
	}
}

func TestNotFound_NotificationIsShortAndSaysMiddleClick(t *testing.T) {
	e := &notFoundError{pr: widgetsPR, target: "/home/u/code/widgets"}
	want := "acme/widgets is not cloned\n~/code/widgets\nmiddle-click copies the clone command"
	if got := e.notification("/home/u", true); got != want {
		t.Errorf("got %q", got)
	}
	// No action to click (macOS): the command itself goes in the body.
	if got := e.notification("/home/u", false); !strings.HasSuffix(got, "\n"+e.Command()) {
		t.Errorf("got %q", got)
	}
}
