package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var openMention = EllyMention{Url: widgetsPR.URL() + "#discussion_r7", Author: "adam", At: t0}

// The contract: no Approve token is minted or honoured while the PR's gate
// blocks, a token minted before the mention included.
func TestApproveTokensWaitForTheMentionGate(t *testing.T) {
	l := openTestLedger(t)
	if err := l.SetPRState(widgetsPR, PRState{State: stAwaiting, Kind: kindApprove, Head: sha, Since: t0}); err != nil {
		t.Fatal(err)
	}
	before, err := l.MintToken(widgetsPR, sha)
	if err != nil {
		t.Fatalf("no gate recorded must not block: %v", err)
	}
	for name, g := range map[string]MentionGate{
		"open":       {Open: []EllyMention{openMention}},
		"unreadable": {Unreadable: true},
	} {
		if err := l.SetMentionGate(freeLock(t, widgetsPR), widgetsPR, g); err != nil {
			t.Fatal(err)
		}
		if _, err := l.MintToken(widgetsPR, sha); !errors.Is(err, errMentionOpen) {
			t.Errorf("%s: minted: %v", name, err)
		}
		if ok, err := l.UseToken(widgetsPR, sha, before, winClosed); ok || !errors.Is(err, errMentionOpen) {
			t.Errorf("%s: an earlier token was honoured: %v %v", name, ok, err)
		}
	}
	if err := l.SetMentionGate(freeLock(t, widgetsPR), widgetsPR, MentionGate{}); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.UseToken(widgetsPR, sha, before, winClosed); !ok || err != nil {
		t.Errorf("cleared gate: the earlier token was refused: %v %v", ok, err)
	}
	if _, err := l.db.Exec(`update mention_gate set open = 'garbage'`); err != nil {
		t.Fatal(err)
	}
	if g, err := l.MentionGate(widgetsPR); err != nil || !g.Blocks() {
		t.Errorf("an unreadable stored list does not block: %+v %v", g, err)
	}
}

func TestMentionGateOfFailsClosed(t *testing.T) {
	no := false
	for name, c := range map[string]struct {
		p    EllyPR
		want bool
	}{
		"older elly":     {ellyPR(func(p *EllyPR) { p.MentionsReadable = nil }), true},
		"threads unread": {ellyPR(func(p *EllyPR) { p.MentionsReadable = &no }), true},
		"open mention":   {ellyPR(func(p *EllyPR) { p.Mentions = []EllyMention{openMention} }), true},
		"readable, none": {ellyPR(nil), false},
	} {
		if got := mentionGateOf(c.p).Blocks(); got != c.want {
			t.Errorf("%s: blocks %v", name, got)
		}
	}
}

// One total choice per state: a state that yields shows the mention, every
// other keeps its row exactly; an unreadable gate changes Approve alone.
func TestMentionRowPrecedence(t *testing.T) {
	yields := map[string]bool{"Idle": true, "AwaitingApprove": true, "HandledPosted": true, "HandledNothing": true, "HandledApproved": true}
	for _, v := range allVariants {
		s := untyped(Typed{V: v, Head: sha, Since: t0})
		name := strings.TrimPrefix(fmt.Sprintf("%T", v), "main.")
		plain := render(widgetsPR, s, View{})
		withMention := render(widgetsPR, s, View{Mentions: MentionGate{Open: []EllyMention{openMention, openMention}}})
		if yields[name] != v.yieldsToMention() {
			t.Errorf("%s: yieldsToMention %v", name, v.yieldsToMention())
		}
		if !v.yieldsToMention() {
			if !sameRow(plain, withMention) {
				t.Errorf("%s: a mention changed a row that keeps its own: %+v", name, withMention)
			}
			continue
		}
		if withMention.State != stateNeedsYou || withMention.Links[0].URL != openMention.Url || !withMention.Links[0].Primary || !strings.Contains(withMention.Note, "adam mentioned you") || !strings.Contains(withMention.Note, "1 of 2") {
			t.Errorf("%s: mention row %+v", name, withMention)
		}
		unreadable := render(widgetsPR, s, View{Mentions: MentionGate{Unreadable: true}})
		_, approve := v.(AwaitingApprove)
		if approve == sameRow(plain, unreadable) {
			t.Errorf("%s: unreadable gate row %+v", name, unreadable)
		}
		if approve && strings.Contains(strings.ToLower(unreadable.Note), "approve waits") == false {
			t.Errorf("unreadable Approve row says nothing: %+v", unreadable)
		}
	}
}

