package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// sessionSource is the elly annotation that links a PR to the Claude session
// that last acted on it, opened by the claude-resume:// handler.
const sessionSource = "claude-session"

// sessionLinkTTL outlives a tick or two, so a session whose transcript
// Claude Code deleted drops off the row on its own.
const sessionLinkTTL = time.Hour

// SessionHit is the session that last acted on a PR, and when.
type SessionHit struct {
	ID ids.SessionID
	At time.Time
}

var (
	prURLRe = regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/([0-9]+)`)
	// a gh pr verb that writes to the PR, by URL or by number and --repo.
	ghPrWriteRe = regexp.MustCompile(`\bgh\s+pr\s+(review|comment|edit|merge|close|ready)\b`)
	ghPrNumRe   = regexp.MustCompile(`\bgh\s+pr\s+\w+\s+([0-9]+)\b`)
	ghRepoRe    = regexp.MustCompile(`(?:--repo|-R)[ =]([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)`)
	// gh api on a PR's (or its issue's) path, and what makes it a write: an
	// explicit method, or fields, which gh sends as a POST.
	ghAPIPathRe   = regexp.MustCompile(`\bgh\s+api\b.*?\brepos/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/(?:pulls|issues)/([0-9]+)\b`)
	ghAPIMethodRe = regexp.MustCompile(`(?:-X|--method)[ =]?(\w+)`)
	ghAPIFieldRe  = regexp.MustCompile(`\s(?:-f|-F|--field|--raw-field|--input)\s`)
	segmentSplit  = regexp.MustCompile(`&&|\|\||;|\n`)
)

// actedOn returns the PRs a Bash command writes to: a gh pr verb that
// changes the PR, or gh api writing to its path. A read (gh pr view, a GET)
// never counts, and neither does what a command printed: text other people
// wrote must not choose the session.
func actedOn(command string) []string {
	var out []string
	for _, seg := range segmentSplit.Split(command, -1) {
		switch {
		case ghPrWriteRe.MatchString(seg):
			if m := prURLRe.FindString(seg); m != "" {
				out = append(out, m)
			} else if n, r := ghPrNumRe.FindStringSubmatch(seg), ghRepoRe.FindStringSubmatch(seg); n != nil && r != nil {
				out = append(out, "https://github.com/"+r[1]+"/pull/"+n[1])
			}
		case ghAPIPathRe.MatchString(seg):
			m := ghAPIPathRe.FindStringSubmatch(seg)
			write := ghAPIFieldRe.MatchString(seg + " ")
			if method := ghAPIMethodRe.FindStringSubmatch(seg); method != nil {
				write = !strings.EqualFold(method[1], "GET")
			}
			if write {
				out = append(out, "https://github.com/"+m[1]+"/"+m[2]+"/pull/"+m[3])
			}
		}
	}
	return out
}

// IndexSessions maps each PR to the session that last acted on it, from the
// transcripts under claudeDir: Claude Code's own pr-link records (a PR the
// session opened) and gh writes in its Bash calls. Subagent transcripts are
// not read; their reads are most of what they do. skip names session ids
// that never count, such as claude-review's own rounds. Errors name the file
// only: transcripts hold whatever the sessions saw.
func IndexSessions(claudeDir string, skip func(prURL string, id ids.SessionID) bool) (map[string]SessionHit, error) {
	files, err := filepath.Glob(filepath.Join(claudeDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	hits := map[string]SessionHit{}
	var errs []string
	for _, f := range files {
		if err := indexFile(f, skip, hits); err != nil {
			errs = append(errs, filepath.Base(f))
		}
	}
	if len(errs) > 0 {
		return hits, fmt.Errorf("could not read %d transcripts: %s", len(errs), strings.Join(errs, ", "))
	}
	return hits, nil
}

func indexFile(path string, skip func(prURL string, id ids.SessionID) bool, hits map[string]SessionHit) error {
	f, err := os.Open(path) //nolint:gosec // a transcript under the user's own Claude dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		// Only a complete line counts: the last one may be mid-write.
		if err == nil && worthDecoding(line) {
			indexLine(line, skip, hits)
		}
		if err != nil {
			return nil
		}
	}
}

// worthDecoding keeps the JSON decoder off the lines that cannot count:
// almost every line is prose or a tool result, and 855 MB of them took minutes.
func worthDecoding(line []byte) bool {
	if bytes.Contains(line, []byte(`"type":"pr-link"`)) {
		return true
	}
	return bytes.Contains(line, []byte(`"type":"assistant"`)) && bytes.Contains(line, []byte(`"name":"Bash"`)) &&
		(bytes.Contains(line, []byte("gh pr ")) || bytes.Contains(line, []byte("gh api ")))
}

func indexLine(line []byte, skip func(prURL string, id ids.SessionID) bool, hits map[string]SessionHit) {
	var rec struct {
		Type        string        `json:"type"`
		SessionID   ids.SessionID `json:"sessionId"`
		Timestamp   time.Time     `json:"timestamp"`
		IsSidechain bool          `json:"isSidechain"`
		PrURL       string        `json:"prUrl"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.IsSidechain || rec.SessionID.IsZero() || rec.Timestamp.IsZero() {
		return
	}
	var prs []string
	switch rec.Type {
	case "pr-link":
		if m := prURLRe.FindString(rec.PrURL); m != "" {
			prs = []string{m}
		}
	case "assistant":
		prs = bashWrites(rec.Message.Content)
	}
	for _, pr := range prs {
		if skip != nil && skip(pr, rec.SessionID) {
			continue
		}
		if h, ok := hits[pr]; !ok || rec.Timestamp.After(h.At) {
			hits[pr] = SessionHit{ID: rec.SessionID, At: rec.Timestamp}
		}
	}
}

// bashWrites are the PRs the Bash calls in an assistant message write to.
func bashWrites(content json.RawMessage) []string {
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			Command string `json:"command"`
		} `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var prs []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == "Bash" {
			prs = append(prs, actedOn(b.Input.Command)...)
		}
	}
	return prs
}

// isRoundSession reports one of claude-review's own sessions for prURL: those
// ran under the round policy and keep their own Attach link.
func isRoundSession(prURL string, id ids.SessionID) bool {
	pr, err := ParseTarget(prURL)
	if err != nil {
		return false
	}
	for _, w := range pr.workerIDs() {
		if w == id {
			return true
		}
	}
	return false
}

// linkSessions puts a link to its session on every listed PR that has one.
// It never fails the tick: a missing link is all a failure costs.
func (h tickHost) linkSessions(prs []EllyPR) {
	if h.sessionIndex == nil {
		return
	}
	hits, err := h.sessionIndex()
	if err != nil {
		fmt.Fprintf(h.out, "claude-review tick: session links: %v\n", err)
	}
	expires := h.now().Add(sessionLinkTTL)
	for _, p := range prs {
		hit, ok := hits[p.Url]
		if !ok {
			continue
		}
		a := Annotation{State: "info", Links: []Link{{Label: "Session", URL: "claude-resume://" + hit.ID.String()}}}
		if err := h.do("link "+p.Url+" to session "+hit.ID.String(), func() error { return h.elly.PutSource(sessionSource, p.Url, a, expires) }); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", p.Url, err)
		}
	}
}
