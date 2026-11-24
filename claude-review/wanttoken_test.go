package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// approveLink is the Approve link on pr's last row elly took, "" for none.
func approveLink(w *tickWorld, pr PR) string {
	for _, l := range w.elly.put[pr.URL()].Links {
		if l.Action == actionApprove {
			return l.URL
		}
	}
	return ""
}

// A mention holds the token back; once it clears, the next locked show
// mints one and the row carries it.
func TestAMentionClearingReMintsOnTheNextShow(t *testing.T) {
	w := newTickWorld(t)
	if err := w.h.ledger.SetMentionGate(freeLock(t, widgetsPR), widgetsPR, MentionGate{Open: []EllyMention{openMention}}); err != nil {
		t.Fatal(err)
	}
	s := PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}
	if err := w.h.move2(widgetsPR, s); err != nil {
		t.Fatal(err)
	}
	if live, _ := w.h.ledger.HasLiveToken(widgetsPR); live {
		t.Fatal("a token was minted beside an open mention")
	}
	if err := w.h.ledger.SetMentionGate(freeLock(t, widgetsPR), widgetsPR, MentionGate{}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.move2(widgetsPR, s); err != nil {
		t.Fatal(err)
	}
	tok, has, err := w.h.ledger.TokenFor(widgetsPR, sha)
	if err != nil || !has {
		t.Fatalf("no token after the mention cleared: %v", err)
	}
	if !strings.Contains(approveLink(w, widgetsPR), "token="+tok.String()) {
		t.Errorf("Approve link %q lacks the fresh token", approveLink(w, widgetsPR))
	}
}

// A click spends its token before it approves; when the approval fails,
// the row still offers Approve, and the next show mints a fresh token for
// it rather than leaving a link nothing accepts.
func TestASpentTokenWhoseApproveFailedIsReMintedOnTheNextShow(t *testing.T) {
	w := newTickWorld(t)
	s := PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}
	if err := w.h.move2(widgetsPR, s); err != nil {
		t.Fatal(err)
	}
	spent, has, err := w.h.ledger.TokenFor(widgetsPR, sha)
	if err != nil || !has {
		t.Fatalf("no token: %v", err)
	}
	if ok, err := w.h.ledger.UseToken(widgetsPR, sha, spent, winClosed); !ok || err != nil {
		t.Fatalf("the click could not spend its token: %v %v", ok, err)
	}
	if err := w.h.move2(widgetsPR, s); err != nil {
		t.Fatal(err)
	}
	fresh, has, err := w.h.ledger.TokenFor(widgetsPR, sha)
	if err != nil || !has || fresh == spent {
		t.Fatalf("no fresh token after the failed approve: %v %v", has, err)
	}
	if !strings.Contains(approveLink(w, widgetsPR), "token="+fresh.String()) {
		t.Errorf("Approve link %q lacks the fresh token", approveLink(w, widgetsPR))
	}
}

// While Claude Code's files cannot say whether a window is open on the
// PR, or say one is, the row offers no Approve and no token is minted: a
// fork may be reading prod data beside it.
func TestApproveHidesUnlessTheWindowReadsClosed(t *testing.T) {
	for name, set := range map[string]func(*fakeSessions){
		"unknown": func(f *fakeSessions) {
			f.windowErr = map[ids.SessionID]error{widgetsPR.SessionID(): errors.New("77.json: unexpected end of JSON input")}
		},
		"open": func(f *fakeSessions) { f.window, f.windowID = 4242, widgetsPR.SessionID() },
	} {
		t.Run(name, func(t *testing.T) {
			w := newTickWorld(t)
			set(w.fs)
			s := PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}
			if err := w.h.move2(widgetsPR, s); err != nil {
				t.Fatal(err)
			}
			if live, _ := w.h.ledger.HasLiveToken(widgetsPR); live {
				t.Error("a token was minted while the window is not closed")
			}
			if l := approveLink(w, widgetsPR); l != "" {
				t.Errorf("the row offers Approve: %q", l)
			}
		})
	}
}

