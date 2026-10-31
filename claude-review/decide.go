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
	// DailyCap is how many rounds one PR may start in 24 hours.
	DailyCap int
	// MaxRunning is how many rounds may run at once.
	MaxRunning int
	// Wrapper prefixes each round's command, e.g. ["capped"].
	Wrapper []string
}

const (
	gateFreshLook = "fresh_look"
	gatePaused    = "paused"

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
	// Base is the PR's base branch now.
	Base(pr PR) (string, error)
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
	if ran, err := l.RanAt(pr, p.HeadRefOid, trigger); err != nil || ran {
		return Planned{}, false, err
	}
	gate, err := gateFor(pr, acc, p.HeadRefOid, cfg, l, g, now)
	return Planned{PR: pr, Head: p.HeadRefOid, Trigger: trigger, Gate: gate}, err == nil, err
}

// eligible: a PR in review_repos that the user did not write.
func eligible(p EllyPR, cfg TickConfig) (PR, bool) {
	pr, err := ParseTarget(p.Url)
	return pr, err == nil && repoAllowed(p.RepoOwner, p.RepoName, cfg.ReviewRepos) && !strings.EqualFold(p.Author, cfg.User)
}

// gateFor is what stops an owed round: a PR that changed under its first
// look, or the daily cap. "" lets it run.
func gateFor(pr PR, acc Acceptance, head string, cfg TickConfig, l *Ledger, g Gate, now time.Time) (string, error) {
	fresh, err := needsFreshLook(pr, acc, head, g)
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
func needsFreshLook(pr PR, acc Acceptance, head string, g Gate) (bool, error) {
	base, err := g.Base(pr)
	if err != nil {
		return false, err
	}
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
	ID       int64
	State    string
	CommitID string
}

// RoundFacts is what GitHub says about a PR after a round. NewComments
// counts inline review comments the user's account posted since the round
// started: the findings, not the summary.
type RoundFacts struct {
	State                 string
	Head                  string
	NewComments           int
	UnresolvedUserThreads int
	UserReview            *Review
	// RoundReviewURL is a review by the user's account at the round's head,
	// posted since the round started: the round's own summary.
	RoundReviewURL string
	// CommitsSinceApproval counts commits since the user's last approval,
	// -1 when unknown or when there is none.
	CommitsSinceApproval int
}

// Decision is what tick does with one finished round.
type Decision struct {
	Clear           bool
	Annotation      *Annotation
	MintFor         string
	DismissReviewID int64
	Retry           bool
}

// decide reads one finished round against GitHub. The session's own words
// (o) never suffice: an Approve link also needs GitHub to show a quiet round
// at the head tick recorded, and "handled" needs the round's review.
func decide(r Round, o Outcome, f RoundFacts, pr PR) Decision {
	attach := Link{Label: "Open session", URL: verbURL(pr, "attach")}
	ignore := Link{Label: "Ignore", URL: verbURL(pr, "ignore")}
	summary := func(links ...Link) []Link {
		if f.RoundReviewURL != "" {
			links = append(links, Link{Label: "Round summary", URL: f.RoundReviewURL})
		}
		return links
	}
	approvedEarlier := f.UserReview != nil && f.UserReview.State == reviewApproved && f.UserReview.CommitID != f.Head

	switch {
	case f.State != prOpen:
		return Decision{Clear: true}
	case o.Kind == OutcomeQuestion:
		return Decision{Annotation: &Annotation{State: stateNeedsYou, Note: "the round has a question for you", Links: summary(attach, ignore)}}
	case f.NewComments > 0 && approvedEarlier:
		return Decision{DismissReviewID: f.UserReview.ID, Annotation: &Annotation{State: stateNeedsYou,
			Note: "new findings after your approval; your approval was dismissed", Links: summary(attach, ignore)}}
	case quietApprove(r, o, f, pr):
		if f.UserReview != nil && f.UserReview.State == reviewApproved {
			return Decision{Annotation: &Annotation{State: stateHandled, Note: "clean round; your approval stands", Links: summary()}}
		}
		note := "clean round at " + r.Head[:9]
		if f.CommitsSinceApproval >= 0 {
			note += fmt.Sprintf("; %d commits since your last approval", f.CommitsSinceApproval)
		}
		return Decision{MintFor: r.Head, Annotation: &Annotation{State: stateNeedsYou, Note: note, Links: summary(attach, ignore)}}
	case f.RoundReviewURL != "":
		return Decision{Annotation: &Annotation{State: stateHandled, Note: "round posted its review", Links: summary()}}
	case r.Retries == 0:
		return Decision{Retry: true}
	}
	return Decision{Annotation: &Annotation{State: stateNeedsYou, Note: "round failed twice", Links: []Link{attach, ignore}}}
}

// quietApprove: the session's approve line names this PR at the head tick
// recorded, the head has not moved, and GitHub shows the round posted no
// findings and leaves none of the user's threads open.
func quietApprove(r Round, o Outcome, f RoundFacts, pr PR) bool {
	return o.Kind == OutcomeApprove && o.PrURL == pr.URL() && o.SHA == r.Head && f.Head == r.Head &&
		f.NewComments == 0 && f.UnresolvedUserThreads == 0
}

// verbURL is a claude-review:// link with a verb, as the URL handler reads it.
func verbURL(pr PR, verb string) string {
	return fmt.Sprintf("claude-review://%s/%s/%d/%s", pr.Owner, pr.Repo, pr.Number, verb)
}

// approveURL carries the sha and the one-time token tick minted for it.
func approveURL(pr PR, sha, token string) string {
	return verbURL(pr, "approve") + "?at=" + sha + "&token=" + token
}
