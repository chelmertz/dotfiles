package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// LastMessage is the text of the session's last assistant message, from its
// transcript under claudeDir: where a round says how it ended. Subagent
// (sidechain) turns and tool calls are skipped. "" when there is none.
func LastMessage(claudeDir, id string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(claudeDir, "projects", "*", id+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	f, err := os.Open(matches[0]) //nolint:gosec // a transcript under the user's own Claude dir
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" || line.IsSidechain {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &blocks) != nil {
			continue
		}
		var text []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				text = append(text, b.Text)
			}
		}
		if len(text) > 0 {
			last = strings.Join(text, "\n")
		}
	}
	return last, sc.Err()
}
