//go:build record

package main

// Records fixtures for TestRecordedRounds from real PRs, read-only:
//
//	CR_RECORD="<pr-url> ..." go test -tags record -run TestRecord .
//
// Every gh call goes through readOnlyGh, and nothing here builds a tickHost,
// so recording can neither post nor start a round. Each fixture is the PR as
// tick would have seen it when settling the session's last round: GitHub's
// answers cut to five minutes after the round's last post, kept to the
// fields this tool decodes, with owner, repo, logins, branches and shas
// renamed. The fixture's want is left empty for a human to fill in.

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // renames shas, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// readOnlyGh runs gh only for calls that cannot write: `gh api` reads, and
// GraphQL queries. It is the only gh the recorder gets, so recording from
// real PRs can never post, dismiss or approve, whatever the code under it does.
func readOnlyGh(args ...string) (string, error) {
	if err := checkReadOnly(args); err != nil {
		return "", err
	}
	out, err := exec.Command("gh", args...).Output() //nolint:gosec // args passed checkReadOnly
	return string(out), err
}

const settleDelay = 5 * time.Minute

func TestRecord(t *testing.T) {
	urls := strings.Fields(os.Getenv("CR_RECORD"))
	if len(urls) == 0 {
		t.Skip("CR_RECORD is empty")
	}
	home, _ := os.UserHomeDir()
	login, err := readOnlyGh("api", "user", "--jq", ".login")
	if err != nil {
		t.Fatal(err)
	}
	existing, _ := filepath.Glob("testdata/rounds/*.json")
	n := len(existing)
	for _, u := range urls {
		pr, err := ParseTarget(u)
		if err != nil {
			t.Fatal(err)
		}
		n++
		f, err := recordRound(filepath.Join(home, ".claude"), pr, strings.TrimSpace(login), n)
		if err != nil {
			t.Logf("%s: skipped: %v", u, err)
			n--
			continue
		}
		b, _ := json.MarshalIndent(f, "", "  ")
		path := fmt.Sprintf("testdata/rounds/%03d.json", n)
		if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s -> %s", u, path)
	}
}

