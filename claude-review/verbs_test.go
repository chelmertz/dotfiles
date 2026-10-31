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
	t.Setenv("XDG_STATE_HOME", "") // the ledger goes under Home, never the real state dir
	return h
}

func TestApproveVerbNeedsAMintedToken(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.Home))
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
	l, err := OpenLedger(LedgerPath(h.d.Home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if ignored, _ := l.Ignored(widgetsPR); !ignored {
		t.Error("ignore did not stick")
	}
}

func TestAttachVerbOpensTheSessionWithoutAPrompt(t *testing.T) {
	h := newHost(t, `{"review_repos":["acme/*"]}`, true, true)
	h.d.Home = t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	// The launcher script quotes each word inside a quoted shell line.
	script := strings.Join(mapValues(h.scripts), "\n")
	if !strings.Contains(script, "--resume'\\'' '\\''"+widgetsPR.SessionID()) || strings.Contains(script, "New review round") {
		t.Errorf("attach opened %q", script)
	}
	h.running = true
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	if script := strings.Join(mapValues(h.scripts), "\n"); !strings.Contains(script, "attach'\\'' '\\''"+widgetsPR.SessionID()) {
		t.Errorf("a running background session was not attached: %q", script)
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
