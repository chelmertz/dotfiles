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
// SHA, which must still be its head. It is for the human, typed with `!` in
// the session's window after a round that found nothing; denyRules keep the
// session from running it. Nothing is read from the PR's comments, which
// the session can write: --at only guards against a push since the review,
// and the approval is bound to that commit.
func runApprove(args []string, gh Gh, stdout, stderr io.Writer) int {
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
	api := fmt.Sprintf("repos/%s/%s", pr.Owner, pr.Repo)
	head, err := headSHA(gh, api, pr.Number)
	if err != nil {
		fmt.Fprintln(stderr, "claude-review approve:", err)
		return 1
	}
	if head != *at {
		fmt.Fprintf(stderr, "claude-review approve: the review covered %.9s, but the head is now %.9s; click claude review again\n", *at, head)
		return 1
	}
	out, err := gh("api", "-X", "POST", fmt.Sprintf("%s/pulls/%d/reviews", api, pr.Number), "-f", "event=APPROVE", "-f", "commit_id="+head)
	if err != nil {
		fmt.Fprintf(stderr, "claude-review approve: %v\n%s\n", err, strings.TrimSpace(out))
		return 1
	}
	fmt.Fprintln(stdout, htmlURL(out))
	return 0
}
