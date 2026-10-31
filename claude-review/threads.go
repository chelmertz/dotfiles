package main

import (
	"fmt"
	"io"
	"strings"
)

// runThreads is `claude-review threads <pr>`: everything already said on
// the PR, so a later round can answer threads instead of repeating them.
// Read-only, and the one GitHub read a session gets besides `gh pr view`
// and `gh pr diff`, because `gh api` is denied (see denyRules).
func runThreads(args []string, gh Gh, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: claude-review threads <pr>")
		return 2
	}
	pr, err := ParseTarget(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	api := fmt.Sprintf("repos/%s/%s", pr.Owner, pr.Repo)
	for _, src := range []struct{ kind, endpoint string }{
		{kindComment, issueComments},
		{kindReview, reviews},
		{kindInline, reviewComments},
	} {
		items, err := ghList(gh, api+"/"+fmt.Sprintf(src.endpoint, pr.Number))
		if err != nil {
			fmt.Fprintln(stderr, "claude-review threads:", err)
			return 1
		}
		for _, c := range items {
			if c.Body == "" {
				continue // a bare approval, or a review that only carries inline comments
			}
			// Bodies are indented so a markdown "---" in one cannot pass for
			// the next item's header.
			body := "    " + strings.ReplaceAll(strings.TrimSpace(c.Body), "\n", "\n    ")
			fmt.Fprintf(stdout, "--- %s\n%s\n\n", header(src.kind, c), body)
		}
	}
	return 0
}

// header is one item's first line: kind, id, author and where it sits.
func header(kind string, c ghItem) string {
	h := fmt.Sprintf("%s %d by %s", kind, c.ID, c.User.Login)
	switch {
	case kind == kindReview:
		h += " " + c.State
	case kind != kindInline:
	case c.Line == 0:
		h += fmt.Sprintf(" on %s, outdated, was line %d", c.Path, c.OriginalLine)
	default:
		h += fmt.Sprintf(" on %s:%d", c.Path, c.Line)
	}
	if c.InReplyTo != 0 {
		h += fmt.Sprintf(", reply to %d", c.InReplyTo)
	}
	return h
}
