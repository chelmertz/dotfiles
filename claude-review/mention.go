package main

import (
	"database/sql"
	"encoding/json"
	"errors"
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

// wantToken is the one rule for an Approve token: the top of s's chain is
// awaiting{approve} at a known head with no window anywhere in the chain,
// Claude Code's files say no window is open on the PR (Unknown is not
// Closed), no mention blocks, and no dismissal of the user's approval is
// pending. show mints a missing token on it and offers the Approve link
// only under it; Commit mints on it; UseToken spends only under it, against
// the state stored in its own transaction.
func wantToken(s PRState, window WindowState, gate MentionGate, dismissing bool) bool {
	return s.State == stAwaiting && s.Kind == kindApprove && s.Head != "" && !windowInChain(s) &&
		window == winClosed && !gate.Blocks() && !dismissing
}

// errNotOffered: the stored state no longer offers Approve at the link's
// commit (a click, a push or a window moved it), so its token is refused.
var errNotOffered = errors.New("the PR no longer offers Approve at that commit; use the row elly shows now")

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
