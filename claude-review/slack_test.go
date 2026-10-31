package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// greenPR is a PR the gate lets through: open, mergeable, one passing check
// run and one passing Buildkite status, one answered and one resolved thread.
const greenPR = `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","author":{"login":"ada"},
 "statusCheckRollup":[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"},
                      {"context":"buildkite/backend","conclusion":null,"state":"SUCCESS"}],
 "commits":[{"messageHeadline":"old work","committedDate":"2026-09-30T10:00:00Z"},
            {"messageHeadline":"seat players in joining order","committedDate":"2026-10-02T10:00:00Z"}]}`

// greenReviews has the person bo as the reviewer: an app's review, the
// author's own and an older one by cy all come after or before and lose.
const greenReviews = `[{"user":{"login":"bo","type":"User"},"submitted_at":"2026-10-01T10:00:00Z"},
 {"user":{"login":"github-actions[bot]","type":"Bot"},"submitted_at":"2026-10-03T10:00:00Z"},
 {"user":{"login":"coderabbitai[bot]","type":"Bot"},"submitted_at":"2026-10-03T11:00:00Z"},
 {"user":{"login":"ada","type":"User"},"submitted_at":"2026-10-04T10:00:00Z"},
 {"user":{"login":"cy","type":"User"},"submitted_at":"2026-09-01T10:00:00Z"}]`

