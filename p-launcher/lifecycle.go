package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Lifecycle: projects are created and archived by writing project_event
// rows; folders never move. Archiving prints a checklist of what the project
// leaves behind and copies the allowlist rules it kept asking for.

var archiveReasons = map[string]bool{"done": true, "scrapped": true, "deprioritized": true, "elsewhere": true}

// Create makes ~/p/<ns>/<name> with a CLAUDE.md stub, registers it and logs
// the created event. The namespace directory must already exist; creating
// namespaces is a deliberate act, not a typo's side effect.
func Create(s *Store, root, path string) (string, error) {
	seg := strings.Split(path, "/")
	if len(seg) != 2 || !cleanSegment(seg[0]) || !cleanSegment(seg[1]) {
		return "", fmt.Errorf("project path must be <namespace>/<name>, got %q", path)
	}
	nsDir := filepath.Join(root, seg[0])
	if !isDir(nsDir) {
		return "", fmt.Errorf("no namespace directory %s", nsDir)
	}
	dir := filepath.Join(nsDir, seg[1])
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	claude := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(claude); os.IsNotExist(err) {
		if err := os.WriteFile(claude, []byte("# "+seg[1]+"\n"), 0o644); err != nil {
			return "", err
		}
	}
	if err := s.UpsertProjects([]Found{{Namespace: seg[0], Name: seg[1], Path: path}}); err != nil {
		return "", err
	}
	return dir, s.ProjectEvent(path, "created", "")
}

// RuleCount is one allowlist rule and how often the project asked for it.
type RuleCount struct {
	Rule string
	N    int
}

// Checklist is what archiving found; nothing in it blocks the archive.
type Checklist struct {
	Path, Reason             string
	Rules                    []RuleCount
	OpenLinks                []string
	OpenIssues, ClosedIssues int
	LiveSessions             int
	DirtyClones              []string // clone dir names with uncommitted or unpushed work
}

// Text renders the checklist for stdout and the notification body.
func (c Checklist) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "archived %s (%s)\n", c.Path, c.Reason)
	if len(c.Rules) > 0 {
		fmt.Fprintf(&b, "%d allowlist rule(s) copied to the clipboard:\n", len(c.Rules))
		for _, r := range c.Rules {
			fmt.Fprintf(&b, "  %s  (%d asks)\n", r.Rule, r.N)
		}
	} else {
		b.WriteString("no permission asks recorded\n")
	}
	if len(c.OpenLinks) > 0 {
		fmt.Fprintf(&b, "%d open link(s):\n", len(c.OpenLinks))
		for _, u := range c.OpenLinks {
			b.WriteString("  " + u + "\n")
		}
	}
	switch total := c.OpenIssues + c.ClosedIssues; {
	case total > 0 && c.OpenIssues == 0:
		fmt.Fprintf(&b, "all %d linked issue(s) are closed\n", total)
	case total > 0:
		fmt.Fprintf(&b, "%d of %d linked issue(s) still open\n", c.OpenIssues, total)
	}
	if c.LiveSessions > 0 {
		fmt.Fprintf(&b, "%d live session(s) still running\n", c.LiveSessions)
	}
	if len(c.DirtyClones) > 0 {
		fmt.Fprintf(&b, "clones with uncommitted or unpushed work: %s\n", strings.Join(c.DirtyClones, ", "))
	}
	return b.String()
}

