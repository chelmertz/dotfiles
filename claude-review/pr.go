package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PR identifies one GitHub pull request.
type PR struct {
	Owner  string
	Repo   string
	Number int
}

// Every entry point hands the tool one of three shapes. The character classes
// are strict on purpose: the input is reachable from a clickable link.
var (
	ghURL  = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/pull/([1-9][0-9]*)(?:[/#?].*)?$`)
	scheme = regexp.MustCompile(`^claude-review://([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([1-9][0-9]*)/?$`)
	bare   = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/([1-9][0-9]*)$`)
)

// ParseTarget accepts a GitHub PR URL, a claude-review:// URI or a bare
// owner/repo/number.
func ParseTarget(s string) (PR, error) {
	for _, re := range []*regexp.Regexp{ghURL, scheme, bare} {
		m := re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		if allDots(m[1]) || allDots(m[2]) {
			// GitHub allows no such name, and in a gh api path it climbs.
			break
		}
		n, err := strconv.Atoi(m[3])
		if err != nil {
			return PR{}, fmt.Errorf("claude-review: PR number out of range in %q", s)
		}
		// GitHub names are case-insensitive; the canonical URL and the session
		// id derive from them, so normalise here or a differently-cased link
		// would start a second session for the same PR.
		return PR{Owner: strings.ToLower(m[1]), Repo: strings.ToLower(m[2]), Number: n}, nil
	}
	return PR{}, fmt.Errorf("claude-review: expected a GitHub PR URL, claude-review://owner/repo/number or owner/repo/number, got %q", s)
}

// allDots is a name made of dots only, such as "." or "..".
func allDots(s string) bool {
	return strings.Trim(s, ".") == ""
}

// URL is the canonical PR URL. It is also the name the session id derives
// from, so its format is a contract: change it and every existing review
// session is orphaned.
func (p PR) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", p.Owner, p.Repo, p.Number)
}

// Slug is the short human form used in session names.
func (p PR) Slug() string {
	return fmt.Sprintf("%s/%s#%d", p.Owner, p.Repo, p.Number)
}

// urlNamespace is the RFC 4122 namespace for URLs.
const urlNamespace = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

// SessionID is the Claude Code session id for this PR: UUIDv5 of the
// canonical URL. Deterministic, so a second click resumes the first click's
// session without any state kept by this tool.
func (p PR) SessionID() string {
	return uuidV5(urlNamespace, p.URL())
}
