package main

import (
	"fmt"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// roundStarter is how a click and tick start a round in a PR's round
// session: create it if it has no transcript, pick the launch from what the
// session saved (LaunchArgv), and relaunch its one worker (Relaunch).
type roundStarter struct {
	sess    Sessions
	exists  func(id ids.SessionID) bool
	run     func(dir string, argv []string) error
	wrapper []string
	// mark, when set, records the end of a stage: bootstrap, clear, launch.
	mark func(stage string)
}

// start launches the next round on pr in dir. rereview asks for the
// re-review prompt even in a session created now (tick's rounds); without
// it the prompt follows whether the session existed (a click).
func (r roundStarter) start(cfg Config, pr PR, dir, remote string, rereview bool, settle func() error) (RelaunchResult, error) {
	id := pr.RoundSessionID()
	existed := r.exists(id)
	if !existed {
		if err := r.run(dir, RoundBootstrapArgs(pr)); err != nil {
			return RelaunchResult{}, fmt.Errorf("creating the round session: %w", err)
		}
		r.marked("bootstrap")
	}
	resume := rereview || existed
	argv, err := r.sess.LaunchArgv(id, PolicyFlags(pr, remote), RoundArgs(cfg, pr, remote, resume), ResumeArgs(cfg, pr, remote, resume))
	if err != nil {
		// Not a failure of the launch: the caller marks the PR needs_you.
		return RelaunchResult{Kind: relaunchPolicy, Detail: err.Error()}, nil //nolint:nilerr // see above
	}
	argv = append(append([]string{}, r.wrapper...), argv...)
	return r.sess.Relaunch(id, settle, func() error {
		r.marked("clear") // settling and stopping the last worker
		defer r.marked("launch")
		return r.run(dir, argv)
	})
}

func (r roundStarter) marked(stage string) {
	if r.mark != nil {
		r.mark(stage)
	}
}
