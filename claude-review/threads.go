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
	var seen []printed
	for _, list := range []func() ([]printed, error){
		func() ([]printed, error) {
			return printItems[ids.IssueCommentID](gh, stdout, kindComment, api+fmt.Sprintf(issueComments, pr.Number))
		},
		func() ([]printed, error) {
			return printItems[ids.ReviewID](gh, stdout, kindReview, api+fmt.Sprintf(reviews, pr.Number))
		},
		func() ([]printed, error) {
			return printItems[ids.CommentID](gh, stdout, kindInline, api+fmt.Sprintf(reviewComments, pr.Number))
		},
	} {
		items, err := list()
		if err != nil {
			fmt.Fprintln(stderr, "claude-review threads:", err)
			return 1
		}
		seen = append(seen, items...)
	}
	printMentions(gh, stdout, seen)
	return 0
}

// printed is what threads keeps of an item to list the mentions after it.
type printed struct{ head, author, body string }

// printMentions names, after everything, the items by someone else that
// @-mention the account the round posts as. Without the login (gh failed)
// it prints nothing: the prompt's mention step then rests on the listing.
func printMentions(gh Gh, stdout io.Writer, items []printed) {
	out, err := gh("api", "user", "--jq", ".login")
	login := strings.TrimSpace(out)
	if err != nil || login == "" {
		return
	}
	var heads []string
	for _, it := range items {
		if !strings.EqualFold(it.author, login) && mentionedIn(it.body, login) {
			heads = append(heads, it.head)
		}
	}
	if len(heads) == 0 {
		return
	}
	fmt.Fprintf(stdout, "--- you post as %s; these mention you:\n    %s\n", login, strings.Join(heads, "\n    "))
}

// ghID is what threads needs of an item's id: how to print it, and whether
// it is absent.
type ghID interface {
	String() string
	IsZero() bool
}

// printItems prints the items at endpoint, each under its header.
func printItems[I ghID](gh Gh, stdout io.Writer, kind, endpoint string) ([]printed, error) {
	items, err := ghList[I](gh, endpoint)
	if err != nil {
		return nil, err
	}
	var out []printed
	for _, c := range items {
		if c.Body == "" {
			continue // a bare approval, or a review that only carries inline comments
		}
		// Bodies are indented so a markdown "---" in one cannot pass for
		// the next item's header.
		body := "    " + strings.ReplaceAll(strings.TrimSpace(c.Body), "\n", "\n    ")
		fmt.Fprintf(stdout, "--- %s\n%s\n\n", header(kind, c), body)
		out = append(out, printed{head: header(kind, c), author: c.User.Login, body: c.Body})
	}
	return out, nil
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
	switch {
	case !c.InReplyTo.IsZero():
		h += fmt.Sprintf(", reply to %s", c.InReplyTo)
	case kind == kindInline:
		// The id resolve --thread takes.
		h += ", starts a thread"
	}
	return h
}
