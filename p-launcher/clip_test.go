package main

import "testing"

func TestParseGitHubURL(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		kind     string
		n        int
		url, sho string
	}{
		{"https://github.com/o/r/issues/12", true, "github_issue", 12, "https://github.com/o/r/issues/12", "o/r#12"},
		{"  https://github.com/o/r/pull/7#issuecomment-99\n", true, "github_pr", 7, "https://github.com/o/r/pull/7", "o/r#7"},
		{"https://github.com/o/r/issues/12?notification_referrer_id=x", true, "github_issue", 12, "https://github.com/o/r/issues/12", "o/r#12"},
		{"see https://github.com/matchi-app/nginx-ingress/issues/3 please", true, "github_issue", 3, "https://github.com/matchi-app/nginx-ingress/issues/3", "matchi-app/nginx-ingress#3"},
		{"https://github.com/o/r", false, "", 0, "", ""},
		{"https://github.com/o/r/issues/", false, "", 0, "", ""},
		{"https://gitlab.com/o/r/issues/1", false, "", 0, "", ""},
		{"random text", false, "", 0, "", ""},
		{"", false, "", 0, "", ""},
	}
	for _, c := range cases {
		r, ok := parseGitHubURL(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v", c.in, ok)
			continue
		}
		if ok && (r.Kind != c.kind || r.Number != c.n || r.URL != c.url || r.Short() != c.sho) {
			t.Errorf("%q: %+v", c.in, r)
		}
	}
}

func TestSlugAndIssueName(t *testing.T) {
	cases := map[string]string{
		"Court booking: block double-submit!":                            "court-booking-block-double-submit",
		"  Ünïcode & symbols ---  ":                                      "n-code-symbols",
		"a very long title that keeps going and going and going forever": "a-very-long-title-that-keeps-going-and-g",
		"":    "",
		"---": "",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q want %q", in, got, want)
		}
	}
	if got := issueProjectName(12, "Fix the thing", "Repo"); got != "12-fix-the-thing" {
		t.Fatalf("%q", got)
	}
	if got := issueProjectName(12, "", "Repo"); got != "repo-12" {
		t.Fatalf("%q", got)
	}
	if got := slug("a very long title that keeps going and going and going forever"); len(got) > 40 {
		t.Fatalf("slug too long: %d", len(got))
	}
}