func sameRow(a, b Annotation) bool {
	if a.State != b.State || a.Note != b.Note || len(a.Links) != len(b.Links) {
		return false
	}
	for i := range a.Links {
		if a.Links[i] != b.Links[i] {
			return false
		}
	}
	return true
}

func TestRoundTriggerOnAnUnseenMention(t *testing.T) {
	later := openMention
	later.At = t0.Add(time.Hour)
	mentioned := ellyPR(func(p *EllyPR) { p.Mentions = []EllyMention{openMention, later}; p.HeadRefOid = "h1" })
	if got := roundTrigger(mentioned, "h1", t0.Add(-time.Minute)); got != (Mentioned{Url: later.Url, At: later.At.UTC()}) {
		t.Errorf("newest unseen mention: %#v", got)
	}
	if got := roundTrigger(mentioned, "h1", t0.Add(2*time.Hour)); got != nil {
		t.Errorf("a round after both saw them: %#v", got)
	}
	both := mentioned
	both.Asked = askedAs(askedRequested)
	if _, ok := roundTrigger(both, "h1", time.Time{}).(Requested); !ok {
		t.Error("a request outranks a mention")
	}
}

// End to end: a clean round on a PR with an open mention gets the mention
// row and no live token; once the mention is answered, Approve works.
func TestTickApproveWaitsForAnOpenMention(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	p := requestedPR(42)
	p.Mentions = []EllyMention{{Url: pr.URL() + "#discussion_r7", Author: "adam", At: t0}}
	w.elly.prs = []EllyPR{p}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("a", 40)
	w.msgs[pr.RoundSessionID()] = "Clean. `! claude-review approve " + pr.URL() + " --at " + full + "`"
	if _, err := w.h.ledger.db.Exec(`update rounds set head = ?`, full); err != nil {
		t.Fatal(err)
	}
	w.gh.facts = RoundFacts{State: "open", Head: full}
	w.gh.head = full
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	a := w.elly.put[pr.URL()]
	if a.State != stateNeedsYou || a.Links[0].Label != "Thread" {
		t.Fatalf("with a mention open elly got %+v", a)
	}
	if _, has, _ := w.h.ledger.TokenFor(pr, full); has {
		t.Error("a token was minted under an open mention")
	}
	w.elly.prs[0].Mentions = nil
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if a := w.elly.put[pr.URL()]; len(a.Links) == 0 || a.Links[0].Label != "Approve" {
		t.Fatalf("after the mention cleared elly got %+v", a)
	}
}

