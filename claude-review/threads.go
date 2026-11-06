package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/chelmertz/dotfiles/claude-review/ids"
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
	api := fmt.Sprintf("repos/%s/%s/", pr.Owner, pr.Repo)
	for _, list := range []func() error{
		func() error {
			return printItems[ids.IssueCommentID](gh, stdout, kindComment, api+fmt.Sprintf(issueComments, pr.Number))
		},
		func() error {
			return printItems[ids.ReviewID](gh, stdout, kindReview, api+fmt.Sprintf(reviews, pr.Number))
		},
		func() error {
			return printItems[ids.CommentID](gh, stdout, kindInline, api+fmt.Sprintf(reviewComments, pr.Number))
		},
	} {
		if err := list(); err != nil {
			fmt.Fprintln(stderr, "claude-review threads:", err)
			return 1
		}
	}
	return 0
}

// ghID is what threads needs of an item's id: how to print it, and whether
// it is absent.
type ghID interface {
	String() string
	IsZero() bool
}

// printItems prints the items at endpoint, each under its header.
func printItems[I ghID](gh Gh, stdout io.Writer, kind, endpoint string) error {
	items, err := ghList[I](gh, endpoint)
	if err != nil {
		return err
	}
	for _, c := range items {
		if c.Body == "" {
			continue // a bare approval, or a review that only carries inline comments
		}
		// Bodies are indented so a markdown "---" in one cannot pass for
		// the next item's header.
		body := "    " + strings.ReplaceAll(strings.TrimSpace(c.Body), "\n", "\n    ")
		fmt.Fprintf(stdout, "--- %s\n%s\n\n", header(kind, c), body)
	}
	return nil
}

// header is one item's first line: kind, id, author and where it sits.
func header[I ghID](kind string, c ghItem[I]) string {
	h := fmt.Sprintf("%s %s by %s", kind, c.ID, c.User.Login)
	switch {
	case kind == kindReview:
		h += " " + c.State
	case kind != kindInline:
	case c.Line == 0:
		h += fmt.Sprintf(" on %s, outdated, was line %d", c.Path, c.OriginalLine)
	default:
		h += fmt.Sprintf(" on %s:%d", c.Path, c.Line)
	}
	if !c.InReplyTo.IsZero() {
		h += fmt.Sprintf(", reply to %s", c.InReplyTo)
	}
	return h
}
