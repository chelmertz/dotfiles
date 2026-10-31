package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLastMessage(t *testing.T) {
	dir := t.TempDir()
	id := widgetsPR.SessionID()
	project := filepath.Join(dir, "projects", "-code-widgets")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"user","message":{"content":"review it"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Looking."},{"type":"tool_use","name":"Bash"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Clean.\n\napprove with ` + "`! claude-review approve https://github.com/acme/widgets/pull/42 --at " + sha + "`" + `"}]}}
{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"subagent chatter"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}
not json
`
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LastMessage(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if o := ReadOutcome(got); o.Kind != OutcomeApprove || o.SHA != sha {
		t.Errorf("last message %q read as %+v", got, o)
	}
	if got, err := LastMessage(dir, "no-such-session"); got != "" || err != nil {
		t.Errorf("missing transcript: %q %v", got, err)
	}
}