// threads lists what is on the PR and nothing about mentions: which ones
// wait on the user is elly's rule, and the prompt names them.
func TestThreadsListsNoMentions(t *testing.T) {
	g := &fakeGh{outs: []string{
		`[{"id":1,"body":"@me can you check","user":{"login":"ada"}}]`,
		`[]`,
		`[]`,
	}}
	var out, errb bytes.Buffer
	if code := runThreads([]string{prURL}, g.run, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if got := out.String(); got != "--- comment 1 by ada\n    @me can you check\n\n" || len(g.calls) != 3 {
		t.Errorf("got %d calls,\n%s", len(g.calls), got)
	}
}

// The first poll found two mentions months old; a tick must not answer them.
func TestTickIgnoresMentionsOnStalePRs(t *testing.T) {
	w := newTickWorld(t)
	p := ellyPR(func(p *EllyPR) {
		p.LastUpdated = w.h.now().Add(-15 * 24 * time.Hour)
		p.Mentions = []EllyMention{{Url: p.Url + "#issuecomment-1", Author: "adam", At: p.LastUpdated}}
	})
	w.h.tcfg.AutoStart = true
	w.elly.prs = []EllyPR{p}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 {
		t.Errorf("a stale mention started a round: %v", w.started)
	}
	if g, _ := w.h.ledger.MentionGate(widgetsPR); g.Blocks() {
		t.Errorf("a stale mention holds Approve: %+v", g)
	}
	fresh := withoutStaleMentions([]EllyPR{ellyPR(func(q *EllyPR) { q.LastUpdated = w.h.now(); q.Mentions = p.Mentions })}, w.h.now())
	if len(fresh[0].Mentions) != 1 {
		t.Error("a fresh PR lost its mention")
	}
}

func ownMentionedPR() EllyPR {
	return ellyPR(func(p *EllyPR) {
		p.Url = PR{Owner: "acme", Repo: "widgets", Number: 42}.URL()
		p.Author = "me"
		p.Mentions = []EllyMention{{Url: p.Url + "#issuecomment-5", Author: "adam", At: t0}}
	})
}

func TestPlanOwnRunsOnlyForAMention(t *testing.T) {
	cfg := TickConfig{ReviewRepos: []string{"acme/widgets"}, User: "me", DailyCap: 6, AutoStart: true}
	for name, c := range map[string]struct {
		p    EllyPR
		cfg  TickConfig
		want bool
	}{
		"own, mentioned":        {ownMentionedPR(), cfg, true},
		"own, nothing":          {ellyPR(func(p *EllyPR) { p.Author = "me" }), cfg, false},
		"own, requested only":   {ellyPR(func(p *EllyPR) { p.Author = "me"; p.Asked = askedAs(askedRequested) }), cfg, false},
		"own, auto_start off":   {ownMentionedPR(), TickConfig{ReviewRepos: cfg.ReviewRepos, User: "me", DailyCap: 6}, false},
		"own, buried":           {func() EllyPR { p := ownMentionedPR(); p.Buried = true; return p }(), cfg, false},
		"own, outside the list": {ownMentionedPR(), TickConfig{ReviewRepos: []string{"other/*"}, User: "me", DailyCap: 6, AutoStart: true}, false},
	} {
		l := openTestLedger(t)
		got, err := planTick([]EllyPR{c.p}, c.cfg, l, fakeGate{descends: true, base: "main"}, t0)
		if err != nil {
			t.Fatal(err)
		}
		started := len(got) == 1 && got[0].Skip == "" && got[0].Gate == ""
		if started != c.want {
			t.Errorf("%s: planned %+v", name, got)
		}
		if started {
			if _, ok := got[0].Trigger.(OwnMention); !ok {
				t.Errorf("%s: trigger %#v, want OwnMention", name, got[0].Trigger)
			}
		}
	}
}

func TestMentionsOnlyPrompt(t *testing.T) {
	p := BuildPrompt(Config{MentionsOnly: true, PostWithoutAsking: true}, widgetsPR, "upstream", true)
	if !strings.Contains(p, "Do not review it") || strings.Contains(p, "post the findings as inline review comments") || strings.Contains(p, "New review round") {
		t.Errorf("prompt reviews: %s", p)
	}
	if !strings.HasSuffix(strings.TrimSpace(p), "`claude-review: question for the user`.") {
		t.Errorf("the question line is not last: %s", p)
	}
}

// The round answers exactly the mentions elly lists as open, by URL; with
// none it answers none, and when elly could not say it answers none and
// says so in its question line. A prompt built with no gate read (a window
// round outside review_repos) says nothing about mentions.
func TestBuildPromptNamesEllysOpenMentions(t *testing.T) {
	second := EllyMention{Url: widgetsPR.URL() + "#issuecomment-9", Author: "bea", At: t0}
	rules := "Answer it only with what the code at the PR head shows, linking the file at that commit, with `claude-review post " + widgetsPR.URL() + " reply --to <id>` for an inline comment"
	for _, mentionsOnly := range []bool{false, true} {
		cfg := Config{MentionsOnly: mentionsOnly, PostWithoutAsking: true}
		cfg.Mentions = &MentionGate{Open: []EllyMention{openMention, second}}
		p := BuildPrompt(cfg, widgetsPR, "upstream", true)
		if !strings.Contains(p, "These comments @-mention the account you post as and wait on the user:\n"+openMention.Url+"\n"+second.Url+"\n") ||
			!strings.Contains(p, "Find each in `claude-review threads "+widgetsPR.URL()+"` by its id") || !strings.Contains(p, rules) {
			t.Errorf("mentionsOnly=%v: open mentions not named: %s", mentionsOnly, p)
		}
		if strings.Contains(p, "ends by listing") {
			t.Errorf("mentionsOnly=%v: prompt still points at the threads listing: %s", mentionsOnly, p)
		}

		cfg.Mentions = &MentionGate{}
		p = BuildPrompt(cfg, widgetsPR, "upstream", true)
		if !strings.Contains(p, "elly lists no comment that @-mentions the account you post as") || strings.Contains(p, rules) {
			t.Errorf("mentionsOnly=%v: no open mention: %s", mentionsOnly, p)
		}

		cfg.Mentions = &MentionGate{Unreadable: true, Open: []EllyMention{openMention}}
		p = BuildPrompt(cfg, widgetsPR, "upstream", true)
		if !strings.Contains(p, "elly could not list") || !strings.Contains(p, "answer none of them") || strings.Contains(p, openMention.Url) || strings.Contains(p, rules) {
			t.Errorf("mentionsOnly=%v: unreadable gate: %s", mentionsOnly, p)
		}
	}
	if p := BuildPrompt(Config{}, widgetsPR, "upstream", false); strings.Contains(p, "@-mention") {
		t.Errorf("no gate read, yet the prompt speaks of mentions: %s", p)
	}
}

// A click reads the PR's stored gate into the round's prompt.
func TestClickPromptNamesTheStoredMentions(t *testing.T) {
	h, _ := clickHost(t, true)
	if err := h.ledger(t).SetMentionGate(freeLock(t, testPR), testPR, MentionGate{Open: []EllyMention{{Url: testPR.URL() + "#discussion_r3", Author: "adam", At: t0}}}); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{target}, h.d); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	for _, argv := range h.outputs {
		if strings.Contains(argv[len(argv)-1], testPR.URL()+"#discussion_r3") {
			return
		}
	}
	t.Errorf("no launch names the stored mention: %q", h.outputs)
}

