package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type action int

const (
	actFocus action = iota
	actLaunch
)

func (a action) String() string { return [...]string{"focus", "launch"}[a] }

// decide picks focus (with target con) or launch for a tag given the live tree.
func decide(treeJSON []byte, tag string) (action, int64, error) {
	wins, err := FindTagged(treeJSON, tag)
	if err != nil {
		return 0, 0, err
	}
	if con, ok := PickFocus(wins); ok {
		return actFocus, con, nil
	}
	return actLaunch, 0, nil
}

// Open focuses the project's terminal if one exists, otherwise launches one,
// and records the activity. root is ~/p.
func Open(s *Store, root, path string) error {
	tag := tagFor(path)
	tree, err := getTree()
	if err != nil {
		return err
	}
	act, con, err := decide(tree, tag)
	if err != nil {
		return err
	}
	switch act {
	case actFocus:
		if err := focusCon(con); err != nil {
			return err
		}
	case actLaunch:
		if err := launch(filepath.Join(root, path), tag); err != nil {
			return err
		}
	}
	// Record before waiting so a slow ghostty never loses the MRU update.
	if err := s.RecordActivity(path, act.String()); err != nil {
		return err
	}
	if act == actLaunch {
		return waitForTagged(tag, 3*time.Second)
	}
	return nil
}

// launch starts a detached ghostty tagged with the project. Its stdio is
// inherited so ghostty's own output lands in the same journal stream.
func launch(dir, tag string) error {
	cmd := exec.Command("ghostty",
		"--x11-instance-name="+tag,
		"--working-directory="+dir,
		"-e", "zsh", "-ic", "claude; exec zsh")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ghostty for %s: %w", dir, err)
	}
	return cmd.Process.Release()
}

// waitForTagged polls the tree until a window with tag appears. A launch that
// produces no tagged window is a fault that must be visible, not a silently
// untracked terminal.
func waitForTagged(tag string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tree, err := getTree()
		if err != nil {
			return err
		}
		wins, err := FindTagged(tree, tag)
		if err != nil {
			return err
		}
		if len(wins) > 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return &hintError{
		msg:  fmt.Sprintf("ghostty started but no window tagged %q appeared within %s", tag, timeout),
		hint: "check `journalctl --user -t p-launcher` for ghostty output; see the single-instance fallback in the design doc",
	}
}
