package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The session brief is the cheap half of resuming work: a synchronous
// SessionStart hook prints it into the session, so a fresh Claude starts
// knowing what HANDOFF.md claims and which of those claims the world has
// already falsified. It never calls the network and never calls a model - the
// links timer has paid for every GitHub fact it reads. The expensive half is
// the /catchup skill, which only earns its cost when this brief says
// something changed.
//
// Output is plain text on stdout and exit 0 always: on SessionStart, Claude
// Code adds a synchronous hook's stdout to the session as context. An "async"
// hook's stdout is discarded, so this must not be folded into the existing
// async "p-launcher hook" entry (see nix/claude.nix).

// linksStaleAfter is the links timer's 10m interval plus slack. Its
// counterpart literal is OnUnitActiveSec in nix/p-launcher.nix, pinned by
// TestLinksTimerIntervalMatchesStaleness.
const linksStaleAfter = 15 * time.Minute

// handoffFacts is what HANDOFF.md claims, as parsed. Every field is optional:
// the layout is a convention (the project-state skill), not a schema, and a
// brief that dropped a section would be worse than one that omits a line.
type handoffFacts struct {
	Present    bool
	Age        time.Duration
	LastAction string
	Done       int
	Total      int
	HasCount   bool
	NextStep   string
	Decisions  int
	Unverified int
}

var (
	reLastAction = regexp.MustCompile(`(?m)^Last action:\s*(.+?)\s*$`)
	reProgress   = regexp.MustCompile(`(?m)^Progress:\s*(\d+)\s*/\s*(\d+)`)
	reHeading    = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)
)

// mdSection returns the body under "## <name>", "" when absent. Headings are
// matched case-insensitively on the first word so "## Open decisions" and
// "## Open Decisions" both land.
func mdSection(text, name string) string {
	locs := reHeading.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		if !strings.EqualFold(strings.TrimSpace(text[loc[2]:loc[3]]), name) {
			continue
		}
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		return text[loc[1]:end]
	}
	return ""
}

// mdItems counts top-level list items, the shape both "## Open decisions" and
// "## Unverified" use. Continuation lines are indented and do not count.
func mdItems(body string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			n++
		}
	}
	return n
}

// firstUncheckedItem returns the top "- [ ]" item under "## Next", wrapped
// continuation lines joined back into one sentence.
func firstUncheckedItem(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "- [ ] ") {
			continue
		}
		out := strings.TrimPrefix(line, "- [ ] ")
		for _, cont := range lines[i+1:] {
			if !strings.HasPrefix(cont, "  ") || strings.TrimSpace(cont) == "" {
				break
			}
			out += " " + strings.TrimSpace(cont)
		}
		return strings.Join(strings.Fields(out), " ")
	}
	return ""
}

func parseHandoff(text string, age time.Duration) handoffFacts {
	f := handoffFacts{Present: true, Age: age}
	if m := reLastAction.FindStringSubmatch(text); m != nil {
		f.LastAction = m[1]
	}
	next := mdSection(text, "Next")
	if m := reProgress.FindStringSubmatch(next); m != nil {
		f.Done, _ = strconv.Atoi(m[1])
		f.Total, _ = strconv.Atoi(m[2])
		f.HasCount = true
	}
	f.NextStep = firstUncheckedItem(next)
	f.Decisions = mdItems(mdSection(text, "Open decisions"))
	f.Unverified = mdItems(mdSection(text, "Unverified"))
	return f
}

// linkChange is one GitHub fact that postdates the handoff. The timestamps
// come from the link table, which the 10-minute timer fills; nothing here
// touches the network.
type linkChange struct {
	URL  string
	What string
}

