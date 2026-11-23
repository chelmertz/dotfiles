package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// transcriptPath is the session's transcript under claudeDir, "" when
// there is none.
func transcriptPath(claudeDir string, id ids.SessionID) (string, error) {
	matches, err := filepath.Glob(filepath.Join(claudeDir, "projects", "*", id.String()+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	return matches[0], nil
}

// TranscriptSize is the byte size of the session's transcript under
// claudeDir: where the next round's lines start, recorded as the round's
// transcript_from. 0 when there is no transcript yet, or it cannot be
// read; 0 reads the whole transcript.
func TranscriptSize(claudeDir string, id ids.SessionID) int64 {
	path, err := transcriptPath(claudeDir, id)
	if err != nil || path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// errTranscriptShrunk: the transcript is shorter than it was at the
// round's launch (rewritten, or gone), so where the round starts is lost.
var errTranscriptShrunk = errors.New("the transcript is shorter than at the round's launch")

// LastMessage is the text of the last assistant message the session wrote
// from byte offset from of its transcript under claudeDir on: where a round
// says how it ended. from is the transcript's size at the round's launch,
// so an earlier round's words are never read as this one's; 0 reads the
// whole transcript. Subagent (sidechain) turns and tool calls are skipped.
// "" when the round wrote none. A transcript shorter than from is an error,
// never "": the round's lines cannot be told apart.
func LastMessage(claudeDir string, id ids.SessionID, from int64) (string, error) {
	path, err := transcriptPath(claudeDir, id)
	if err != nil {
		return "", err
	}
	if path == "" {
		if from > 0 {
			return "", errTranscriptShrunk
		}
		return "", nil
	}
	f, err := os.Open(path) //nolint:gosec // a transcript under the user's own Claude dir
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() < from {
		return "", fmt.Errorf("%w (%d bytes, %d then)", errTranscriptShrunk, fi.Size(), from)
	}
	// A from inside a line leaves a fragment, which parses as no record.
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return "", err
	}
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		if text := assistantText(sc.Bytes()); text != "" {
			last = text
		}
	}
	return last, sc.Err()
}

// assistantText is the text of one transcript record when it is a main-chain
// assistant message with any: "" for every other record, a tool call, or a
// line that does not parse.
func assistantText(record []byte) string {
	var line struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(record, &line) != nil || line.Type != "assistant" || line.IsSidechain {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return ""
	}
	var text []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			text = append(text, b.Text)
		}
	}
	return strings.Join(text, "\n")
}