// roundStart is when the session's last round prompt was sent.
func roundStart(claudeDir, id string) (time.Time, error) {
	files, _ := filepath.Glob(filepath.Join(claudeDir, "projects", "*", id+".jsonl"))
	if len(files) == 0 {
		return time.Time{}, fmt.Errorf("no transcript")
	}
	fh, err := os.Open(files[0])
	if err != nil {
		return time.Time{}, err
	}
	defer fh.Close()
	var last time.Time
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e struct {
			Type      string          `json:"type"`
			Timestamp time.Time       `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "user" {
			continue
		}
		m := string(e.Message)
		if strings.Contains(m, "adversarial code review of") || strings.Contains(m, "New review round for") {
			last = e.Timestamp
		}
	}
	if last.IsZero() {
		return last, fmt.Errorf("no round prompt in the transcript")
	}
	return last, sc.Err()
}

// recorder answers the adapter's gh calls from GitHub as it stood at `at`.
type recorder struct {
	pr       PR
	user     string
	at       time.Time
	head     string
	answers  map[string]string
	pullJSON map[string]any
}

func (r *recorder) gh(args ...string) (string, error) {
	out, err := readOnlyGh(args...)
	if err != nil {
		return out, err
	}
	key := strings.Join(args, " ")
	switch {
	case strings.HasSuffix(key, fmt.Sprintf("pulls/%d", r.pr.Number)):
		out = r.cutPull(out)
	case strings.HasSuffix(key, "/reviews"):
		out = cutList(out, "submitted_at", r.at, []string{"id", "user", "state", "commit_id", "submitted_at", "html_url"})
	case strings.HasSuffix(key, "/comments"):
		out = cutList(out, "created_at", r.at, []string{"id", "user", "created_at"})
	case args[1] == "graphql":
		out, err = r.cutThreads(out)
	case strings.Contains(key, "/compare/"):
		out = keep(out, []string{"status", "ahead_by"})
	}
	r.answers[key] = out
	return out, err
}

func (r *recorder) cutPull(out string) string {
	var p map[string]any
	_ = json.Unmarshal([]byte(out), &p)
	state, merged := "open", false
	if s, _ := p["closed_at"].(string); s != "" {
		if c, _ := time.Parse(time.RFC3339, s); !c.After(r.at) {
			state = "closed"
			merged = p["merged_at"] != nil
		}
	}
	b, _ := json.Marshal(map[string]any{"state": state, "merged": merged,
		"head": map[string]any{"sha": r.head}, "base": map[string]any{"ref": "main"}})
	return string(b)
}

// cutList joins gh's pages, drops items dated after at, and keeps fields.
func cutList(out, dateField string, at time.Time, fields []string) string {
	var all []map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var page []map[string]any
		if dec.Decode(&page) != nil {
			break
		}
		for _, it := range page {
			if d, _ := time.Parse(time.RFC3339, fmt.Sprint(it[dateField])); d.After(at) {
				continue
			}
			kept := map[string]any{}
			for _, f := range fields {
				kept[f] = it[f]
			}
			if u, ok := it["user"].(map[string]any); ok {
				kept["user"] = map[string]any{"login": u["login"]}
			}
			all = append(all, kept)
		}
	}
	b, _ := json.Marshal(all)
	return string(b)
}

func keep(out string, fields []string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	kept := map[string]any{}
	for _, f := range fields {
		kept[f] = m[f]
	}
	b, _ := json.Marshal(kept)
	return string(b)
}

// cutThreads drops threads started after at. The adapter's query has no
// dates, so a second read-only query supplies them in the same order.
// Resolution is GitHub's today: there is no history of it.
func (r *recorder) cutThreads(out string) (string, error) {
	dated, err := readOnlyGh("api", "graphql", "-f", `query=query($owner: String!, $repo: String!, $n: Int!) {
  repository(owner: $owner, name: $repo) { pullRequest(number: $n) {
    reviewThreads(first: 100) { nodes { comments(first: 1) { nodes { createdAt } } } } } } }`,
		"-F", "owner="+r.pr.Owner, "-F", "repo="+r.pr.Repo, "-F", fmt.Sprintf("n=%d", r.pr.Number))
	if err != nil {
		return "", err
	}
	type nodes struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []json.RawMessage `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	var full, dates nodes
	if err := json.Unmarshal([]byte(out), &full); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(dated), &dates); err != nil {
		return "", err
	}
	var kept []json.RawMessage
	for i, th := range full.Data.Repository.PullRequest.ReviewThreads.Nodes {
		var d struct {
			Comments struct {
				Nodes []struct {
					CreatedAt time.Time `json:"createdAt"`
				} `json:"nodes"`
			} `json:"comments"`
		}
		_ = json.Unmarshal(dates.Data.Repository.PullRequest.ReviewThreads.Nodes[i], &d)
		if len(d.Comments.Nodes) > 0 && d.Comments.Nodes[0].CreatedAt.After(r.at) {
			continue
		}
		kept = append(kept, th)
	}
	full.Data.Repository.PullRequest.ReviewThreads.Nodes = kept
	b, _ := json.Marshal(full)
	return string(b), nil
}

// settleTime is five minutes after the user's last post since the round
// started, or half an hour after the start when the round posted nothing.
func settleTime(pr PR, user string, start time.Time) (time.Time, string, error) {
	at, head := start.Add(30*time.Minute), ""
	for _, ep := range []string{"reviews", "comments"} {
		out, err := readOnlyGh("api", "--paginate", fmt.Sprintf("repos/%s/%s/pulls/%d/%s", pr.Owner, pr.Repo, pr.Number, ep))
		if err != nil {
			return at, "", err
		}
		items := []ghItem{}
		dec := json.NewDecoder(strings.NewReader(out))
		for dec.More() {
			var page []ghItem
			if dec.Decode(&page) != nil {
				break
			}
			items = append(items, page...)
		}
		for _, it := range items {
			d := it.CreatedAt
			if ep == "reviews" {
				d = it.SubmittedAt
			}
			if !strings.EqualFold(it.User.Login, user) || !d.After(start) || d.After(start.Add(2*time.Hour)) {
				continue
			}
			if t := d.Add(settleDelay); t.After(at) || head == "" {
				at = t
			}
			if ep == "reviews" && it.CommitID != "" {
				head = it.CommitID
			}
		}
	}
	return at, head, nil
}

// headAt is the head a round reviewed, else the newest commit dated by then.
func headAt(pr PR, at time.Time, reviewed string) (string, error) {
	if reviewed != "" {
		return reviewed, nil
	}
	out, err := readOnlyGh("api", "--paginate", fmt.Sprintf("repos/%s/%s/pulls/%d/commits", pr.Owner, pr.Repo, pr.Number))
	if err != nil {
		return "", err
	}
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	_ = json.Unmarshal([]byte(out), &commits)
	head := ""
	for _, c := range commits {
		if !c.Commit.Committer.Date.After(at) || head == "" {
			head = c.SHA
		}
	}
	return head, nil
}

