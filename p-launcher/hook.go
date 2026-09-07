package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// HookInput is the subset of the Claude Code hook stdin JSON that p-launcher
// uses (https://code.claude.com/docs/en/hooks). Unknown fields are ignored.
type HookInput struct {
	SessionID        string          `json:"session_id"`
	Cwd              string          `json:"cwd"`
	Event            string          `json:"hook_event_name"`
	NotificationType string          `json:"notification_type"` // Notification
	Trigger          string          `json:"trigger"`           // PreCompact: manual|auto
	Reason           string          `json:"reason"`            // SessionEnd
	Source           string          `json:"source"`            // SessionStart
	AgentID          string          `json:"agent_id"`          // set when a subagent fired the hook
	ToolName         string          `json:"tool_name"`         // PostToolUse
	ToolInput        json.RawMessage `json:"tool_input"`
	ToolResponse     json.RawMessage `json:"tool_response"`
}

// projectPathFor maps a session cwd to "namespace/name" when cwd is inside a
// project directory under root (any depth), else "".
func projectPathFor(root, cwd string) string {
	if cwd == "" {
		return ""
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || !cleanSegment(parts[0]) || !cleanSegment(parts[1]) {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// waitsOnUser lists the notification types that mean Claude is blocked until
// the user acts. auth/quota/agent_completed and elicitation results are not.
var waitsOnUser = map[string]bool{
	"permission_prompt":      true,
	"idle_prompt":            true,
	"elicitation_dialog":     true,
	"elicitation_url_dialog": true,
	"agent_needs_input":      true,
}

// transition maps a hook event to the event-log kind and detail, and to the
// ball state it implies: "claude" (working), "you" (waiting on the user),
// "clear" (session over) or "" (no change). Subagent hooks (AgentID set)
// are logged but never move the ball; hook() enforces that.
func transition(in HookInput) (kind, detail, state string) {
	switch in.Event {
	case "SessionStart":
		return "session_start", in.Source, ""
	case "UserPromptSubmit":
		return "prompt", "", "claude"
	case "Stop":
		return "stop", "", "you"
	case "Notification":
		if waitsOnUser[in.NotificationType] {
			return "notification", in.NotificationType, "you"
		}
		return "notification", in.NotificationType, ""
	case "PreCompact":
		return "pre_compact", in.Trigger, ""
	case "SessionEnd":
		return "session_end", in.Reason, "clear"
	}
	return snake(in.Event), "", ""
}

// snake turns "SubagentStop" into "subagent_stop" for events we log but do
// not otherwise know.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r | 0x20)
	}
	return b.String()
}

// hook handles one Claude Code hook invocation: decode stdin, resolve the
// project, log the event, update the session's ball state. A cwd under root
// whose project is not yet in the DB is registered first (as `open` does);
// a cwd whose directory is gone is logged without a project. Errors are
// returned for main to log; the exit code stays 0 regardless.
func hook(s *Store, root string, r io.Reader) error {
	var in HookInput
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return fmt.Errorf("decode hook input: %w", err)
	}
	if in.SessionID == "" || in.Event == "" {
		return errors.New("hook input missing session_id or hook_event_name")
	}
	path := projectPathFor(root, in.Cwd)
	if path != "" {
		if _, f, err := projectDir(root, path); err == nil {
			if err := s.UpsertProjects([]Found{f}); err != nil {
				return err
			}
		} else {
			path = "" // directory gone: keep the event, drop the project link
		}
	}
	if in.Event == "PostToolUse" {
		// Not logged as an event (one per tool call would swamp the log);
		// it only hands the ball back after an approval and captures links.
		return postToolUse(s, path, in)
	}
	kind, detail, state := transition(in)
	if in.AgentID != "" {
		state = "" // a subagent's Stop is not the session's turn ending
	}
	if err := s.RecordSessionEvent(SessionEvent{SessionID: in.SessionID, Path: path, Cwd: in.Cwd, Kind: kind, Detail: detail}); err != nil {
		return err
	}
	switch {
	case state == "clear":
		return s.ClearSession(in.SessionID)
	case state != "" && path != "":
		reason := ""
		if state == "you" {
			reason = detail // notification type
			if in.Event == "Stop" {
				reason = "stop"
			}
		}
		return s.SetSessionState(in.SessionID, path, state, reason)
	}
	return nil
}

