package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// EllyPR is the part of a row of elly's GET /api/v0/prs that tick reads.
type EllyPR struct {
	Url                      string
	RepoOwner                string
	RepoName                 string
	Author                   string
	HeadRefOid               string
	LastUpdated              time.Time
	ReviewRequestedFromUsers []string
	RereviewFrom             []string
	ThreadsActionable        int
	Buried                   bool
}

// TickConfig is what tick needs from the config.
type TickConfig struct {
	// ReviewRepos lists "owner/*" or "owner/repo"; nothing else is ever run.
	ReviewRepos []string
	// User is the GitHub login the rounds post as.
	User string
	// DailyCap is how many rounds one PR may start in 24 hours; 0 is no
	// cap (LoadConfig makes an unset one 6).
	DailyCap int
	// MaxRunning is how many rounds may run at once.
	MaxRunning int
	// TotalDailyCap is how many rounds, retries included, may start in 24
	// hours across all PRs before the breaker trips. 0 starts nothing.
	TotalDailyCap int
	// Wrapper prefixes each round's command, e.g. ["capped"].
	Wrapper []string
}

const (
	gateFreshLook = "fresh_look"
	gatePaused    = "paused"
	// gateClosed drops a PR merged or closed since elly last polled it.
	gateClosed = "closed"

	// GitHub's words for a PR and a review, as tick compares them.
	prOpen         = "open"
	prClosed       = "closed"
	reviewApproved = "APPROVED"
)

// repoAllowed reports whether owner/repo is in the allowlist.
func repoAllowed(owner, repo string, allow []string) bool {
	owner, repo = strings.ToLower(owner), strings.ToLower(repo)
	for _, a := range allow {
		o, r, _ := strings.Cut(strings.ToLower(a), "/")
		if o == owner && (r == "*" || r == repo) {
			return true
		}
	}
	return false
}

// roundTrigger names why a round is owed on p, or "" if none is. Requests
// and replies are keyed by the PR's last update, so the same request does not
// run twice while a new reply at the same head still does. lastHead is the
// head of the last round, or the accepted head before any.
func roundTrigger(p EllyPR, user, lastHead string) string {
	at := "@" + p.LastUpdated.UTC().Format(time.RFC3339)
	switch {
	case slices.Contains(p.ReviewRequestedFromUsers, user):
		return "review_requested" + at
	case slices.Contains(p.RereviewFrom, user):
		return "rereview_requested" + at
	case p.ThreadsActionable > 0:
		return "replies" + at
	case p.HeadRefOid != "" && p.HeadRefOid != lastHead:
		return "new_head"
	}
	return ""
}

// Gate is what tick asks GitHub before running a PR again.
type Gate interface {
	// Descends reports whether head has from in its history.
	Descends(pr PR, from, head string) (bool, error)
	// Base is the PR's base branch now, and whether the PR is still open.
	Base(pr PR) (base string, open bool, err error)
}

// Planned is one PR this tick acts on: a round to start, or a gate that
// stops it and needs the user.
type Planned struct {
	PR      PR
	Head    string
	Trigger string
	Gate    string
}

// planTick picks the PRs to act on, in elly's order.
func planTick(prs []EllyPR, cfg TickConfig, l *Ledger, g Gate, now time.Time) ([]Planned, error) {
	var out []Planned
	for _, p := range prs {
		planned, ok, err := planOne(p, cfg, l, g, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, planned)
		}
	}
	return out, nil
}

// planOne decides about one PR: whether a round is owed, and whether a gate
// stops it.
func planOne(p EllyPR, cfg TickConfig, l *Ledger, g Gate, now time.Time) (Planned, bool, error) {
	pr, ok := eligible(p, cfg)
	if !ok {
		return Planned{}, false, nil
	}
	acc, accepted, err := l.Acceptance(pr)
	if err != nil || !accepted {
		return Planned{}, false, err
	}
	if ignored, err := l.Ignored(pr); err != nil || ignored {
		return Planned{}, false, err
	}
	lastHead, err := l.LastHead(pr)
	if err != nil {
		return Planned{}, false, err
	}
	if lastHead == "" {
		lastHead = acc.Head
	}
	trigger := roundTrigger(p, cfg.User, lastHead)
	if trigger == "" {
		return Planned{}, false, nil
	}
	// A round at this head that started after the PR last changed already
	// saw everything elly reports: a click answering a review request is
	// one, while elly lists the request until its next poll.
	if ran, err := l.RanAt(pr, p.HeadRefOid, trigger, p.LastUpdated); err != nil || ran {
		return Planned{}, false, err
	}
	gate, err := gateFor(pr, acc, p.HeadRefOid, cfg, l, g, now)
	return Planned{PR: pr, Head: p.HeadRefOid, Trigger: trigger, Gate: gate}, err == nil && gate != gateClosed, err
}

