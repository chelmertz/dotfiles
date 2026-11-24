package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/chelmertz/dotfiles/claude-review/prlock"
)

// EllyMention is one open @-mention of the user as elly's poll found it
// (elly's types.Mention; github.mentions has the rule): a colleague's
// comment the user has answered neither by reply nor by reaction.
type EllyMention struct {
	Url    string
	Author string
	At     time.Time
}

// MentionGate is what tick last learned about a PR's open mentions.
// Approve waits while it Blocks: a mention open, or elly unable to say.
// The zero value is "none open": fail-closed is decided where elly's data
// comes in (mentionGateOf, MentionGate on a bad stored list), not by
// every caller remembering to fill it.
type MentionGate struct {
	Unreadable bool
	Open       []EllyMention
}

// Blocks is the precondition every Approve token is minted and used under.
func (g MentionGate) Blocks() bool { return g.Unreadable || len(g.Open) > 0 }

// mentionGateOf is p's gate as elly reports it. An elly that sends no
// MentionsReadable cannot say, which blocks.
func mentionGateOf(p EllyPR) MentionGate {
	return MentionGate{Unreadable: p.MentionsReadable == nil || !*p.MentionsReadable, Open: p.Mentions}
}

// errMentionOpen: the PR has an open mention, or elly could not say; no
// Approve token is minted or honoured until it clears.
var errMentionOpen = errors.New("a mention of you on this PR is unanswered (reply or react on GitHub first)")

// SetMentionGate records g for pr, under pr's lock lk: minting reads it.
// tick writes it for every review-repo PR it reads from elly, each pass.
func (l *Ledger) SetMentionGate(lk prlock.Locked, pr PR, g MentionGate) error {
	if err := lk.Check(prKey(pr)); err != nil {
		return err
	}
	open, err := json.Marshal(g.Open)
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`insert into mention_gate (pr, readable, open) values (?, ?, ?)
		on conflict (pr) do update set readable = excluded.readable, open = excluded.open`, pr.URL(), !g.Unreadable, string(open))
	return err
}

// MentionGate is pr's last recorded gate. A PR tick never recorded is not
// blocked: tick records every PR elly lists, and a PR elly does not list
// has no mention elly can see. A stored list that does not parse blocks.
func (l *Ledger) MentionGate(pr PR) (MentionGate, error) { return mentionGate(l.db, pr) }

func mentionGate(q querier, pr PR) (MentionGate, error) {
	var readable bool
	var open string
	err := q.QueryRow(`select readable, open from mention_gate where pr = ?`, pr.URL()).Scan(&readable, &open)
	if errors.Is(err, sql.ErrNoRows) {
		return MentionGate{}, nil
	}
	if err != nil {
		return MentionGate{}, err
	}
	g := MentionGate{Unreadable: !readable}
	if json.Unmarshal([]byte(open), &g.Open) != nil {
		g.Unreadable = true
	}
	return g, nil
}

// approveAllowed is the contract both token functions check: false with
// errMentionOpen while pr's gate blocks.
func approveAllowed(q querier, pr PR) error {
	g, err := mentionGate(q, pr)
	if err != nil {
		return err
	}
	if g.Blocks() {
		return errMentionOpen
	}
	return nil
}

var inlineCode = regexp.MustCompile("`[^`\n]*`")

// mentionedIn says whether body names @user outside quoted lines, code
// fences and code spans, word-bounded on both sides. elly's
// github.mentionedIn is the same rule (it decides which mentions are
// open); each side pins it with the same cases.
func mentionedIn(body, user string) bool {
	if user == "" {
		return false
	}
	at := regexp.MustCompile(`(?i)(^|[^\w@.])@` + regexp.QuoteMeta(user) + `($|[^\w-])`)
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(trimmed, ">") {
			continue
		}
		if at.MatchString(inlineCode.ReplaceAllString(line, "")) {
			return true
		}
	}
	return false
}

// staleMention is how long a PR may sit untouched before its open mentions
// stop counting: elly's icebox. A months-old question answered now helps
// nobody, and would start a round on a PR nobody works on (two such on
// the first poll, 2026-11-21).
const staleMention = 14 * 24 * time.Hour

// withoutStaleMentions drops the mentions of PRs untouched for
// staleMention, before anything reads them: they start no round, accept
// nothing and hold no Approve.
func withoutStaleMentions(prs []EllyPR, now time.Time) []EllyPR {
	out := make([]EllyPR, len(prs))
	for i, p := range prs {
		if p.LastUpdated.Before(now.Add(-staleMention)) {
			p.Mentions = nil
		}
		out[i] = p
	}
	return out
}