// changedSince reports what moved on a project's links after the handoff was
// written. Order is by how much it should change the plan: a merged or closed
// PR retires work, a reviewer waiting blocks it, failing checks reopen it,
// and plain activity is only worth a line because someone else wrote it.
func changedSince(db *sql.DB, path string, since, now time.Time) ([]linkChange, error) {
	rows, err := db.Query(`select l.url, l.merged, coalesce(l.closed_at, ''), coalesce(l.check_state, ''),
		coalesce(l.check_at, ''), coalesce(l.github_updated_at, ''), coalesce(l.elly_updated_at, ''),
		l.action_needed, coalesce(l.detail, ''), coalesce(l.last_commenter, '')
		from link l join project p on p.id = l.project_id where p.path = ? order by l.id`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var merged, blocked, failing, active []linkChange
	for rows.Next() {
		var url, closedAt, checkState, checkAt, ghUpdated, ellyUpdated, detail, commenter string
		var isMerged, needed int
		if err := rows.Scan(&url, &isMerged, &closedAt, &checkState, &checkAt, &ghUpdated, &ellyUpdated, &needed, &detail, &commenter); err != nil {
			return nil, err
		}
		switch {
		case tsAfter(closedAt, since) && isMerged == 1:
			merged = append(merged, linkChange{url, "merged " + tsAgo(closedAt, now)})
		case tsAfter(closedAt, since):
			merged = append(merged, linkChange{url, "closed unmerged " + tsAgo(closedAt, now)})
		case needed == 1 && tsAfter(ellyUpdated, since):
			blocked = append(blocked, linkChange{url, waitingText(detail, commenter) + " " + tsAgo(ellyUpdated, now)})
		case checkState == "failure" && tsAfter(checkAt, since):
			failing = append(failing, linkChange{url, "checks failing " + tsAgo(checkAt, now)})
		case tsAfter(ghUpdated, since):
			active = append(active, linkChange{url, activityText(commenter) + " " + tsAgo(ghUpdated, now)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return append(append(append(merged, blocked...), failing...), active...), nil
}

// waitingText phrases elly's verdict for a reader with no context. detail is
// elly's own words ("2 unresolved threads"); the commenter is the fallback.
func waitingText(detail, commenter string) string {
	if detail != "" {
		return detail + ", waiting on you since"
	}
	if commenter != "" {
		return "waiting on you, last word from " + commenter
	}
	return "waiting on you since"
}

func activityText(commenter string) string {
	if commenter != "" {
		return "new activity from " + commenter
	}
	return "new activity"
}

func tsAfter(ts string, since time.Time) bool {
	t, err := time.Parse(time.RFC3339, ts)
	return err == nil && t.After(since)
}

// tsAgo renders a timestamp as an age. Anything the brief prints is a fact
// that went stale while the user was away, so the age is the part that
// matters. "just now" already reads as a time, so it takes no "ago".
func tsAgo(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	age := ageText(now.Sub(t))
	if age == justNow {
		return age
	}
	return age + " ago"
}

// justNow is the sub-minute age, shared so tsAgo can recognise it rather than
// re-deriving the threshold.
const justNow = "just now"

// ageText is coarse on purpose, unlike its sibling fmtDur in
// report_metrics.go: that one measures work and keeps the minutes ("1h 01m"),
// this one measures how long ago something happened, where "2d" beats
// "51h 12m" and the extra precision is noise in a one-line brief.
func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return justNow
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// staleness says how old the GitHub facts are, and only when they are older
// than the timer should allow. Silence is the freshness signal: a line here
// means the timer is failing, not that the data is merely a few minutes old.
func linksStaleness(s *Store, now time.Time) string {
	last, err := s.kvGet("links.last_refresh")
	if err != nil || last == "" {
		return "GitHub data never refreshed; check: systemctl --user status p-launcher-links"
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil || now.Sub(t) <= linksStaleAfter {
		if lastErr, _ := s.kvGet("links.last_error"); lastErr != "" {
			return "last links refresh reported: " + clip(lastErr)
		}
		return ""
	}
	out := fmt.Sprintf("GitHub data %s old (the timer refreshes every 10m)", ageText(now.Sub(t)))
	if lastErr, _ := s.kvGet("links.last_error"); lastErr != "" {
		out += "; last error: " + clip(lastErr)
	}
	return out
}

// renderBrief writes the brief, or nothing at all when there is nothing worth
// a line. Every line is a fact the reader can act on; no line is printed to
// say that a fact is absent.
func renderSessionBrief(project string, f handoffFacts, changes []linkChange, stale string, w io.Writer) {
	var b strings.Builder
	if !f.Present {
		fmt.Fprintf(&b, "p-launcher · %s · no HANDOFF.md yet (see the project-state skill)\n", project)
		if stale != "" {
			fmt.Fprintf(&b, "%s\n", stale)
		}
		io.WriteString(w, b.String())
		return
	}
	fmt.Fprintf(&b, "p-launcher · %s · handoff written %s ago\n", project, ageText(f.Age))
	if f.LastAction != "" {
		fmt.Fprintf(&b, "Last action: %s\n", f.LastAction)
	}
	if f.HasCount {
		fmt.Fprintf(&b, "Progress:    %d/%d items\n", f.Done, f.Total)
	}
	if f.NextStep != "" {
		fmt.Fprintf(&b, "Next step:   %s\n", f.NextStep)
	}
	if f.Decisions > 0 {
		fmt.Fprintf(&b, "Decisions:   %d waiting on you\n", f.Decisions)
	}
	if f.Unverified > 0 {
		fmt.Fprintf(&b, "Unverified:  %d claims\n", f.Unverified)
	}
	if stale != "" {
		fmt.Fprintf(&b, "%s\n", stale)
	}
	if len(changes) > 0 {
		b.WriteString("Changed since the handoff was written:\n")
		for _, c := range changes {
			fmt.Fprintf(&b, "  %s %s\n", c.URL, c.What)
		}
		b.WriteString("  → run /catchup: HANDOFF.md has not caught up with these yet.\n")
	}
	io.WriteString(w, b.String())
}

// briefSession is the subcommand. A cwd outside a project prints nothing, so
// the hook can be registered globally without noise in other repos.
func briefSession(s *Store, root, cwd string, now time.Time, w io.Writer) error {
	path := projectPathFor(root, cwd)
	if path == "" {
		return nil
	}
	f := handoffFacts{}
	handoff := filepath.Join(root, path, "HANDOFF.md")
	if st, err := os.Stat(handoff); err == nil {
		text, err := os.ReadFile(handoff)
		if err != nil {
			return err
		}
		f = parseHandoff(string(text), now.Sub(st.ModTime()))
	}
	// Only facts that postdate the handoff can contradict it; with no
	// handoff there is nothing to contradict, so the links are skipped.
	var changes []linkChange
	if f.Present {
		var err error
		if changes, err = changedSince(s.db, path, now.Add(-f.Age), now); err != nil {
			return err
		}
	}
	renderSessionBrief(path, f, changes, linksStaleness(s, now), w)
	return nil
}
