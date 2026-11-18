package main

import (
	"github.com/chelmertz/dotfiles/claude-review/ids"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The fork runs with the user watching, so nothing outbound is pre-allowed:
// no settings source (the user's own allow list pre-approves curl, gh and
// git push), no MCP servers, default mode, and the exfiltration channels
// denied outright. mb-sql and claude-review post are on no list: they ask.
func TestForkArgsAreLocked(t *testing.T) {
	fid := ids.NewSession()
	args := ForkArgs(widgetsPR, fid, "upstream", metabaseReadRules(t.TempDir()))
	id := widgetsPR.RoundSessionID().String()
	head := []string{claudeCmd, "--resume", id, "--fork-session", "--session-id", fid.String(),
		"--setting-sources", "", "--strict-mcp-config", "--permission-mode", "default"}
	if !slices.Equal(args[:len(head)], head) {
		t.Fatalf("head %q", args[:len(head)])
	}
	prompt := args[len(head)]
	if !strings.Contains(prompt, "mb-sql") || !strings.Contains(prompt, "<<'SQL'") {
		t.Errorf("prompt does not ask for inline SQL through mb-sql: %q", prompt)
	}
	allow := listAfter(args, "--allowedTools")
	deny := listAfter(args, "--disallowedTools")
	for _, want := range []string{"Bash(curl *)", "Bash(wget *)", "WebFetch", "WebSearch", "Bash(git push *)",
		"Bash(gh gist *)", "Bash(op *)", "Bash(printenv *)", "Bash(env *)", "Bash(MB_URL=*)", "Bash(MB_API_KEY*)",
		"Write(./**)", "Edit(./**)", "Bash(gh api *)", "Read(**/.env)"} {
		if !slices.Contains(deny, want) {
			t.Errorf("not denied: %s", want)
		}
	}
	for _, rule := range allow {
		if strings.Contains(rule, "mb-sql") || strings.Contains(rule, "claude-review post") || rule == "Bash(*)" || rule == "Bash" {
			t.Errorf("pre-allowed, so it would not ask: %s", rule)
		}
	}
	for _, want := range []string{"Read(./**)", "Read(~/.claude/skills/metabase-sql/**)",
		"Bash(claude-review threads https://github.com/acme/widgets/pull/42)",
		"Bash(gh pr diff 42 -R acme/widgets)"} {
		if !slices.Contains(allow, want) {
			t.Errorf("not allowed: %s", want)
		}
	}
}

// listAfter is the values of a list flag: everything up to the next flag.
func listAfter(args []string, flag string) []string {
	i := slices.Index(args, flag)
	if i < 0 {
		return nil
	}
	var out []string
	for _, a := range args[i+1:] {
		if strings.HasPrefix(a, "--") {
			break
		}
		out = append(out, a)
	}
	return out
}

// Every row Claude has a session behind offers Open Claude, and none still
// offers the old fork link: Open Claude is the fork when nothing works.
func TestDecideOffersOpenClaudeOnEveryRow(t *testing.T) {
	f := RoundFacts{State: prOpen, Head: sha}
	for name, d := range map[string]Decision{
		"question":     decide(Round{Head: sha, StartedAt: time.Now()}, Outcome{Kind: OutcomeQuestion, Question: "run this?"}, f, widgetsPR),
		"post refused": decide(Round{Head: sha, PostRefused: true}, Outcome{Kind: OutcomeQuestion}, f, widgetsPR),
		"failed twice": decide(Round{Head: sha, Retries: 1}, Outcome{}, f, widgetsPR),
		"nothing":      decide(Round{Head: sha}, Outcome{Kind: OutcomeNothing}, f, widgetsPR),
	} {
		if !hasLink(row(d), verbURL(widgetsPR, "attach")) || hasLink(row(d), verbURL(widgetsPR, "fork")) {
			t.Errorf("%s: %+v", name, row(d))
		}
	}
}

// The link is not the gate: Open Claude forks only a PR in the review repos
// whose review has ended, while nothing works on it, under its lock; a PR
// Claude never looked at gets a notice instead. The old fork link does the
// same.
func TestOpenClaudeForksAnEndedReview(t *testing.T) {
	for _, verb := range []string{"attach", "fork"} {
		t.Run(verb, func(t *testing.T) {
			h, fs := clickHost(t, true)
			link := "claude-review://acme/widgets/42/" + verb
			if code := run([]string{link}, h.d); code != 0 || len(h.scripts) != 0 || !strings.Contains(notified(h), "has not looked at") {
				t.Fatalf("no review yet: exit %d, scripts %q, notified %q", code, h.scripts, notified(h))
			}
			endedReview(t, h, &PRState{State: stIdle}, false)
			fs.states[widgetsPR.RoundSessionID()] = "working"
			if code := run([]string{link}, h.d); code != 1 || len(h.scripts) != 0 {
				t.Fatalf("something working: exit %d, scripts %q", code, h.scripts)
			}
			fs.states[widgetsPR.RoundSessionID()] = jobDone
			h.locked = true
			if code := run([]string{link}, h.d); code != 1 || len(h.scripts) != 0 {
				t.Fatalf("ignored the PR's lock: exit %d", code)
			}
			h.locked = false
			if code := run([]string{link}, h.d); code != 0 {
				t.Fatalf("exit %d: %s", code, h.stderr)
			}
			script := strings.Join(mapValues(h.scripts), "\n")
			if !strings.Contains(script, "--fork-session") || !strings.Contains(script, widgetsPR.RoundSessionID().String()) || !strings.Contains(script, flagSettings) || !strings.Contains(script, flagStrictMCP) {
				t.Errorf("no locked-down fork of the PR's session: %q", script)
			}
			if code := run([]string{"claude-review://other/repo/1/" + verb}, h.d); code != 1 {
				t.Errorf("a PR outside the review repos: exit %d", code)
			}
		})
	}
}

// The question row is the user's until the user replies in the round's
// thread after it settled; the signal lives on GitHub.
func TestTickQuestionClearsOnceTheUserPosts(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	id, err := w.h.ledger.StartRound(pr, "h1", "test", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.h.ledger.MarkRead(id); err != nil {
		t.Fatal(err)
	}
	q := decide(Round{Head: "h1"}, Outcome{Kind: OutcomeQuestion}, RoundFacts{State: prOpen, Head: "h1"}, pr)
	if err := w.h.ledger.SetPRState(pr, *q.Next); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.elly.deleted) != 0 {
		t.Fatalf("cleared before the user posted: %v", w.elly.deleted)
	}
	w.gh.posted = true
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.elly.deleted) != 1 {
		t.Errorf("the user's post did not clear the question: %v", w.elly.deleted)
	}
}