// A tick on the user's own mentioned PR launches with the mentions-only
// prompt, and its round is recorded as OwnMention for post's gate.
func TestTickRunsAMentionsOnlyRoundOnTheUsersOwnPR(t *testing.T) {
	w := newTickWorld(t)
	w.h.tcfg.AutoStart = true
	w.h.tcfg.User = "me"
	w.elly.prs = []EllyPR{ownMentionedPR()}
	if err := w.h.tick(); err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 1 || !strings.Contains(strings.Join(w.started[0], " "), "Do not review it") ||
		!strings.Contains(strings.Join(w.started[0], " "), ownMentionedPR().Url+"#issuecomment-5") {
		t.Fatalf("started %q", w.started)
	}
	pr := PR{Owner: "acme", Repo: "widgets", Number: 42}
	if tr, err := w.h.ledger.NewestTrigger(pr); err != nil || !mentionsOnly(tr) {
		t.Errorf("recorded trigger %#v %v", tr, err)
	}
}

func TestMentionsOnlyPostRefusesReviews(t *testing.T) {
	own := OwnMention{Url: "u"}
	for name, c := range map[string]struct {
		t       Trigger
		kind    string
		draft   bool
		refused bool
	}{
		"inline":               {own, kindInline, false, true},
		"inline, draft":        {own, kindInline, true, true},
		"summary review":       {own, kindReview, false, true},
		"review sends replies": {own, kindReview, true, false},
		"reply":                {own, kindReply, false, false},
		"comment":              {own, kindComment, false, false},
		"normal round inline":  {Mentioned{Url: "u"}, kindInline, false, false},
		"no round":             {nil, kindReview, false, false},
	} {
		if err := mentionsOnlyPost(c.t, c.kind, c.draft); (err != nil) != c.refused {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Through the command: in a mentions-only round, post inline is refused
// before anything reaches GitHub's write endpoints.
func TestPostInlineRefusedInAMentionsOnlyRound(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.StartRound(widgetsPR, sha, OwnMention{Url: widgetsPR.URL() + "#issuecomment-5", At: t0}, t0); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	code := run([]string{"post", widgetsPR.URL(), "inline", "--path", "a.go", "--line", "1", "--at", sha, "--body", "nit"}, h.d)
	if code == 0 || !strings.Contains(h.stderr.String(), "no inline comment") {
		t.Errorf("exit %d: %s", code, h.stderr.String())
	}
	for _, c := range h.ghCalls {
		if strings.Contains(strings.Join(c, " "), "POST") {
			t.Errorf("a write reached GitHub: %q", c)
		}
	}
}

// A click holding a PR's lock while tick records the mention gates leaves
// that PR's gate to refresh, which stores it under its own lock before the
// row renders: no Approve is minted or shown over a mention elly reported.
func TestRefreshStoresAGateAClickHeldBack(t *testing.T) {
	w := newTickWorld(t)
	pr := w.accept(t, 42)
	if err := w.h.ledger.SetPRState(pr, PRState{State: stAwaiting, Kind: kindApprove, Head: "h1", Since: t0}); err != nil {
		t.Fatal(err)
	}
	p := requestedPR(42)
	p.HeadRefOid = "h1"
	p.Mentions = []EllyMention{{Url: pr.URL() + "#discussion_r7", Author: "adam", At: t0}}
	w.locked[prKey(pr)] = true
	gates, err := w.h.recordMentions([]EllyPR{p})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := w.h.ledger.MentionGate(pr); g.Blocks() {
		t.Fatal("recordMentions wrote under a click's lock")
	}
	if _, ok := gates[pr.URL()]; !ok {
		t.Fatalf("the held-back gate was dropped: %v", gates)
	}
	w.locked[prKey(pr)] = false
	if err := w.h.refresh(nil, gates, []EllyPR{p}); err != nil {
		t.Fatal(err)
	}
	if g, _ := w.h.ledger.MentionGate(pr); !g.Blocks() {
		t.Error("refresh did not store the held-back gate")
	}
	if live, err := w.h.ledger.HasLiveToken(pr); err != nil || live {
		t.Errorf("an Approve token over an open mention: %v %v", live, err)
	}
	if got := w.elly.put[pr.URL()]; got.State != stateNeedsYou || !strings.Contains(got.Note, "adam mentioned you") {
		t.Errorf("the row ignores the mention: %+v", got)
	}
}

// A mentions-only round on the user's own PR keeps the daily cap's pause,
// and drops a fresh-look gate, since it reviews nothing.
func TestPlanOwnKeepsThePauseAndDropsTheFreshLook(t *testing.T) {
	p := ownMentionedPR()
	pr, _ := ParseTarget(p.Url)
	for name, c := range map[string]struct {
		cap  int
		gate fakeGate
		want string
	}{
		"cap reached":   {1, fakeGate{descends: true, base: "main"}, gatePaused},
		"new base":      {6, fakeGate{descends: true, base: "release"}, ""},
		"under the cap": {6, fakeGate{descends: true, base: "main"}, ""},
	} {
		l := openTestLedger(t)
		if err := l.Accept(pr, p.HeadRefOid, "main", t0.Add(-2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := l.StartRound(pr, "hx", NewHead{}, t0.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		cfg := TickConfig{ReviewRepos: []string{"acme/widgets"}, User: "me", DailyCap: c.cap, AutoStart: true}
		got, err := planTick([]EllyPR{p}, cfg, l, c.gate, t0)
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
		if got[0].Gate != c.want {
			t.Errorf("%s: gate %q, want %q", name, got[0].Gate, c.want)
		}
	}
}

// Through the command: in a mentions-only round with no draft review, a
// review has no queued replies to send and is refused.
func TestPostReviewWithoutADraftRefusedInAMentionsOnlyRound(t *testing.T) {
	h := verbHost(t)
	l, err := OpenLedger(LedgerPath(h.d.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.StartRound(widgetsPR, sha, OwnMention{Url: widgetsPR.URL() + "#issuecomment-5", At: t0}, t0); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	code := run([]string{"post", widgetsPR.URL(), "review", "--at", sha, "--body", "summary"}, h.d)
	if code == 0 || !strings.Contains(h.stderr.String(), "posts no review") {
		t.Errorf("exit %d: %s", code, h.stderr.String())
	}
}
