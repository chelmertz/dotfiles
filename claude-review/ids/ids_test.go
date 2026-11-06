package ids_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chelmertz/dotfiles/claude-review/ids"
)

const full = "7e9b0cb1-bf5d-5d6b-8227-689aa3d4f93c"

// Pinned: a change in the derivation would lose every existing session.
func TestSessionFor(t *testing.T) {
	if got := ids.SessionFor("https://github.com/acme/widgets/pull/42").String(); got != "7675322f-1e4a-5085-bff1-0eeed7575ddc" {
		t.Errorf("got %s", got)
	}
}

// `claude attach` and `claude stop` match only the roster key; the full id
// fails with "No job matching" (2.1.314).
func TestKeyIsTheFirstEightCharacters(t *testing.T) {
	id, err := ids.ParseSession(full)
	if err != nil {
		t.Fatal(err)
	}
	if got := id.Key().String(); got != "7e9b0cb1" {
		t.Errorf("key %q", got)
	}
	if !(ids.SessionID{}).Key().IsZero() {
		t.Error("the zero session has a key")
	}
}

func TestParseSession(t *testing.T) {
	if id, err := ids.ParseSession(full); err != nil || id.String() != full || id.IsZero() {
		t.Errorf("%q %v", id, err)
	}
	for _, bad := range []string{"", "x; rm -rf ~", "7e9b0cb1", strings.ToUpper(full), full + " ", "7e9b0cb1-bf5d-5d6b-8227-689aa3d4f93"} {
		if id, err := ids.ParseSession(bad); err == nil {
			t.Errorf("%q read as %q", bad, id)
		}
	}
}

func TestSessionFromJSON(t *testing.T) {
	var v struct {
		ID ids.SessionID `json:"sessionId"`
	}
	if err := json.Unmarshal([]byte(`{"sessionId":"`+full+`"}`), &v); err != nil || v.ID.String() != full {
		t.Errorf("%q %v", v.ID, err)
	}
	if err := json.Unmarshal([]byte(`{"sessionId":""}`), &v); err != nil || !v.ID.IsZero() {
		t.Errorf("empty: %q %v", v.ID, err)
	}
	for _, bad := range []string{`{"sessionId":"x; rm -rf ~"}`, `{"sessionId":7}`} {
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s read", bad)
		}
	}
}

func TestNewSession(t *testing.T) {
	a, b := ids.NewSession(), ids.NewSession()
	if _, err := ids.ParseSession(a.String()); err != nil || a == b {
		t.Errorf("%s %s %v", a, b, err)
	}
	if v := a.String(); v[14] != '4' || !strings.ContainsRune("89ab", rune(v[19])) {
		t.Errorf("not a version 4 id: %s", v)
	}
}

// Roster keys are read as map keys; anything but 8 hex characters never
// reaches a shell.
func TestWorkerKeyFromRoster(t *testing.T) {
	var m map[ids.WorkerKey]int
	if err := json.Unmarshal([]byte(`{"7e9b0cb1":1}`), &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if k.String() != "7e9b0cb1" || k.IsZero() {
			t.Errorf("key %q", k)
		}
	}
	for _, bad := range []string{full, "x; rm -rf ~", "", "7E9B0CB1", "7e9b0cb"} {
		if err := json.Unmarshal([]byte(`{"`+bad+`":1}`), &m); err == nil {
			t.Errorf("%q read as a worker key", bad)
		}
	}
}

func TestToken(t *testing.T) {
	tok, err := ids.NewToken()
	if err != nil || len(tok.String()) != 48 || tok.IsZero() {
		t.Fatalf("%q %v", tok, err)
	}
	if back, err := ids.ParseToken(tok.String()); err != nil || back != tok {
		t.Errorf("round trip: %q %v", back, err)
	}
	if other, _ := ids.NewToken(); other == tok {
		t.Error("two tokens alike")
	}
	for _, bad := range []string{"", "forged", strings.Repeat("0", 47), strings.Repeat("0", 49), strings.Repeat("A", 48)} {
		if _, err := ids.ParseToken(bad); err == nil {
			t.Errorf("%q read as a token", bad)
		}
	}
	if v, err := tok.Value(); err != nil || v != tok.String() {
		t.Errorf("value %v %v", v, err)
	}
	var scanned ids.Token
	if err := scanned.Scan(tok.String()); err != nil || scanned != tok {
		t.Errorf("scan: %q %v", scanned, err)
	}
	for _, bad := range []any{"forged", int64(1), nil} {
		if err := scanned.Scan(bad); err == nil {
			t.Errorf("scanned %v", bad)
		}
	}
}

func TestRoundID(t *testing.T) {
	var r ids.RoundID
	if !r.IsZero() {
		t.Error("zero round not zero")
	}
	if err := r.Scan(int64(12)); err != nil || r.String() != "12" || r.IsZero() {
		t.Errorf("%s %v", r, err)
	}
	if v, err := r.Value(); err != nil || v != int64(12) {
		t.Errorf("value %v %v", v, err)
	}
	for _, bad := range []any{int64(0), int64(-1), "12", nil} {
		if err := r.Scan(bad); err == nil {
			t.Errorf("scanned %v", bad)
		}
	}
}

// GitHub numbers reviews, review comments and PR-level comments apart; each
// reads only from GitHub's JSON.
func TestGitHubIDsFromJSON(t *testing.T) {
	var v struct {
		R ids.ReviewID       `json:"r"`
		C ids.CommentID      `json:"c"`
		I ids.IssueCommentID `json:"i"`
	}
	if err := json.Unmarshal([]byte(`{"r":9,"c":77,"i":3}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.R.String() != "9" || v.C.String() != "77" || v.I.String() != "3" || v.R.IsZero() || v.C.IsZero() || v.I.IsZero() {
		t.Errorf("%+v", v)
	}
	if err := json.Unmarshal([]byte(`{"r":null,"c":null,"i":null}`), &v); err != nil || !v.R.IsZero() || !v.C.IsZero() || !v.I.IsZero() {
		t.Errorf("null: %+v %v", v, err)
	}
	for _, bad := range []string{`{"r":0}`, `{"c":-1}`, `{"i":0}`, `{"r":"9"}`, `{"c":"77"}`, `{"i":1.5}`} {
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s read", bad)
		}
	}
}

func TestCommentIDFlag(t *testing.T) {
	var c ids.CommentID
	if err := c.Set("77"); err != nil || c.String() != "77" {
		t.Errorf("%s %v", c, err)
	}
	for _, bad := range []string{"0", "-1", "x", ""} {
		var d ids.CommentID
		if err := d.Set(bad); err == nil || !d.IsZero() {
			t.Errorf("%q set %s", bad, d)
		}
	}
	var older, newer ids.CommentID
	_ = older.Set("5")
	_ = newer.Set("6")
	if !older.Before(newer) || newer.Before(older) || older.Before(older) {
		t.Error("Before does not order by GitHub's numbering")
	}
}
