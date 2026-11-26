package main

import (
	"errors"
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
	if err := os.WriteFile(filepath.Join(project, id.String()+".jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LastMessage(dir, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if o := ReadOutcome(got); !o.Approves(widgetsPR, sha) {
		t.Errorf("last message %q read as %+v", got, o)
	}
	if got, err := LastMessage(dir, forkID, 0); got != "" || err != nil {
		t.Errorf("missing transcript: %q %v", got, err)
	}
}

// The round boundary: from is where the round's lines start, 0 reads the
// whole transcript as rows from before the offset did, and a transcript
// shorter than from (rewritten, or gone) is an error, never "".
func TestLastMessageFrom(t *testing.T) {
	dir := t.TempDir()
	id := widgetsPR.RoundSessionID()
	project := filepath.Join(dir, "projects", "-code-widgets")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if n := TranscriptSize(dir, id); n != 0 {
		t.Errorf("no transcript measures %d, want 0", n)
	}
	path := filepath.Join(project, id.String()+".jsonl")
	first := transcriptLine(t, turn("assistant", "First round."))
	appendTranscript(t, path, transcriptLine(t, turn("user", "Review.")), first)
	from := TranscriptSize(dir, id)
	appendTranscript(t, path, transcriptLine(t, turn("user", "Again.")))
	for _, c := range []struct {
		from int64
		want string
	}{{0, "First round."}, {from, ""}, {from - int64(len(first)) + 3, ""}, {TranscriptSize(dir, id), ""}} {
		if got, err := LastMessage(dir, id, c.from); got != c.want || err != nil {
			t.Errorf("from %d: %q %v, want %q", c.from, got, err, c.want)
		}
	}
	if err := os.WriteFile(path, []byte(transcriptLine(t, turn("assistant", "Rewritten."))), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LastMessage(dir, id, from); !errors.Is(err, errTranscriptShrunk) {
		t.Errorf("shorter than from: %q %v, want errTranscriptShrunk", got, err)
	}
	if got, err := LastMessage(dir, forkID, from); !errors.Is(err, errTranscriptShrunk) {
		t.Errorf("gone since the launch: %q %v, want errTranscriptShrunk", got, err)
	}
}
