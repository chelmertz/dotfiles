package main

import (
	"fmt"
	"io"
	"time"
)

// annotationTTL is how long elly shows an annotation tick wrote. Every tick
// re-sends the live ones, so a runner that stops shows its PRs as needing
// the user again within this time.
const annotationTTL = 30 * time.Minute

// tickElly is what tick needs from elly.
type tickElly interface {
	PRs() ([]EllyPR, error)
	Put(prURL string, a Annotation, expires time.Time) error
	Delete(prURL string) error
}

// tickGitHub is what tick needs from GitHub.
type tickGitHub interface {
	Gate
	RoundFacts(pr PR, r Round) (RoundFacts, error)
	Standing(pr PR) (state, head string, user *Review, err error)
	Dismiss(pr PR, id int64, message string) error
}

// tickHost is one tick's world. Every write goes through do, which only
// prints in a dry run.
type tickHost struct {
	cfg         Config
	tcfg        TickConfig
	ledger      *Ledger
	elly        tickElly
	gh          tickGitHub
	running     func(sessionID string) bool
	lastMessage func(sessionID string) (string, error)
	findRepo    func(pr PR) (dir, remote string, err error)
	start       func(dir string, argv []string) error
	now         func() time.Time
	out         io.Writer
	dry         bool
}

func (h tickHost) do(what string, fn func() error) error {
	if h.dry {
		fmt.Fprintln(h.out, "would", what)
		return nil
	}
	return fn()
}

// tick is one pass: read elly, settle finished rounds, start owed ones,
// refresh annotations. Without elly it does nothing at all; the annotations
// already written expire on their own.
func (h tickHost) tick() error {
	prs, err := h.elly.PRs()
	if err != nil {
		return err
	}
	running, err := h.settle()
	if err != nil {
		return err
	}
	if err := h.startOwed(prs, running); err != nil {
		return err
	}
	return h.refresh()
}

// settle reads every round the ledger started and has not read, and returns
// how many are still running.
func (h tickHost) settle() (int, error) {
	rounds, err := h.ledger.UnreadRounds()
	if err != nil {
		return 0, err
	}
	running := 0
	for _, r := range rounds {
		pr, err := ParseTarget(r.PR)
		if err != nil {
			continue
		}
		if h.running(pr.SessionID()) {
			running++
			continue
		}
		msg, err := h.lastMessage(pr.SessionID())
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: reading the round: %v\n", pr.Slug(), err)
			continue
		}
		facts, err := h.gh.RoundFacts(pr, r)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
			continue // GitHub unreachable: read it next tick
		}
		started, err := h.apply(pr, r, decide(r, ReadOutcome(msg), facts, pr))
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
			continue
		}
		if started {
			running++
		}
	}
	return running, nil
}

// apply carries out one decision; it reports whether a retry started.
func (h tickHost) apply(pr PR, r Round, d Decision) (bool, error) {
	if d.Retry {
		if err := h.do("retry "+pr.Slug(), func() error { return h.ledger.MarkRetried(r.ID) }); err != nil {
			return false, err
		}
		return true, h.launch(pr)
	}
	if d.DismissReviewID != 0 {
		msg := "A later review round found new problems at the current head, so this approval no longer covers it."
		if err := h.do(fmt.Sprintf("dismiss review %d on %s", d.DismissReviewID, pr.Slug()), func() error { return h.gh.Dismiss(pr, d.DismissReviewID, msg) }); err != nil {
			return false, err
		}
	}
	if d.Clear {
		if err := h.do("clear "+pr.Slug(), func() error { return h.clear(pr) }); err != nil {
			return false, err
		}
	}
	if a := d.Annotation; a != nil {
		if d.MintFor != "" && !h.dry {
			tok, err := h.ledger.MintToken(pr, d.MintFor)
			if err != nil {
				return false, err
			}
			a.Links = append([]Link{{Label: "Approve", URL: approveURL(pr, d.MintFor, tok)}}, a.Links...)
		}
		if err := h.do(fmt.Sprintf("mark %s %s: %s", pr.Slug(), a.State, a.Note), func() error { return h.ledger.SetAnnotation(pr, *a) }); err != nil {
			return false, err
		}
	}
	return false, h.do("mark round "+pr.Slug()+" read", func() error { return h.ledger.MarkRead(r.ID) })
}

func (h tickHost) clear(pr PR) error {
	if err := h.ledger.ClearAnnotation(pr); err != nil {
		return err
	}
	return h.elly.Delete(pr.URL())
}

// startOwed starts the rounds planTick picks, at most MaxRunning at once.
func (h tickHost) startOwed(prs []EllyPR, running int) error {
	plans, err := planTick(prs, h.tcfg, h.ledger, h.gh, h.now())
	if err != nil {
		return err
	}
	for _, p := range plans {
		var gate *Annotation
		switch p.Gate {
		case gateFreshLook:
			gate = &Annotation{State: stateNeedsYou, Note: "the PR changed under its first look (force-push or new base); click claude review to look again",
				Links: []Link{{Label: "Ignore", URL: verbURL(p.PR, "ignore")}}}
		case gatePaused:
			gate = &Annotation{State: stateNeedsYou, Note: "paused: the daily round limit for this PR is reached",
				Links: []Link{{Label: "Open session", URL: verbURL(p.PR, "attach")}, {Label: "Ignore", URL: verbURL(p.PR, "ignore")}}}
		}
		if gate != nil {
			if err := h.do(fmt.Sprintf("mark %s %s: %s", p.PR.Slug(), gate.State, gate.Note), func() error { return h.ledger.SetAnnotation(p.PR, *gate) }); err != nil {
				return err
			}
			continue
		}
		if running >= h.tcfg.MaxRunning || h.running(p.PR.SessionID()) {
			continue
		}
		if err := h.do(fmt.Sprintf("start a round on %s at %.9s (%s)", p.PR.Slug(), p.Head, p.Trigger), func() error {
			if _, err := h.ledger.StartRound(p.PR, p.Head, p.Trigger, h.now()); err != nil {
				return err
			}
			return h.launch(p.PR)
		}); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", p.PR.Slug(), err)
			continue
		}
		running++
	}
	return nil
}

// launch starts an unattended round on pr in its clone.
func (h tickHost) launch(pr PR) error {
	dir, remote, err := h.findRepo(pr)
	if err != nil {
		return err
	}
	argv := append(append([]string{}, h.tcfg.Wrapper...), UnattendedArgs(h.cfg, pr, remote)...)
	return h.start(dir, argv)
}

// refresh re-sends every live annotation with a fresh expiry, and drops one
// only on positive evidence: the PR is closed, or the user's own approval
// stands at its head. A PR missing from elly is not evidence.
func (h tickHost) refresh() error {
	all, err := h.ledger.Annotations()
	if err != nil {
		return err
	}
	for url, a := range all {
		pr, err := ParseTarget(url)
		if err != nil {
			continue
		}
		state, head, user, err := h.gh.Standing(pr)
		if err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
			continue
		}
		if state != prOpen || (user != nil && user.State == reviewApproved && user.CommitID == head) {
			if err := h.do("clear "+pr.Slug(), func() error { return h.clear(pr) }); err != nil {
				fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
			}
			continue
		}
		if err := h.do("send "+pr.Slug()+" to elly", func() error { return h.elly.Put(url, a, h.now().Add(annotationTTL)) }); err != nil {
			fmt.Fprintf(h.out, "claude-review tick: %s: %v\n", pr.Slug(), err)
		}
	}
	return nil
}
