package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordedRound is one fixture under testdata/rounds: a real round, recorded
// read-only by record_test.go and renamed. Want is written by hand from what
// actually happened on the PR, never copied from tick's output.
type recordedRound struct {
	PR          string            `json:"pr"`
	Round       recordedRoundRow  `json:"round"`
	SettleAt    time.Time         `json:"settle_at"`
	LastMessage string            `json:"last_message"`
	Gh          map[string]string `json:"gh"`
	Happened    string            `json:"happened"`
	Want        *recordedWant     `json:"want"`
}

type recordedRoundRow struct {
	Head      string    `json:"head"`
	StartedAt time.Time `json:"started_at"`
}

type recordedWant struct {
	Annotation string `json:"annotation"` // handled, needs_you, or "" for none
	Note       string `json:"note"`       // a substring of the annotation's note
	Retry      bool   `json:"retry"`
	Dismiss    bool   `json:"dismiss"`
}

// fixtureGitHub is the real adapter over recorded answers; its only writer
// is replaced, and an unrecorded call is an error.
type fixtureGitHub struct {
	GitHub
	dismissed []int64
}

func (g *fixtureGitHub) Dismiss(_ PR, id int64, _ string) error {
	g.dismissed = append(g.dismissed, id)
	return nil
}

func TestRecordedRounds(t *testing.T) {
	files, _ := filepath.Glob("testdata/rounds/*.json")
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, err := os.ReadFile(f) //nolint:gosec // test fixtures
			if err != nil {
				t.Fatal(err)
			}
			var rr recordedRound
			if err := json.Unmarshal(b, &rr); err != nil {
				t.Fatal(err)
			}
			if rr.Want == nil || rr.Happened == "" {
				t.Fatal("no hand-written happened/want: read the real PR and write them")
			}
			pr, err := ParseTarget(rr.PR)
			if err != nil {
				t.Fatal(err)
			}
			gh := &fixtureGitHub{GitHub: GitHub{User: "me", Gh: func(args ...string) (string, error) {
				out, ok := rr.Gh[strings.Join(args, " ")]
				if !ok {
					t.Errorf("unrecorded gh call: %q", args)
				}
				return out, nil
			}}}
			l := openTestLedger(t)
			if err := l.Accept(pr, rr.Round.Head, "main", rr.Round.StartedAt.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := l.StartRound(pr, rr.Round.Head, "recorded", rr.Round.StartedAt); err != nil {
				t.Fatal(err)
			}
			var started [][]string
			out := &bytes.Buffer{}
			fs := newFakeSessions()
			h := tickHost{
				tcfg:        TickConfig{ReviewRepos: []string{"acme/*"}, User: "me", DailyCap: 6, MaxRunning: 2, TotalDailyCap: 24},
				ledger:      l,
				elly:        &fakeElly{login: "me", put: map[string]Annotation{}},
				gh:          gh,
				running:     func(string) bool { return false },
				blocked:     func(string) bool { return false },
				lastMessage: func(string) (string, error) { return rr.LastMessage, nil },
				findRepo:    func(PR) (string, string, error) { return "/clone", "upstream", nil },
				start: func(_ string, argv []string) error {
					started = append(started, argv)
					fs.workers = append(fs.workers, workerFor(pr.RoundSessionID(), 700))
					return nil
				},
				now:       func() time.Time { return rr.SettleAt },
				out:       out,
				lock:      func(string) (func(), error) { return func() {}, nil },
				sess:      fs.sessions(),
				exists:    func(string) bool { return true },
				trust:     func(string) error { return nil },
				version:   func() (string, error) { return "2.1.314", nil },
				selfCheck: func() error { return nil },
			}
			// Actions are logged now; only error lines fail a replay.
			if err := h.tick(); err != nil || strings.Contains(out.String(), "claude-review tick:") {
				t.Fatalf("tick: %v %s", err, out)
			}
			all, err := l.Annotations()
			if err != nil {
				t.Fatal(err)
			}
			a, annotated := all[pr.URL()]
			got := recordedWant{Retry: len(started) == 1, Dismiss: len(gh.dismissed) == 1}
			if annotated {
				got.Annotation, got.Note = a.State, a.Note
			}
			w := *rr.Want
			if got.Annotation != w.Annotation || !strings.Contains(got.Note, w.Note) || got.Retry != w.Retry || got.Dismiss != w.Dismiss {
				t.Errorf("got %+v\nwant %+v\nhappened: %s", got, w, rr.Happened)
			}
		})
	}
}
