// Package ids holds the identifiers claude-review passes between Claude
// Code, GitHub and its ledger. Each is a struct with one unexported field,
// so code outside this package can make one neither by conversion nor by
// literal: a value exists only where that kind of id enters the program,
// through a constructor named for that place. A session id handed to
// `claude attach`, or a GitHub review id handed to the ledger, does not
// compile. Only the zero value can be made anywhere, and every id reports
// it with IsZero.
//
// Where each id enters:
//   - SessionID: derived from a name (SessionFor), random for a
//     throwaway session (NewSession), or read from Claude Code's files
//     (ParseSession, UnmarshalJSON).
//   - WorkerKey: a SessionID's Key, or a roster.json key (UnmarshalText).
//   - Token: minted (NewToken), read back from the ledger (Scan), or
//     presented on an Approve link (ParseToken).
//   - RoundID: a ledger row (Scan).
//   - ReviewID, CommentID, IssueCommentID: a GitHub response
//     (UnmarshalJSON); a CommentID also from a command-line flag (Set).
package ids

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// urlNamespace is the RFC 4122 namespace for URLs.
const urlNamespace = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

var sessionPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// SessionID is a Claude Code session id, a lowercase UUID.
type SessionID struct{ v string }

// SessionFor is the session id for name: UUIDv5 of name in the URL
// namespace. Deterministic, so the same name always names the same session.
func SessionFor(name string) SessionID {
	return SessionID{uuidV5(urlNamespace, name)}
}

// NewSession is a random (version 4) session id, for a throwaway session.
func NewSession() SessionID {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return SessionID{fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])}
}

// ParseSession reads a session id Claude Code wrote.
func ParseSession(s string) (SessionID, error) {
	if !sessionPattern.MatchString(s) {
		return SessionID{}, fmt.Errorf("not a session id: %q", s)
	}
	return SessionID{s}, nil
}

// UnmarshalJSON reads a session id from Claude Code's JSON. An empty
// string is the zero value, as an absent field is.
func (s *SessionID) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw == "" {
		*s = SessionID{}
		return nil
	}
	id, err := ParseSession(raw)
	*s = id
	return err
}

func (s SessionID) String() string { return s.v }
func (s SessionID) IsZero() bool   { return s.v == "" }

// Key is the roster key a worker for s has: the first 8 characters.
func (s SessionID) Key() WorkerKey {
	if s.IsZero() {
		return WorkerKey{}
	}
	return WorkerKey{s.v[:8]}
}

var workerKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)

// WorkerKey is a background worker's roster key, the first 8 characters of
// its session id. `claude attach` and `claude stop` take only this and fail
// on the full session id ("No job matching", Claude Code 2.1.314).
type WorkerKey struct{ v string }

// UnmarshalText reads a roster.json key; anything but 8 hex characters is
// refused, and never reaches a shell.
func (k *WorkerKey) UnmarshalText(b []byte) error {
	if !workerKeyPattern.Match(b) {
		return fmt.Errorf("not a worker id: %q", b)
	}
	k.v = string(b)
	return nil
}

func (k WorkerKey) String() string { return k.v }
func (k WorkerKey) IsZero() bool   { return k.v == "" }

const tokenBytes = 24

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{48}$`)

// Token is the one-time secret an Approve link carries.
type Token struct{ v string }

// NewToken mints a token from crypto/rand.
func NewToken() (Token, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return Token{}, err
	}
	return Token{hex.EncodeToString(b)}, nil
}

// ParseToken reads a token presented on an Approve link.
func ParseToken(s string) (Token, error) {
	if !tokenPattern.MatchString(s) {
		return Token{}, errors.New("not an approve token")
	}
	return Token{s}, nil
}

func (t Token) String() string { return t.v }
func (t Token) IsZero() bool   { return t.v == "" }

// Value writes t to the ledger.
func (t Token) Value() (driver.Value, error) { return t.v, nil }

// Scan reads a token back from the ledger.
func (t *Token) Scan(src any) error {
	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("token: scanning %T", src)
	}
	tok, err := ParseToken(s)
	*t = tok
	return err
}

// RoundID is a ledger row of one review round.
type RoundID struct{ v int64 }

func (r RoundID) String() string { return strconv.FormatInt(r.v, 10) }
func (r RoundID) IsZero() bool   { return r.v == 0 }

// Value writes r to the ledger.
func (r RoundID) Value() (driver.Value, error) { return r.v, nil }

// Scan reads a round id from the ledger.
func (r *RoundID) Scan(src any) error {
	n, ok := src.(int64)
	if !ok || n <= 0 {
		return fmt.Errorf("round id: scanning %v", src)
	}
	r.v = n
	return nil
}

// ReviewID is a GitHub pull request review's REST id.
type ReviewID struct{ v int64 }

func (r *ReviewID) UnmarshalJSON(b []byte) error { return unmarshalGitHubID(b, &r.v) }
func (r ReviewID) String() string                { return strconv.FormatInt(r.v, 10) }
func (r ReviewID) IsZero() bool                  { return r.v == 0 }

// CommentID is a GitHub pull request review comment's REST id: an inline
// comment or a reply in its thread.
type CommentID struct{ v int64 }

func (c *CommentID) UnmarshalJSON(b []byte) error { return unmarshalGitHubID(b, &c.v) }
func (c CommentID) String() string                { return strconv.FormatInt(c.v, 10) }
func (c CommentID) IsZero() bool                  { return c.v == 0 }

// Before orders comments as GitHub numbers them, oldest first.
func (c CommentID) Before(o CommentID) bool { return c.v < o.v }

// Set reads a comment id from a command-line flag, as flag.Value.
func (c *CommentID) Set(s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return fmt.Errorf("not a comment id: %q", s)
	}
	c.v = n
	return nil
}

// IssueCommentID is a GitHub PR-level comment's REST id; GitHub numbers
// these apart from review comments.
type IssueCommentID struct{ v int64 }

func (c *IssueCommentID) UnmarshalJSON(b []byte) error { return unmarshalGitHubID(b, &c.v) }
func (c IssueCommentID) String() string                { return strconv.FormatInt(c.v, 10) }
func (c IssueCommentID) IsZero() bool                  { return c.v == 0 }

// unmarshalGitHubID reads a REST id: a positive number, or null for none.
func unmarshalGitHubID(b []byte, v *int64) error {
	var n *int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	switch {
	case n == nil:
		*v = 0
	case *n <= 0:
		return fmt.Errorf("not a GitHub id: %d", *n)
	default:
		*v = *n
	}
	return nil
}
