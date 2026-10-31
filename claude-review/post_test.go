package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeGh struct {
	calls [][]string
	outs  []string
	errs  []error
}

func (g *fakeGh) run(args ...string) (string, error) {
	g.calls = append(g.calls, args)
	i := len(g.calls) - 1
	var out string
	var err error
	if i < len(g.outs) {
		out = g.outs[i]
	}
	if i < len(g.errs) {
		err = g.errs[i]
	}
	return out, err
}

func post(t *testing.T, g *fakeGh, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runPost(args, g.run, &out, &errb)
	return code, out.String(), errb.String()
}

const (
	prURL        = "https://github.com/acme/widgets/pull/42"
	api          = "api"
	httpPost     = "POST"
	issuesURL    = "repos/acme/widgets/issues/42/comments"
	reviewsURL   = "repos/acme/widgets/pulls/42/reviews"
	commentsURL  = "repos/acme/widgets/pulls/42/comments"
	threadsAllow = "Bash(claude-review threads https://github.com/acme/widgets/pull/42)"
	bodyFlag     = "--body"
	pathFlag     = "--path"
	lineFlag     = "--line"
	signedHello  = "hello\n\n--claude"
	bogusFlag    = "--bogus"
	atFlag       = "--at"
	badTarget    = "nope"
	notFound     = "gh: Not Found"
	urlU         = `{"html_url":"u"}`
)

func TestPost_Comment(t *testing.T) {
	g := &fakeGh{outs: []string{"[]", `{"id":1,"html_url":"https://github.com/acme/widgets/pull/42#issuecomment-9"}`}}
	code, out, errb := post(t, g, prURL, kindComment, bodyFlag, "Looks wrong.")
	if code != 0 || errb != "" {
		t.Fatalf("code %d stderr %q", code, errb)
	}
	sameArgv(t, "duplicate check", g.calls[0], []string{api, paginate, issuesURL})
	want := []string{api, "-X", httpPost, issuesURL, "-f", "body=Looks wrong.\n\n--claude"}
	sameArgv(t, "gh", g.calls[1], want)
	if out != "https://github.com/acme/widgets/pull/42#issuecomment-9\n" {
		t.Errorf("stdout %q", out)
	}
}

func TestPost_CommentAcceptsBareTarget(t *testing.T) {
	g := &fakeGh{outs: []string{"[]", `{}`}}
	code, out, _ := post(t, g, "acme/widgets/42", kindComment, bodyFlag, "x")
	if code != 0 || g.calls[1][3] != issuesURL || out != "{}\n" {
		t.Errorf("code %d calls %q out %q", code, g.calls, out)
	}
}

func TestPost_ReviewIsAlwaysAComment(t *testing.T) {
	g := &fakeGh{outs: []string{"[]", urlU}}
	code, _, _ := post(t, g, prURL, kindReview, bodyFlag, "Ship it.")
	if code != 0 {
		t.Fatal(code)
	}
	sameArgv(t, "duplicate check", g.calls[0], []string{api, paginate, reviewsURL})
	sameArgv(t, "gh", g.calls[1], []string{api, "-X", httpPost, reviewsURL, "-f", "body=Ship it.\n\n--claude", "-f", "event=COMMENT"})
	// No way to approve or request changes through the pre-approved command.
	if code, _, errb := post(t, &fakeGh{}, prURL, kindReview, bodyFlag, "x", "--event", "APPROVE"); code != 2 || !strings.Contains(errb, "flag provided but not defined") {
		t.Errorf("--event must not exist: code %d stderr %q", code, errb)
	}
}

// The line was picked in the reviewed commit, so the comment anchors there,
// not on whatever the head is by the time it posts.
func TestPost_InlineAnchorsOnTheReviewedCommit(t *testing.T) {
	g := &fakeGh{outs: []string{"[]", urlU}}
	code, out, errb := post(t, g, prURL, kindInline, bodyFlag, "Off by one.", pathFlag, "pkg/a.go", lineFlag, "12", atFlag, olderSha)
	if code != 0 || len(g.calls) != 2 {
		t.Fatalf("code %d calls %d stderr %q", code, len(g.calls), errb)
	}
	sameArgv(t, "duplicate check", g.calls[0], []string{api, paginate, commentsURL})
	sameArgv(t, "post", g.calls[1], []string{api, "-X", httpPost, commentsURL,
		"-f", "body=Off by one.\n\n--claude", "-f", "commit_id=" + olderSha, "-f", "path=pkg/a.go", "-F", "line=12", "-f", "side=RIGHT"})
	if out != "u\n" {
		t.Errorf("stdout %q", out)
	}
}

