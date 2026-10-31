package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GitHub is what tick asks GitHub, through gh, as the user.
type GitHub struct {
	Gh   Gh
	User string
}

type ghPull struct {
	State  string `json:"state"`
	Merged bool   `json:"merged"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (g GitHub) api(pr PR, format string, a ...any) string {
	return fmt.Sprintf("repos/%s/%s/", pr.Owner, pr.Repo) + fmt.Sprintf(format, a...)
}

func (g GitHub) getJSON(v any, args ...string) error {
	out, err := g.Gh(append([]string{"api"}, args...)...)
	if err != nil {
		return fmt.Errorf("gh api %s: %w", strings.Join(args, " "), err)
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("gh api %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (g GitHub) pull(pr PR) (ghPull, error) {
	var p ghPull
	err := g.getJSON(&p, g.api(pr, "pulls/%d", pr.Number))
	return p, err
}

func (g GitHub) Base(pr PR) (string, error) {
	p, err := g.pull(pr)
	return p.Base.Ref, err
}

// Descends: GitHub's compare of from...head is "ahead" or "identical" when
// head still contains from, and "diverged" or "behind" after a force-push.
func (g GitHub) Descends(pr PR, from, head string) (bool, error) {
	var c struct {
		Status string `json:"status"`
	}
	if err := g.getJSON(&c, g.api(pr, "compare/%s...%s", from, head)); err != nil {
		return false, err
	}
	return c.Status == "ahead" || c.Status == "identical", nil
}

const unresolvedThreadsQuery = `query($owner: String!, $repo: String!, $n: Int!) {
  repository(owner: $owner, name: $repo) { pullRequest(number: $n) {
    reviewThreads(first: 100) { nodes { isResolved comments(first: 1) { nodes { author { login } } } } } } } }`

// RoundFacts is GitHub's account of a PR after round r.
func (g GitHub) RoundFacts(pr PR, r Round) (RoundFacts, error) {
	f := RoundFacts{CommitsSinceApproval: -1}
	p, err := g.pull(pr)
	if err != nil {
		return f, err
	}
	f.Head, f.State = p.Head.SHA, pullState(p)
	if f.State != prOpen {
		return f, nil
	}
	reviewList, err := ghList(g.Gh, g.api(pr, reviews, pr.Number))
	if err != nil {
		return f, err
	}
	f.UserReview = g.userReview(reviewList)
	if f.RoundReviewURL, f.NewComments, err = g.roundActivity(pr, r, reviewList); err != nil {
		return f, err
	}
	if f.UnresolvedUserThreads, err = g.unresolvedUserThreads(pr); err != nil {
		return f, err
	}
	if f.UserReview != nil && f.UserReview.State == reviewApproved && f.UserReview.CommitID != "" {
		var c struct {
			AheadBy int `json:"ahead_by"`
		}
		if err := g.getJSON(&c, g.api(pr, "compare/%s...%s", f.UserReview.CommitID, f.Head)); err == nil {
			f.CommitsSinceApproval = c.AheadBy
		}
	}
	return f, nil
}

// roundActivity is what the user's account posted since r started: the
// round's own review at its head, and the findings. Findings are inline
// review comments, new threads and replies alike; the PR-level summary every
// round posts is not one, or no round could ever come out clean.
func (g GitHub) roundActivity(pr PR, r Round, reviewList []ghItem) (string, int, error) {
	review := ""
	for _, rv := range reviewList {
		if strings.EqualFold(rv.User.Login, g.User) && rv.SubmittedAt.After(r.StartedAt) && rv.CommitID == r.Head {
			review = rv.HTMLURL
		}
	}
	inline, err := ghList(g.Gh, g.api(pr, reviewComments, pr.Number))
	if err != nil {
		return "", 0, err
	}
	n := 0
	for _, c := range inline {
		if strings.EqualFold(c.User.Login, g.User) && c.CreatedAt.After(r.StartedAt) {
			n++
		}
	}
	return review, n, nil
}

// pullState folds merged into closed: either way no round is owed.
func pullState(p ghPull) string {
	if p.Merged {
		return prClosed
	}
	return p.State
}

// userReview is the user's standing review: the last approval, change
// request or dismissal. A COMMENTED review does not replace one.
func (g GitHub) userReview(reviewList []ghItem) *Review {
	var user *Review
	for _, rv := range reviewList {
		if strings.EqualFold(rv.User.Login, g.User) && (rv.State == reviewApproved || rv.State == "CHANGES_REQUESTED" || rv.State == "DISMISSED") {
			user = &Review{ID: rv.ID, State: rv.State, CommitID: rv.CommitID}
		}
	}
	return user
}

// unresolvedUserThreads counts open review threads the user started.
func (g GitHub) unresolvedUserThreads(pr PR) (int, error) {
	var q struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := g.getJSON(&q, "graphql", "-f", "query="+unresolvedThreadsQuery,
		"-F", "owner="+pr.Owner, "-F", "repo="+pr.Repo, "-F", fmt.Sprintf("n=%d", pr.Number)); err != nil {
		return 0, err
	}
	n := 0
	for _, th := range q.Data.Repository.PullRequest.ReviewThreads.Nodes {
		c := th.Comments.Nodes
		if !th.IsResolved && len(c) > 0 && strings.EqualFold(c[0].Author.Login, g.User) {
			n++
		}
	}
	return n, nil
}

// Dismiss dismisses the user's own review id with message, signed.
func (g GitHub) Dismiss(pr PR, id int64, message string) error {
	out, err := g.Gh("api", "-X", "PUT", g.api(pr, "pulls/%d/reviews/%d/dismissals", pr.Number, id),
		"-f", "message="+message+"\n\n--claude", "-f", "event=DISMISS")
	if err != nil {
		return fmt.Errorf("dismiss review %d: %w\n%s", id, err, strings.TrimSpace(out))
	}
	return nil
}

// Standing is the PR's state, head and the user's standing review, for
// deciding whether an annotation still applies.
func (g GitHub) Standing(pr PR) (string, string, *Review, error) {
	p, err := g.pull(pr)
	if err != nil {
		return "", "", nil, err
	}
	reviewList, err := ghList(g.Gh, g.api(pr, reviews, pr.Number))
	if err != nil {
		return "", "", nil, err
	}
	return pullState(p), p.Head.SHA, g.userReview(reviewList), nil
}
