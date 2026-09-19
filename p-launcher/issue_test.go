package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextItemsParsesBothBoxStyles(t *testing.T) {
	section := `
Progress: 1/3.

- [x] done one
- [ ] open one
      that wrapped
- [] typo box
- not an item
- [ ]
`
	got := nextItems(section)
	want := []nextItem{
		{Text: "done one", Done: true},
		{Text: "open one that wrapped", Done: false},
		{Text: "typo box", Done: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSpliceBlockKeepsTextOutsideTheMarkers(t *testing.T) {
	block := issueBlock([]nextItem{{Text: "one"}})
	body := "Intent paragraph.\n\n" + issueBlock([]nextItem{{Text: "old", Done: true}}) + "\n\nA human wrote this below.\n"
	got := spliceBlock(body, block)
	if !strings.HasPrefix(got, "Intent paragraph.") {
		t.Errorf("intent lost:\n%s", got)
	}
	if !strings.Contains(got, "A human wrote this below.") {
		t.Errorf("text below the block lost:\n%s", got)
	}
	if strings.Contains(got, "old") {
		t.Errorf("stale item survived:\n%s", got)
	}
	if strings.Count(got, nextOpen) != 1 || strings.Count(got, nextClose) != 1 {
		t.Errorf("markers duplicated:\n%s", got)
	}
}

func TestSpliceBlockAppendsWhenAbsent(t *testing.T) {
	got := spliceBlock("Only prose.\n", issueBlock(nil))
	if !strings.HasPrefix(got, "Only prose.") || !strings.Contains(got, nextOpen) {
		t.Errorf("got:\n%s", got)
	}
}

func TestIntentFromSkipsHeadingAndPlaceholder(t *testing.T) {
	if got := intentFrom("# proj\n\nWhat this is for,\nacross two lines.\n\nMore.\n"); got != "What this is for, across two lines." {
		t.Errorf("got %q", got)
	}
	if got := intentFrom("# proj\n\n<One paragraph: what this is for. Replace this.>\n"); got != "" {
		t.Errorf("placeholder became an intent: %q", got)
	}
}

// fakeGitHub is the remote as a map of URL to title and body.
type fakeGitHub struct {
	issues   map[string][2]string
	comments map[string][]string
	n        int
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{issues: map[string][2]string{}, comments: map[string][]string{}}
}

func (g *fakeGitHub) deps(root string) issueDeps {
	return issueDeps{
		repo: "o/r", root: root, now: ts("2026-09-19T10:00:00Z"),
		create: func(repo, title, body string) (string, error) {
			g.n++
			url := "https://github.com/" + repo + "/issues/" + string(rune('0'+g.n))
			g.issues[url] = [2]string{title, body}
			return url, nil
		},
		get: func(url string) (string, string, error) {
			v := g.issues[url]
			return v[0], v[1], nil
		},
		edit: func(url, title, body string) error {
			g.issues[url] = [2]string{title, body}
			return nil
		},
		comment: func(url, body string) error {
			g.comments[url] = append(g.comments[url], body)
			return nil
		},
	}
}

func issueFixture(t *testing.T, handoff string) (*Store, *fakeGitHub, string) {
	t.Helper()
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIssuePref("m/a", "yes"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "m", "a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# a\n\nWhy a exists.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if handoff != "" {
		if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte(handoff), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, newFakeGitHub(), root
}

const handoffOne = `# a handoff

Updated 2026-09-19.
Last action: Did the first thing.

## Next

Progress: 1/2.

- [x] first
- [ ] second
`

func TestSyncIssueCreatesThenStaysQuiet(t *testing.T) {
	s, g, root := issueFixture(t, handoffOne)
	d := g.deps(root)

	res, err := syncIssues(s, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 {
		t.Fatalf("got %+v, want one created", res)
	}
	url, ok, err := s.PrimaryIssue("m/a")
	if err != nil || !ok {
		t.Fatalf("primary issue not registered: %v %v", ok, err)
	}
	title, body := g.issues[url][0], g.issues[url][1]
	if title != "a (1/2)" {
		t.Errorf("title = %q, want the progress counter", title)
	}
	if !strings.HasPrefix(body, "Why a exists.") {
		t.Errorf("intent missing from body:\n%s", body)
	}
	if !strings.Contains(body, "- [x] first") || !strings.Contains(body, "- [ ] second") {
		t.Errorf("checklist missing:\n%s", body)
	}
	if len(g.comments[url]) != 0 {
		t.Errorf("creation should not also comment: %v", g.comments[url])
	}

	// Nothing changed: a second pass must write nothing at all.
	res, err = syncIssues(s, d)
	if err != nil {
		t.Fatal(err)
	}
	if res != (issueResult{}) {
		t.Errorf("idempotence broken: %+v", res)
	}
}

func TestSyncIssueUpdatesAndCommentsOnChange(t *testing.T) {
	s, g, root := issueFixture(t, handoffOne)
	d := g.deps(root)
	if _, err := syncIssues(s, d); err != nil {
		t.Fatal(err)
	}
	url, _, _ := s.PrimaryIssue("m/a")

	next := strings.Replace(handoffOne, "Last action: Did the first thing.", "Last action: Did the second thing.", 1)
	next = strings.Replace(next, "Progress: 1/2.", "Progress: 2/2.", 1)
	next = strings.Replace(next, "- [ ] second", "- [x] second", 1)
	if err := os.WriteFile(filepath.Join(root, "m", "a", "HANDOFF.md"), []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := syncIssues(s, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Commented != 1 {
		t.Fatalf("got %+v, want one update and one comment", res)
	}
	if g.issues[url][0] != "a (2/2)" {
		t.Errorf("title = %q", g.issues[url][0])
	}
	if !strings.Contains(g.issues[url][1], "- [x] second") {
		t.Errorf("checklist not updated:\n%s", g.issues[url][1])
	}
	if got := g.comments[url]; len(got) != 1 || !strings.HasPrefix(got[0], "Did the second thing.") {
		t.Fatalf("comment = %v", got)
	}
	if !strings.Contains(g.comments[url][0], "Progress: 2/2") {
		t.Errorf("comment lacks progress: %q", g.comments[url][0])
	}
	// n/n does not close the issue: automatic progression is not agreed.
	if _, err := syncIssues(s, d); err != nil {
		t.Fatal(err)
	}
	if len(g.comments[url]) != 1 {
		t.Errorf("same Last action commented twice: %v", g.comments[url])
	}
}

func TestSyncIssueSkipsProjectWithoutHandoff(t *testing.T) {
	s, g, root := issueFixture(t, "")
	res, err := syncIssues(s, g.deps(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Created != 0 {
		t.Fatalf("got %+v, want one skip and no issue", res)
	}
}

func TestSyncIssueIgnoresProjectsThatDidNotOptIn(t *testing.T) {
	s, g, root := issueFixture(t, handoffOne)
	if err := s.SetIssuePref("m/a", "no"); err != nil {
		t.Fatal(err)
	}
	res, err := syncIssues(s, g.deps(root))
	if err != nil {
		t.Fatal(err)
	}
	if res != (issueResult{}) || len(g.issues) != 0 {
		t.Fatalf("opt-out project synced: %+v %v", res, g.issues)
	}
}

func TestIssuePrefRoundTrip(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertProjects(found("m/a")); err != nil {
		t.Fatal(err)
	}
	pref, asked, err := s.IssuePref("m/a")
	if err != nil || pref != "" || !asked.IsZero() {
		t.Fatalf("fresh project: %q %v %v", pref, asked, err)
	}
	if err := s.MarkIssueAsked("m/a", ts("2026-09-19T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if _, asked, _ = s.IssuePref("m/a"); asked.IsZero() {
		t.Error("asked-at not stored")
	}
	if pref, _, _ := mustPref(t, s, "m/a", "yes"); pref != "yes" {
		t.Errorf("pref = %q", pref)
	}
	if _, _, err := s.IssuePref("m/nope"); err != nil {
		t.Errorf("unknown project should read as unset, got %v", err)
	}
}

func mustPref(t *testing.T, s *Store, path, set string) (string, time.Time, error) {
	t.Helper()
	if err := s.SetIssuePref(path, set); err != nil {
		t.Fatal(err)
	}
	return s.IssuePref(path)
}
