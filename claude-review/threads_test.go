package main

import (
	"bytes"
	"errors"
	"testing"
)

func threads(t *testing.T, g *fakeGh, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runThreads(args, g.run, &out, &errb)
	return code, out.String(), errb.String()
}

func TestThreads_PrintsEverythingWithIDs(t *testing.T) {
	g := &fakeGh{outs: []string{
		`[{"id":1,"body":"summary\n---\nmore\n","user":{"login":"ada"}}]`,
		`[{"id":2,"body":"","state":"APPROVED","user":{"login":"bo"}},{"id":3,"body":"see inline","state":"COMMENTED","user":{"login":"bo"}}]`,
		`[{"id":4,"body":"nit","path":"a.go","line":7,"user":{"login":"cy"}}]` + "\n" +
			`[{"id":5,"body":"done","path":"a.go","line":7,"in_reply_to_id":4,"user":{"login":"ada"}},{"id":6,"body":"old","path":"b.go","original_line":233,"user":{"login":"cy"}}]`,
	}}
	code, out, errb := threads(t, g, prURL)
	if code != 0 || errb != "" {
		t.Fatalf("code %d stderr %q", code, errb)
	}
	want := "--- comment 1 by ada\n    summary\n    ---\n    more\n\n" +
		"--- review 3 by bo COMMENTED\n    see inline\n\n" +
		"--- inline 4 by cy on a.go:7\n    nit\n\n" +
		"--- inline 5 by ada on a.go:7, reply to 4\n    done\n\n" +
		"--- inline 6 by cy on b.go, outdated, was line 233\n    old\n\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
	sameArgv(t, "issue comments", g.calls[0], []string{api, paginate, issuesURL})
	sameArgv(t, "reviews", g.calls[1], []string{api, paginate, reviewsURL})
	sameArgv(t, "inline", g.calls[2], []string{api, paginate, commentsURL})
}

func TestThreads_Failures(t *testing.T) {
	if code, _, _ := threads(t, &fakeGh{}); code != 2 {
		t.Errorf("no args: %d", code)
	}
	if code, _, _ := threads(t, &fakeGh{}, prURL, "extra"); code != 2 {
		t.Errorf("two args: %d", code)
	}
	if code, _, _ := threads(t, &fakeGh{}, "nope"); code != 2 {
		t.Errorf("bad target: %d", code)
	}
	g := &fakeGh{outs: []string{"[]", "boom"}, errs: []error{nil, errors.New("exit status 1")}}
	if code, _, errb := threads(t, g, prURL); code != 1 || len(g.calls) != 2 || errb == "" {
		t.Errorf("gh failure: code %d calls %d stderr %q", code, len(g.calls), errb)
	}
}
