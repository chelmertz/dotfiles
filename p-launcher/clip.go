package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Clipboard-aware menu: F5 with a GitHub issue/PR URL in the clipboard
// offers to open the project that owns it or to create one from the issue.
// Design: 2026-09-08-p-launcher-clipboard-design.md.

// readClipboard returns the X clipboard text, "" on any failure or delay:
// the menu must stay instant, so 300 ms is the whole budget.
func readClipboard() string {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := runCmd(ctx, "xclip", "-o", "-selection", "clipboard")
	if err != nil {
		return ""
	}
	return string(out)
}

type ghRef struct {
	Kind        string // github_issue | github_pr
	Owner, Repo string
	Number      int
	URL         string // canonical, no fragment or query
}

var ghURLRe = regexp.MustCompile(`https://github\.com/([\w.-]+)/([\w.-]+)/(issues|pull)/(\d+)`)

// parseGitHubURL recognises the first GitHub issue or pull request URL in s.
func parseGitHubURL(s string) (ghRef, bool) {
	m := ghURLRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return ghRef{}, false
	}
	n, err := strconv.Atoi(m[4])
	if err != nil || n <= 0 {
		return ghRef{}, false
	}
	kind := "github_pr"
	if m[3] == "issues" {
		kind = "github_issue"
	}
	return ghRef{Kind: kind, Owner: m[1], Repo: m[2], Number: n, URL: m[0]}, true
}

// Short is "owner/repo#12".
func (r ghRef) Short() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug makes a directory-safe name from a title: lowercase, runs of anything
// but a-z0-9 become one dash, cut at 40 chars, no dashes at the ends.
func slug(title string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// issueProjectName is "<n>-<slug>", or "<repo>-<n>" when no title is known.
func issueProjectName(n int, title, repo string) string {
	if sl := slug(title); sl != "" {
		return fmt.Sprintf("%d-%s", n, sl)
	}
	return fmt.Sprintf("%s-%d", strings.ToLower(repo), n)
}
