package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
	"pgregory.net/rapid"
)

// routeGh answers gh calls by the first route its arguments contain.
type routeGh struct {
	routes map[string]string
	calls  [][]string
}

func (g *routeGh) run(args ...string) (string, error) {
	g.calls = append(g.calls, args)
	joined := strings.Join(args, " ")
	best := ""
	for k := range g.routes {
		if strings.Contains(joined, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return "", errors.New("unexpected gh call: " + joined)
	}
	return g.routes[best], nil
}

func TestGitHubGate(t *testing.T) {
	g := &routeGh{routes: map[string]string{
		"repos/acme/widgets/pulls/42":        `{"state":"open","head":{"sha":"h2"},"base":{"ref":"main"}}`,
		"repos/acme/widgets/compare/h1...h2": `{"status":"ahead"}`,
		"repos/acme/widgets/compare/hx...h2": `{"status":"diverged"}`,
	}}
	gh := GitHub{Gh: g.run, User: "me"}
	if base, open, err := gh.Base(widgetsPR); err != nil || base != "main" || !open {
		t.Errorf("base %q open %v %v", base, open, err)
	}
	merged := GitHub{Gh: (&routeGh{routes: map[string]string{"repos/acme/widgets/pulls/42": `{"state":"closed","merged":true,"base":{"ref":"main"}}`}}).run}
	if _, open, err := merged.Base(widgetsPR); err != nil || open {
		t.Errorf("a merged PR reads as open: %v %v", open, err)
	}
	if ok, err := gh.Descends(widgetsPR, "h1", "h2"); err != nil || !ok {
		t.Errorf("ahead: %v %v", ok, err)
	}
	if ok, err := gh.Descends(widgetsPR, "hx", "h2"); err != nil || ok {
		t.Errorf("diverged: %v %v", ok, err)
	}
}

func TestGitHubRoundFacts(t *testing.T) {
	start := t0
	before, after := start.Add(-time.Hour).Format(time.RFC3339), start.Add(time.Minute).Format(time.RFC3339)
	g := &routeGh{routes: map[string]string{
		"repos/acme/widgets/pulls/42": `{"state":"open","merged":false,"user":{"login":"Bob"},"head":{"sha":"h2"},"base":{"ref":"main"}}`,
		"repos/acme/widgets/pulls/42/reviews": `[
			{"id":1,"user":{"login":"me"},"state":"APPROVED","commit_id":"h1","submitted_at":"` + before + `","html_url":"r1"},
			{"id":2,"user":{"login":"bob"},"state":"COMMENTED","commit_id":"h2","submitted_at":"` + after + `","html_url":"r2"},
			{"id":3,"user":{"login":"me"},"state":"COMMENTED","commit_id":"h2","submitted_at":"` + after + `","html_url":"r3","body":"summary"}]`,
		"repos/acme/widgets/pulls/42/comments": `[
			{"id":10,"user":{"login":"me"},"created_at":"` + after + `"},
			{"id":11,"user":{"login":"me"},"created_at":"` + before + `"},
			{"id":12,"user":{"login":"bob"},"created_at":"` + after + `"},
			{"id":13,"user":{"login":"me"},"in_reply_to_id":11,"created_at":"` + after + `"},
			{"id":14,"user":{"login":"me"},"in_reply_to_id":12,"created_at":"` + after + `"}]`,
		"repos/acme/widgets/issues/42/comments": `[{"id":20,"user":{"login":"me"},"created_at":"` + after + `"}]`,
		"graphql":                               `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false,"comments":{"nodes":[{"author":{"login":"me"}}]}},{"isResolved":true,"comments":{"nodes":[{"author":{"login":"me"}}]}},{"isResolved":false,"comments":{"nodes":[{"author":{"login":"bob"}}]}}]}}}}}`,
	}}
	f, err := GitHub{Gh: g.run, User: "me"}.RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	if f.State != "open" || f.Head != "h2" {
		t.Errorf("state/head: %+v", f)
	}
	if f.Own {
		t.Error("bob's PR reads as the user's own")
	}
	if own, err := (GitHub{Gh: g.run, User: "bob"}).RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: start}); err != nil || !own.Own {
		t.Errorf("the author's own PR: %v %v", own.Own, err)
	}
	// Inline comment 10 only: a new thread by the user's account, after the
	// start. The replies 13 and 14 confirm or answer, and the summary review
	// (3) and the PR comment (20) are not findings either.
	if f.NewThreads != 1 || f.NewReplies != 2 {
		t.Errorf("new threads %d, want 1; new replies %d, want 2", f.NewThreads, f.NewReplies)
	}
	if f.UnresolvedUserThreads != 1 {
		t.Errorf("unresolved user threads %d, want 1", f.UnresolvedUserThreads)
	}
	if f.UserReview == nil || f.UserReview.ID != reviewID(1) || f.UserReview.State != "APPROVED" || !f.UserReview.SubmittedAt.Equal(start.Add(-time.Hour)) {
		t.Errorf("the user's standing review: %+v (a COMMENTED review must not replace an approval)", f.UserReview)
	}
	if f.RoundReviewURL != "r3" {
		t.Errorf("round review %q", f.RoundReviewURL)
	}
	// A push mid-round puts the summary on a newer commit than the one tick
	// recorded; it is still the round's summary, the sign it finished.
	if moved, err := (GitHub{Gh: g.run, User: "me"}).RoundFacts(widgetsPR, Round{Head: "h1", StartedAt: start}); err != nil || moved.RoundReviewURL != "r3" {
		t.Errorf("summary on a newer head: %q %v", moved.RoundReviewURL, err)
	}
}