const greenThreads = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
 {"isResolved":false,"path":"a.go","comments":{"nodes":[
   {"author":{"login":"bo"},"body":"why?","createdAt":"2026-10-01T09:00:00Z"},
   {"author":{"login":"ada"},"body":"because\nof X","createdAt":"2026-10-02T09:00:00Z"}]},
  "lastComment":{"nodes":[{"author":{"login":"ada"}}]}},
 {"isResolved":true,"path":"b.go","comments":{"nodes":[
   {"author":{"login":"bo"},"body":"nit","createdAt":"2026-10-01T09:00:00Z"}]}}]}}}}}` + "\n" +
	`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
 {"isResolved":false,"path":"c.go","comments":{"nodes":[]}}]}}}}}`

type slackHost struct {
	gh      *fakeGh
	exists  bool
	reply   string
	outErr  error
	outputs [][]string
	// realClaude runs the claude on PATH through the host's own capture.
	realClaude bool
	stdout     bytes.Buffer
	stderr     bytes.Buffer
}

func (h *slackHost) run(args ...string) int {
	d := Deps{
		Stdout:        &h.stdout,
		Stderr:        &h.stderr,
		ClaudeDir:     "/claude",
		Gh:            h.gh.run,
		SessionExists: func(string, string) bool { return h.exists },
		Capture: func(dir string, argv []string) (string, error) {
			h.outputs = append(h.outputs, argv)
			return h.reply, h.outErr
		},
	}
	if h.realClaude {
		d.Capture = execStdout
	}
	return run(append([]string{"slack"}, args...), d)
}

func ghFor(prs ...string) *fakeGh {
	g := &fakeGh{}
	for _, p := range prs {
		g.outs = append(g.outs, p, greenReviews, greenThreads)
	}
	return g
}

func TestSlack_GreenPRGetsALine(t *testing.T) {
	h := &slackHost{gh: ghFor(greenPR), exists: true, reply: "  - \"two late comments answered\"\n"}
	if code := h.run(prURL); code != 0 || h.stderr.String() != "" {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	if got := h.stdout.String(); got != "- "+prURL+": two late comments answered\n" {
		t.Errorf("stdout %q", got)
	}
	sameArgv(t, "pr view", h.gh.calls[0], []string{"pr", "view", prURL, "--json", prViewFields})
	sameArgv(t, "reviews", h.gh.calls[1], []string{api, paginate, reviewsURL})
	sameArgv(t, "threads", h.gh.calls[2], []string{api, "graphql", paginate, "-F", "owner=acme", "-F", "name=widgets", "-F", "number=42", "-f", "query=" + threadsQuery})
	argv := h.outputs[0]
	sameArgv(t, "claude", append(argv[:8:8], argv[9:]...), []string{claudeBin, "-p", resume, widgetsPR.SessionID(), "--no-session-persistence", "--strict-mcp-config", "--max-turns", "1", "--tools", ""})
	prompt := argv[8]
	for _, want := range []string{
		"pings the reviewer about " + prURL + ":",
		"Commits since then:\n",
		"Reviewer: bo, last review 2026-10-01T10:00:00Z.",
		"- seat players in joining order\n",
		"- a.go, ada: because of X\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "old work") || strings.Contains(prompt, "{url}") {
		t.Errorf("prompt has a commit from before the review, or a placeholder:\n%s", prompt)
	}
}

func TestSlack_WithoutSessionStartsFresh(t *testing.T) {
	h := &slackHost{gh: &fakeGh{outs: []string{greenPR, "[]", greenThreads}}, reply: "first look"}
	if code := h.run(prURL); code != 0 {
		t.Fatalf("code %d stderr %q", code, h.stderr.String())
	}
	argv := h.outputs[0]
	if argv[2] != "--no-session-persistence" {
		t.Errorf("fresh run must not resume: %q", argv)
	}
	if p := argv[6]; !strings.Contains(p, "Nobody has reviewed it yet.") || !strings.Contains(p, "Commits:\n- old work") || strings.Contains(p, "Replies on") {
		t.Errorf("prompt:\n%s", p)
	}
}

func TestSlack_GateRefuses(t *testing.T) {
	for name, c := range map[string]struct{ old, new, want string }{
		"red check":         {`"conclusion":"SUCCESS"`, `"conclusion":"FAILURE"`, "1 checks red: test"},
		"buildkite red":     {`"state":"SUCCESS"`, `"state":"FAILURE"`, "1 checks red: buildkite/backend"},
		"buildkite pending": {`"state":"SUCCESS"`, `"state":"PENDING"`, "1 checks pending: buildkite/backend"},
		"check running":     {`"status":"COMPLETED","conclusion":"SUCCESS"`, `"status":"IN_PROGRESS","conclusion":""`, "1 checks pending: test"},
		"conflict":          {`"MERGEABLE"`, `"CONFLICTING"`, "it has a merge conflict"},
		"mergeable unknown": {`"MERGEABLE"`, `"UNKNOWN"`, "not computed mergeability"},
		"draft":             {`"isDraft":false`, `"isDraft":true`, "it is a draft"},
		"merged":            {`"OPEN"`, `"MERGED"`, "it is merged"},
		"open thread":       {`"author":{"login":"ada"},`, `"author":{"login":"eve"},`, "1 review threads wait on the author: a.go"},
	} {
		h := &slackHost{gh: ghFor(strings.Replace(greenPR, c.old, c.new, 1)), reply: "words"}
		code := h.run(prURL)
		if code != 1 || h.stdout.String() != "" || len(h.outputs) != 0 || !strings.Contains(h.stderr.String(), c.want) {
			t.Errorf("%s: code %d stdout %q claude runs %d stderr %q", name, code, h.stdout.String(), len(h.outputs), h.stderr.String())
		}
	}
}

// A thread's comments come 100 at a time; the last word is read on its own
// so a long thread is judged by its real last comment.
func TestSlack_LongThreadIsJudgedByItsLastComment(t *testing.T) {
	long := strings.Replace(greenThreads, `"lastComment":{"nodes":[{"author":{"login":"ada"}}]}`, `"lastComment":{"nodes":[{"author":{"login":"bo"}}]}`, 1)
	h := &slackHost{gh: &fakeGh{outs: []string{greenPR, greenReviews, long}}, reply: "words"}
	if code := h.run(prURL); code != 1 || !strings.Contains(h.stderr.String(), "1 review threads wait on the author: a.go") {
		t.Errorf("code %d stderr %q", code, h.stderr.String())
	}
}

func TestSlack_RefusedPRDoesNotStopTheOthers(t *testing.T) {
	draft := strings.Replace(greenPR, `"isDraft":false`, `"isDraft":true`, 1)
	h := &slackHost{gh: ghFor(draft, greenPR), exists: true, reply: "ready"}
	code := h.run(prURL, "acme/widgets/43")
	if code != 1 || h.stdout.String() != "- https://github.com/acme/widgets/pull/43: ready\n" {
		t.Errorf("code %d stdout %q", code, h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), prURL+" not sent: it is a draft") {
		t.Errorf("stderr %q", h.stderr.String())
	}
}

func TestSlack_DryRunPrintsTheCommand(t *testing.T) {
	h := &slackHost{gh: ghFor(greenPR), exists: true}
	if code := h.run(dryRunFlag, prURL); code != 0 || len(h.outputs) != 0 {
		t.Fatalf("code %d claude runs %d", code, len(h.outputs))
	}
	if out := h.stdout.String(); !strings.HasPrefix(out, "'claude' '-p' '--resume' '"+widgetsPR.SessionID()+"' ") || !strings.HasSuffix(out, " '--tools' ''\n") {
		t.Errorf("stdout %q", out)
	}
}

func TestSlack_Failures(t *testing.T) {
	boom := errors.New("exit status 1")
	for name, c := range map[string]struct {
		h    *slackHost
		args []string
		code int
		want string
	}{
		"no args":      {&slackHost{gh: &fakeGh{}}, nil, 2, slackUsage},
		"bad flag":     {&slackHost{gh: &fakeGh{}}, []string{bogusFlag}, 2, "bogus"},
		"bad target":   {&slackHost{gh: &fakeGh{}}, []string{prURL, badTarget}, 2, "expected a GitHub PR URL"},
		"pr view":      {&slackHost{gh: &fakeGh{outs: []string{notFound}, errs: []error{boom}}}, []string{prURL}, 1, "gh pr view: exit status 1\ngh: Not Found"},
		"pr view json": {&slackHost{gh: &fakeGh{outs: []string{"nope"}}}, []string{prURL}, 1, "gh pr view: invalid"},
		"reviews":      {&slackHost{gh: &fakeGh{outs: []string{greenPR, notFound}, errs: []error{nil, boom}}}, []string{prURL}, 1, reviewsURL + ": exit status 1"},
		"threads":      {&slackHost{gh: &fakeGh{outs: []string{greenPR, "[]", notFound}, errs: []error{nil, nil, boom}}}, []string{prURL}, 1, "review threads: exit status 1"},
		"threads json": {&slackHost{gh: &fakeGh{outs: []string{greenPR, "[]", "{"}}}, []string{prURL}, 1, "review threads: unexpected EOF"},
		"claude":       {&slackHost{gh: ghFor(greenPR), reply: "Not logged in\n", outErr: boom}, []string{prURL}, 1, "claude: exit status 1 (Not logged in)"},
		"claude quiet": {&slackHost{gh: ghFor(greenPR), outErr: boom}, []string{prURL}, 1, "claude: exit status 1\n"},
		"bad words":    {&slackHost{gh: ghFor(greenPR), reply: "see #12"}, []string{prURL}, 1, "names a PR by number"},
	} {
		code := c.h.run(c.args...)
		if code != c.code || !strings.Contains(c.h.stderr.String(), c.want) || c.h.stdout.String() != "" {
			t.Errorf("%s: code %d stdout %q stderr %q", name, code, c.h.stdout.String(), c.h.stderr.String())
		}
	}
}

func TestSlackWords(t *testing.T) {
	long := strings.Repeat("x", maxSlackWords+1)
	for reply, want := range map[string]string{
		"two late comments answered":             "two late comments answered",
		"  “players seated in order”  \n":        "players seated in order",
		"- 'quoted and bulleted'":                "quoted and bulleted",
		"* `code`":                               "code",
		"1. numbered":                            "numbered",
		"- " + prURL + ": the whole line echoed": "the whole line echoed",
		"\"\"nested\"\"":                         "nested",
		"players' order":                         "players' order",
		"'":                                      "'",
		strings.Repeat("y", maxSlackWords):       strings.Repeat("y", maxSlackWords),
		"ERR:two\nlines":                         "",
		"ERR:":                                   "",
		"ERR:\"\"":                               "",
		"ERR:" + long:                            "",
		"ERR:follow-up in owner/repo#3":          "",
		"ERR:fixed in #3":                        "",
	} {
		in, wantErr := strings.CutPrefix(reply, "ERR:")
		got, err := slackWords(widgetsPR, in)
		if wantErr != (err != nil) || got != want {
			t.Errorf("%q: got %q, err %v", in, got, err)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("a\n b  c", 5); got != "a b c" {
		t.Errorf("short: %q", got)
	}
	if got := clip("åäö åäö", 3); got != "åäö..." {
		t.Errorf("long: %q", got)
	}
}

// Claude Code writes warnings to stderr. They must neither break a valid
// reply nor become the line themselves when stdout is empty.
func TestSlack_ClaudeStderrIsNotTheWords(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	for name, c := range map[string]struct{ script, stdout, stderr string }{
		"warning and reply": {"echo 'warning: update available' >&2; echo 'two late comments answered'", "- " + prURL + ": two late comments answered\n", ""},
		"warning only":      {"echo 'warning: update available' >&2", "", "claude replied with nothing"},
	} {
		if err := writeFile(bin+"/claude", "#!/bin/sh\n"+c.script+"\n"); err != nil {
			t.Fatal(err)
		}
		h := &slackHost{gh: ghFor(greenPR), realClaude: true}
		h.run(prURL)
		if h.stdout.String() != c.stdout || !strings.Contains(h.stderr.String(), c.stderr) {
			t.Errorf("%s: stdout %q stderr %q", name, h.stdout.String(), h.stderr.String())
		}
	}
}
