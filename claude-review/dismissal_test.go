package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

func TestDismissalRecordsLiveUntilSettled(t *testing.T) {
	l := openTestLedger(t)
	pr := widgetsPR
	if p, _ := l.DismissalPending(pr); p {
		t.Fatal("pending before any record")
	}
	if err := l.AddDismissal(pr, reviewID(7), t0); err != nil {
		t.Fatal(err)
	}
	if err := l.AddDismissal(pr, reviewID(7), t0); err != nil {
		t.Fatalf("a second write of the same record: %v", err)
	}
	if p, _ := l.DismissalPending(pr); !p {
		t.Error("not pending after the write")
	}
	if err := l.DeletePRState(pr); err != nil {
		t.Fatal(err)
	}
	all, err := l.PendingDismissals()
	if err != nil || len(all) != 1 || all[0].PR != pr.URL() || all[0].Review != reviewID(7) || all[0].Notified {
		t.Fatalf("a closed PR's record did not survive: %+v %v", all, err)
	}
	if err := l.MarkDismissalNotified(all[0]); err != nil {
		t.Fatal(err)
	}
	if all, _ = l.PendingDismissals(); !all[0].Notified {
		t.Error("the notified mark did not stick")
	}
	if err := l.SettleDismissal(all[0]); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.DismissalPending(pr); p {
		t.Error("still pending after settling")
	}
}

func TestGitHubDismissalStatus(t *testing.T) {
	g := &routeGh{routes: map[string]string{
		"pulls/42/reviews/7": `{"id":7,"state":"DISMISSED","user":{"login":"Me"}}`,
		"pulls/42":           `{"state":"closed","merged":true,"base":{"ref":"main","repo":{"archived":true}}}`,
	}}
	s, err := (GitHub{Gh: g.run, User: "me"}).DismissalStatus(widgetsPR, reviewID(7))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Dismissed || !s.Merged || !s.Archived {
		t.Errorf("status %+v", s)
	}
	g.routes["pulls/42/reviews/7"] = `{"id":7,"state":"DISMISSED","user":{"login":"someone"}}`
	if s, _ := (GitHub{Gh: g.run, User: "me"}).DismissalStatus(widgetsPR, reviewID(7)); s.Dismissed {
		t.Error("a review by someone else read as the user's dismissal")
	}
}

// approvalRound leaves a finished round with new findings on a PR the user
// approved at an older head: decide asks for a dismissal.
func approvalRound(t *testing.T, w *tickWorld) PR {
	t.Helper()
	pr := w.accept(t, 42)
	if _, err := w.h.ledger.StartRound(pr, "h1", "new_head", t0); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: "h1", NewThreads: 2, UserReview: &Review{ID: reviewID(7), State: "APPROVED", CommitID: "h0"}}
	return pr
}

