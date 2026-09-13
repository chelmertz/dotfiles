package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Auto-handoff: when a session ends, a headless `claude -p` reads the
// transcript it just wrote and updates the project's HANDOFF.md.
//
// The risk this carries is not token spend, it is a background process
// rewriting a state file with no undo. That is why it was parked until `~/p`
// became a git repo committed on every turn end - the commit is the undo - and
// why it stays inert until `p-launcher kv set handoff.enabled 1`, the same
// shape `tend` uses. Every decision is logged whether or not it acts, so the
// journal shows what it *would* have done before it is ever allowed to.

// handoffCfg is the knobs, read from kv so they change without a rebuild.
type handoffCfg struct {
	Enabled  bool
	DailyCap int
	UsedToday int
}

// handoffDecision is one ended session and what to do about it. Kind is
// "write", "skip" or "cap".
type handoffDecision struct {
	Path       string
	Transcript string
	Kind       string
	Reason     string
}

func (d handoffDecision) String() string {
	return fmt.Sprintf("auto-handoff %s: %s (%s)", d.Path, d.Kind, d.Reason)
}

// handoffDecide answers one question: did this session leave the project's
// handoff behind the work? It deliberately reuses `drifted` rather than
// inventing a second notion of "needs a handoff" - the F5 marker and this must
// never disagree, or the launcher says one thing and the machine does another.
func handoffDecide(p Project, root, transcript string, live bool, cfg handoffCfg) handoffDecision {
	d := handoffDecision{Path: p.Path, Transcript: transcript, Kind: "skip"}
	switch {
	case transcript == "":
		d.Reason = "no transcript path"
	case !fileExists(transcript):
		d.Reason = "transcript " + transcript + " is gone"
	case live:
		// Another session is still working in this project. Rewriting the
		// file underneath it is the exact rug pull the liveness gate exists
		// to prevent.
		d.Reason = "another session is live in the project"
	case !drifted(p, root, false):
		d.Reason = "handoff is not behind the work"
	case !cfg.Enabled:
		d.Reason = "would write, but handoff.enabled is unset"
	case cfg.DailyCap > 0 && cfg.UsedToday >= cfg.DailyCap:
		d.Kind = "cap"
		d.Reason = fmt.Sprintf("daily cap %d reached", cfg.DailyCap)
	default:
		d.Kind = "write"
		d.Reason = "session ended past the handoff line, handoff older than it"
	}
	return d
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// handoffPrompt is what the headless run is told. It names the skill rather
// than restating it: the skill is the thing that gets maintained, and two
// copies of "how to write a handoff" would drift apart immediately.
const handoffPrompt = `The session whose transcript is at %s has just ended in this project.
Run the /handoff skill over it and update HANDOFF.md accordingly.
Do not start any new work, do not touch any file other than the project's state
files, and do not run git commands - the workspace hook commits on its own.`

// loadHandoffCfg reads the knobs. Absent keys mean disabled with the default
// cap, so a machine that has never set them does nothing but log.
func loadHandoffCfg(s *Store, now time.Time) handoffCfg {
	cfg := handoffCfg{DailyCap: 4}
	if v, _ := s.kvGet("handoff.enabled"); v == "1" {
		cfg.Enabled = true
	}
	if v, _ := s.kvGet("handoff.daily_cap"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DailyCap = n
		}
	}
	if v, _ := s.kvGet("handoff.used." + now.Format("2006-01-02")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.UsedToday = n
		}
	}
	return cfg
}

// runAutoHandoff spawns the headless run and returns without waiting. A
// SessionEnd hook must not block the terminal closing, and the run takes
// minutes. Output goes to the journal through the parent's stderr.
func runAutoHandoff(s *Store, root string, d handoffDecision, now time.Time) error {
	if d.Kind != "write" {
		return nil
	}
	dir := filepath.Join(root, d.Path)
	cmd := exec.Command("claude", "-p", fmt.Sprintf(handoffPrompt, d.Transcript))
	cmd.Dir = dir
	cmd.Env = scrubEnv(os.Environ())
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("auto-handoff %s: %w", d.Path, err)
	}
	go cmd.Wait()
	key := "handoff.used." + now.Format("2006-01-02")
	used := 0
	if v, _ := s.kvGet(key); v != "" {
		used, _ = strconv.Atoi(v)
	}
	return s.kvSet(key, strconv.Itoa(used+1))
}

// autoHandoff runs the decision for a session that just ended and logs it,
// whether or not it acts. It never returns an error into the hook path: a
// SessionEnd hook that fails takes the ball-clearing with it, and a missed
// handoff is worth less than a corrupted state row.
func autoHandoff(s *Store, root string, in HookInput) {
	path := projectPathFor(root, in.Cwd)
	if path == "" {
		return
	}
	now := time.Now()
	ps, err := s.ListProjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "auto-handoff %s: %v\n", path, err)
		return
	}
	var p Project
	for _, c := range ps {
		if c.Path == path {
			p = c
		}
	}
	if p.Path == "" {
		return
	}
	// Any *other* session still in the project; this one is ending.
	live := false
	for l := range liveFromProc(procRoot, root) {
		if l == path {
			live = true
		}
	}
	d := handoffDecide(p, root, in.TranscriptPath, live, loadHandoffCfg(s, now))
	fmt.Fprintln(os.Stderr, d)
	// A SessionEnd hook's stderr is swallowed by Claude Code, so the line above
	// reaches nobody. The row is the only durable record of what this decided,
	// and reading a few days of them is the whole plan for trusting it.
	//
	// session_event, not project_event: the latter's newest row per project is
	// what marks a project archived or snoozed (latestKindExpr), so writing
	// here would silently un-archive one. `tend` logs its decisions the same
	// way for the same reason.
	if err := s.RecordSessionEvent(SessionEvent{
		SessionID: "auto-handoff:" + in.SessionID,
		Path:      path,
		Cwd:       in.Cwd,
		Kind:      "auto_handoff",
		Detail:    d.Kind + ": " + d.Reason,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "auto-handoff %s: %v\n", path, err)
	}
	if err := runAutoHandoff(s, root, d, now); err != nil {
		fmt.Fprintf(os.Stderr, "auto-handoff %s: %v\n", path, err)
	}
}
