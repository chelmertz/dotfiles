package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// reviewThreadsQuery is the PR's review threads, each with its comments'
// REST ids, and the account gh posts as.
const reviewThreadsQuery = `query($owner: String!, $repo: String!, $n: Int!) {
  viewer { login }
  repository(owner: $owner, name: $repo) { pullRequest(number: $n) {
    reviewThreads(first: 100) { nodes { id isResolved comments(first: 100) { nodes { databaseId url author { login } } } } } } } }`

const resolveThreadMutation = `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { isResolved } } }`

// reviewThread is one thread as reviewThreadsQuery returns it.
type reviewThread struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	Comments   struct {
		Nodes []struct {
			DatabaseID int64  `json:"databaseId"`
			URL        string `json:"url"`
			Author     struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
}

// runResolve parses `claude-review post <pr> resolve --thread <id>`.
func runResolve(target string, args []string, gh Gh, spend spendFunc, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claude-review post resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, postUsage) }
	thread := fs.Int64("thread", 0, "id of any comment in the thread, from `claude-review threads`")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pr, err := ParseTarget(target)
	if err != nil || fs.NArg() != 0 || *thread <= 0 {
		fmt.Fprintln(stderr, "claude-review post: resolve needs a PR and --thread with the id of a comment in the thread")
		return 2
	}
	return resolveThread(pr, *thread, gh, spend, stdout, stderr)
}

// resolveThread is `claude-review post <pr> resolve --thread <id>`: it
// resolves the review thread holding comment id, a thread a round judged
// settled. Only a thread the account itself started: resolving someone
// else's would close their question. Resolving is a post like any other,
// so it spends the budget (one per thread at a head) and refuses a PR that
// is not open. A thread already resolved is left as it is.
func resolveThread(pr PR, id int64, gh Gh, spend spendFunc, stdout, stderr io.Writer) int {
	th, viewer, err := findThread(gh, pr, id)
	if err != nil {
		fmt.Fprintln(stderr, "claude-review post:", err)
		return 1
	}
	first := th.Comments.Nodes[0]
	switch {
	case !strings.EqualFold(first.Author.Login, viewer):
		fmt.Fprintf(stderr, "claude-review post: the thread of comment %d is not one this account started; not resolved\n", id)
		return 1
	case th.IsResolved:
		fmt.Fprintln(stderr, "claude-review post: already resolved")
		fmt.Fprintln(stdout, first.URL)
		return 0
	}
	if err := spend(pr, "resolve "+th.ID, threadPostLimit); err != nil {
		fmt.Fprintln(stderr, "claude-review post: not resolved:", err)
		return 1
	}
	if out, err := gh("api", "graphql", "-f", "query="+resolveThreadMutation, "-f", "threadId="+th.ID); err != nil {
		fmt.Fprintf(stderr, "claude-review post: %v\n%s\n", err, strings.TrimSpace(out))
		return 1
	}
	fmt.Fprintln(stdout, first.URL)
	return 0
}

// findThread is the review thread on pr holding comment id, and the login
// gh posts as.
func findThread(gh Gh, pr PR, id int64) (reviewThread, string, error) {
	var q struct {
		Data struct {
			Viewer struct {
				Login string `json:"login"`
			} `json:"viewer"`
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []reviewThread `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := (GitHub{Gh: gh}).getJSON(&q, "graphql", "-f", "query="+reviewThreadsQuery,
		"-F", "owner="+pr.Owner, "-F", "repo="+pr.Repo, "-F", "n="+strconv.Itoa(pr.Number)); err != nil {
		return reviewThread{}, "", err
	}
	for _, th := range q.Data.Repository.PullRequest.ReviewThreads.Nodes {
		for _, c := range th.Comments.Nodes {
			if c.DatabaseID == id {
				return th, q.Data.Viewer.Login, nil
			}
		}
	}
	return reviewThread{}, "", fmt.Errorf("comment %d is in no review thread on %s", id, pr.URL())
}
