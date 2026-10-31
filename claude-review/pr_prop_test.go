package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var nameGen = rapid.StringMatching(`[A-Za-z0-9._-]{1,20}`).Filter(func(s string) bool { return !allDots(s) })

func TestPropEveryFormParsesToTheSamePR(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		owner, repo := nameGen.Draw(t, "owner"), nameGen.Draw(t, "repo")
		n := rapid.IntRange(1, 1_000_000).Draw(t, "n")
		want := PR{Owner: strings.ToLower(owner), Repo: strings.ToLower(repo), Number: n}
		forms := []string{
			PR{Owner: owner, Repo: repo, Number: n}.URL(),
			"claude-review://" + owner + "/" + repo + "/" + itoa(n),
			owner + "/" + repo + "/" + itoa(n),
		}
		for _, f := range forms {
			got, err := ParseTarget(f)
			if err != nil || got != want {
				t.Fatalf("%q parsed to %+v, %v; want %+v", f, got, err, want)
			}
			if got.SessionID() != want.SessionID() {
				t.Fatalf("%q: session id differs from the lower-cased PR's", f)
			}
		}
	})
}

var uuidV5Shape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestPropSessionIdsAreVersion5AndDistinct(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := PR{Owner: nameGen.Draw(t, "o"), Repo: nameGen.Draw(t, "r"), Number: rapid.IntRange(1, 1000).Draw(t, "n")}
		b := a
		b.Number++
		if !uuidV5Shape.MatchString(a.SessionID()) {
			t.Fatalf("not a v5 uuid: %s", a.SessionID())
		}
		if a.SessionID() == b.SessionID() {
			t.Fatalf("%v and %v share a session id", a, b)
		}
	})
}

func TestPropNothingParsedEverClimbsOrSplits(t *testing.T) {
	safe := regexp.MustCompile(`^[a-z0-9._-]+$`)
	rapid.Check(t, func(t *rapid.T) {
		// Built from parts, so names of only dots and the separators the
		// patterns care about come up often, not once in a million strings.
		seg := rapid.OneOf(rapid.SampledFrom([]string{".", "..", "...", "", "a/b", "%2e%2e"}), rapid.StringMatching(`[A-Za-z0-9./_%-]{0,12}`))
		s := rapid.OneOf(
			rapid.String(),
			rapid.Custom(func(t *rapid.T) string {
				prefix := rapid.SampledFrom([]string{"", "https://github.com/", "claude-review://"}).Draw(t, "prefix")
				mid := rapid.SampledFrom([]string{"/", "/pull/"}).Draw(t, "mid")
				return prefix + seg.Draw(t, "owner") + "/" + seg.Draw(t, "repo") + mid + rapid.StringMatching(`-?[0-9]{0,6}`).Draw(t, "n")
			}),
		).Draw(t, "input")
		pr, err := ParseTarget(s)
		if err != nil {
			return
		}
		if !safe.MatchString(pr.Owner) || !safe.MatchString(pr.Repo) || allDots(pr.Owner) || allDots(pr.Repo) || pr.Number < 1 {
			t.Fatalf("%q parsed to unsafe %+v", s, pr)
		}
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
