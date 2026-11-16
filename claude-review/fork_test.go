package main

import (
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
	args := ForkArgs(widgetsPR, "upstream", metabaseReadRules(t.TempDir()))
	id := widgetsPR.RoundSessionID().String()
	head := []string{claudeCmd, "--resume", id, "--fork-session",
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

func TestDecideOffersAForkOnAQuestionOnly(t *testing.T) {
	r := Round{Head: sha, StartedAt: time.Now()}
	f := RoundFacts{State: prOpen, Head: sha}
	q := decide(r, Outcome{Kind: OutcomeQuestion, Question: "run this?"}, f, widgetsPR)
	if !hasLink(q.Annotation, verbURL(widgetsPR, "fork")) {
		t.Errorf("a question offers no fork: %+v", q.Annotation)
	}
	refused := decide(Round{Head: sha, PostRefused: true}, Outcome{Kind: OutcomeQuestion}, f, widgetsPR)
	failed := decide(Round{Head: sha, Retries: 1}, Outcome{}, f, widgetsPR)
	for name, d := range map[string]Decision{"post refused": refused, "failed twice": failed} {
		if hasLink(d.Annotation, verbURL(widgetsPR, "fork")) {
			t.Errorf("%s offers a fork: %+v", name, d.Annotation)
		}
	}
}

// The link is not the gate: a forged one for a PR with no question, outside
// the review repos, or while a round works opens nothing.
func TestForkVerbOpensOnlyOnAWaitingQuestion(t *testing.T) {
	h, fs := clickHost(t, true)
	link := "claude-review://acme/widgets/42/fork"
	if code := run([]string{link}, h.d); code != 1 || len(h.scripts) != 0 {
		t.Fatalf("no question waiting: exit %d, scripts %q", code, h.scripts)
	}
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	q := decide(Round{Head: sha}, Outcome{Kind: OutcomeQuestion}, RoundFacts{State: prOpen, Head: sha}, widgetsPR)
	if err := l.SetAnnotation(widgetsPR, *q.Annotation); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	fs.states[widgetsPR.RoundSessionID()] = "working"
	if code := run([]string{link}, h.d); code != 1 || len(h.scripts) != 0 {
		t.Fatalf("a working round: exit %d, scripts %q", code, h.scripts)
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
	if !strings.Contains(script, "--fork-session") || !strings.Contains(script, widgetsPR.RoundSessionID().String()) {
		t.Errorf("no fork of the round session: %q", script)
	}
	if code := run([]string{"claude-review://other/repo/1/fork"}, h.d); code != 1 {
		t.Errorf("a PR outside the review repos: exit %d", code)
	}
}

// The question row is the user's until the user's account posts on the PR
// after the round settled; the signal lives on GitHub.
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
	if err := w.h.ledger.SetAnnotation(pr, *q.Annotation); err != nil {
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
