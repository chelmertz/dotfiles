package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The GitHub issue mirror. One issue per opted-in project: the first
// paragraph of the project's CLAUDE.md is the intent, and a marked block in
// the body is HANDOFF.md's "## Next" rendered as a checklist. The links timer
// drives it, so nothing here is typed by hand - the only human moment is the
// opt-in the session brief asks for once.
//
// Only the marked block is machine-owned. Anything a human writes above or
// below it survives every sync, and conversation happens in comments, which
// are append-only and can never collide with a body write. That split is why
// there is no merge logic here.

const (
	nextOpen  = "<!-- p-launcher:next -->"
	nextClose = "<!-- /p-launcher:next -->"
	// blockNote tells a reader who did not write the block where to edit.
	blockNote = "_Mirrored from HANDOFF.md by p-launcher. Edit the handoff, not this list._"
	// defaultIssueRepo is where project issues go until the AI team names a
	// home repository; `p-launcher kv set issue.repo <owner/name>` overrides.
	// Its counterpart is the convention in ~/p/m/CLAUDE.md.
	defaultIssueRepo = "eversport/ai-platform"
)

// nextItem is one "## Next" line. Done covers both "[x]" and "[X]".
type nextItem struct {
	Text string
	Done bool
}

// reNextItem accepts the "- []" typo as well as "- [ ]": p-launcher's own
// Progress count already tolerates it, and an item that renders one way in
// the brief and another way on GitHub would be worse than either.
var reNextItem = regexp.MustCompile(`^- \[([ xX]?)\] ?(.*)$`)

// nextItems parses the checklist under "## Next". Wrapped continuation lines
// are folded back into the item, the same way firstUncheckedItem does it, so
// a two-line handoff entry becomes one checklist line on GitHub.
func nextItems(section string) []nextItem {
	var out []nextItem
	lines := strings.Split(section, "\n")
	for i, line := range lines {
		m := reNextItem.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := m[2]
		for _, cont := range lines[i+1:] {
			if strings.TrimSpace(cont) == "" || !strings.HasPrefix(cont, "  ") {
				break
			}
			text += " " + strings.TrimSpace(cont)
		}
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			continue
		}
		out = append(out, nextItem{Text: text, Done: strings.EqualFold(m[1], "x")})
	}
	return out
}

// issueBlock renders the machine-owned block, markers included.
func issueBlock(items []nextItem) string {
	var b strings.Builder
	b.WriteString(nextOpen + "\n")
	b.WriteString("### Next\n\n")
	for _, it := range items {
		box := " "
		if it.Done {
			box = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", box, it.Text)
	}
	if len(items) == 0 {
		b.WriteString("_No open items._\n")
	}
	b.WriteString("\n" + blockNote + "\n")
	b.WriteString(nextClose)
	return b.String()
}

// spliceBlock replaces the marked block in an existing body, or appends it
// when the body has none. Text outside the markers is never touched: that is
// what makes a hand-written note on the issue safe.
func spliceBlock(body, block string) string {
	i := strings.Index(body, nextOpen)
	j := strings.Index(body, nextClose)
	if i >= 0 && j > i {
		return body[:i] + block + body[j+len(nextClose):]
	}
	if strings.TrimSpace(body) == "" {
		return block + "\n"
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block + "\n"
}

// reIntentSkip matches the CLAUDE.md template's placeholder paragraph, which
// is not an intent and must never become the top of an issue.
var reIntentSkip = regexp.MustCompile(`^<.*>$|Replace this\.`)

// intentFrom returns the first real paragraph of a project's CLAUDE.md: what
// the initiative is for, in the author's own words. Empty when the file still
// holds the template placeholder, so the issue simply has no intent rather
// than a fake one.
func intentFrom(md string) string {
	var para []string
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "#"):
			if len(para) > 0 {
				return paraText(para)
			}
		case t == "":
			if len(para) > 0 {
				return paraText(para)
			}
		default:
			para = append(para, t)
		}
	}
	return paraText(para)
}

func paraText(lines []string) string {
	s := strings.Join(lines, " ")
	if reIntentSkip.MatchString(s) {
		return ""
	}
	return s
}

// issueTitle is what the board card shows. The counter is in the title
// because project 171's only progress field counts sub-issues, and body
// checkboxes are invisible there.
func issueTitle(name string, done, total int) string {
	return fmt.Sprintf("%s (%d/%d)", name, done, total)
}

// issueComment is what gets posted when the handoff's Last action changes:
// the sentence itself, then where that leaves the project.
func issueComment(f handoffFacts) string {
	var b strings.Builder
	b.WriteString(f.LastAction)
	var tail []string
	if f.HasCount {
		tail = append(tail, fmt.Sprintf("Progress: %d/%d", f.Done, f.Total))
	}
	if f.NextStep != "" {
		tail = append(tail, "next: "+f.NextStep)
	}
	if len(tail) > 0 {
		b.WriteString("\n\n" + strings.Join(tail, " · "))
	}
	return b.String()
}

