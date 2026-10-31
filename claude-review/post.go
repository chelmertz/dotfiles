package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// retryWindow is how recent an identical comment or review must be to count
// as an earlier attempt at the same post rather than an earlier round.
const retryWindow = 10 * time.Minute

// signature is the line every posted comment ends with, so readers can tell
// Claude's comments from the human's.
const signature = "--claude"

const postUsage = `usage: claude-review post <pr> comment --body TEXT
       claude-review post <pr> inline  --body TEXT --path FILE --line N [--side RIGHT|LEFT] --at SHA
       claude-review post <pr> reply   --body TEXT --to COMMENT_ID
       claude-review post <pr> review  --body TEXT

Posts to the PR through gh, taking the body only as inline text, and appends
the ` + "`--claude`" + ` signature. This is what a review session with
post_without_asking is allowed to run without a prompt. It has no flag that
reads a file, so gh's own file-reading flags cannot be used to post from
disk; text the session has already read can still be posted (see the
README's blast radius). A review is always a COMMENT, so the session cannot
approve a PR or request changes on the user's behalf. Posting text that is
already on the PR in the same place prints the existing comment's URL and
posts nothing, so a retry after an ambiguous error cannot duplicate it; for
a comment or review that holds only within 10 minutes. An inline comment
anchors on --at, the full sha of the commit the line was read in, so a push
since the review cannot move it onto other code.
`

// The post kinds, and the GitHub list endpoint (under repos/OWNER/REPO/,
// with the PR number) that holds each kind's earlier posts.
const (
	kindComment = "comment"
	kindInline  = "inline"
	kindReply   = "reply"
	kindReview  = "review"

	issueComments  = "issues/%d/comments"
	reviews        = "pulls/%d/reviews"
	reviewComments = "pulls/%d/comments"
)

// Gh runs gh with args and returns stdout. A variable so tests can fake it.
type Gh func(args ...string) (string, error)

// runPost is `claude-review post`. Exit codes as run: 0, 1, 2 for usage.
func runPost(args []string, gh Gh, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "claude-review post: need <pr> and a kind (comment, inline, reply or review)")
		fmt.Fprint(stderr, postUsage)
		return 2
	}
	pr, err := ParseTarget(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	kind := args[1]
	fs := flag.NewFlagSet("claude-review post "+kind, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, postUsage) }
	body := fs.String("body", "", "comment text")
	path := fs.String("path", "", "inline: file path in the PR")
	line := fs.Int("line", 0, "inline: line number in the file (new file for RIGHT, old file for LEFT)")
	side := fs.String("side", "RIGHT", "inline: RIGHT (new code) or LEFT (old code)")
	to := fs.Int64("to", 0, "reply: id of the review comment to answer, from `claude-review threads`")
	at := fs.String("at", "", "inline: full sha of the reviewed commit (git rev-parse FETCH_HEAD)")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*body) == "" {
		fs.Usage()
		return 2
	}
	in := postInput{pr: pr, kind: kind, body: sign(*body), path: *path, line: *line, side: *side, to: *to, at: *at}
	if err := in.validate(); err != nil {
		fmt.Fprintln(stderr, "claude-review post:", err)
		fs.Usage()
		return 2
	}
	if url, err := in.existing(gh); err != nil {
		fmt.Fprintf(stderr, "claude-review post: checking for an earlier copy: %v\n", err)
		return 1
	} else if url != "" {
		fmt.Fprintln(stderr, "claude-review post: already on the PR, not posted again")
		fmt.Fprintln(stdout, url)
		return 0
	}
	out, err := in.send(gh)
	if err != nil {
		fmt.Fprintf(stderr, "claude-review post: %v\n%s\n", err, strings.TrimSpace(out))
		return 1
	}
	fmt.Fprintln(stdout, htmlURL(out))
	return 0
}

// postInput is one validated-enough post request; send validates the rest.
type postInput struct {
	pr                     PR
	kind, body, path, side string
	at                     string
	line                   int
	to                     int64
}

// validate checks the per-kind arguments before anything touches GitHub.
func (in postInput) validate() error {
	switch in.kind {
	case kindComment, kindReview:
		return nil
	case kindInline:
		if in.path == "" || in.line <= 0 || (in.side != "RIGHT" && in.side != "LEFT") || !fullSHA.MatchString(in.at) {
			return errors.New("inline needs --path, --line > 0, --side RIGHT or LEFT and --at with the reviewed commit's full sha")
		}
		return nil
	case kindReply:
		if in.to <= 0 {
			return errors.New("reply needs --to with the id of a review comment")
		}
		return nil
	default:
		return fmt.Errorf("unknown kind %q", in.kind)
	}
}

// existing returns the URL of a comment already on the PR with the same
// signed body in the same place, or "". A post that fails after GitHub
// stored it (seen as a 422 on an inline comment) is then safe to retry.
// A PR-level comment or review has no place, so only one from the last
// retryWindow counts: a later round may repeat a short summary.
func (in postInput) existing(gh Gh) (string, error) {
	api := fmt.Sprintf("repos/%s/%s", in.pr.Owner, in.pr.Repo)
	endpoint := map[string]string{
		kindComment: issueComments,
		kindReview:  reviews,
		kindInline:  reviewComments,
		kindReply:   reviewComments,
	}[in.kind]
	items, err := ghList(gh, api+"/"+fmt.Sprintf(endpoint, in.pr.Number))
	if err != nil {
		return "", err
	}
	// GitHub files a reply to a reply under the thread's root comment, so
	// --to with any comment in the thread must look for the root.
	root := in.to
	for _, c := range items {
		if c.ID == in.to && c.InReplyTo != 0 {
			root = c.InReplyTo
		}
	}
	for _, c := range items {
		if in.sameAs(c, root) {
			return c.HTMLURL, nil
		}
	}
	return "", nil
}