func TestGitHubMergedIsNotOpen(t *testing.T) {
	g := &routeGh{routes: map[string]string{
		"repos/acme/widgets/pulls/42": `{"state":"closed","merged":true,"head":{"sha":"h2"},"base":{"ref":"main"}}`,
	}}
	f, err := GitHub{Gh: g.run, User: "me"}.RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: t0})
	if err != nil || f.State != prClosed {
		t.Errorf("%+v %v", f, err)
	}
}

func TestGitHubDismiss(t *testing.T) {
	g := &routeGh{routes: map[string]string{"dismissals": `{}`}}
	if err := (GitHub{Gh: g.run, User: "me"}).Dismiss(widgetsPR, reviewID(9), "why"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(g.calls[0], " ")
	if !strings.Contains(joined, "-X PUT repos/acme/widgets/pulls/42/reviews/9/dismissals") || !strings.Contains(joined, "message=why\n\n--claude") {
		t.Errorf("dismiss call: %q", joined)
	}
}

// reviewID is a review id as GitHub's JSON carries it.
func reviewID(n int64) ids.ReviewID {
	var id ids.ReviewID
	if err := json.Unmarshal([]byte(strconv.FormatInt(n, 10)), &id); err != nil {
		panic(err)
	}
	return id
}

// A question clears on the user's reply in a thread the round posted in,
// after it settled; not on a post elsewhere, a signed post, or one before.
func TestRepliedInRoundThread(t *testing.T) {
	started := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	settled := started.Add(time.Hour)
	c := func(id, to int64, login, body string, at time.Time) ghItem[ids.CommentID] {
		var it ghItem[ids.CommentID]
		if err := it.ID.UnmarshalJSON([]byte(strconv.FormatInt(id, 10))); err != nil {
			t.Fatal(err)
		}
		if to != 0 {
			if err := it.InReplyTo.UnmarshalJSON([]byte(strconv.FormatInt(to, 10))); err != nil {
				t.Fatal(err)
			}
		}
		it.Body, it.CreatedAt = body, at
		it.User.Login = login
		return it
	}
	roundRoot := c(1, 0, "me", "finding\n\n--claude", started.Add(time.Minute))
	oldRoot := c(5, 0, "me", "old finding\n\n--claude", started.Add(-time.Hour))
	authorRoot := c(7, 0, "author", "why?", started.Add(-time.Hour))
	roundReply := c(8, 7, "me", "because\n\n--claude", started.Add(time.Minute))
	later := settled.Add(time.Minute)
	cases := []struct {
		name string
		list []ghItem[ids.CommentID]
		want bool
	}{
		{"reply under the round's comment", []ghItem[ids.CommentID]{roundRoot, c(2, 1, "Me", "yes, run it", later)}, true},
		{"reply in a thread the round replied in", []ghItem[ids.CommentID]{authorRoot, roundReply, c(9, 7, "me", "ok", later)}, true},
		{"reply before the round settled", []ghItem[ids.CommentID]{roundRoot, c(2, 1, "me", "yes", settled.Add(-time.Minute))}, false},
		{"a signed reply is a round's", []ghItem[ids.CommentID]{roundRoot, c(2, 1, "me", "x\n\n--claude", later)}, false},
		{"someone else's reply", []ghItem[ids.CommentID]{roundRoot, c(2, 1, "author", "yes", later)}, false},
		{"a new thread of the user's", []ghItem[ids.CommentID]{roundRoot, c(3, 0, "me", "unrelated", later)}, false},
		{"an earlier round's thread", []ghItem[ids.CommentID]{oldRoot, c(6, 5, "me", "yes", later)}, false},
	}
	for _, k := range cases {
		if got := repliedInRoundThread("me", k.list, started, settled); got != k.want {
			t.Errorf("%s: got %v, want %v", k.name, got, k.want)
		}
	}
}

// A failed read of the unresolved threads or of the draft review fails the
// facts: a zero count would read as "nothing left to do".
func TestGitHubRoundFactsFailsOnAFailedGraphQLRead(t *testing.T) {
	routes := func(drop string) map[string]string {
		m := map[string]string{
			"repos/acme/widgets/pulls/42":           `{"state":"open","user":{"login":"bob"},"head":{"sha":"h2"},"base":{"ref":"main"}}`,
			"repos/acme/widgets/pulls/42/reviews":   `[]`,
			"repos/acme/widgets/pulls/42/comments":  `[]`,
			"repos/acme/widgets/issues/42/comments": `[]`,
			"reviewThreads":                         `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`,
			"states: PENDING":                       `{"data":{"repository":{"pullRequest":{"id":"P","reviews":{"nodes":[]}}}}}`,
		}
		delete(m, drop)
		return m
	}
	for _, drop := range []string{"reviewThreads", "states: PENDING"} {
		g := &routeGh{routes: routes(drop)}
		if _, err := (GitHub{Gh: g.run, User: "me"}).RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: t0}); err == nil {
			t.Errorf("without %q: no error", drop)
		}
	}
	g := &routeGh{routes: routes("")}
	if _, err := (GitHub{Gh: g.run, User: "me"}).RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: t0}); err != nil {
		t.Errorf("every read answered: %v", err)
	}
}