func TestPost_InlineValidation(t *testing.T) {
	for _, args := range [][]string{
		{prURL, kindInline, bodyFlag, "x", lineFlag, "3", atFlag, headSha},                                  // no path
		{prURL, kindInline, bodyFlag, "x", pathFlag, "a", atFlag, headSha},                                  // no line
		{prURL, kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "0", atFlag, headSha},                   // bad line
		{prURL, kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", "--side", "BOTH", atFlag, headSha}, // bad side
		{prURL, kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3"},                                    // no --at
		{prURL, kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, "abc1234"},                 // short sha
	} {
		g := &fakeGh{}
		if code, _, _ := post(t, g, args...); code != 2 || len(g.calls) != 0 {
			t.Errorf("%q: code %d calls %q", args, code, g.calls)
		}
	}
}

func TestPost_GhFailureIsReported(t *testing.T) {
	g := &fakeGh{outs: []string{"[]", `{"message":"Validation Failed"}`}, errs: []error{nil, errors.New("exit status 1")}}
	code, out, errb := post(t, g, prURL, kindComment, bodyFlag, "x")
	if code != 1 || out != "" || !strings.Contains(errb, "exit status 1") || !strings.Contains(errb, "Validation Failed") {
		t.Errorf("code %d out %q stderr %q", code, out, errb)
	}
}

func TestPost_Usage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{prURL},
		{badTarget, kindComment, bodyFlag, "x"},
		{prURL, kindComment},
		{prURL, kindComment, bodyFlag, "   "},
		{prURL, kindComment, bodyFlag, "x", "extra"},
		{prURL, kindComment, bogusFlag},
		{prURL, "emoji", bodyFlag, "x"},
	} {
		g := &fakeGh{}
		code, _, errb := post(t, g, args...)
		if code != 2 || len(g.calls) != 0 {
			t.Errorf("%q: code %d calls %q", args, code, g.calls)
		}
		if !strings.Contains(errb, "usage: claude-review post") && !strings.Contains(errb, "claude-review:") {
			t.Errorf("%q: stderr %q", args, errb)
		}
	}
}

func TestPost_TooFewArgsVersusMissingBody(t *testing.T) {
	_, _, errb := post(t, &fakeGh{}, prURL)
	if !strings.Contains(errb, "need <pr> and a kind") {
		t.Errorf("one arg: %q", errb)
	}
	_, _, errb = post(t, &fakeGh{}, prURL, kindComment)
	if strings.Contains(errb, "need <pr> and a kind") || !strings.Contains(errb, "usage: claude-review post") {
		t.Errorf("two args reach the flag parser, not the count guard: %q", errb)
	}
}