// Archive logs the archived event with its reason, then gathers the
// checklist. clip receives the rules (one per line) when there are any; it is
// injected so tests need no clipboard.
func Archive(s *Store, root, path, reason string, clip func(string) error) (Checklist, error) {
	if !archiveReasons[reason] {
		return Checklist{}, fmt.Errorf("archive reason must be done, scrapped, deprioritized or elsewhere, got %q", reason)
	}
	if err := s.ProjectEvent(path, "archived", reason); err != nil {
		return Checklist{}, err
	}
	c := Checklist{Path: path, Reason: reason}
	rows, err := s.db.Query(`select pr.rule, count(*) from permission_request pr join project p on p.id = pr.project_id
		where p.path = ? group by pr.rule order by count(*) desc, pr.rule`, path)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var r RuleCount
		if err := rows.Scan(&r.Rule, &r.N); err != nil {
			rows.Close()
			return c, err
		}
		c.Rules = append(c.Rules, r)
	}
	rows.Close()
	rows, err = s.db.Query(`select l.url from link l join project p on p.id = l.project_id
		where p.path = ? and l.merged = 0 and l.closed_at is null order by l.id`, path)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return c, err
		}
		c.OpenLinks = append(c.OpenLinks, u)
	}
	rows.Close()
	if err := s.db.QueryRow(`select coalesce(sum(l.closed_at is null), 0), coalesce(sum(l.closed_at is not null), 0)
		from link l join project p on p.id = l.project_id where p.path = ? and l.kind = 'github_issue'`, path).Scan(&c.OpenIssues, &c.ClosedIssues); err != nil {
		return c, err
	}
	cutoff := time.Now().Add(-staleSession).UTC().Format(time.RFC3339)
	if err := s.db.QueryRow(`select count(*) from session_state ss join project p on p.id = ss.project_id
		where p.path = ? and ss.since > ?`, path, cutoff).Scan(&c.LiveSessions); err != nil {
		return c, err
	}
	if dir := filepath.Join(root, path); isDir(dir) {
		c.DirtyClones = dirtyClones(dir)
	}
	if len(c.Rules) > 0 && clip != nil {
		var lines []string
		for _, r := range c.Rules {
			lines = append(lines, r.Rule)
		}
		if err := clip(strings.Join(lines, "\n") + "\n"); err != nil {
			return c, fmt.Errorf("clipboard: %w", err)
		}
	}
	return c, nil
}

// dirtyClones lists git checkouts one level below dir that have uncommitted
// changes or commits not on their upstream. No upstream means no unpushed
// check. Errors from git count as clean: this is a hint, not a gate.
func dirtyClones(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !isDir(filepath.Join(dir, e.Name(), ".git")) {
			continue
		}
		repo := filepath.Join(dir, e.Name())
		if gitOut(repo, "status", "--porcelain") != "" {
			out = append(out, e.Name())
			continue
		}
		if n := gitOut(repo, "rev-list", "--count", "@{upstream}..HEAD"); n != "" && n != "0" {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func gitOut(repo string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// copyqCopy puts text on the clipboard through copyq (the user's clipboard
// manager), so pasted rules also land in its history.
func copyqCopy(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "copyq", "copy", "-")
	cmd.Stdin = bytes.NewReader([]byte(text))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &CmdError{Cmd: "copyq copy -", ExitCode: exitCode(err), Stderr: clip(stderr.String())}
	}
	return nil
}

// Rename moves ~/p/<ns>/<old> to ~/p/<ns>/<new> and updates the project row
// in place, so sessions, events, links and lifecycle history follow. Open
// windows keep the old tag until relaunched; the caller warns about that.
func Rename(s *Store, root, path, newName string) (string, error) {
	seg := strings.Split(path, "/")
	if len(seg) != 2 || !cleanSegment(seg[0]) || !cleanSegment(seg[1]) {
		return "", fmt.Errorf("project path must be <namespace>/<name>, got %q", path)
	}
	if !cleanSegment(newName) || strings.ContainsAny(newName, "/ \t") {
		return "", fmt.Errorf("new name must be a single clean directory name, got %q", newName)
	}
	src, dst := filepath.Join(root, path), filepath.Join(root, seg[0], newName)
	if !isDir(src) {
		return "", fmt.Errorf("no project directory %s", src)
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("%s already exists", dst)
	}
	newPath := seg[0] + "/" + newName
	if err := s.RenameProject(path, newPath, newName); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		// keep DB and disk consistent: undo the row
		_ = s.RenameProject(newPath, path, seg[1])
		return "", fmt.Errorf("rename %s: %w", src, err)
	}
	return newPath, nil
}

