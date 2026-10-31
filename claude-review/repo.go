package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxRepoDepth is how many directory levels below a root a repo folder may
// sit. ~/code/<repo> and ~/code/<org>/<repo> are the common layouts; three
// levels leaves room for one more without scanning the world.
const maxRepoDepth = 3

// Remote is one git remote of a checkout.
type Remote struct{ Name, URL string }

// Remotes returns every remote of the git checkout at dir. It is a variable
// so tests can run without git; the default shells out.
var Remotes = gitRemotes

var remoteKey = regexp.MustCompile(`^remote\.(.+)\.url$`)

func gitRemotes(dir string) ([]Remote, error) {
	// dir has a .git (see dirsNamed); refusing bare repositories keeps git
	// from reading a config committed as a plain file all the same.
	out, err := exec.Command("git", "-c", "safe.bareRepository=explicit", "-C", dir, "config", "--get-regexp", `^remote\..*\.url$`).Output()
	if err != nil {
		return nil, err
	}
	return parseRemotes(string(out)), nil
}

// parseRemotes reads `git config --get-regexp` output: one "remote.<name>.url
// <url>" per line. Lines of any other shape are ignored.
func parseRemotes(out string) []Remote {
	var remotes []Remote
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		key, u, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if m := remoteKey.FindStringSubmatch(key); m != nil {
			remotes = append(remotes, Remote{Name: m[1], URL: u})
		}
	}
	return remotes
}

// remoteSlug normalises a git remote URL to "owner/repo", or returns "" for
// anything that is not a GitHub URL. Handles git@github.com:o/r.git,
// ssh://git@github.com/o/r.git, https://github.com/o/r.git and the same
// without .git.
var remoteSlug = regexp.MustCompile(`^(?:git@github\.com:|ssh://git@github\.com/|https://github\.com/)([^/]+)/([^/]+?)(?:\.git)?/?$`)

func slugOf(remote string) string {
	m := remoteSlug.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// isDir is d.IsDir() that also accepts a symlink to a directory.
func isDir(p string, d fs.DirEntry) bool {
	if d.IsDir() {
		return true
	}
	if d.Type()&fs.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// FindRepo locates the local checkout of pr: a directory named like the
// repository, at most maxRepoDepth below one of roots, with a remote that
// resolves to owner/repo. It returns the directory and the name of that
// remote, which the review prompt fetches the PR ref from (a fork-style
// clone has it as "upstream", not "origin"). Several matches: the shortest
// path wins. Nothing is written and no network is touched. Not found: the
// error carries the clone command for clonePath ({owner}, {repo}; empty is
// <first root>/{repo}), for the user to run.
func FindRepo(roots []string, clonePath string, pr PR) (string, string, error) {
	var candidates []string
	for _, root := range roots {
		candidates = append(candidates, dirsNamed(root, pr.Repo)...)
	}
	// Shortest path first; alphabetical among equals so the choice is stable.
	sort.Strings(candidates)
	sort.SliceStable(candidates, func(i, j int) bool { return len(candidates[i]) < len(candidates[j]) })
	want := pr.Owner + "/" + pr.Repo
	for _, dir := range candidates {
		remotes, err := Remotes(dir)
		if err != nil {
			continue // not a git checkout, or git refused; try the next one
		}
		for _, r := range remotes {
			if strings.EqualFold(slugOf(r.URL), want) {
				return dir, r.Name, nil
			}
		}
	}
	if clonePath == "" {
		clonePath = filepath.Join(roots[0], "{repo}")
	}
	target := strings.NewReplacer("{owner}", pr.Owner, "{repo}", pr.Repo).Replace(clonePath)
	return "", "", &notFoundError{pr: pr, target: target}
}

// notFoundError is FindRepo's "no clone": where to clone to. fail turns
// it into a notification whose action copies Command.
type notFoundError struct {
	pr     PR
	target string
}

func (e *notFoundError) repo() string { return e.pr.Owner + "/" + e.pr.Repo }

func (e *notFoundError) Error() string {
	return fmt.Sprintf("claude-review: %s is not cloned at %s\n  %s\n  then click review again", e.repo(), e.target, e.Command())
}

// Command only clones; the user clicks review again after. gh picks the
// user's protocol, https or ssh; git does not.
func (e *notFoundError) Command() string {
	return "gh repo clone " + e.repo() + " " + pasteQuote(e.target)
}

// notification is the short body: copyable says a click copies Command,
// otherwise Command is the last line.
func (e *notFoundError) notification(home string, copyable bool) string {
	at := e.target
	if rel, err := filepath.Rel(home, at); err == nil && !strings.HasPrefix(rel, "..") {
		at = filepath.Join("~", rel)
	}
	last := e.Command()
	if copyable {
		last = "middle-click copies the clone command"
	}
	return fmt.Sprintf("%s is not cloned\n%s\n%s", e.repo(), at, last)
}

// plainPath is a path that pastes into a shell unquoted.
var plainPath = regexp.MustCompile(`^[\w./~+-]+$`)

func pasteQuote(p string) string {
	if plainPath.MatchString(p) {
		return p
	}
	return shellQuote(p)
}

// dirsNamed lists checkouts called name (case-insensitively, since GitHub
// names are and ParseTarget lower-cases them) within depth levels of root.
// A folder of that name that is not a checkout is searched like any other,
// so ~/code/github/<owner>/<repo> is found when owner and repo are equal.
// Hidden directories are skipped. A root that is a symlink is resolved
// first, and a symlink entry that points at a directory counts as one, since
// ~/code and clones inside it are often links.
func dirsNamed(root, name string) []string {
	var found []string
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !isDir(p, d) {
			return nil //nolint:nilerr // unreadable entries are simply not candidates
		}
		// SkipDir is only safe on a real directory: WalkDir never descends a
		// symlink, and SkipDir returned for a non-directory entry skips the
		// rest of its parent, which would hide clones sorted after it.
		skip := func() error {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			return skip()
		}
		if strings.Count(p, string(filepath.Separator))-rootDepth > maxRepoDepth {
			return skip()
		}
		if isCheckout(p) {
			if strings.EqualFold(d.Name(), name) {
				found = append(found, p)
			}
			// Never look inside a checkout: its tree is whatever was cloned,
			// and a folder in it shaped like a bare repository (HEAD, config,
			// objects/, refs/ committed as files) would pass for a clone.
			return skip()
		}
		return nil
	})
	return found
}

// isCheckout reports a .git entry in dir: a directory in a clone, a file in a
// worktree or submodule. Git refuses to commit a path named .git, so a cloned
// tree cannot plant one.
func isCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}