// eligible: a PR in review_repos that the user did not write.
func eligible(p EllyPR, cfg TickConfig) (PR, bool) {
	pr, err := ParseTarget(p.Url)
	return pr, err == nil && repoAllowed(p.RepoOwner, p.RepoName, cfg.ReviewRepos) && !strings.EqualFold(p.Author, cfg.User)
}

// gateFor is what stops an owed round: a PR closed since elly's last poll,
// one that changed under its first look, or the daily cap. "" lets it run.
func gateFor(pr PR, acc Acceptance, head string, cfg TickConfig, l *Ledger, g Gate, now time.Time) (string, error) {
	base, open, err := g.Base(pr)
	if err != nil {
		return "", err
	}
	if !open {
		return gateClosed, nil
	}
	fresh, err := needsFreshLook(pr, acc, base, head, g)
	if err != nil {
		return "", err
	}
	if fresh {
		return gateFreshLook, nil
	}
	n, err := l.RoundsSince(pr, now.Add(-24*time.Hour))
	if err != nil {
		return "", err
	}
	if cfg.DailyCap > 0 && n >= cfg.DailyCap {
		return gatePaused, nil
	}
	return "", nil
}

// needsFreshLook: a head that no longer contains the accepted one, or a new
// base, is not the PR the user looked at.
func needsFreshLook(pr PR, acc Acceptance, base, head string, g Gate) (bool, error) {
	if base != acc.Base {
		return true, nil
	}
	if head == acc.Head {
		return false, nil
	}
	ok, err := g.Descends(pr, acc.Head, head)
	return !ok, err
}

// Review is the user's latest review on a PR.
type Review struct {
	ID          int64
	State       string
	CommitID    string
	SubmittedAt time.Time
}

// RoundFacts is what GitHub says about a PR after a round. NewThreads
// counts review threads the user's account started since the round
// started: the findings, not the summary. NewReplies counts its replies in
// threads, which answer or confirm and are no finding.
type RoundFacts struct {
	State                 string
	Head                  string
	NewThreads            int
	NewReplies            int
	UnresolvedUserThreads int
	UserReview            *Review
	// RoundReviewURL is a review by the user's account posted since the round
	// started, at any head (a push mid-round moves it): the round's summary.
	RoundReviewURL string
}

// Decision is what tick does with one finished round.
type Decision struct {
	Clear           bool
	Annotation      *Annotation
	MintFor         string
	DismissReviewID int64
	Retry           bool
	// Ignore holds the PR back from rounds until the user's next click.
	Ignore bool
}

