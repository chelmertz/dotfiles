package main

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// runApprove is `claude-review approve <pr> --at SHA`: approve the PR at
// SHA, which must still be the head of a PR still open. It is for the human, typed with `!` in
// the session's window after a round that found nothing; denyRules keep the
// session from running it. Nothing is read from the PR's comments, which
// the session can write: --at only guards against a push since the review,
// and the approval is bound to that commit.
func runApprove(args []string, gh Gh, stdout, stderr io.Writer) int {
	return runApproveGuarded(args, gh, stdout, stderr, nil, nil)
}

// runApproveGuarded is runApprove with guard run on the parsed PR before
// anything reaches GitHub; guard holds what it returns until the end. Once
// GitHub was asked, record logs the outcome, still under guard.
func runApproveGuarded(args []string, gh Gh, stdout, stderr io.Writer, guard func(PR) (func(), error), record func(pr PR, at, outcome string) error) int {
	const usage = "usage: claude-review approve <pr> --at SHA"
	fs := flag.NewFlagSet("claude-review approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, usage) }
	if len(args) == 0 {
		fs.Usage()
		return 2
	}
	at := fs.String("at", "", "the full commit sha the review covered")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	pr, err := ParseTarget(args[0])
	if err != nil || fs.NArg() != 0 || !fullSHA.MatchString(*at) {
		fs.Usage()
		return 2
	}
	if guard != nil {
		release, err := guard(pr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer release()
	}
	code, outcome := approveAt(gh, pr, *at, stdout, stderr)
	if record == nil {
		return code
	}
	if err := record(pr, *at, outcome); err != nil {
		fmt.Fprintf(stderr, "claude-review approve: %s at %.9s, but logging it failed: %v\n", outcome, *at, err)
		return 1
	}
	return code
}

// approveAt approves pr at the full sha at if pr is open and at its head,
// and says how it went (outcomeApproved, ...) for the decision log.
func approveAt(gh Gh, pr PR, at string, stdout, stderr io.Writer) (code int, outcome string) {
	api := fmt.Sprintf("repos/%s/%s", pr.Owner, pr.Repo)
	state, head, err := pullStateHead(gh, api, pr.Number)
	if err != nil {
		fmt.Fprintln(stderr, "claude-review approve:", err)
		return 1, outcomeFailed
	}
	if state != "open" {
		fmt.Fprintf(stderr, "claude-review approve: %s is %s; there is nothing to approve\n", pr.URL(), state)
		return 1, outcomeClosed
	}
	if head != at {
		fmt.Fprintf(stderr, "claude-review approve: the review covered %.9s, but the head is now %.9s; click claude review again\n", at, head)
		return 1, outcomeStale
	}
	out, err := gh("api", "-X", "POST", fmt.Sprintf("%s/pulls/%d/reviews", api, pr.Number), "-f", "event=APPROVE", "-f", "commit_id="+head)
	if err != nil {
		fmt.Fprintf(stderr, "claude-review approve: %v\n%s\n", err, strings.TrimSpace(out))
		return 1, outcomeFailed
	}
	fmt.Fprintln(stdout, htmlURL(out))
	return 0, outcomeApproved
}

// pullStateHead reads the PR's state (open or closed, merged included) and
// head sha in one call.
func pullStateHead(gh Gh, api string, number int) (state, head string, err error) {
	out, err := gh("api", fmt.Sprintf("%s/pulls/%d", api, number), "--jq", `.state + " " + .head.sha`)
	if err != nil {
		return "", "", fmt.Errorf("reading PR head: %w\n%s", err, strings.TrimSpace(out))
	}
	state, head, _ = strings.Cut(strings.TrimSpace(out), " ")
	if state == "" || head == "" {
		return "", "", fmt.Errorf("reading PR head: empty sha")
	}
	return state, head, nil
}
