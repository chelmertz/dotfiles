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
