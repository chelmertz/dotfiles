package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The ball FSM, driven by realistic hook payloads through the real entry
// point. Each scenario asserts the ball after every step, because the
// approval gap (permission → tool runs → still "you") slipped past the
// per-event table test.

func mk(t *testing.T, root, path string) string {
	t.Helper()
	dir := filepath.Join(root, path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func feed(t *testing.T, s *Store, root, payload string) {
	t.Helper()
	if err := hook(s, root, strings.NewReader(payload)); err != nil {
		t.Fatalf("hook(%s): %v", payload, err)
	}
}

func ball(t *testing.T, s *Store, path string) string {
	t.Helper()
	ps, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Path == path {
			return p.Ball
		}
	}
	t.Fatalf("project %s not listed", path)
	return ""
}

func stateReason(t *testing.T, s *Store, sid string) (string, string) {
	t.Helper()
	st, why, err := s.SessionBall(sid)
	if err != nil {
		t.Fatal(err)
	}
	return st, why
}

func permRules(t *testing.T, s *Store, path string) []string {
	t.Helper()
	rows, err := s.db.Query(`select pr.rule from permission_request pr join project p on p.id = pr.project_id where p.path = ? order by pr.id`, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func countEvents(t *testing.T, s *Store, sid string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`select count(*) from session_event where session_id = ?`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func linkURLs(t *testing.T, s *Store, path string) []string {
	t.Helper()
	rows, err := s.db.Query(`select l.url from link l join project p on p.id = l.project_id where p.path = ? order by l.id`, path)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

func TestFSMApprovalRoundTrip(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	cwd := mk(t, root, "m/a")
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"SessionStart","source":"startup"}`)
	if b := ball(t, s, "m/a"); b != "" {
		t.Fatalf("start must not claim the ball: %q", b)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"UserPromptSubmit","prompt":"go"}`)
	if b := ball(t, s, "m/a"); b != "claude" {
		t.Fatalf("prompt → claude, got %q", b)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"Notification","notification_type":"permission_prompt"}`)
	if st, why := stateReason(t, s, "s1"); st != "you" || why != "permission_prompt" {
		t.Fatalf("permission → you/permission_prompt, got %s/%s", st, why)
	}
	// the approved tool runs: ball back to Claude, rule recorded
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"stdout":"ok","stderr":"","exit_code":0}}`)
	if b := ball(t, s, "m/a"); b != "claude" {
		t.Fatalf("approval must return the ball, got %q", b)
	}
	if got := permRules(t, s, "m/a"); !reflect.DeepEqual(got, []string{"Bash(go test *)"}) {
		t.Fatalf("rules %v", got)
	}
	// a second tool in the same turn: no new rule
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"`+cwd+`/x.go"},"tool_response":{}}`)
	if got := permRules(t, s, "m/a"); len(got) != 1 {
		t.Fatalf("rules %v", got)
	}
	// A question: PreToolUse fires before the dialog; Claude Code then sends
	// the same permission_prompt notification as a real ask plus idle
	// reminders, which must not overwrite the question reason. Answering
	// (PostToolUse) returns the ball and records no rule.
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[]}}`)
	if st, why := stateReason(t, s, "s1"); st != "you" || why != "question" {
		t.Fatalf("question → you/question, got %s/%s", st, why)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`)
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"Notification","notification_type":"idle_prompt","message":"Claude is waiting for your input"}`)
	if st, why := stateReason(t, s, "s1"); st != "you" || why != "question" {
		t.Fatalf("notifications must not mask the question, got %s/%s", st, why)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[]},"tool_response":{}}`)
	if got := permRules(t, s, "m/a"); len(got) != 1 {
		t.Fatalf("question recorded as a permission rule: %v", got)
	}
	if b := ball(t, s, "m/a"); b != "claude" {
		t.Fatalf("answered question must return the ball, got %q", b)
	}
	// other PreToolUse events (if ever registered) change nothing
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if b := ball(t, s, "m/a"); b != "claude" {
		t.Fatalf("PreToolUse Bash moved the ball: %q", b)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"Stop"}`)
	if st, why := stateReason(t, s, "s1"); st != "you" || why != "stop" {
		t.Fatalf("stop → you/stop, got %s/%s", st, why)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	if st, why := stateReason(t, s, "s1"); st != "you" || why != "idle_prompt" {
		t.Fatalf("idle → you/idle_prompt, got %s/%s", st, why)
	}
	// a tool run after an idle prompt is not an approval: nothing recorded
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":{"stdout":"","stderr":"","exit_code":0}}`)
	if got := permRules(t, s, "m/a"); len(got) != 1 {
		t.Fatalf("idle-prompt tool recorded a rule: %v", got)
	}
	if b := ball(t, s, "m/a"); b != "claude" {
		t.Fatalf("a tool running means Claude works, got %q", b)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"SessionEnd","reason":"other"}`)
	if b := ball(t, s, "m/a"); b != "" {
		t.Fatalf("end must clear, got %q", b)
	}
	// PostToolUse and non-question PreToolUse are not logged; the question,
	// its two notifications and the six others are
	if n := countEvents(t, s, "s1"); n != 9 {
		t.Fatalf("events = %d, want 9", n)
	}
}

func TestFSMTwoSessionsOneProject(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	cwd := mk(t, root, "m/a")
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"UserPromptSubmit"}`)
	feed(t, s, root, `{"session_id":"s2","cwd":"`+cwd+`","hook_event_name":"UserPromptSubmit"}`)
	feed(t, s, root, `{"session_id":"s2","cwd":"`+cwd+`","hook_event_name":"Stop"}`)
	if b := ball(t, s, "m/a"); b != "you" {
		t.Fatalf("any waiting session wins: %q", b)
	}
	feed(t, s, root, `{"session_id":"s2","cwd":"`+cwd+`","hook_event_name":"SessionEnd","reason":"other"}`)
	if b := ball(t, s, "m/a"); b != "claude" {
		t.Fatalf("remaining session works: %q", b)
	}
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"SessionEnd","reason":"other"}`)
	if b := ball(t, s, "m/a"); b != "" {
		t.Fatalf("no sessions: %q", b)
	}
}

func TestFSMSubagentToolDoesNotFlip(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	cwd := mk(t, root, "m/a")
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"UserPromptSubmit"}`)
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"Notification","notification_type":"permission_prompt"}`)
	feed(t, s, root, `{"session_id":"s1","agent_id":"sub","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":{"stdout":""}}`)
	if st, _ := stateReason(t, s, "s1"); st != "you" {
		t.Fatalf("subagent tool flipped the ball: %s", st)
	}
	if got := permRules(t, s, "m/a"); len(got) != 0 {
		t.Fatalf("subagent tool recorded a rule: %v", got)
	}
}

func TestFSMLinkCapture(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	cwd := mk(t, root, "m/a")
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"UserPromptSubmit"}`)
	out := `{"stdout":"Creating pull request for feat in o/r\nhttps://github.com/o/r/pull/12\n","stderr":"","exit_code":0}`
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr create --fill"},"tool_response":`+out+`}`)
	if got := linkURLs(t, s, "m/a"); !reflect.DeepEqual(got, []string{"https://github.com/o/r/pull/12"}) {
		t.Fatalf("links %v", got)
	}
	// same URL again: no duplicate; a view is not a create
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"FOO=1 gh pr create --fill"},"tool_response":`+out+`}`)
	feed(t, s, root, `{"session_id":"s1","cwd":"`+cwd+`","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr view 13"},"tool_response":{"stdout":"https://github.com/o/r/pull/13"}}`)
	if got := linkURLs(t, s, "m/a"); len(got) != 1 {
		t.Fatalf("links %v", got)
	}
	var opened string
	if err := s.db.QueryRow(`select coalesce(opened_at,'') from link`).Scan(&opened); err != nil || opened == "" {
		t.Fatalf("opened_at %q %v", opened, err)
	}
}

func TestRuleFor(t *testing.T) {
	cases := []struct{ tool, input, want string }{
		{"Bash", `{"command":"go test ./..."}`, "Bash(go test *)"},
		{"Bash", `{"command":"FOO=1 gh pr view 12 --json state"}`, "Bash(gh pr view *)"},
		{"Bash", `{"command":"ls -la"}`, "Bash(ls *)"},
		{"Bash", `{"command":"git -C x status"}`, "Bash(git *)"},
		{"Bash", `{"command":"  "}`, "Bash"},
		{"Edit", `{"file_path":"/home/x/p/m/a/src/main.go"}`, "Edit(/home/x/p/m/a/src/**)"},
		{"Write", `{"file_path":"rel/file.txt"}`, "Write(rel/**)"},
		{"WebFetch", `{"url":"https://github.com/o/r/pull/1"}`, "WebFetch(domain:github.com)"},
		{"mcp__linear__create_issue", `{}`, "mcp__linear__create_issue"},
		{"Glob", `{"pattern":"**/*.go"}`, "Glob"},
		{"Bash", `not json`, "Bash"},
	}
	for _, c := range cases {
		if got := ruleFor(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s %s: got %q want %q", c.tool, c.input, got, c.want)
		}
	}
}

// A hook stores the claude PID behind the session, and a later reap with
// that process gone drops the ball while a live one keeps it.
func TestHookRecordsPIDAndReap(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	dir := mk(t, root, "m/a")
	old := claudePIDFor
	claudePIDFor = func() int { return 4242 }
	t.Cleanup(func() { claudePIDFor = old })
	feed(t, s, root, `{"session_id":"s1","hook_event_name":"UserPromptSubmit","cwd":"`+dir+`"}`)
	var pid int
	if err := s.db.QueryRow(`select pid from session_state where session_id = 's1'`).Scan(&pid); err != nil || pid != 4242 {
		t.Fatalf("pid=%d %v", pid, err)
	}
	if n, err := s.ReapDead(func(pid int) bool { return pid == 4242 }); err != nil || n != 0 || ball(t, s, "m/a") != "claude" {
		t.Fatalf("live process must keep the ball: n=%d %v", n, err)
	}
	if n, err := s.ReapDead(func(int) bool { return false }); err != nil || n != 1 || ball(t, s, "m/a") != "" {
		t.Fatalf("dead process must drop the ball: n=%d ball=%q %v", n, ball(t, s, "m/a"), err)
	}
}
