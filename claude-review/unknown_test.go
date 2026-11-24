package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// liveFiles makes the given pids the only live processes, with no start
// time to compare, for a test that reads Claude Code's real files.
func liveFiles(t *testing.T, pids ...int) {
	t.Helper()
	oldAlive, oldStart := processAlive, procStart
	t.Cleanup(func() { processAlive, procStart = oldAlive, oldStart })
	alive := map[int]bool{}
	for _, p := range pids {
		alive[p] = true
	}
	processAlive = func(pid int) bool { return alive[pid] }
	procStart = func(int) string { return "" }
}

// A roster entry whose launch Claude Code wrote in a shape this tool does
// not know.
func brokenEntry(key, session string) string {
	return fmt.Sprintf(`%q: {"pid": 5, "sessionId": %q, "dispatch": {"launch": {"sessionId": "/home/u/notes.jsonl"}}}`, key, session)
}

// One worker this tool cannot read is unknown for its own PR and nobody
// else's: on 2026-11-23 a fork of an interactive session held every PR's
// launches and close-outs.
func TestAForeignBrokenRosterEntryHoldsNoOtherPR(t *testing.T) {
	liveFiles(t, 5)
	dir := t.TempDir()
	writeRoster(t, dir, `{"workers":{`+brokenEntry(forkID.Key().String(), forkID.String())+`}}`)
	if RoundRunning(dir, sid) {
		t.Error("a broken entry for another session read as this one's round running")
	}
	if res, err := realSessions(dir, "linux", nil).clear(sid, nil); err != nil || res.Kind != 0 {
		t.Errorf("a broken entry for another session held this one's launch: %+v %v", res, err)
	}
}

// The same entry under this PR's key, or naming its id anywhere, holds it.
func TestABrokenRosterEntryForThisPRHoldsIt(t *testing.T) {
	liveFiles(t, 5)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"its key":        brokenEntry(sid.Key().String(), sid.String()),
		"its id in body": fmt.Sprintf(`"zzzzzzzz": {"pid": 5, "sessionId": %q}`, sid),
	} {
		writeRoster(t, dir, `{"workers":{`+body+`}}`)
		if !RoundRunning(dir, sid) {
			t.Errorf("%s: a broken entry for this session read as no round running", name)
		}
		if res, _ := realSessions(dir, "linux", nil).clear(sid, nil); res.Kind != relaunchUnknown {
			t.Errorf("%s: launch %+v, want unknown", name, res)
		}
	}
	// Named by id, not by key: the other session is not held by it.
	if res, err := realSessions(dir, "linux", nil).clear(forkID, nil); err != nil || res.Kind != 0 {
		t.Errorf("an entry naming only %s held %s: %+v %v", sid, forkID, res, err)
	}
}

// An entry that names no session at all may be any PR's worker.
func TestAnIDlessBrokenRosterEntryHoldsEveryPR(t *testing.T) {
	liveFiles(t, 5)
	dir := t.TempDir()
	writeRoster(t, dir, `{"workers":{"x; rm -rf ~": {"pid": 5, "sessionId": "x"}}}`)
	for _, id := range []ids.SessionID{sid, forkID} {
		if res, _ := realSessions(dir, "linux", nil).clear(id, nil); res.Kind != relaunchUnknown {
			t.Errorf("%s: launch %+v beside an entry naming no session", id, res)
		}
		if !RoundRunning(dir, id) {
			t.Errorf("%s: no round running beside an entry naming no session", id)
		}
	}
}

func writeSessionFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A session file caught half-written is unknown for the sessions it names
// and nobody else's.
func TestAHalfWrittenSessionFileIsUnknownOnlyForItsIDs(t *testing.T) {
	liveFiles(t, 77)
	dir := t.TempDir()
	l := openTestLedger(t)
	s := realSessions(dir, "linux", nil)
	fid := ids.NewSession()
	if err := l.SetForkSession(widgetsPR, fid); err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, dir, "77.json", fmt.Sprintf(`{"pid":77,"sessionId":%q,"procSt`, ids.NewSession()))
	if got := windowOf(s, l, widgetsPR); got != winClosed {
		t.Errorf("an unrelated half-written file: window %v, want closed", got)
	}
	for name, id := range map[string]ids.SessionID{"the fork's": fid, "the round's": widgetsPR.RoundSessionID()} {
		writeSessionFile(t, dir, "77.json", fmt.Sprintf(`{"pid":77,"sessionId":%q,"procSt`, id))
		if got := windowOf(s, l, widgetsPR); got != winUnknown {
			t.Errorf("a half-written file naming %s id: window %v, want unknown", name, got)
		}
	}
	// A file left by a process that is gone is ignored, as a parsed one is.
	writeSessionFile(t, dir, "77.json", "")
	writeSessionFile(t, dir, "78.json", fmt.Sprintf(`{"pid":78,"sessionId":%q,"procSt`, fid))
	if got := windowOf(s, l, widgetsPR); got != winClosed {
		t.Errorf("a half-written file of a dead pid: window %v, want closed", got)
	}
}

