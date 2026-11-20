package main

import "testing"

// One merged hold per PR per pass, the breaker winning over the daily cap:
// a tripped breaker stops every PR, so its words are the ones that tell
// the user what to do.
func TestHoldForMergesTheDailyCapAndTheBreaker(t *testing.T) {
	cases := []struct {
		name     string
		dailyCap bool
		breaker  string
		want     Hold
	}{
		{"neither", false, "", Hold{}},
		{"daily cap", true, "", Hold{kind: holdDailyCap}},
		{"breaker", false, breakerDuplicate + "x", Hold{kind: holdBreaker, cause: causeDuplicate}},
		{"both: the breaker wins", true, breakerCap + "7 rounds", Hold{kind: holdBreaker, cause: causeTotalCap}},
	}
	for _, c := range cases {
		if got := holdFor(c.dailyCap, c.breaker); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// The breaker's stored reason is typed by its prefix; a reason no prefix
// names (an older build's text) is Unknown and gets neutral words.
func TestCauseOfAStoredReason(t *testing.T) {
	for reason, want := range map[string]Cause{
		breakerCap + "12 rounds started in 24 hours, the limit across all PRs": causeTotalCap,
		"12 rounds started in 24 hours, the limit across all PRs":              causeTotalCap,
		breakerLoop + "12 rounds":                                              causeLoop,
		breakerDuplicate + "acme/widgets#4":                                    causeDuplicate,
		breakerSelfCheck + "2.1.330":                                           causeSelfCheck,
		"something an older build wrote":                                       causeUnknown,
	} {
		if got := causeOf(reason); got != want {
			t.Errorf("%q: cause %v, want %v", reason, got, want)
		}
	}
	if n := (Hold{kind: holdBreaker, cause: causeUnknown}).note(); n != "paused: the breaker tripped; claude-review starts no round until it is reset" {
		t.Errorf("unknown cause: %q", n)
	}
	if n := (Hold{kind: holdBreaker, cause: causeTotalCap}).note(); n != "paused: too many rounds started in 24 hours, the limit across all PRs; claude-review starts again once fewer than the limit started in the last 24 hours" {
		t.Errorf("total cap: %q", n)
	}
	if n := (Hold{kind: holdDailyCap}).note(); n != "paused: the daily review limit for this PR is reached" {
		t.Errorf("daily cap: %q", n)
	}
	if n := (Hold{}).note(); n != "" {
		t.Errorf("no hold: %q", n)
	}
}
