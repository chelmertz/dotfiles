package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const claudeJSONBody = `{
  "numStartups": 1793613068945123456789,
  "projects": {
    "/h/code/other": {"hasTrustDialogAccepted": true, "allowedTools": []},
    "/h/code/widgets": {"hasTrustDialogAccepted": false, "lastCost": 0.25}
  },
  "userID": "abc"
}`

func writeClaudeJSON(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func rawKeys(t *testing.T, b []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A clone Claude Code has not trusted refuses `claude --bg`; claude-review
// trusts it by setting that one key, and keeps everything else as it was.
func TestEnsureTrustedSetsOnlyThatKey(t *testing.T) {
	for _, dir := range []string{"/h/code/widgets", "/h/code/gadgets"} {
		p := writeClaudeJSON(t, claudeJSONBody)
		if err := ensureTrusted(p, dir); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		b, err := os.ReadFile(p) //nolint:gosec // test file
		if err != nil {
			t.Fatal(err)
		}
		top := rawKeys(t, b)
		if string(top["numStartups"]) != "1793613068945123456789" || string(top["userID"]) != `"abc"` {
			t.Errorf("%s: other keys changed: %s", dir, b)
		}
		projects := rawKeys(t, top["projects"])
		mine := rawKeys(t, projects[dir])
		if string(mine["hasTrustDialogAccepted"]) != "true" {
			t.Errorf("%s: not trusted: %s", dir, projects[dir])
		}
		if dir == "/h/code/widgets" && string(mine["lastCost"]) != "0.25" {
			t.Errorf("the project's other keys changed: %s", projects[dir])
		}
		if other := rawKeys(t, projects["/h/code/other"]); string(other["allowedTools"]) != "[]" || len(projects) < 2 {
			t.Errorf("another project changed: %s", projects)
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v %v", fi.Mode(), err)
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*")); len(left) != 1 {
			t.Errorf("temp files left behind: %q", left)
		}
	}
}

func TestEnsureTrustedLeavesATrustedCloneUntouched(t *testing.T) {
	p := writeClaudeJSON(t, claudeJSONBody)
	if err := ensureTrusted(p, "/h/code/other"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != claudeJSONBody { //nolint:gosec // test file
		t.Errorf("rewrote a file that needed no change:\n%s", b)
	}
}

func TestEnsureTrustedRefusesWhatItCannotRead(t *testing.T) {
	p := writeClaudeJSON(t, "{half")
	if err := ensureTrusted(p, "/h/code/widgets"); err == nil {
		t.Error("an unparseable file was trusted")
	}
	if b, _ := os.ReadFile(p); string(b) != "{half" { //nolint:gosec // test file
		t.Errorf("an unparseable file was rewritten: %s", b)
	}
	if err := ensureTrusted(filepath.Join(t.TempDir(), "missing.json"), "/h/code/widgets"); err == nil || !strings.Contains(err.Error(), "missing.json") {
		t.Errorf("a missing file: %v", err)
	}
}
