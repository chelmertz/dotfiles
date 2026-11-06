package main

import (
	"errors"
	"fmt"
	"strconv"
)

// Inline comments and replies go into the account's pending (draft) review
// and reach the author with the summary review, in one notification: one
// email per comment was what the author complained about. GitHub keeps one
// pending review per account per PR, visible only to that account.

const pendingReviewQuery = `query($owner: String!, $repo: String!, $n: Int!) {
  repository(owner: $owner, name: $repo) { pullRequest(number: $n) { id
    reviews(states: PENDING, first: 5) { nodes { id comments(first: 100) { nodes { body } } } } } } }`

const addPendingReviewMutation = `mutation($pr: ID!, $at: GitObjectID!) {
  addPullRequestReview(input: {pullRequestId: $pr, commitOID: $at}) { pullRequestReview { id } } }`

const addPendingThreadMutation = `mutation($review: ID!, $path: String!, $line: Int!, $side: DiffSide!, $body: String!) {
  addPullRequestReviewThread(input: {pullRequestReviewId: $review, path: $path, line: $line, side: $side, body: $body}) { thread { id } } }`

const addPendingReplyMutation = `mutation($review: ID!, $thread: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewId: $review, pullRequestReviewThreadId: $thread, body: $body}) { comment { id } } }`

const submitPendingMutation = `mutation($review: ID!, $body: String!) {
  submitPullRequestReview(input: {pullRequestReviewId: $review, event: COMMENT, body: $body}) { pullRequestReview { url } } }`

// errForeignDraft refuses to add to a pending review holding a comment this
// tool did not sign: it is the user's own draft, and the summary would send
// it.
var errForeignDraft = errors.New("a draft review of yours is open on this PR with comments not from claude-review; submit or discard it on GitHub first")

// pendingReview is the account's pending review on a PR; ID is "" when
// there is none.
type pendingReview struct {
	PRID, ID string
	Bodies   []string
}

func (p pendingReview) holds(body string) bool {
	for _, b := range p.Bodies {
		if b == body {
			return true
		}
	}
	return false
}

// foreign reports a comment in the draft that this tool did not sign.
func (p pendingReview) foreign() bool {
	for _, b := range p.Bodies {
		if !signed(b) {
			return true
		}
	}
	return false
}

func fetchPending(gh Gh, pr PR) (pendingReview, error) {
	var q struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ID      string `json:"id"`
					Reviews struct {
						Nodes []struct {
							ID       string `json:"id"`
							Comments struct {
								Nodes []struct {
									Body string `json:"body"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := (GitHub{Gh: gh}).getJSON(&q, "graphql", "-f", "query="+pendingReviewQuery,
		"-F", "owner="+pr.Owner, "-F", "repo="+pr.Repo, "-F", "n="+strconv.Itoa(pr.Number)); err != nil {
		return pendingReview{}, err
	}
	p := pendingReview{PRID: q.Data.Repository.PullRequest.ID}
	if nodes := q.Data.Repository.PullRequest.Reviews.Nodes; len(nodes) > 0 {
		p.ID = nodes[0].ID
		for _, c := range nodes[0].Comments.Nodes {
			p.Bodies = append(p.Bodies, c.Body)
		}
	}
	return p, nil
}

// openPending returns the pending review to add to, starting one on commit
// at when there is none.
func openPending(gh Gh, p pendingReview, at string) (string, error) {
	if p.foreign() {
		return "", errForeignDraft
	}
	if p.ID != "" {
		return p.ID, nil
	}
	var r struct {
		Data struct {
			AddPullRequestReview struct {
				PullRequestReview struct {
					ID string `json:"id"`
				} `json:"pullRequestReview"`
			} `json:"addPullRequestReview"`
		} `json:"data"`
	}
	if err := (GitHub{Gh: gh}).getJSON(&r, "graphql", "-f", "query="+addPendingReviewMutation, "-f", "pr="+p.PRID, "-f", "at="+at); err != nil {
		return "", err
	}
	if id := r.Data.AddPullRequestReview.PullRequestReview.ID; id != "" {
		return id, nil
	}
	return "", errors.New("GitHub started no pending review")
}

// queue adds an inline comment or a reply to the pending review. A reply's
// thread comes from --to; its draft starts on the PR's head.
func (in postInput) queue(gh Gh, p pendingReview) error {
	at := in.at
	var thread string
	if in.kind == kindReply {
		th, _, err := findThread(gh, in.pr, in.to)
		if err != nil {
			return err
		}
		thread = th.ID
		if p.ID == "" {
			if at, err = headSHA(gh, fmt.Sprintf("repos/%s/%s", in.pr.Owner, in.pr.Repo), in.pr.Number); err != nil {
				return err
			}
		}
	}
	review, err := openPending(gh, p, at)
	if err != nil {
		return err
	}
	if in.kind == kindReply {
		_, err = gh("api", "graphql", "-f", "query="+addPendingReplyMutation, "-f", "review="+review, "-f", "thread="+thread, "-f", "body="+in.body)
		return err
	}
	_, err = gh("api", "graphql", "-f", "query="+addPendingThreadMutation, "-f", "review="+review,
		"-f", "path="+in.path, "-F", "line="+strconv.Itoa(in.line), "-f", "side="+in.side, "-f", "body="+in.body)
	return err
}

// submitPending sends the pending review with the summary as its body and
// returns its URL.
func submitPending(gh Gh, p pendingReview, body string) (string, error) {
	if p.foreign() {
		return "", errForeignDraft
	}
	var r struct {
		Data struct {
			SubmitPullRequestReview struct {
				PullRequestReview struct {
					URL string `json:"url"`
				} `json:"pullRequestReview"`
			} `json:"submitPullRequestReview"`
		} `json:"data"`
	}
	if err := (GitHub{Gh: gh}).getJSON(&r, "graphql", "-f", "query="+submitPendingMutation, "-f", "review="+p.ID, "-f", "body="+body); err != nil {
		return "", err
	}
	return r.Data.SubmitPullRequestReview.PullRequestReview.URL, nil
}