func recordRound(claudeDir string, pr PR, user string, n int) (recordedRound, error) {
	start, err := roundStart(claudeDir, pr.SessionID())
	if err != nil {
		return recordedRound{}, err
	}
	msg, err := LastMessage(claudeDir, pr.SessionID())
	if err != nil {
		return recordedRound{}, err
	}
	at, reviewed, err := settleTime(pr, user, start)
	if err != nil {
		return recordedRound{}, err
	}
	head, err := headAt(pr, start, reviewed)
	if err != nil {
		return recordedRound{}, err
	}
	rec := &recorder{pr: pr, user: user, at: at, head: head, answers: map[string]string{}}
	g := GitHub{Gh: rec.gh, User: user}
	round := Round{PR: pr.URL(), Head: head, StartedAt: start}
	if _, err := g.RoundFacts(pr, round); err != nil {
		return recordedRound{}, err
	}
	if _, _, _, err := g.Standing(pr); err != nil {
		return recordedRound{}, err
	}
	author, _ := readOnlyGh("api", fmt.Sprintf("repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number), "--jq", ".user.login")
	return rename(pr, user, strings.TrimSpace(author), n, recordedRound{
		Round:       recordedRoundRow{Head: head, StartedAt: start},
		SettleAt:    at,
		LastMessage: synthMessage(ReadOutcome(msg), pr),
		Gh:          rec.answers,
	}), nil
}

// synthMessage keeps what tick reads from the round's last message, and
// none of its prose.
func synthMessage(o Outcome, pr PR) string {
	switch o.Kind {
	case OutcomeApprove:
		return fmt.Sprintf("(prose omitted)\n`! claude-review approve %s --at %s`", o.PrURL, o.SHA)
	case OutcomeQuestion:
		return "(prose omitted)\n\n" + questionLine
	case OutcomeNothing:
		return "(prose omitted)\n\n" + nothingLine
	}
	return "(prose omitted)"
}

var shaRe = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

// rename replaces everything that names the real PR.
func rename(pr PR, user, author string, n int, f recordedRound) recordedRound {
	fake := PR{Owner: "acme", Repo: "widgets", Number: n}
	logins := map[string]string{strings.ToLower(author): "author"}
	logins[strings.ToLower(user)] = "me" // after author: on the user's own PR they are one login
	loginRe := regexp.MustCompile(`"login":"([^"]*)"`)
	fix := func(s string) string {
		s = shaRe.ReplaceAllStringFunc(s, fakeSHA)
		s = strings.ReplaceAll(s, fmt.Sprintf("%s/%s/pull/%d", pr.Owner, pr.Repo, pr.Number), fake.Owner+"/"+fake.Repo+fmt.Sprintf("/pull/%d", n))
		s = strings.ReplaceAll(s, fmt.Sprintf("repos/%s/%s/pulls/%d", pr.Owner, pr.Repo, pr.Number), fmt.Sprintf("repos/acme/widgets/pulls/%d", n))
		s = strings.ReplaceAll(s, fmt.Sprintf("repos/%s/%s/", pr.Owner, pr.Repo), "repos/acme/widgets/")
		s = strings.ReplaceAll(s, "owner="+pr.Owner+" ", "owner=acme ")
		s = strings.ReplaceAll(s, "repo="+pr.Repo+" ", "repo=widgets ")
		s = strings.ReplaceAll(s, fmt.Sprintf("n=%d", pr.Number), fmt.Sprintf("n=%d", n))
		return loginRe.ReplaceAllStringFunc(s, func(m string) string {
			l := strings.ToLower(loginRe.FindStringSubmatch(m)[1])
			if _, ok := logins[l]; !ok {
				logins[l] = fmt.Sprintf("reviewer%d", len(logins)-1)
			}
			return `"login":"` + logins[l] + `"`
		})
	}
	gh := map[string]string{}
	for k, v := range f.Gh {
		gh[fix(k)] = fix(v)
	}
	f.Gh = gh
	f.PR = fake.URL()
	f.Round.Head = fakeSHA(f.Round.Head)
	f.LastMessage = fix(f.LastMessage)
	return f
}

func fakeSHA(s string) string {
	h := sha1.Sum([]byte("claude-review fixture:" + s)) //nolint:gosec // renaming only
	return hex.EncodeToString(h[:])
}