// issueDeps are the GitHub writes, injected so tests need no network.
type issueDeps struct {
	create  func(repo, title, body string) (string, error)
	get     func(url string) (title, body string, err error)
	edit    func(url, title, body string) error
	comment func(url, body string) error
	repo    string
	root    string
	now     time.Time
}

type issueResult struct{ Created, Updated, Commented, Skipped int }

func (r issueResult) String() string {
	return fmt.Sprintf("issues: %d created, %d updated, %d commented, %d skipped",
		r.Created, r.Updated, r.Commented, r.Skipped)
}

func (r *issueResult) add(o issueResult) {
	r.Created += o.Created
	r.Updated += o.Updated
	r.Commented += o.Commented
	r.Skipped += o.Skipped
}

// issuePlan is what one project's issue should become. Planning is read-only
// so `issue sync --dry-run` and the real thing decide identically - a preview
// that reasons separately is a preview of something else.
type issuePlan struct {
	Path, Name    string
	URL           string // empty when the issue does not exist yet
	Title, Body   string
	Comment       string
	Create, Edit  bool
	Post, Skipped bool
}

// syncIssues mirrors every opted-in project. One project's failure does not
// stop the rest: the timer runs again in ten minutes.
func syncIssues(s *Store, d issueDeps) (issueResult, error) {
	projects, err := s.IssueProjects()
	if err != nil {
		return issueResult{}, err
	}
	var res issueResult
	var firstErr error
	for _, p := range projects {
		one, err := syncIssue(s, p, d)
		res.add(one)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return res, firstErr
}

// syncIssue brings one project's issue up to date with its handoff.
func syncIssue(s *Store, p Found, d issueDeps) (issueResult, error) {
	pl, err := planIssue(s, p, d)
	if err != nil {
		return issueResult{}, err
	}
	return applyIssue(s, pl, d)
}

// planIssue decides what the issue should say. A project with no HANDOFF.md
// is skipped rather than given an empty issue: the handoff is the content.
func planIssue(s *Store, p Found, d issueDeps) (issuePlan, error) {
	pl := issuePlan{Path: p.Path, Name: p.Name}
	text, err := os.ReadFile(filepath.Join(d.root, p.Path, "HANDOFF.md"))
	if err != nil {
		pl.Skipped = true
		return pl, nil
	}
	f := parseHandoff(string(text), 0)
	items := nextItems(mdSection(string(text), "Next"))
	done, total := f.Done, f.Total
	if !f.HasCount {
		for _, it := range items {
			if it.Done {
				done++
			}
		}
		total = len(items)
	}
	pl.Title = issueTitle(p.Name, done, total)
	block := issueBlock(items)

	url, ok, err := s.PrimaryIssue(p.Path)
	if err != nil {
		return pl, err
	}
	if !ok {
		pl.Create = true
		pl.Body = block + "\n"
		if intent := projectIntent(d.root, p.Path); intent != "" {
			pl.Body = intent + "\n\n" + pl.Body
		}
		pl.Comment = f.LastAction // recorded as already said, never posted
		return pl, nil
	}
	pl.URL = url
	remoteTitle, remoteBody, err := d.get(url)
	if err != nil {
		return pl, fmt.Errorf("read issue %s: %w", url, err)
	}
	pl.Body = spliceBlock(remoteBody, block)
	pl.Edit = pl.Body != remoteBody || remoteTitle != pl.Title
	synced, err := s.SyncedAction(url)
	if err != nil {
		return pl, err
	}
	if f.LastAction != "" && f.LastAction != synced {
		pl.Post, pl.Comment = true, issueComment(f)
	}
	return pl, nil
}

// applyIssue performs the plan's writes and records what was said, so the
// next pass stays quiet.
func applyIssue(s *Store, pl issuePlan, d issueDeps) (issueResult, error) {
	switch {
	case pl.Skipped:
		return issueResult{Skipped: 1}, nil
	case pl.Create:
		url, err := d.create(d.repo, pl.Title, pl.Body)
		if err != nil {
			return issueResult{}, fmt.Errorf("create issue for %s: %w", pl.Path, err)
		}
		if err := s.SetPrimaryIssue(pl.Path, url); err != nil {
			return issueResult{}, err
		}
		// The issue is born carrying this Last action, so the first comment
		// is owed only once the handoff says something new.
		if err := s.SetSyncedAction(url, pl.Comment); err != nil {
			return issueResult{}, err
		}
		return issueResult{Created: 1}, nil
	}
	var res issueResult
	if pl.Edit {
		if err := d.edit(pl.URL, pl.Title, pl.Body); err != nil {
			return res, fmt.Errorf("edit issue %s: %w", pl.URL, err)
		}
		res.Updated = 1
	}
	if pl.Post {
		if err := d.comment(pl.URL, pl.Comment); err != nil {
			return res, fmt.Errorf("comment on %s: %w", pl.URL, err)
		}
		if err := s.SetSyncedAction(pl.URL, firstLine(pl.Comment)); err != nil {
			return res, err
		}
		res.Commented = 1
	}
	return res, nil
}

// firstLine is the Last-action sentence back out of a rendered comment, which
// is what the next pass compares against.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// previewIssues prints what a sync would do, writing nothing anywhere.
func previewIssues(s *Store, d issueDeps, w io.Writer) error {
	projects, err := s.IssueProjects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintln(w, "no project has opted in (p-launcher issue enable <ns/name>)")
		return nil
	}
	for _, p := range projects {
		pl, err := planIssue(s, p, d)
		if err != nil {
			fmt.Fprintf(w, "%s: %v\n", p.Path, err)
			continue
		}
		switch {
		case pl.Skipped:
			fmt.Fprintf(w, "%s: skipped, no HANDOFF.md\n", p.Path)
		case pl.Create:
			fmt.Fprintf(w, "%s: would create in %s\n  title: %s\n%s\n", p.Path, d.repo, pl.Title, indent(pl.Body))
		default:
			what := "unchanged"
			if pl.Edit {
				what = "would edit"
			}
			fmt.Fprintf(w, "%s: %s %s\n  title: %s\n", p.Path, what, pl.URL, pl.Title)
			if pl.Edit {
				fmt.Fprintf(w, "%s\n", indent(pl.Body))
			}
			if pl.Post {
				fmt.Fprintf(w, "  would comment: %s\n", firstLine(pl.Comment))
			}
		}
	}
	return nil
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  | " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// projectIntent reads the first real paragraph of a project's CLAUDE.md; ""
// when there is no file or it still holds the template placeholder.
func projectIntent(root, path string) string {
	b, err := os.ReadFile(filepath.Join(root, path, "CLAUDE.md"))
	if err != nil {
		return ""
	}
	return intentFrom(string(b))
}

// issueRepo is where new issues are opened: the kv override, else the default.
func issueRepo(s *Store) string {
	if v, err := s.kvGet("issue.repo"); err == nil && v != "" {
		return v
	}
	return defaultIssueRepo
}

func realIssueDeps(s *Store, root string, now time.Time) issueDeps {
	return issueDeps{create: ghIssueCreate, get: ghIssueGet, edit: ghIssueEdit, comment: ghIssueComment,
		repo: issueRepo(s), root: root, now: now}
}

// runCmdStdin is runCmd with a body on stdin: gh takes long bodies through
// `--body-file -` rather than an argv the shell would have to quote.
func runCmdStdin(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %s", name, clip(strings.TrimSpace(stderr.String()+" "+err.Error())))
	}
	return stdout.Bytes(), nil
}