// postToolUse runs after every tool call. A tool running means Claude is
// working: if the session was waiting on a permission, that permission was
// granted for this tool, so the allowlist rule is recorded and the ball
// returns. Any other "you" state (stop, idle prompt) also flips back, without
// a rule. Subagent tool calls change nothing. A pull-request creation's
// output becomes a link for the session's project.
func postToolUse(s *Store, path string, in HookInput) error {
	if in.AgentID != "" {
		return nil
	}
	state, reason, err := s.SessionBall(in.SessionID)
	if err != nil {
		return err
	}
	if state == "you" {
		// AskUserQuestion dialogs arrive as permission_prompt notifications too;
		// answering one is not granting a permission, so no allowlist rule.
		if reason == "permission_prompt" && in.ToolName != "AskUserQuestion" {
			if err := s.RecordPermission(in.SessionID, path, in.ToolName, ruleFor(in.ToolName, in.ToolInput)); err != nil {
				return err
			}
		}
		if path != "" {
			if err := s.SetSessionState(in.SessionID, path, "claude", ""); err != nil {
				return err
			}
		}
	}
	if in.ToolName == "Bash" && path != "" {
		var ti struct {
			Command string `json:"command"`
		}
		var tr struct {
			Stdout string `json:"stdout"`
		}
		_ = json.Unmarshal(in.ToolInput, &ti)
		_ = json.Unmarshal(in.ToolResponse, &tr)
		if isPRCreate(ti.Command) {
			for _, u := range prURLs(tr.Stdout) {
				if err := s.AddLink(path, u); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// bashWords splits a command and drops leading VAR=value assignments, the
// way the allowlist matcher does.
func bashWords(cmd string) []string {
	words := strings.Fields(cmd)
	for len(words) > 0 && strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "=") {
		words = words[1:]
	}
	return words
}

// isPRCreate recognises the gh subcommand that opens a pull request.
func isPRCreate(cmd string) bool {
	w := bashWords(cmd)
	return len(w) >= 3 && w[0] == "gh" && w[1] == "pr" && w[2] == "create"
}

var prURLRe = regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+/pull/\d+`)

func prURLs(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range prURLRe.FindAllString(s, -1) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// subcommandTools are commands whose first argument names a subcommand, so
// the rule keeps it: Bash(git status *) rather than Bash(git *).
var subcommandTools = map[string]bool{"gh": true, "git": true, "go": true, "kubectl": true, "docker": true, "npm": true, "pnpm": true, "yarn": true, "make": true, "cargo": true, "terraform": true, "systemctl": true, "journalctl": true, "nix": true, "home-manager": true}

// ruleFor derives the settings.json allowlist rule that would have granted
// this tool call. Bash keeps one to three words with a trailing wildcard;
// file tools keep the directory; WebFetch keeps the domain; MCP tools and
// everything else are the bare tool name.
func ruleFor(tool string, input json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(input, &m)
	str := func(k string) string {
		v, _ := m[k].(string)
		return v
	}
	switch tool {
	case "Bash":
		w := bashWords(str("command"))
		switch {
		case len(w) == 0:
			return "Bash"
		case len(w) >= 2 && subcommandTools[w[0]] && !strings.HasPrefix(w[1], "-"):
			// gh has two-level subcommands (gh pr view); keep the third word too
			if w[0] == "gh" && len(w) >= 3 && !strings.HasPrefix(w[2], "-") {
				return "Bash(" + w[0] + " " + w[1] + " " + w[2] + " *)"
			}
			return "Bash(" + w[0] + " " + w[1] + " *)"
		}
		return "Bash(" + w[0] + " *)"
	case "Edit", "Write", "Read", "MultiEdit", "NotebookEdit":
		if fp := str("file_path"); fp != "" {
			return tool + "(" + filepath.Dir(fp) + "/**)"
		}
		return tool
	case "WebFetch":
		if u, err := url.Parse(str("url")); err == nil && u.Host != "" {
			return "WebFetch(domain:" + u.Host + ")"
		}
		return tool
	}
	return tool
}
