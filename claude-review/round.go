package main

import (
	"fmt"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// roundStarter is how a click and tick start a round in a PR's round
// session: create it if it has no transcript, pick the launch from what the
// session saved (RoundArgv), and relaunch its one worker (Relaunch).
type roundStarter struct {
	sess    Sessions
	exists  func(id ids.SessionID) bool
	run     func(dir string, argv []string) error
	wrapper []string
	// mark, when set, records the end of a stage: bootstrap, clear, launch.
	mark func(stage string)
	// beforeLaunch, when set, runs just before the round's worker launches,
	// after the bootstrap and the last worker's stop: where the caller
	// measures the transcript the round appends to and writes the round's
	// row. An error stops the launch.
	beforeLaunch func() error
	// promptFromCaller: rereview alone picks the prompt (tick, which knows
	// whether any round settled); otherwise an existing session also means
	// re-review (a click).
	promptFromCaller bool
}

// start launches the next round on pr in dir. rereview asks for the
// re-review prompt; unless promptFromCaller, a session that existed asks
// for it too.
func (r roundStarter) start(cfg Config, pr PR, dir, remote string, rereview bool, settle func() error) (RelaunchResult, error) {
	id := pr.RoundSessionID()
	existed := r.exists(id)
	if !existed {
		if err := r.run(dir, RoundBootstrapArgs(pr)); err != nil {
			return RelaunchResult{}, fmt.Errorf("creating the round session: %w", err)
		}
		r.marked("bootstrap")
	}
	resume := rereview || (existed && !r.promptFromCaller)
	policy := PolicyFlags(pr, remote)
	argv, restore, err := r.sess.RoundArgv(id, policy, RoundArgs(cfg, pr, remote, resume), ResumeArgs(cfg, pr, remote, resume))
	if err != nil {
		// Not a failure of the launch: the caller marks the PR needs_you.
		return RelaunchResult{Kind: relaunchPolicy, Detail: err.Error()}, nil //nolint:nilerr // see above
	}
	argv = append(append([]string{}, r.wrapper...), argv...)
	return r.sess.Relaunch(id, settle, func() error {
		r.marked("clear") // settling and stopping the last worker
		defer r.marked("launch")
		if restore {
			if err := r.sess.Restore(id, policy); err != nil {
				return err
			}
		}
		if r.beforeLaunch != nil {
			if err := r.beforeLaunch(); err != nil {
				return err
			}
		}
		return r.run(dir, argv)
	})
}

func (r roundStarter) marked(stage string) {
	if r.mark != nil {
		r.mark(stage)
	}
}