// The skill's files are symlinks; the folder they resolve to needs its own
// Read rule, found at launch.
func TestMetabaseReadRulesFollowTheSymlink(t *testing.T) {
	home, real := t.TempDir(), t.TempDir()
	dir := filepath.Join(home, ".claude", "skills", "metabase-sql")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "SKILL.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "SKILL.md"), filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	want := "Read(/" + real + "/**)"
	if got := metabaseReadRules(home); !slices.Contains(got, want) {
		t.Errorf("%q lacks %s", got, want)
	}
}

// home-manager links the skill folder through two store paths before the
// clone (2026-11-16): ~/.claude/skills/metabase-sql -> <hm-files>/... ->
// <hm_metabasesql> -> clone. Claude Code checks a Read rule on every hop.
func TestMetabaseReadRulesAllowEveryHop(t *testing.T) {
	home, store, clone := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(clone, "SKILL.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hop2 := filepath.Join(store, "x-hm_metabasesql")
	hop1 := filepath.Join(store, "files", "metabase-sql")
	for _, d := range []string{filepath.Dir(hop1), filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range [][2]string{{clone, hop2}, {hop2, hop1}, {hop1, filepath.Join(home, ".claude", "skills", "metabase-sql")}} {
		if err := os.Symlink(l[0], l[1]); err != nil {
			t.Fatal(err)
		}
	}
	got := metabaseReadRules(home)
	for _, want := range []string{"Read(~/.claude/skills/metabase-sql/**)", "Read(/" + hop1 + "/**)", "Read(/" + hop2 + "/**)", "Read(/" + clone + "/**)"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q lacks %s", got, want)
		}
	}
}

// Open Claude opens a fork only from a stored state with no worker that
// may still be alive and no live Approve token beside it.
func TestOpenClaudeGateByState(t *testing.T) {
	cases := []struct {
		st     PRState
		token  bool
		opens  bool
		reason string
	}{
		{PRState{State: stIdle}, false, true, ""},
		{PRState{State: stHandled, Kind: kindPosted}, false, true, ""},
		{PRState{State: stStuck, Kind: kindFailed}, false, true, ""},
		{PRState{State: stHeld, Kind: kindFresh}, false, true, ""},
		{PRState{State: stAwaiting, Kind: kindDrafts}, false, true, ""},
		{PRState{State: stAwaiting, Kind: kindDismiss}, false, true, ""},
		{PRState{State: stHandled, Kind: kindNothing}, true, false, "Approve"},
		{PRState{State: stHeld, Kind: kindWindow}, false, false, "already open in a window"},
		{PRState{State: stRunning}, false, false, ""},
		{PRState{State: stStarting}, false, false, ""},
		{PRState{State: stAwaiting, Kind: kindQuestion}, false, false, ""},
		{PRState{State: stAwaiting, Kind: kindApprove}, true, false, ""},
	}
	for _, c := range cases {
		t.Run(string(c.st.State)+"/"+c.st.Kind, func(t *testing.T) {
			h, _ := clickHost(t, true)
			endedReview(t, h, &c.st, c.token)
			code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d)
			opened := len(h.scripts) > 0
			if opened != c.opens {
				t.Fatalf("opened %v, want %v (exit %d): %s", opened, c.opens, code, h.stderr)
			}
			if !c.opens && (code != 1 || !strings.Contains(h.stderr.String(), c.reason)) {
				t.Errorf("refusal: exit %d, %q lacks %q", code, h.stderr, c.reason)
			}
		})
	}
}

func endedReview(t *testing.T, h *fakeHost, st *PRState, token bool) {
	t.Helper()
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Accept(widgetsPR, sha, "base", time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	id, err := l.StartRound(widgetsPR, sha, "test", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.MarkRead(id); err != nil {
		t.Fatal(err)
	}
	if st != nil {
		st.Head = sha
		if err := l.SetPRState(widgetsPR, *st); err != nil {
			t.Fatal(err)
		}
	}
	if token {
		if _, err := l.MintToken(widgetsPR, sha); err != nil {
			t.Fatal(err)
		}
	}
}

// A fork runs under its own session id, recorded at launch, so tick and a
// click see its window although no session holds the round's id.
func TestForkWindowIsSeen(t *testing.T) {
	h, fs := clickHost(t, true)
	endedReview(t, h, &PRState{State: stIdle}, false)
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	fid, ok, err := l.ForkSession(widgetsPR)
	_ = l.Close()
	if err != nil || !ok || fid == widgetsPR.RoundSessionID() {
		t.Fatalf("fork id %v ok %v err %v", fid, ok, err)
	}
	if script := strings.Join(mapValues(h.scripts), "\n"); !strings.Contains(script, "--session-id'\\'' '\\''"+fid.String()) {
		t.Errorf("fork not launched under its recorded id: %q", script)
	}
	fs.window, fs.windowID = 4242, fid
	l, _ = OpenLedger(LedgerPath(h.d.StateDir))
	open := windowOpen(h.d.Sessions, l, widgetsPR)
	_ = l.Close()
	if !open {
		t.Error("the fork's window was not seen")
	}
	h.scripts = map[string]string{}
	if code := run([]string{"claude-review://acme/widgets/42/attach"}, h.d); code != 1 || len(h.scripts) != 0 || !strings.Contains(h.stderr.String(), "already open in a window") {
		t.Errorf("a second Open Claude beside the fork: exit %d, scripts %q, %s", code, h.scripts, h.stderr)
	}
}