func TestSign(t *testing.T) {
	cases := map[string]string{
		"hello":                 signedHello,
		"hello\n":               signedHello,
		signedHello:             signedHello,
		"hello\n--claude\n\n":   "hello\n--claude",
		"--claude":              "--claude",
		"mentions --claude mid": "mentions --claude mid\n\n--claude",
	}
	for in, want := range cases {
		if got := sign(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestHtmlURL(t *testing.T) {
	if got := htmlURL(`{"html_url":"x"}`); got != "x" {
		t.Errorf("got %q", got)
	}
	if got := htmlURL(`{"id":1}` + "\n"); got != `{"id":1}` {
		t.Errorf("got %q", got)
	}
	if got := htmlURL("not json\n"); got != "not json" {
		t.Errorf("got %q", got)
	}
}

func TestPostRule(t *testing.T) {
	if got := postRule(widgetsPR); got != "Bash(claude-review post https://github.com/acme/widgets/pull/42 *)" {
		t.Errorf("got %q", got)
	}
}

func TestPost_Reply(t *testing.T) {
	g := &fakeGh{outs: []string{"[]", urlU}}
	code, out, errb := post(t, g, prURL, kindReply, bodyFlag, "Conceded.", toFlag, "77")
	if code != 0 || out != "u\n" {
		t.Fatalf("code %d out %q stderr %q", code, out, errb)
	}
	sameArgv(t, "duplicate check", g.calls[0], []string{api, paginate, commentsURL})
	sameArgv(t, "post", g.calls[1], []string{api, "-X", httpPost, "repos/acme/widgets/pulls/42/comments/77/replies", "-f", "body=Conceded.\n\n--claude"})
	for _, to := range []string{"0", "-1"} {
		g := &fakeGh{}
		if code, _, errb := post(t, g, prURL, kindReply, bodyFlag, "x", toFlag, to); code != 2 || len(g.calls) != 0 || !strings.Contains(errb, "reply needs --to") {
			t.Errorf("--to %s: code %d calls %q stderr %q", to, code, g.calls, errb)
		}
	}
}

// The duplicate on the second round: a 422 that had stored the comment,
// then a retry. The retry must find the stored copy and post nothing.
func TestPost_AlreadyOnThePRIsNotPostedAgain(t *testing.T) {
	inline := `{"id":5,"body":"x\n\n--claude","path":"a","original_line":3,"original_commit_id":"` + headSha + `","side":"RIGHT","html_url":"found"}`
	recent := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	reply := `{"id":6,"body":"x\n\n--claude","path":"a","line":3,"in_reply_to_id":77,"html_url":"found-reply"}`
	cases := []struct {
		name, list, want string
		args             []string
	}{
		{"inline", `[` + inline + `]`, "found", []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		{"inline, second page", `[]` + "\n" + `[` + inline + `]`, "found", []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		{"reply", `[` + inline + `,` + reply + `]`, "found-reply", []string{kindReply, bodyFlag, "x", toFlag, "77"}},
		// GitHub files a reply to a reply under the thread's root, so --to
		// with a later comment's id must still find the stored copy.
		{"reply to a reply", `[{"id":78,"in_reply_to_id":77},` + reply + `]`, "found-reply", []string{kindReply, bodyFlag, "x", toFlag, "78"}},
		{"comment", `[{"body":"x\n\n--claude","created_at":"` + recent + `","html_url":"found-c"}]`, "found-c", []string{kindComment, bodyFlag, "x"}},
		{"review", `[{"body":"x\n\n--claude","submitted_at":"` + recent + `","html_url":"found-r"}]`, "found-r", []string{kindReview, bodyFlag, "x"}},
	}
	for _, c := range cases {
		g := &fakeGh{outs: []string{c.list}}
		code, out, errb := post(t, g, append([]string{prURL}, c.args...)...)
		if code != 0 || out != c.want+"\n" || len(g.calls) != 1 || !strings.Contains(errb, "already on the PR") {
			t.Errorf("%s: code %d out %q calls %d stderr %q", c.name, code, out, len(g.calls), errb)
		}
	}
}

// Same text elsewhere is not a duplicate: each mismatch must reach the POST.
// Every item carries a URL, so a match would print it instead of posting.
func TestPost_SameTextElsewhereIsPosted(t *testing.T) {
	old := time.Now().UTC().Add(-retryWindow - time.Minute).Format(time.RFC3339)
	cases := []struct {
		name, list string
		args       []string
	}{
		{"other body", `[{"body":"y\n\n--claude","path":"a","line":3,"html_url":"dup"}]`, []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		{"other path", `[{"body":"x\n\n--claude","path":"b","original_line":3,"original_commit_id":"` + headSha + `","side":"RIGHT","html_url":"dup"}]`, []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		{"other line", `[{"body":"x\n\n--claude","path":"a","original_line":4,"original_commit_id":"` + headSha + `","side":"RIGHT","html_url":"dup"}]`, []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		{"a reply is not a new thread", `[{"body":"x\n\n--claude","path":"a","original_line":3,"original_commit_id":"` + headSha + `","side":"RIGHT","in_reply_to_id":9,"html_url":"dup"}]`, []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		{"reply in another thread", `[{"body":"x\n\n--claude","in_reply_to_id":9,"html_url":"dup"}]`, []string{kindReply, bodyFlag, "x", toFlag, "77"}},
		{"unsigned copy", `[{"body":"x","html_url":"dup"}]`, []string{kindComment, bodyFlag, "x"}},
		// The same line number in another commit is other code, even where
		// GitHub has mapped it onto the same current line.
		{"other commit", `[{"body":"x\n\n--claude","path":"a","line":3,"original_line":3,"original_commit_id":"` + olderSha + `","side":"RIGHT","html_url":"dup"}]`, []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", atFlag, headSha}},
		// The same line number on the old side is another place.
		{"other side", `[{"body":"x\n\n--claude","path":"a","original_line":3,"original_commit_id":"` + headSha + `","side":"RIGHT","html_url":"dup"}]`, []string{kindInline, bodyFlag, "x", pathFlag, "a", lineFlag, "3", "--side", "LEFT", atFlag, headSha}},
		// A later round may say the same thing again; only a retry is a copy.
		{"comment from an earlier round", `[{"body":"x\n\n--claude","created_at":"` + old + `","html_url":"dup"}]`, []string{kindComment, bodyFlag, "x"}},
		{"review from an earlier round", `[{"body":"x\n\n--claude","submitted_at":"` + old + `","html_url":"dup"}]`, []string{kindReview, bodyFlag, "x"}},
	}
	for _, c := range cases {
		g := &fakeGh{outs: []string{c.list, `{"html_url":"new"}`}}
		code, out, errb := post(t, g, append([]string{prURL}, c.args...)...)
		if code != 0 || out != "new\n" || strings.Contains(errb, "already") {
			t.Errorf("%s: code %d out %q stderr %q", c.name, code, out, errb)
		}
	}
}

func TestPost_DuplicateCheckFailureStopsThePost(t *testing.T) {
	for name, g := range map[string]*fakeGh{
		"gh fails": {outs: []string{notFound}, errs: []error{errors.New("exit status 1")}},
		"bad json": {outs: []string{"[{"}},
	} {
		code, _, errb := post(t, g, prURL, kindComment, bodyFlag, "x")
		if code != 1 || len(g.calls) != 1 || !strings.Contains(errb, "checking for an earlier copy") {
			t.Errorf("%s: code %d calls %d stderr %q", name, code, len(g.calls), errb)
		}
	}
}

func TestThreadsRule(t *testing.T) {
	if got := threadsRule(widgetsPR); got != threadsAllow {
		t.Errorf("got %q", got)
	}
}
