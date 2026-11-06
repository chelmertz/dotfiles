package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

var checkID = mustSession("5e1fc4ec-0000-4000-8000-000000000001")

// checkWorld is a scratch repo in a fake Claude Code: a background launch
// lists a worker, whose job reaches done a few polls later.
type checkWorld struct {
	fs     *fakeSessions
	runs   []string
	fork   bool // the second launch forks instead of continuing
	other  bool // Claude Code saves other options than the ones passed
	lines  []string
	never  bool // the job never reaches done
	pid    int
	polls  int
	trusts []string
}

func (w *checkWorld) checker(t *testing.T) selfChecker {
	if w.fs.saved == nil {
		w.fs.saved = map[ids.SessionID][]string{}
	}
	sess := w.fs.sessions()
	state := sess.JobState
	sess.JobState = func(id ids.SessionID) string {
		w.polls++
		if !w.never && w.polls%3 == 0 && len(w.fs.workers) > 0 {
			w.fs.states[id] = "done"
		}
		return state(id)
	}
	return selfChecker{
		sess:  sess,
		trust: func(dir string) error { w.trusts = append(w.trusts, dir); return nil },
		newID: func() ids.SessionID { return checkID },
		dir:   t.TempDir(),
		sleep: func(time.Duration) {},
		run: func(_ string, argv []string) (string, error) {
			line := strings.ReplaceAll(argv[len(argv)-1], `'\''`, "'")
			switch {
			case strings.Contains(line, "'--bg'"):
				w.runs = append(w.runs, "bg")
				w.lines = append(w.lines, line)
				if _, ok := w.fs.saved[checkID]; !ok {
					w.fs.saved[checkID] = slices.Clone(selfCheckFlags)
					if w.other {
						w.fs.saved[checkID] = []string{"--permission-mode", "auto"}
					}
				}
				w.fs.events = append(w.fs.events, "launch")
				w.pid++
				w.fs.states[checkID] = "working"
				if w.fork && w.pid > 1 {
					w.fs.workers = append(w.fs.workers, Worker{Short: forkID.Key(), SessionID: forkID, PID: 900 + w.pid, Fork: true, LaunchID: checkID})
				} else {
					w.fs.workers = append(w.fs.workers, workerFor(checkID, 900+w.pid))
				}
			case strings.Contains(line, "'-p'"):
				w.runs = append(w.runs, "bootstrap")
				if !strings.Contains(line, "'--setting-sources' '' ") || !strings.Contains(line, "'dontAsk'") {
					t.Errorf("bootstrap not under dontAsk with no settings: %s", line)
				}
			case strings.Contains(line, "'git' 'init'"):
				w.runs = append(w.runs, "git init")
			default:
				t.Errorf("unexpected command %s", line)
			}
			return "", nil
		},
	}
}

func TestSelfCheckPassesWhenClaudeCodeBehavesAsObserved(t *testing.T) {
	w := &checkWorld{fs: newFakeSessions()}
	c := w.checker(t)
	if err := c.check(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(w.runs, ","); got != "git init,bootstrap,bg,bg" {
		t.Errorf("runs %s", got)
	}
	short := checkID.Key().String()
	if got := strings.Join(w.fs.events, ","); got != "launch,stop "+short+",launch,stop "+short {
		t.Errorf("events %s", got)
	}
	// The second launch resumes without flags, as every later round does.
	if len(w.lines) != 2 || !strings.Contains(w.lines[0], "'--allowedTools' 'Agent'") || !strings.HasSuffix(w.lines[1], "'claude' '--bg' '--resume' '"+checkID.String()+"' '"+selfCheckPrompt+"''") {
		t.Errorf("launches %d:\n%s", len(w.lines), strings.Join(w.lines, "\n"))
	}
	if !slices.Equal(w.trusts, []string{c.dir}) {
		t.Errorf("trusted %q", w.trusts)
	}
}

func TestSelfCheckFailsOnAnyMismatch(t *testing.T) {
	for name, w := range map[string]*checkWorld{
		"a resume forks":      {fs: newFakeSessions(), fork: true},
		"the job never ends":  {fs: newFakeSessions(), never: true},
		"stop leaves the pid": func() *checkWorld { w := &checkWorld{fs: newFakeSessions()}; w.fs.pidLinger = 1 << 20; return w }(),
		"stop fails": func() *checkWorld {
			w := &checkWorld{fs: newFakeSessions()}
			w.fs.stopErr = errors.New("no tty")
			return w
		}(),
		"roster is unreadable": func() *checkWorld { w := &checkWorld{fs: newFakeSessions()}; w.fs.unreadable = true; return w }(),
	} {
		err := w.checker(t).check()
		if err == nil {
			t.Errorf("%s: the self-check passed", name)
		}
		if name == "the job never ends" && !strings.Contains(fmt.Sprint(err), "a first background round: its job never reached done") {
			t.Errorf("%s: failed for another reason: %v", name, err)
		}
	}
}

func TestSelfCheckLeavesNoWorkerBehind(t *testing.T) {
	w := &checkWorld{fs: newFakeSessions(), never: true}
	if err := w.checker(t).check(); err == nil {
		t.Fatal("passed")
	}
	if len(w.fs.workers) != 0 {
		t.Errorf("left %+v running", w.fs.workers)
	}
}