func pending(t *testing.T, w *tickWorld) []Dismissal {
	t.Helper()
	all, err := w.h.ledger.PendingDismissals()
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestTickRecordsADismissalBeforeSendingIt(t *testing.T) {
	w := newTickWorld(t)
	pr := approvalRound(t, w)
	if _, err := w.h.ledger.MintToken(pr, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.gh.dismissed, []ids.ReviewID{reviewID(7)}) {
		t.Errorf("dismissed %v", w.gh.dismissed)
	}
	if all := pending(t, w); len(all) != 1 || all[0].Review != reviewID(7) {
		t.Errorf("pending %+v: a sent dismissal is settled only once read back", all)
	}
	if live, _ := w.h.ledger.HasLiveToken(pr); live {
		t.Error("the PR's tokens outlived the dismissal record")
	}
	if a := w.elly.put[pr.URL()]; a.Note != noteDismissing {
		t.Errorf("row %q, want %q", a.Note, noteDismissing)
	}
	// Read back as the user's dismissal: settled, the row says so.
	w.gh.status = DismissalStatus{Dismissed: true}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if all := pending(t, w); len(all) != 0 {
		t.Errorf("a dismissal read back stayed pending: %+v", all)
	}
	if a := w.elly.put[pr.URL()]; a.Note != noteDismissed {
		t.Errorf("row %q after settling, want %q", a.Note, noteDismissed)
	}
}

func TestTickARefusedDismissalStaysPendingAndNotifiesOnce(t *testing.T) {
	w := newTickWorld(t)
	pr := approvalRound(t, w)
	w.gh.dismissErr = errors.New("403")
	for range 3 {
		if err := w.h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if unread, _ := w.h.ledger.UnreadRounds(); len(unread) != 0 {
		t.Errorf("the round stays unread behind a refused dismissal: %v", unread)
	}
	if all := pending(t, w); len(all) != 1 {
		t.Fatalf("a refused dismissal settled: %+v", all)
	}
	n := 0
	for _, m := range w.notified {
		if strings.Contains(m, "could not dismiss") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d refusal notifications over three ticks, want 1: %q", n, w.notified)
	}
	if !strings.Contains(w.out.String(), "403") {
		t.Errorf("the refusal is not in tick's output: %q", w.out.String())
	}
	if a := w.elly.put[pr.URL()]; a.Note != noteDismissing {
		t.Errorf("row %q", a.Note)
	}
}

// A crash after GitHub took the dismissal but before anything else: the
// record is all that is left, and the next tick settles it by reading back.
func TestTickSettlesADismissalACrashLeftBehind(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.AddDismissal(pr, reviewID(7), t0); err != nil {
		t.Fatal(err)
	}
	w.gh.status = DismissalStatus{Dismissed: true}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if all := pending(t, w); len(all) != 0 || len(w.gh.dismissed) != 0 {
		t.Errorf("pending %+v, dismissed again %v", all, w.gh.dismissed)
	}
}

func TestTickResendsADismissalThatDidNotLand(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.AddDismissal(pr, reviewID(7), t0); err != nil {
		t.Fatal(err)
	}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.gh.dismissed, []ids.ReviewID{reviewID(7)}) || len(pending(t, w)) != 1 {
		t.Errorf("dismissed %v pending %+v", w.gh.dismissed, pending(t, w))
	}
}

func TestTickDropsADismissalOnlyForAMergeOrAnArchive(t *testing.T) {
	for _, c := range []struct {
		name    string
		status  DismissalStatus
		err     error
		dropped bool
	}{
		{"merged", DismissalStatus{Merged: true}, nil, true},
		{"archived", DismissalStatus{Archived: true}, nil, true},
		{"closed, not merged", DismissalStatus{}, nil, false},
		{"404", DismissalStatus{}, errors.New("HTTP 404"), false},
		{"403", DismissalStatus{}, errors.New("HTTP 403"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newTickWorld(t)
			pr := w.accept(t, 42)
			if err := w.h.ledger.AddDismissal(pr, reviewID(7), t0); err != nil {
				t.Fatal(err)
			}
			w.gh.status, w.gh.statusErr = c.status, c.err
			w.gh.dismissErr = errors.New("closed")
			for range 2 {
				if err := w.h.tick(); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(pending(t, w)) == 0; got != c.dropped {
				t.Errorf("dropped %v, want %v", got, c.dropped)
			}
			if len(w.notified) != 1 || !strings.Contains(w.notified[0], pr.URL()) {
				t.Errorf("notified %q, want one line naming the PR", w.notified)
			}
		})
	}
}

func TestAPendingDismissalOffersNoApprove(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.AddDismissal(pr, reviewID(7), t0); err != nil {
		t.Fatal(err)
	}
	w.gh.statusErr = errors.New("HTTP 502")
	full := strings.Repeat("a", 40)
	if _, err := w.h.ledger.StartRound(pr, full, "new_head", t0); err != nil {
		t.Fatal(err)
	}
	// A clean round that would offer Approve.
	w.msgs[pr.RoundSessionID()] = "Clean. `! claude-review approve " + pr.URL() + " --at " + full + "`"
	w.gh.facts = RoundFacts{State: "open", Head: full}
	w.gh.head = full
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	s, _ := w.h.stateOf(pr)
	if s.Kind == kindApprove || s.Kind == kindApproved {
		t.Errorf("state %s{%s} while a dismissal is pending", s.State, s.Kind)
	}
	if live, _ := w.h.ledger.HasLiveToken(pr); live {
		t.Error("a token was minted while a dismissal is pending")
	}
}

// Both approve paths refuse while a dismissal is pending: the link (which
// keeps its token for later) and the typed command.
func TestApproveRefusesBesideAPendingDismissal(t *testing.T) {
	h := verbHost(t)
	sha := strings.Repeat("a", 40)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := l.MintToken(widgetsPR, sha)
	if err == nil {
		err = l.AddDismissal(widgetsPR, reviewID(7), t0)
	}
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	h.d.Gh = func(args ...string) (string, error) {
		if slices.Contains(args, "event=APPROVE") {
			posts++
			return `{"html_url":"approved"}`, nil
		}
		return sha, nil
	}
	if code := run([]string{"claude-review://acme/widgets/42/approve?at=" + sha + "&token=" + tok.String()}, h.d); code == 0 || posts != 0 {
		t.Errorf("link: exit %d, approvals %d", code, posts)
	}
	if code := run([]string{"approve", widgetsPR.URL(), "--at", sha}, h.d); code == 0 || posts != 0 {
		t.Errorf("typed: exit %d, approvals %d", code, posts)
	}
	if !strings.Contains(h.stderr.String(), "dismissal") {
		t.Errorf("stderr %q does not say why", h.stderr.String())
	}
	l, _ = OpenLedger(LedgerPath(h.d.StateDir))
	defer func() { _ = l.Close() }()
	if _, has, _ := l.TokenFor(widgetsPR, sha); !has {
		t.Error("the refused click spent its token")
	}
}
