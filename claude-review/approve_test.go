package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

var (
	headSha  = strings.Repeat("a", 40)
	olderSha = strings.Repeat("b", 40)
)

func approveRun(t *testing.T, g *fakeGh, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runApprove(args, g.run, &out, &errb)
	return code, out.String(), errb.String()
}

func TestApprove_AtHeadApprovesThatCommit(t *testing.T) {
	g := &fakeGh{outs: []string{headSha + "\n", urlU}}
	code, out, errb := approveRun(t, g, prURL, atFlag, headSha)
	if code != 0 || out != "u\n" || len(g.calls) != 2 {
		t.Fatalf("code %d out %q stderr %q calls %d", code, out, errb, len(g.calls))
	}
	sameArgv(t, "approve", g.calls[1], []string{api, "-X", httpPost, reviewsURL, "-f", "event=APPROVE", "-f", "commit_id=" + headSha})
}

func TestApprove_RefusesAfterAPush(t *testing.T) {
	g := &fakeGh{outs: []string{headSha}}
	code, out, errb := approveRun(t, g, prURL, atFlag, olderSha)
	if code != 1 || out != "" || len(g.calls) != 1 || !strings.Contains(errb, "covered bbbbbbbbb, but the head is now aaaaaaaaa") {
		t.Errorf("code %d out %q calls %d stderr %q", code, out, len(g.calls), errb)
	}
}

func TestApprove_Usage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{prURL},
		{prURL, atFlag, "abc1234"}, // short sha: must name one commit exactly
		{prURL, atFlag, strings.ToUpper(headSha)},
		{prURL, atFlag, headSha, "extra"},
		{badTarget, atFlag, headSha},
		{prURL, bogusFlag},
	} {
		g := &fakeGh{}
		if code, _, errb := approveRun(t, g, args...); code != 2 || len(g.calls) != 0 || !strings.Contains(errb, "usage: claude-review approve") {
			t.Errorf("%q: code %d calls %d stderr %q", args, code, len(g.calls), errb)
		}
	}
}

func TestApprove_GhFailures(t *testing.T) {
	boom := errors.New("exit status 1")
	for name, c := range map[string]struct {
		g    *fakeGh
		want string
	}{
		"head":       {&fakeGh{outs: []string{notFound}, errs: []error{boom}}, "reading PR head"},
		"approve":    {&fakeGh{outs: []string{headSha, "Unprocessable"}, errs: []error{nil, boom}}, "Unprocessable"},
		"empty head": {&fakeGh{outs: []string{"  \n"}}, "empty sha"},
	} {
		if code, _, errb := approveRun(t, c.g, prURL, atFlag, headSha); code != 1 || !strings.Contains(errb, c.want) {
			t.Errorf("%s: code %d stderr %q", name, code, errb)
		}
	}
}