// sameAs reports whether c is this post already made: the same signed body
// in the same place. root is the thread a reply goes to.
func (in postInput) sameAs(c ghItem, root int64) bool {
	if c.Body != in.body {
		return false
	}
	switch in.kind {
	case kindInline:
		// Line is GitHub's mapping onto the current head and moves with
		// pushes; a retry matches where the comment was made.
		return c.Path == in.path && c.OriginalCommitID == in.at && c.OriginalLine == in.line && c.Side == in.side && c.InReplyTo == 0
	case kindReply:
		return c.InReplyTo == root
	default: // comment or review
		return c.recent()
	}
}

// send performs the gh calls for one post and returns gh's last output.
func (in postInput) send(gh Gh) (string, error) {
	api := fmt.Sprintf("repos/%s/%s", in.pr.Owner, in.pr.Repo)
	switch in.kind {
	case kindComment:
		return gh("api", "-X", "POST", fmt.Sprintf("%s/issues/%d/comments", api, in.pr.Number), "-f", "body="+in.body)
	case kindReview:
		// Always a COMMENT review: approving or requesting changes is the
		// human's call, and this command runs without a prompt.
		return gh("api", "-X", "POST", fmt.Sprintf("%s/pulls/%d/reviews", api, in.pr.Number), "-f", "body="+in.body, "-f", "event=COMMENT")
	case kindInline:
		// The line number belongs to the reviewed commit; anchoring on the
		// head instead would land it on other code after a push.
		return gh("api", "-X", "POST", fmt.Sprintf("%s/pulls/%d/comments", api, in.pr.Number),
			"-f", "body="+in.body, "-f", "commit_id="+in.at, "-f", "path="+in.path,
			"-F", "line="+strconv.Itoa(in.line), "-f", "side="+in.side)
	default: // reply; validate admits nothing else
		return gh("api", "-X", "POST", fmt.Sprintf("%s/pulls/%d/comments/%d/replies", api, in.pr.Number, in.to), "-f", "body="+in.body)
	}
}

// sign appends the signature line unless the body already ends with it.
func sign(body string) string {
	b := strings.TrimRight(body, " \t\n")
	if strings.HasSuffix(b, "\n"+signature) || b == signature {
		return b
	}
	return b + "\n\n" + signature
}

// headSHA is the PR's current head commit, which approve checks --at against.
func headSHA(gh Gh, api string, number int) (string, error) {
	out, err := gh("api", fmt.Sprintf("%s/pulls/%d", api, number), "--jq", ".head.sha")
	if err != nil {
		return "", fmt.Errorf("reading PR head: %w\n%s", err, strings.TrimSpace(out))
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", fmt.Errorf("reading PR head: empty sha")
	}
	return sha, nil
}

// htmlURL pulls html_url out of a gh api response, or returns the raw
// response trimmed when it is not JSON with that field.
func htmlURL(out string) string {
	var v struct {
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal([]byte(out), &v) == nil && v.HTMLURL != "" {
		return v.HTMLURL
	}
	return strings.TrimSpace(out)
}

// ghItem is the subset of a GitHub comment or review that threads and the
// duplicate check read.
type ghItem struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	Path string `json:"path"`
	Line int    `json:"line"`
	// OriginalLine is where an outdated comment sat; Line is then null.
	OriginalLine     int    `json:"original_line"`
	OriginalCommitID string `json:"original_commit_id"`
	InReplyTo        int64  `json:"in_reply_to_id"`
	Side             string `json:"side"`
	// CreatedAt dates a comment, SubmittedAt a review. Only a recent copy
	// is a retry; an older one is an earlier round saying the same thing.
	CreatedAt   time.Time `json:"created_at"`
	SubmittedAt time.Time `json:"submitted_at"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	// CommitID is the commit a review was submitted against.
	CommitID string `json:"commit_id"`
	User     struct {
		Login string `json:"login"`
		Type  string `json:"type"` // "Bot" for an app
	} `json:"user"`
}

// recent is an item dated within retryWindow; each kind sets one date.
func (c ghItem) recent() bool {
	return time.Since(c.CreatedAt) <= retryWindow || time.Since(c.SubmittedAt) <= retryWindow
}

const paginate = "--paginate"

// ghList reads every page of a GitHub list endpoint. `gh api --paginate`
// prints one JSON array per page, back to back.
func ghList(gh Gh, endpoint string) ([]ghItem, error) {
	out, err := gh("api", paginate, endpoint)
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", endpoint, err, strings.TrimSpace(out))
	}
	var all []ghItem
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []ghItem
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("%s: %w", endpoint, err)
		}
		all = append(all, page...)
	}
	return all, nil
}

// denyRules close the ways around `claude-review post`. Deny rules hold in
// every permission mode, auto included, where an allow-list alone decides
// nothing: in auto mode a session once answered a thread with a raw
// `gh api` POST because the tool had no reply kind. Reads go through
// `gh pr view`, `gh pr diff` and `claude-review threads` instead of gh api.
var denyRules = []string{
	"Bash(gh api *)",
	"Bash(gh pr comment *)",
	"Bash(gh pr review *)",
	"Bash(gh pr edit *)",
	"Bash(gh pr merge *)",
	"Bash(gh pr close *)",
	"Bash(gh issue comment *)",
	"Bash(claude-review approve *)",
}

// threadsRule pre-approves the read-only `claude-review threads` for this PR.
func threadsRule(pr PR) string {
	return "Bash(claude-review threads " + pr.URL() + ")"
}

// postRule is the one permission a post_without_asking session gets: this
// tool, pinned to the PR. Everything before the * is matched literally.
func postRule(pr PR) string {
	return "Bash(claude-review post " + pr.URL() + " *)"
}
