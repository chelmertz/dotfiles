package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestParseVerb(t *testing.T) {
	cases := map[string]string{
		"claude-review://acme/widgets/42/approve?at=" + sha + "&token=ab12": "approve",
		"claude-review://acme/widgets/42/attach":                            "attach",
		"claude-review://acme/widgets/42/ignore":                            "ignore",
		"claude-review://acme/widgets/42":                                   "",
		"claude-review://acme/widgets/42/merge":                             "",
		"claude-review://../widgets/42/ignore":                              "",
		"https://github.com/acme/widgets/pull/42/approve":                   "",
	}
	for in, want := range cases {
		pr, verb, _, ok := ParseVerb(in)
		if verb != want || ok != (want != "") || (ok && pr != widgetsPR) {
			t.Errorf("%q: %+v %q %v", in, pr, verb, ok)
		}
	}
}

func verbHost(t *testing.T) *fakeHost {
	h := newHost(t, `{"terminal": false, "review_repos":["acme/*"]}`, true, true)
	h.d.Home = t.TempDir()
	h.d.StateDir = h.d.Home + "/.local/state" // the ledger stays in the test's dir
	return h
}

func TestApproveVerbNeedsAMintedToken(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := l.MintToken(widgetsPR, sha)
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	h.d.Gh = func(args ...string) (string, error) {
		h.ghCalls = append(h.ghCalls, args)
		if strings.Contains(strings.Join(args, " "), "-X POST") {
			return `{"html_url":"approved"}`, nil
		}
		return sha, nil // headSHA asks gh for .head.sha alone
	}
	approvals := func() int {
		n := 0
		for _, c := range h.ghCalls {
			if slices.Contains(c, "event=APPROVE") {
				n++
			}
		}
		return n
	}

	if code := run([]string{"claude-review://acme/widgets/42/approve?at=" + sha + "&token=forged"}, h.d); code == 0 || approvals() != 0 {
		t.Fatalf("forged token: exit %d, approvals %d", code, approvals())
	}
	if code := run([]string{"claude-review://acme/widgets/42/approve?at=" + sha + "&token=" + tok}, h.d); code != 0 || approvals() != 1 {
		t.Fatalf("minted token: exit %d, approvals %d: %s", code, approvals(), h.stderr)
	}
	if code := run([]string{"claude-review://acme/widgets/42/approve?at=" + sha + "&token=" + tok}, h.d); code == 0 || approvals() != 1 {
		t.Errorf("reused token: exit %d, approvals %d", code, approvals())
	}
}

func TestIgnoreVerbStopsRounds(t *testing.T) {
	h := verbHost(t)
	if code := run([]string{"claude-review://acme/widgets/42/ignore"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if ignored, _ := l.Ignored(widgetsPR); !ignored {
		t.Error("ignore did not stick")
	}
}

// attach never resumes a session interactively: that would be a window
// under the user's own settings, which blocks rounds. A live worker is
// attached to; with none there is nothing to watch.
func TestAttachVerbNeverResumes(t *testing.T) {
	h, fs := clickHost(t, true)
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	if len(h.scripts) != 0 || notified(h) != "no round running on "+target {
		t.Errorf("no worker: scripts %q, notified %q", h.scripts, notified(h))
	}
	fs.workers = []Worker{workerFor(widgetsPR.RoundSessionID(), 500)}
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	script := strings.Join(mapValues(h.scripts), "\n")
	if !strings.Contains(script, "attach'\\'' '\\''"+widgetsPR.RoundSessionID()) || strings.Contains(script, "--resume") {
		t.Errorf("a live worker was not attached: %q", script)
	}
	h.locked = true
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 1 {
		t.Errorf("attach ignored the PR's lock: exit %d", code)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// TestThunderbirdHostPassesNoVerbs pins the native host's pattern in
// nix/thunderbird.nix: mail must never carry an approve, attach or ignore
// link to the URL handler. Skipped where the nix file is not beside this
// module, as in the nix build's sandbox.
func TestThunderbirdHostPassesNoVerbs(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "nix", "thunderbird.nix"))
	if err != nil {
		t.Skip("nix/thunderbird.nix not beside this module")
	}
	m := regexp.MustCompile(`re\.fullmatch\(r"([^"]+)"`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no re.fullmatch pattern in nix/thunderbird.nix")
	}
	full := regexp.MustCompile("^(?:" + string(m[1]) + ")$")
	if !full.MatchString("claude-review://acme/widgets/42") {
		t.Errorf("pattern %s refuses a plain PR link", m[1])
	}
	for _, verb := range []string{"approve?at=" + sha + "&token=x", "attach", "ignore"} {
		if u := "claude-review://acme/widgets/42/" + verb; full.MatchString(u) {
			t.Errorf("pattern %s passes %s", m[1], u)
		}
	}
}
