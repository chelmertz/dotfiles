package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

// liveToken stands for a minted Approve token. Tokens are hex, which %q,
// JSON and URL encoding leave unchanged, so asserting against the raw value
// covers every form an error or log can carry.
const liveToken = "9f86d081884c7d659a2feaa0c55ad015"

func TestARefusedPutNeverCarriesTheResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// An older elly echoed the refused link.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `link "claude-review://o/r/1/approve?token=`+liveToken+`": scheme not allowed`)
	}))
	defer srv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := Elly{Base: srv.URL, TokenFile: tokenFile}
	a := Annotation{State: "needs_you", Links: []Link{{Label: "Approve", URL: "claude-review://o/r/1/approve?token=" + liveToken}}}
	err := e.Put("https://github.com/o/r/pull/1", a, time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), liveToken) {
		t.Errorf("error carries the token: %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error lost the status: %v", err)
	}
}

func TestAMalformedLinkIsNeverQuoted(t *testing.T) {
	for _, in := range []string{
		"claude-review://o/r/1/approve?token=" + liveToken + "#x",
		"claude-review://o/r/0/approve?token=" + liveToken,
		"claude-review://o/r/99999999999999999999/approve?token=" + liveToken,
	} {
		if _, err := ParseTarget(in); err == nil || strings.Contains(err.Error(), liveToken) {
			t.Errorf("ParseTarget(%s) = %v, want an error without the token", in, err)
		}
	}
}

func TestTokenAlphabetIsHex(t *testing.T) {
	t2, err := ids.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tok := t2.String()
	if tok == "" || strings.Trim(tok, "0123456789abcdef") != "" {
		t.Errorf("token %q is not lowercase hex", tok)
	}
}

// Notes are fixed words per class: stored error text never reaches elly.
func TestNotesNeverCarryStoredText(t *testing.T) {
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	leak := "session 3f2a argv token=" + liveToken
	for _, s := range []PRState{
		{State: stHeld, Kind: kindLaunch, Detail: leak},
		{State: stStuck, Kind: kindFailed, Detail: leak},
	} {
		if a := renderRow(pr, s, View{}); strings.Contains(a.Note, liveToken) || strings.Contains(a.Note, "3f2a") {
			t.Errorf("%s{%s} note %q carries stored text", s.State, s.Kind, a.Note)
		}
	}
	for _, kind := range []string{breakerCap, breakerDuplicate, breakerSelfCheck, ""} {
		if w := breakerWords(kind + leak); strings.Contains(w, liveToken) || strings.Contains(w, "3f2a") {
			t.Errorf("breaker %q words %q carry stored text", kind, w)
		}
	}
}

// Every token minted before C7 may sit in an old log line; the first open
// after it spends them all, once.
func TestOpeningSpendsPreC7TokensOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	pr := PR{Owner: "o", Repo: "r", Number: 1}
	if err := l.SetMeta(metaTokensRevoked, ""); err != nil { // a pre-C7 ledger
		t.Fatal(err)
	}
	if _, err := l.MintToken(pr, "h1"); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if l, err = OpenLedger(path); err != nil {
		t.Fatal(err)
	}
	if _, has, _ := l.TokenFor(pr, "h1"); has {
		t.Error("a pre-C7 token survived the open")
	}
	fresh, err := l.MintToken(pr, "h1")
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if l, err = OpenLedger(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if tok, has, _ := l.TokenFor(pr, "h1"); !has || tok != fresh {
		t.Error("a later open spent a fresh token")
	}
}

// An Approve row whose token was spent by the one-time revocation gets a
// fresh one when next shown, never a link without a token.
func TestShowingAnApproveRowWithoutATokenMintsOne(t *testing.T) {
	w := newTickWorld(t)
	s := PRState{State: stAwaiting, Kind: kindApprove, Head: "h1"}
	if err := w.h.move2(widgetsPR, s); err != nil {
		t.Fatal(err)
	}
	tok, has, err := w.h.ledger.TokenFor(widgetsPR, "h1")
	if err != nil || !has {
		t.Fatalf("no token after showing the row: %v", err)
	}
	if a := w.elly.put[widgetsPR.URL()]; !strings.Contains(a.Links[0].URL, "token="+tok.String()) {
		t.Errorf("Approve link %q lacks the fresh token", a.Links[0].URL)
	}
}