// resolveNamespace maps a GitHub owner to a namespace dir, asking through
// pick (and remembering the answer) when the owner is unknown. ok is false
// when the user cancelled.
func resolveNamespace(s *Store, owner string, pick func(options []string) (string, bool)) (string, bool, error) {
	if ns := s.NamespaceFor(owner); ns != "" {
		return ns, true, nil
	}
	options, err := s.Namespaces()
	if err != nil {
		return "", false, err
	}
	ns, ok := pick(options)
	if !ok || ns == "" {
		return "", false, nil
	}
	return ns, true, s.SetNamespaceFor(owner, ns)
}

// issuePrompt is Claude's first message in a project created from an issue:
// intent and a plan, no code yet. The user is at the keyboard.
func issuePrompt(url string) string {
	return fmt.Sprintf(`This project was created from %s. Read the issue (gh issue view), write one sentence of intent into CLAUDE.md under the title, propose a short plan as a numbered list, and stop. Do not change any code yet.`, url)
}

// contextText summarises one project for the "context" verb: status, ball,
// links with their state, live sessions, last activity.
func contextText(s *Store, path string) (string, error) {
	ps, err := s.ListProjects()
	if err != nil {
		return "", err
	}
	var p Project
	for _, x := range ps {
		if x.Path == path {
			p = x
		}
	}
	if p.Path == "" {
		return "", fmt.Errorf("unknown project %q", path)
	}
	var b strings.Builder
	status := "ongoing"
	if p.Archived {
		status = "archived"
	}
	fmt.Fprintf(&b, "%s · %s", p.Path, status)
	if p.Description != "" {
		fmt.Fprintf(&b, "\n%s", p.Description)
	}
	if p.Ball != "" {
		fmt.Fprintf(&b, " · ball: %s", p.Ball)
	}
	if p.LastActive != "" {
		fmt.Fprintf(&b, " · last active %s", p.LastActive)
	}
	b.WriteString("\n")
	rows, err := s.db.Query(`select l.url, l.merged, l.closed_at is not null from link l join project pr on pr.id = l.project_id
		where pr.path = ? order by l.id desc limit 8`, path)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		var merged, closed bool
		if err := rows.Scan(&u, &merged, &closed); err != nil {
			return "", err
		}
		state := "open"
		if merged {
			state = "merged"
		} else if closed {
			state = "closed"
		}
		fmt.Fprintf(&b, "%s  %s\n", state, u)
	}
	var live int
	cutoff := time.Now().Add(-staleSession).UTC().Format(time.RFC3339)
	if err := s.db.QueryRow(`select count(*) from session_state ss join project pr on pr.id = ss.project_id where pr.path = ? and ss.since > ?`, path, cutoff).Scan(&live); err != nil {
		return "", err
	}
	if live > 0 {
		fmt.Fprintf(&b, "%d live session(s)\n", live)
	}
	return b.String(), nil
}

// reopenIfArchived writes a reopened event when the project's latest
// lifecycle event is archived; opening an archived project is the reopen.
func reopenIfArchived(s *Store, path string) (bool, error) {
	var kind string
	err := s.db.QueryRow(`select pe.kind from project_event pe join project p on p.id = pe.project_id
		where p.path = ? order by pe.occurred_at desc, pe.id desc limit 1`, path).Scan(&kind)
	if err != nil {
		if errors.Is(err, errNoRows) {
			return false, nil
		}
		return false, err
	}
	switch kind {
	case "archived":
		return true, s.ProjectEvent(path, "reopened", "")
	case "snoozed":
		// opening a postponed project wakes it, expired or not
		return true, s.ProjectEvent(path, "woken", "")
	}
	return false, nil
}

// Postpone hides a project from the menu for days days (a "snoozed" event
// with the wake time as detail). It comes back by itself, or earlier when
// Claude or a reviewer waits on the user, or when it is opened.
func Postpone(s *Store, path string, days int, now time.Time) (time.Time, error) {
	if days <= 0 {
		return time.Time{}, fmt.Errorf("postpone: days must be positive, got %d", days)
	}
	until := now.AddDate(0, 0, days)
	return until, s.ProjectEvent(path, "snoozed", until.UTC().Format(time.RFC3339))
}
