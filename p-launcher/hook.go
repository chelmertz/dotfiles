package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// HookInput is the subset of the Claude Code hook stdin JSON that p-launcher
// uses (https://code.claude.com/docs/en/hooks). Unknown fields are ignored.
type HookInput struct {
	SessionID        string `json:"session_id"`
	Cwd              string `json:"cwd"`
	Event            string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"` // Notification
	Trigger          string `json:"trigger"`           // PreCompact: manual|auto
	Reason           string `json:"reason"`            // SessionEnd
	Source           string `json:"source"`            // SessionStart
	AgentID          string `json:"agent_id"`          // set when a subagent fired the hook
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