// Unsent drafts count the signed comments in the account's draft review,
// the round's own; a comment the user wrote there by hand is not one.
func TestGitHubRoundFactsCountsSignedDrafts(t *testing.T) {
	g := &routeGh{routes: map[string]string{
		"repos/acme/widgets/pulls/42":           `{"state":"open","user":{"login":"bob"},"head":{"sha":"h2"},"base":{"ref":"main"}}`,
		"repos/acme/widgets/pulls/42/reviews":   `[]`,
		"repos/acme/widgets/pulls/42/comments":  `[]`,
		"repos/acme/widgets/issues/42/comments": `[]`,
		"reviewThreads":                         `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`,
		"states: PENDING": `{"data":{"repository":{"pullRequest":{"id":"P","reviews":{"nodes":[{"id":"R","comments":{"nodes":[` +
			`{"body":"nit\n\n` + signature + `"},{"body":"mine"},{"body":"also\n` + signature + `"}]}}]}}}}}`,
	}}
	f, err := GitHub{Gh: g.run, User: "me"}.RoundFacts(widgetsPR, Round{Head: "h2", StartedAt: t0})
	if err != nil || f.UnsentDrafts != 2 {
		t.Errorf("unsent drafts %d, want 2 (%v)", f.UnsentDrafts, err)
	}
}

// A failed read of the review comments fails the check rather than reading
// as "no reply".
func TestRepliedInRoundThreadFailsOnAFailedRead(t *testing.T) {
	g := &routeGh{routes: map[string]string{}}
	if _, err := (GitHub{Gh: g.run, User: "me"}).RepliedInRoundThread(widgetsPR, t0, t0.Add(time.Hour)); err == nil {
		t.Error("no error")
	}
}

// The user's standing review and the round's own review are the latest by
// time (the higher id on a tie), however GitHub orders the list.
func TestPropReviewsArePickedByTimeNotListOrder(t *testing.T) {
	start := t0
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 6).Draw(rt, "reviews")
		list := make([]ghItem[ids.ReviewID], n)
		for i := range list {
			if err := list[i].ID.UnmarshalJSON([]byte(strconv.Itoa(i + 1))); err != nil {
				rt.Fatal(err)
			}
			list[i].User.Login = rapid.SampledFrom([]string{"me", "bob"}).Draw(rt, "login")
			list[i].State = rapid.SampledFrom([]string{reviewApproved, "CHANGES_REQUESTED", "DISMISSED", "COMMENTED"}).Draw(rt, "state")
			list[i].SubmittedAt = start.Add(time.Duration(rapid.IntRange(-2, 2).Draw(rt, "minute")) * time.Minute)
			list[i].HTMLURL = "r" + strconv.Itoa(i+1)
		}
		// The spec, independent of the code: sort by (time, id), take the last kept.
		sorted := slices.Clone(list)
		slices.SortFunc(sorted, func(a, b ghItem[ids.ReviewID]) int {
			return cmp.Or(a.SubmittedAt.Compare(b.SubmittedAt), a.ID.Compare(b.ID))
		})
		var standing *ids.ReviewID
		roundReview := ""
		for _, rv := range sorted {
			if rv.User.Login == "me" && rv.State != "COMMENTED" {
				id := rv.ID
				standing = &id
			}
			if rv.User.Login == "me" && rv.SubmittedAt.After(start) {
				roundReview = rv.HTMLURL
			}
		}
		shuffled := slices.Clone(list)
		for i := len(shuffled) - 1; i > 0; i-- {
			j := rapid.IntRange(0, i).Draw(rt, "swap")
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}
		g := GitHub{Gh: (&routeGh{routes: map[string]string{"repos/acme/widgets/pulls/42/comments": `[]`}}).run, User: "me"}
		got := g.userReview(shuffled)
		if (got == nil) != (standing == nil) || got != nil && got.ID != *standing {
			rt.Fatalf("standing review %+v, want %v", got, standing)
		}
		review, _, _, err := g.roundActivity(widgetsPR, Round{StartedAt: start}, shuffled)
		if err != nil || review != roundReview {
			rt.Fatalf("round review %q (%v), want %q", review, err, roundReview)
		}
	})
}