func ghIssueCreate(repo, title, body string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runCmdStdin(ctx, body, "gh", "issue", "create", "-R", repo, "--title", title, "--body-file", "-")
	if err != nil {
		return "", err
	}
	// gh prints the new issue's URL, sometimes after a blank or banner line.
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "https://") {
			return line, nil
		}
	}
	return "", fmt.Errorf("gh issue create: no URL in output %q", clip(string(out)))
}

func ghIssueGet(url string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := runCmd(ctx, "gh", "issue", "view", url, "--json", "title,body")
	if err != nil {
		return "", "", err
	}
	var v struct{ Title, Body string }
	if err := json.Unmarshal(out, &v); err != nil {
		return "", "", err
	}
	return v.Title, v.Body, nil
}

func ghIssueEdit(url, title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := runCmdStdin(ctx, body, "gh", "issue", "edit", url, "--title", title, "--body-file", "-")
	return err
}

func ghIssueComment(url, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := runCmdStdin(ctx, body, "gh", "issue", "comment", url, "--body-file", "-")
	return err
}

// ghIssueLastComment reads the newest comment through GraphQL's comments(last:
// 1). The REST list endpoint returns the oldest page first and its sort
// parameters are documented for the repository-wide collection, not this one,
// so "newest" through REST would be a guess.
func ghIssueLastComment(url string) (author, body string, at time.Time, err error) {
	ref, ok := parseGitHubURL(url)
	if !ok {
		return "", "", time.Time{}, fmt.Errorf("not a GitHub issue URL: %s", url)
	}
	const q = `query($o:String!,$r:String!,$n:Int!){repository(owner:$o,name:$r){issue(number:$n){comments(last:1){nodes{author{login} createdAt body}}}}}`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := runCmd(ctx, "gh", "api", "graphql", "-f", "query="+q,
		"-F", "o="+ref.Owner, "-F", "r="+ref.Repo, "-F", fmt.Sprintf("n=%d", ref.Number))
	if err != nil {
		return "", "", time.Time{}, err
	}
	var v struct {
		Data struct {
			Repository struct {
				Issue struct {
					Comments struct {
						Nodes []struct {
							Author    struct{ Login string }
							CreatedAt string
							Body      string
						}
					}
				}
			}
		}
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", "", time.Time{}, err
	}
	nodes := v.Data.Repository.Issue.Comments.Nodes
	if len(nodes) == 0 {
		return "", "", time.Time{}, nil
	}
	return nodes[0].Author.Login, nodes[0].Body, parseTime(nodes[0].CreatedAt), nil
}
