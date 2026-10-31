package main

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestLedger(t *testing.T) *Ledger {
	t.Helper()
	l, err := OpenLedger(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func TestLedgerAcceptance(t *testing.T) {
	l := openTestLedger(t)
	pr := widgetsPR

	if _, ok, _ := l.Acceptance(pr); ok {
		t.Fatal("a PR nobody clicked is accepted")
	}
	if err := l.Accept(pr, "head1", "main", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ignore(pr); err != nil {
		t.Fatal(err)
	}
	if ignored, _ := l.Ignored(pr); !ignored {
		t.Fatal("ignore did not stick")
	}
	if err := l.Accept(pr, "head2", "develop", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, ok, err := l.Acceptance(pr)
	if err != nil || !ok || a.Head != "head2" || a.Base != "develop" {
		t.Fatalf("got %+v %v %v, want the later click's head and base", a, ok, err)
	}
	if ignored, _ := l.Ignored(pr); ignored {
		t.Error("a new first-look click must clear the ignore")
	}
}

func TestLedgerRounds(t *testing.T) {
	l := openTestLedger(t)
	pr := widgetsPR

	id, err := l.StartRound(pr, "head1", "review_requested", t0)
	if err != nil {
		t.Fatal(err)
	}
	if ran, _ := l.RanAt(pr, "head1", "review_requested"); !ran {
		t.Error("a started round is not recorded")
	}
	if ran, _ := l.RanAt(pr, "head1", "new_head"); ran {
		t.Error("another trigger at the same head counts as run")
	}
	if n, _ := l.RoundsSince(pr, t0.Add(-time.Hour)); n != 1 {
		t.Errorf("rounds since: %d", n)
	}

	unread, err := l.UnreadRounds()
	if err != nil || len(unread) != 1 || unread[0].ID != id || unread[0].Head != "head1" || !unread[0].StartedAt.Equal(t0) {
		t.Fatalf("unread: %+v %v", unread, err)
	}
	if err := l.MarkRetried(id); err != nil {
		t.Fatal(err)
	}
	if unread, _ := l.UnreadRounds(); unread[0].Retries != 1 {
		t.Errorf("retries: %+v", unread)
	}
	if err := l.MarkRead(id); err != nil {
		t.Fatal(err)
	}
	if unread, _ := l.UnreadRounds(); len(unread) != 0 {
		t.Errorf("a read round is still unread: %+v", unread)
	}
}

func TestLedgerApproveTokensAreOneTime(t *testing.T) {
	l := openTestLedger(t)
	tok, err := l.MintToken(widgetsPR, "sha1")
	if err != nil || len(tok) < 32 {
		t.Fatalf("token %q, %v", tok, err)
	}
	if ok, _ := l.UseToken(widgetsPR, "sha2", tok); ok {
		t.Error("token accepted for another sha")
	}
	if ok, _ := l.UseToken(PR{Owner: owner, Repo: repoName, Number: 43}, "sha1", tok); ok {
		t.Error("token accepted for another PR")
	}
	if ok, _ := l.UseToken(widgetsPR, "sha1", "forged"); ok {
		t.Error("forged token accepted")
	}
	if ok, _ := l.UseToken(widgetsPR, "sha1", tok); !ok {
		t.Error("the minted token was refused")
	}
	if ok, _ := l.UseToken(widgetsPR, "sha1", tok); ok {
		t.Error("a used token worked twice")
	}
}

func TestLedgerAnnotations(t *testing.T) {
	l := openTestLedger(t)
	a := Annotation{State: stateNeedsYou, Note: "look", Links: []Link{{Label: "Open session", URL: "claude-review://acme/widgets/42/attach"}}}
	if err := l.SetAnnotation(widgetsPR, a); err != nil {
		t.Fatal(err)
	}
	all, err := l.Annotations()
	if err != nil || len(all) != 1 || all[widgetsPR.URL()].Note != "look" || all[widgetsPR.URL()].Links[0].Label != "Open session" {
		t.Fatalf("got %+v %v", all, err)
	}
	if err := l.ClearAnnotation(widgetsPR); err != nil {
		t.Fatal(err)
	}
	if all, _ := l.Annotations(); len(all) != 0 {
		t.Errorf("cleared annotation still there: %+v", all)
	}
}
