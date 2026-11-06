package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActedOnCountsWritesOnly(t *testing.T) {
	const pr = "https://github.com/acme/widgets/pull/42"
	for _, c := range []struct {
		cmd  string
		want []string
	}{
		{"gh pr review " + pr + " --approve", []string{pr}},
		{"gh pr comment 42 --repo acme/widgets --body x", []string{pr}},
		{"gh pr comment 42 -R acme/widgets -b x", []string{pr}},
		{"gh api repos/acme/widgets/pulls/42/comments/7/replies -f body='ok'", []string{pr}},
		{"gh api -X PATCH repos/acme/widgets/issues/42 -f state=closed", []string{pr}},
		{"gh api --method POST repos/acme/widgets/pulls/42/reviews --input r.json", []string{pr}},
		{"cd x && gh pr review " + pr + " -c -b y", []string{pr}},
		// reads
		{"gh pr view " + pr + " --comments", nil},
		{"gh pr diff " + pr, nil},
		{"gh api repos/acme/widgets/pulls/42/comments --jq .", nil},
		{"gh api -X GET repos/acme/widgets/pulls/42/comments -f per_page=100", nil},
		{"gh pr comment 42 --body x", nil}, // no repo: which PR is unknown
		{"echo gh pr review", nil},
	} {
		got := actedOn(c.cmd)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: got %v, want %v", c.cmd, got, c.want)
		}
	}
}

func transcriptLine(t *testing.T, rec map[string]any) string {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func bashCall(session, at, cmd string) map[string]any {
	return map[string]any{"type": "assistant", "sessionId": session, "timestamp": at,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": cmd}}}}}
}

func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The newest write wins; a PR's URL in a tool result, a sidechain record, a
// subagent transcript, a round session or a half-written last line does not.
func TestIndexSessions(t *testing.T) {
	const (
		pr     = "https://github.com/acme/widgets/pull/42"
		other  = "https://github.com/acme/widgets/pull/7"
		author = "11111111-1111-1111-1111-111111111111"
		review = "22222222-2222-2222-2222-222222222222"
		reader = "33333333-3333-3333-3333-333333333333"
	)
	round := widgetsPR.RoundSessionID()
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "projects", "-a", author+".jsonl"),
		transcriptLine(t, map[string]any{"type": "pr-link", "sessionId": author, "prUrl": pr, "timestamp": "2026-11-01T10:00:00Z"}))
	writeTranscript(t, filepath.Join(dir, "projects", "-b", review+".jsonl"),
		transcriptLine(t, bashCall(review, "2026-11-02T10:00:00Z", "gh pr review "+pr+" --comment -b ok")))
	writeTranscript(t, filepath.Join(dir, "projects", "-c", reader+".jsonl"),
		// a read whose output carries other people's text naming both PRs
		transcriptLine(t, bashCall(reader, "2026-11-03T10:00:00Z", "gh pr view "+pr+" --comments")),
		transcriptLine(t, map[string]any{"type": "user", "sessionId": reader, "timestamp": "2026-11-03T10:00:01Z",
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "see " + other + " and gh pr review " + pr}}}}),
		transcriptLine(t, map[string]any{"type": "assistant", "isSidechain": true, "sessionId": reader, "timestamp": "2026-11-03T10:00:02Z",
			"message": bashCall(reader, "", "gh pr review "+pr+" -c -b x")["message"]}),
		// mid-write: no newline yet
		strings.TrimSuffix(transcriptLine(t, bashCall(reader, "2026-11-04T10:00:00Z", "gh pr review "+pr+" -c -b y")), "\n"))
	writeTranscript(t, filepath.Join(dir, "projects", "-c", reader, "subagents", "agent-x.jsonl"),
		transcriptLine(t, bashCall(reader, "2026-11-05T10:00:00Z", "gh pr review "+pr+" -c -b z")))
	writeTranscript(t, filepath.Join(dir, "projects", "-d", round+".jsonl"),
		transcriptLine(t, bashCall(round, "2026-11-06T10:00:00Z", "gh pr review "+pr+" -c -b r")))

	hits, err := IndexSessions(dir, isRoundSession)
	if err != nil {
		t.Fatal(err)
	}
	if h := hits[pr]; h.ID != review || !h.At.Equal(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("%s: got %+v, want the review session", pr, h)
	}
	if h, ok := hits[other]; ok {
		t.Errorf("a URL in a tool result chose a session: %+v", h)
	}
}

// Every listed PR with a session gets an info link to it, under its own
// source; a PR without one gets nothing.
func TestLinkSessions(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	e := &fakeElly{}
	h := tickHost{elly: e, now: time.Now, out: &strings.Builder{},
		sessionIndex: func() (map[string]SessionHit, error) {
			return map[string]SessionHit{widgetsPR.URL(): {ID: id}, "https://github.com/acme/gone/pull/1": {ID: id}}, nil
		}}
	h.linkSessions([]EllyPR{{Url: widgetsPR.URL()}, {Url: "https://github.com/acme/widgets/pull/43"}})
	got, ok := e.sourced["claude-session "+widgetsPR.URL()]
	if !ok || got.State != "info" || len(got.Links) != 1 || got.Links[0].URL != "claude-resume://"+id {
		t.Errorf("got %+v", e.sourced)
	}
	if len(e.sourced) != 1 {
		t.Errorf("linked PRs that are not listed or have no session: %+v", e.sourced)
	}
}