// decide reads one finished round against GitHub. stuck, not needs_you,
// marks the outcomes nothing but the user moves: a refused post holds the PR
// back until the next click, and a round that failed twice is not retried. The session's own words
// (o) never suffice: an Approve link also needs GitHub to show a quiet round
// at the head tick recorded, and "handled" needs the round's review.
func decide(r Round, o Outcome, f RoundFacts, pr PR) Decision {
	attach, ignore := attachLink(pr), ignoreLink(pr)
	summary := func(links ...Link) []Link {
		if f.RoundReviewURL != "" {
			links = append(links, Link{Label: "Round summary", URL: f.RoundReviewURL})
		}
		return links
	}
	approvedEarlier := approvedBefore(r, f)

	switch {
	case f.State != prOpen:
		return Decision{Clear: true}
	// Before any reading of the round's words or findings: a round pushed
	// over its post limit is suspect whatever it says, and never earns an
	// Approve link or a retry.
	case r.PostRefused:
		return Decision{Ignore: true, Annotation: &Annotation{State: stateStuck,
			Note: "a post was refused over the limit; no more rounds on this PR until you click it again", Links: summary(attach)}}
	case o.Kind == OutcomeQuestion:
		return Decision{Annotation: &Annotation{State: stateNeedsYou, Note: questionNote(o), Links: summary(attach, ignore)}}
	case f.NewThreads > 0 && approvedEarlier:
		return Decision{DismissReviewID: f.UserReview.ID, Annotation: &Annotation{State: stateNeedsYou,
			Note: "new findings after your approval; your approval was dismissed", Links: summary(attach, ignore)}}
	case quietApprove(r, o, f, pr):
		if f.UserReview != nil && f.UserReview.State == reviewApproved {
			return Decision{Annotation: &Annotation{State: stateHandled, Note: "clean round; your approval stands", Links: summary()}}
		}
		return Decision{MintFor: r.Head, Annotation: &Annotation{State: stateNeedsYou, Note: "clean round at " + r.Head[:9], Links: summary(attach, ignore)}}
	// The summary is posted last, so it is what says the round finished.
	case f.RoundReviewURL != "":
		return Decision{Annotation: &Annotation{State: stateHandled, Note: "round posted its review", Links: summary()}}
	// Trusted like the question line: a crashed round never prints it, and
	// GitHub confirmed above that the round posted nothing.
	case o.Kind == OutcomeNothing && !postedInline(f):
		return Decision{Annotation: &Annotation{State: stateHandled, Note: "round found nothing to post", Links: summary()}}
	// Cut off (a shutdown, a crash) before its summary: the retry resumes the
	// same session, which knows what it posted, and the post budget caps what
	// it can add at that commit.
	case r.Retries == 0:
		return Decision{Retry: true}
	}
	return Decision{Annotation: &Annotation{State: stateStuck, Note: failedTwice(f), Links: []Link{attach, ignore}}}
}

// questionNote carries the round's question when it found one.
func questionNote(o Outcome) string {
	if o.Question == "" {
		return "the round has a question for you"
	}
	return "the round has a question for you: " + o.Question
}

// failedTwice names how a round that never finished failed.
func failedTwice(f RoundFacts) string {
	if postedInline(f) {
		return "round stopped twice before posting its summary"
	}
	return "round failed twice"
}

// postedInline: inline comments (threads or replies) the round made.
func postedInline(f RoundFacts) bool {
	return f.NewThreads > 0 || f.NewReplies > 0
}

// approvedBefore: the user approved an older head before the round started.
// An approval given after was given with the round's findings in view, so the
// round does not overturn it.
func approvedBefore(r Round, f RoundFacts) bool {
	u := f.UserReview
	return u != nil && u.State == reviewApproved && u.CommitID != f.Head && u.SubmittedAt.Before(r.StartedAt)
}

func attachLink(pr PR) Link { return Link{Label: "Open session", URL: verbURL(pr, "attach")} }
func ignoreLink(pr PR) Link { return Link{Label: "Ignore", URL: verbURL(pr, "ignore")} }

// quietApprove: the session's approve line names this PR at the head tick
// recorded, the head has not moved, and GitHub shows the round started no
// thread and leaves none of the user's threads open. Replies do not count:
// a round that only confirms fixes, and resolves those threads, is clean.
func quietApprove(r Round, o Outcome, f RoundFacts, pr PR) bool {
	return o.Kind == OutcomeApprove && o.PrURL == pr.URL() && o.SHA == r.Head && f.Head == r.Head &&
		f.NewThreads == 0 && f.UnresolvedUserThreads == 0
}

// verbURL is a claude-review:// link with a verb, as the URL handler reads it.
func verbURL(pr PR, verb string) string {
	return fmt.Sprintf("claude-review://%s/%s/%d/%s", pr.Owner, pr.Repo, pr.Number, verb)
}

// approveURL carries the sha and the one-time token tick minted for it.
func approveURL(pr PR, sha, token string) string {
	return verbURL(pr, "approve") + "?at=" + sha + "&token=" + token
}
