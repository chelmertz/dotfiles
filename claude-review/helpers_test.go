package main

import (
	"os"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

const (
	owner       = "acme"
	repoName    = "widgets"
	originURL   = "git@github.com:acme/widgets.git"
	claudeBin   = "claude"
	resume      = "--resume"
	sh          = "/bin/sh"
	slug        = "acme/widgets"
	httpsURL    = "https://github.com/acme/widgets"
	codeRoot    = "/h/code"
	ghostty     = "ghostty"
	upstream    = "upstream"
	originName  = "origin"
	minimalPath = "PATH=/bin:/usr/bin"
	toFlag      = "--to"
	threadFlag  = "--thread"
	detachedCfg = `{"terminal": false}`
)

// origin builds the usual single-remote map.
func origin(url string) map[string]string { return map[string]string{originName: url} }

var widgetsPR = PR{Owner: owner, Repo: repoName, Number: 42}

func writeFile(p, body string) error {
	return os.WriteFile(p, []byte(body), 0o700) //nolint:gosec // test helper writes executable stubs
}

// move2 stores s as pr's state and shows its row, as a transition would.
func (h tickHost) move2(pr PR, s PRState) error {
	take := h.lock
	if take == nil { // a click's host, which never locks itself
		take = func(string) (func(), error) { return func() {}, nil }
	}
	lk, err := lockPR(pr, take)
	if err != nil {
		return err
	}
	defer lk.Unlock()
	if err := h.ledger.SetPRState(pr, s); err != nil {
		return err
	}
	return h.show(lk, pr, s, Hold{})
}

// mustLock takes pr's lock through h's taker until the test ends.
func mustLock(t testing.TB, h tickHost, pr PR) prlock.Locked {
	t.Helper()
	lk, err := lockPR(pr, h.lock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lk.Unlock)
	return lk
}

// freeLock is pr's lock from a taker no one else uses, for a ledger test
// with no host.
func freeLock(t testing.TB, pr PR) prlock.Locked {
	t.Helper()
	return mustLock(t, tickHost{lock: func(string) (func(), error) { return func() {}, nil }}, pr)
}