// The spend checks the stored state in its own transaction: a token left
// unspent under a state that no longer offers Approve at that commit is
// refused.
func TestUseTokenRefusesWhenTheStoredStateNoLongerOffersApprove(t *testing.T) {
	approve := PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}
	for name, stored := range map[string]PRState{
		"a click moved it on": {State: stStarting, Kind: kindClick, Head: sha, Since: t0, Prev: &approve},
		"running":             {State: stRunning, Kind: kindClick, Head: sha, Since: t0},
		"another head":        {State: stAwaiting, Kind: kindApprove, Head: strings.Repeat("b", 40), Since: t0},
		"idle":                {State: stIdle, Head: sha, Since: t0},
	} {
		t.Run(name, func(t *testing.T) {
			l := openTestLedger(t)
			tok, err := l.MintToken(widgetsPR, sha)
			if err != nil {
				t.Fatal(err)
			}
			if err := l.SetPRState(widgetsPR, stored); err != nil {
				t.Fatal(err)
			}
			if ok, _ := l.UseToken(widgetsPR, sha, tok, winClosed); ok {
				t.Error("spent a token the stored state no longer offers")
			}
		})
	}
	t.Run("offered", func(t *testing.T) {
		l := openTestLedger(t)
		tok, err := l.MintToken(widgetsPR, sha)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.SetPRState(widgetsPR, approve); err != nil {
			t.Fatal(err)
		}
		if ok, err := l.UseToken(widgetsPR, sha, tok, winUnknown); ok {
			t.Errorf("spent under an unknown window: %v", err)
		}
		if ok, err := l.UseToken(widgetsPR, sha, tok, winClosed); !ok || err != nil {
			t.Errorf("refused an offered Approve: %v %v", ok, err)
		}
	})
}

// fakeApproveGh answers runApprove's read of the PR in whichever shape it asks
// for (the head alone, or the state and head), and records an approval.
func fakeApproveGh(state, head string, approved *bool) Gh {
	return func(args ...string) (string, error) {
		switch {
		case slices.Contains(args, "event=APPROVE"):
			*approved = true
			return `{"html_url":"approved"}`, nil
		case slices.Contains(args, ".head.sha"):
			return head, nil
		}
		return state + " " + head, nil
	}
}

// runApprove refuses a PR that is no longer open, whatever its head.
func TestApprove_RefusesAClosedPR(t *testing.T) {
	approved := false
	gh := fakeApproveGh("closed", headSha, &approved)
	var out, errb strings.Builder
	code := runApprove([]string{prURL, atFlag, headSha}, gh, &out, &errb)
	if code != 1 || out.String() != "" || approved || !strings.Contains(errb.String(), "is closed") {
		t.Errorf("code %d out %q approved %v stderr %q", code, out.String(), approved, errb.String())
	}
}

// The Approve link refuses while the PR's window is unknown: the verb
// reads the window itself under the PR's lock.
func TestApproveVerbRefusesUnderAnUnknownWindow(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetPRState(widgetsPR, PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}); err != nil {
		t.Fatal(err)
	}
	tok, err := l.MintToken(widgetsPR, sha)
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	fs := newFakeSessions()
	fs.windowErr = map[ids.SessionID]error{widgetsPR.SessionID(): errors.New("77.json: unexpected end of JSON input")}
	h.d.Sessions = fs.sessions()
	approved := false
	h.d.Gh = fakeApproveGh("open", sha, &approved)
	if code := run([]string{"claude-review://acme/widgets/42/approve?at=" + sha + "&token=" + tok.String()}, h.d); code == 0 || approved {
		t.Fatalf("exit %d, approved %v", code, approved)
	}
	if !strings.Contains(h.stderr.String(), "window") {
		t.Errorf("the refusal does not name the window: %s", h.stderr)
	}
}

// Commit mints on the same rule: a step into Approve mints only when the
// host read the window Closed; the zero window, Unknown, mints nothing.
func TestCommitMintsOnlyUnderAClosedWindow(t *testing.T) {
	to := PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}
	for _, c := range []struct {
		window WindowState
		want   bool
	}{{winUnknown, false}, {winOpen, false}, {winClosed, true}} {
		l := openTestLedger(t)
		p := plan{at: t0, origin: originTick, from: PRState{State: stIdle}, to: to, window: c.window}
		if err := l.Commit(freeLock(t, widgetsPR), widgetsPR, p); err != nil {
			t.Fatal(err)
		}
		if _, has, err := l.TokenFor(widgetsPR, sha); err != nil || has != c.want {
			t.Errorf("window %s: minted %v, want %v (%v)", c.window, has, c.want, err)
		}
	}
}
