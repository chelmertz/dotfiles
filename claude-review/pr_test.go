package main

import (
	"strings"
	"testing"
)

func TestParseTarget_AcceptsEveryInputShape(t *testing.T) {
	want := PR{Owner: owner, Repo: repoName, Number: 1234}
	for _, in := range []string{
		httpsURL + "/pull/1234",
		httpsURL + "/pull/1234/files",
		httpsURL + "/pull/1234#issuecomment-1",
		"claude-review://acme/widgets/1234",
		"claude-review://acme/widgets/1234/",
		"acme/widgets/1234",
		"https://github.com/Acme/Widgets/pull/1234",
	} {
		got, err := ParseTarget(in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if got != want {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
	}
}

func TestParseTarget_RejectsAnythingElse(t *testing.T) {
	for _, in := range []string{
		"",
		httpsURL,
		httpsURL + "/issues/12",
		"https://gitlab.com/acme/widgets/pull/12",
		"claude-review://acme/widgets/abc",
		slug,
		"acme/web app/12",
		"acme/widgets/0",
		"acme/widgets/-1",
		"../../etc/12",
		"../../12",
		"claude-review://../../1",
		"https://github.com/./widgets/pull/1",
		"acme/.../3",
		"claude-review://acme/widgets/12/extra",
	} {
		if _, err := ParseTarget(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestParseTarget_RejectsOverflowingNumber(t *testing.T) {
	in := slug + "/99999999999999999999999"
	_, err := ParseTarget(in)
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("got %v", err)
	}
}

func TestParseTarget_ErrorNamesTheInput(t *testing.T) {
	_, err := ParseTarget("nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should quote the input, got %v", err)
	}
}

func TestPR_URL(t *testing.T) {
	pr := PR{Owner: owner, Repo: repoName, Number: 7}
	if got := pr.URL(); got != httpsURL+"/pull/7" {
		t.Errorf("got %q", got)
	}
}

func TestPR_Slug(t *testing.T) {
	pr := PR{Owner: owner, Repo: repoName, Number: 7}
	if got := pr.Slug(); got != slug+"#7" {
		t.Errorf("got %q", got)
	}
}

func TestPR_SessionID_IsStableAndPerPR(t *testing.T) {
	a := PR{Owner: owner, Repo: repoName, Number: 7}
	b := PR{Owner: owner, Repo: repoName, Number: 8}
	again := PR{Owner: owner, Repo: repoName, Number: 7}
	if a.SessionID() != again.SessionID() {
		t.Fatal("session id must be deterministic")
	}
	if a.SessionID() == b.SessionID() {
		t.Fatal("different PRs must get different ids")
	}
	// Pinned so a change in the derivation (namespace, canonical URL) is a
	// visible, deliberate break: every existing review session would be lost.
	if got := a.SessionID(); got != "cfee23f0-9e8b-5191-927a-7b236f6d63f7" {
		t.Errorf("derivation changed: %s", got)
	}
}
