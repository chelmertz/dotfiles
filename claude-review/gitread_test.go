package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// prClone is a clone of acme/widgets: base, then head on top of it, and a
// commit on a side branch standing in for a stash or unpushed work.
type prClone struct{ base, head, side string }

func newPRClone(t *testing.T) prClone {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("remote", "add", "upstream", "git@github.com:acme/widgets.git")
	write("app.go", "package app\n")
	git("add", ".")
	git("commit", "-qm", "base")
	var r prClone
	r.base = git("rev-parse", "HEAD")
	write("app.go", "package app\n\n// -O touch pwned\nfunc New() {}\n")
	git("commit", "-qam", "add New")
	r.head = git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "side", r.base)
	write("secret.txt", "TOKEN=hunter2\n")
	git("add", ".")
	git("commit", "-qm", "unpushed")
	r.side = git("rev-parse", "HEAD")
	git("checkout", "-q", "main")
	return r
}

func (r prClone) reader(base string) prReader {
	d := newPRReader(Deps{})
	d.head = func(PR) (string, error) { return r.head, nil }
	d.diffBase = func(PR) (string, bool, error) { return base, base != "", nil }
	return d
}

func read(t *testing.T, cmd string, r prReader, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := readCommands[cmd](args, r, &out, &errb)
	return code, out.String(), errb.String()
}

func TestShowReadsTheFileAtTheGitHubHead(t *testing.T) {
	r := newPRClone(t)
	code, out, errb := read(t, "show", r.reader(""), prURL, "app.go")
	if code != 0 || !strings.Contains(out, "func New") || !strings.Contains(errb, "head "+r.head) {
		t.Errorf("code %d out %q stderr %q", code, out, errb)
	}
	if code, _, _ := read(t, "show", r.reader(""), prURL, "secret.txt"); code != 1 {
		t.Errorf("a file that is not at the PR head: code %d", code)
	}
	if code, _, _ := read(t, "show", r.reader(""), prURL, "app.go", "--output=x"); code != 2 {
		t.Errorf("an extra operand: code %d", code)
	}
}

// The base comes from the ledger and must be in the PR's history, so a
// stash or unpushed branch in the clone can never be diffed into a post.
func TestDiffOnlyAlongThePRsHistory(t *testing.T) {
	r := newPRClone(t)
	code, out, errb := read(t, "diff", r.reader(r.base), prURL)
	if code != 0 || !strings.HasPrefix(out, "head "+r.head+", since "+r.base) || !strings.Contains(out, r.head+" add New") || !strings.Contains(out, "+func New() {}") {
		t.Errorf("code %d stderr %q out\n%s", code, errb, out)
	}
	for name, base := range map[string]string{"a side branch": r.side, "no earlier round": "", "a ref, not a sha": "HEAD~1"} {
		code, out, errb := read(t, "diff", r.reader(base), prURL)
		if code != 1 || out != "" || strings.Contains(errb, "hunter2") || !strings.Contains(errb, "gh pr diff 42") {
			t.Errorf("%s: code %d out %q stderr %q", name, code, out, errb)
		}
	}
	if code, _, _ := read(t, "diff", r.reader(r.base), prURL, r.side); code != 2 {
		t.Errorf("a base from the session: code %d", code)
	}
}

func TestGrepTakesTheTextAsText(t *testing.T) {
	r := newPRClone(t)
	code, out, _ := read(t, "grep", r.reader(""), prURL, "-O touch pwned")
	if code != 0 || !strings.Contains(out, r.head+":app.go:3:// -O touch pwned") {
		t.Errorf("code %d out %q", code, out)
	}
	code, out, _ = read(t, "grep", r.reader(""), prURL, "--open-files-in-pager=touch pwned")
	if _, err := os.Stat("pwned"); code != 0 || out != "head "+r.head+"\n" || err == nil {
		t.Errorf("a flag-shaped text: code %d out %q, file made: %v", code, out, err == nil)
	}
	if code, _, _ := read(t, "grep", r.reader(""), prURL, "a", "b"); code != 2 {
		t.Errorf("two operands: code %d", code)
	}
}

func TestReadsRefuseAnotherCloneOrAMissingHead(t *testing.T) {
	r := newPRClone(t)
	other := r.reader("")
	other.remotes = func() ([]Remote, error) {
		return []Remote{{Name: "origin", URL: "git@github.com:acme/gadgets.git"}}, nil
	}
	if code, _, errb := read(t, "show", other, prURL, "app.go"); code != 1 || !strings.Contains(errb, "not a clone of acme/widgets") {
		t.Errorf("another repository: code %d stderr %q", code, errb)
	}
	missing := r.reader("")
	missing.head = func(PR) (string, error) { return strings.Repeat("e", 40), nil }
	if code, _, errb := read(t, "grep", missing, prURL, "New"); code != 1 || !strings.Contains(errb, "git fetch upstream pull/42/head") {
		t.Errorf("head not fetched: code %d stderr %q", code, errb)
	}
}

// The user's global git config must not change what runs on PR content.
func TestGitReadIgnoresTheGlobalConfig(t *testing.T) {
	r := newPRClone(t)
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[diff]\n\tnoprefix = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	if _, out, _ := read(t, "diff", r.reader(r.base), prURL); !strings.Contains(out, "--- a/app.go") {
		t.Errorf("global config applied:\n%s", out)
	}
}

func TestGitReadCapsOutput(t *testing.T) {
	var b bytes.Buffer
	c := &capped{w: &b, left: 4}
	for _, s := range []string{"abc", "def", "ghi"} {
		if n, err := c.Write([]byte(s)); n != 3 || err != nil {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if b.String() != "abcd\n[output cut]\n" {
		t.Errorf("got %q", b.String())
	}
}
