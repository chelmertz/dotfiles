package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectPathFor(t *testing.T) {
	root := "/home/x/p"
	cases := map[string]string{
		"/home/x/p/m/dependabot":          "m/dependabot",
		"/home/x/p/m/dependabot/src/deep": "m/dependabot",
		"/home/x/p/m":                     "", // namespace dir, not a project
		"/home/x/p":                       "",
		"/home/x/code/matchi":             "",
		"/home/x/pp/m/x":                  "", // prefix trap
		"":                                "",
	}
	for cwd, want := range cases {
		if got := projectPathFor(root, cwd); got != want {
			t.Errorf("%q: got %q want %q", cwd, got, want)
		}
	}
}

func TestTransition(t *testing.T) {
	cases := []struct {
		in        HookInput
		kind, det string
		state     string // "" = no state change, "clear" = delete
	}{
		{HookInput{Event: "SessionStart", Source: "startup"}, "session_start", "startup", ""},
		{HookInput{Event: "UserPromptSubmit"}, "prompt", "", "claude"},
		{HookInput{Event: "Stop"}, "stop", "", "you"},
		{HookInput{Event: "Notification", NotificationType: "permission_prompt"}, "notification", "permission_prompt", "you"},
		{HookInput{Event: "Notification", NotificationType: "idle_prompt"}, "notification", "idle_prompt", "you"},
		{HookInput{Event: "Notification", NotificationType: "elicitation_dialog"}, "notification", "elicitation_dialog", "you"},
		{HookInput{Event: "Notification", NotificationType: "agent_needs_input"}, "notification", "agent_needs_input", "you"},
		{HookInput{Event: "Notification", NotificationType: "auth_success"}, "notification", "auth_success", ""},
		{HookInput{Event: "Notification", NotificationType: "agent_completed"}, "notification", "agent_completed", ""},
		{HookInput{Event: "PreCompact", Trigger: "auto"}, "pre_compact", "auto", ""},
		{HookInput{Event: "SessionEnd", Reason: "logout"}, "session_end", "logout", "clear"},
		{HookInput{Event: "SubagentStop"}, "subagent_stop", "", ""},
	}
	for _, c := range cases {
		kind, det, state := transition(c.in)
		if kind != c.kind || det != c.det || state != c.state {
			t.Errorf("%+v: got (%q,%q,%q) want (%q,%q,%q)", c.in, kind, det, state, c.kind, c.det, c.state)
		}
	}
}

func TestHookEndToEnd(t *testing.T) {
	s := openTestStore(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "m", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	in := `{"session_id":"s1","cwd":"` + root + `/m/a","hook_event_name":"UserPromptSubmit","prompt":"hi"}`
	if err := hook(s, root, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Ball != "claude" {
		t.Fatalf("%+v", ps)
	}
	// outside root: event only, no state, no error
	in = `{"session_id":"s2","cwd":"/elsewhere","hook_event_name":"Stop"}`
	if err := hook(s, root, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`select count(*) from session_event`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("events = %d", n)
	}
	// under root but never discovered: the hook registers it, like `open` does
	if err := os.MkdirAll(filepath.Join(root, "m", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	in = `{"session_id":"s3","cwd":"` + root + `/m/new","hook_event_name":"Stop"}`
	if err := hook(s, root, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.ListProjects()
	if len(ps) != 2 || ps[0].Path != "m/new" || ps[0].Ball != "you" {
		t.Fatalf("%+v", ps)
	}
	// a subagent's Stop is logged but does not hand the ball over
	if err := s.SetSessionState("s3", "m/new", "claude"); err != nil {
		t.Fatal(err)
	}
	in = `{"session_id":"s3","agent_id":"sub1","cwd":"` + root + `/m/new","hook_event_name":"Stop"}`
	if err := hook(s, root, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	if b := ballOf(t, s, "m/new"); b != "claude" {
		t.Fatalf("subagent stop moved the ball: %q", b)
	}
	// under root but the directory does not exist (deleted project): event with NULL project, no error
	in = `{"session_id":"s4","cwd":"` + root + `/m/gone","hook_event_name":"Stop"}`
	if err := hook(s, root, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`select count(*) from session_event where session_id='s4' and project_id is null`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("gone-dir event rows with null project = %d", n)
	}
	// SessionEnd clears
	in = `{"session_id":"s3","cwd":"` + root + `/m/new","hook_event_name":"SessionEnd","reason":"other"}`
	if err := hook(s, root, strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	if b := ballOf(t, s, "m/new"); b != "" {
		t.Fatalf("session end left ball %q", b)
	}
	// garbage and incomplete input are errors for the caller to log, not panics
	for _, bad := range []string{"not json", `{"cwd":"/x"}`} {
		if err := hook(s, root, strings.NewReader(bad)); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func ballOf(t *testing.T, s *Store, path string) string {
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