// refresh closes a window only on a pass that read every session file.
func TestRefreshClosesNoWindowOnAPassWithAnUnreadableSessionFile(t *testing.T) {
	liveFiles(t, 4242, 77)
	dir := t.TempDir()
	w := newTickWorld(t)
	w.h.sess = realSessions(dir, "linux", nil)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	fid := ids.NewSession()
	if err := w.h.ledger.SetForkSession(pr, fid); err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, dir, "4242.json", fmt.Sprintf(`{"pid":4242,"sessionId":%q}`, fid))
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stHeld || s.Kind != kindWindow {
		t.Fatalf("state %+v, want held{window}", s)
	}
	w.elly.prs = nil
	if err := os.Remove(filepath.Join(dir, "sessions", "4242.json")); err != nil { // the fork exited
		t.Fatal(err)
	}
	writeSessionFile(t, dir, "77.json", fmt.Sprintf(`{"pid":77,"sessionId":%q,"procSt`, ids.NewSession()))
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != stHeld || s.Kind != kindWindow {
		t.Errorf("a pass with a half-written session file closed the window: %+v", s)
	}
	writeSessionFile(t, dir, "77.json", fmt.Sprintf(`{"pid":77,"sessionId":%q}`, ids.NewSession()))
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State == stHeld && s.Kind == kindWindow {
		t.Errorf("held{window} outlived the fork once every file read: %+v", s)
	}
}

// For is unknown only for the ids a failed entry names; an id-less one is
// unknown for every id.
func TestRosterForIsUnknownOnlyForNamedIDs(t *testing.T) {
	r := Roster{workers: []Worker{workerFor(sid, 5)}, failed: []failedEntry{
		{where: rosterKeyPrefix + forkID.Key().String(), raw: []byte(`{"pid":`), err: errors.New("unexpected end of JSON input")},
	}}
	if ws, err := r.For(sid); err != nil || len(ws) != 1 {
		t.Errorf("a broken entry for another key: %+v %v", ws, err)
	}
	if _, err := r.For(forkID); err == nil || !strings.Contains(err.Error(), forkID.Key().String()) {
		t.Errorf("a broken entry under this key: %v, want an error naming it", err)
	}
	r.failed = append(r.failed, failedEntry{where: rosterKeyPrefix + "?", raw: []byte(`{}`), err: errors.New("not a worker id")})
	if _, err := r.For(sid); err == nil {
		t.Error("an entry naming no session held nothing")
	}
}

// The error names the session file that failed; Unparsed names every one.
func TestASessionFileThatFailsIsNamed(t *testing.T) {
	liveFiles(t, 77, 78)
	dir := t.TempDir()
	writeSessionFile(t, dir, "77.json", fmt.Sprintf(`{"pid":77,"sessionId":%q,"procSt`, sid))
	writeSessionFile(t, dir, "78.json", `{"pid":78}`)
	s := realSessions(dir, "linux", nil)
	if _, err := s.Window(sid); err == nil || !strings.Contains(err.Error(), "77.json") {
		t.Errorf("Window: %v, want an error naming 77.json", err)
	}
	if pid, err := s.Window(forkID); err != nil || pid != 0 {
		t.Errorf("another session: %d %v", pid, err)
	}
	if err := s.Unparsed(); err == nil || !strings.Contains(err.Error(), "77.json") || !strings.Contains(err.Error(), "78.json") {
		t.Errorf("Unparsed: %v, want both files named (78.json has no session id)", err)
	}
	if live := SessionRunning(dir, sid); live.Err == nil {
		t.Errorf("the window path read %+v beside a broken file naming its session", live)
	}
}

// Unknown holds the launch for the pass and stores nothing.
func TestTickHoldsAnUnknownWindowWithoutStoringIt(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	w.elly.prs = []EllyPR{requestedPR(42)}
	w.fs.windowErr = map[ids.SessionID]error{pr.RoundSessionID(): errors.New("sessions/77.json: unexpected end of JSON input")}
	before, _, _ := w.h.ledger.PRState(pr)
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("started beside an unknown window: %q", w.started)
	}
	if s, _, _ := w.h.ledger.PRState(pr); s.State != before.State || s.Kind != before.Kind {
		t.Errorf("an unknown window was stored: %+v, was %+v", s, before)
	}
	if !strings.Contains(w.out.String(), string(skipWindowUnknown)) {
		t.Errorf("tick did not say why:\n%s", w.out)
	}
	w.fs.windowErr = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) == 0 {
		t.Error("the next readable pass started nothing")
	}
}

// A click beside an unknown window refuses and records nothing.
func TestClickBesideAnUnknownWindowStartsNothing(t *testing.T) {
	h, fs := clickHost(t, true)
	fs.windowErr = map[ids.SessionID]error{testPR.RoundSessionID(): errors.New("sessions/77.json: unexpected end of JSON input")}
	if code := run([]string{target}, h.d); code != 1 || slices.Contains(fs.events, "launch") {
		t.Errorf("exit %d, events %q", code, fs.events)
	}
	if !strings.Contains(notified(h), windowUnknownNote) {
		t.Errorf("notified %q", notified(h))
	}
}
