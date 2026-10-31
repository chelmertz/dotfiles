package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// annotationSource is this tool's name in elly's annotations.
const annotationSource = "claude-review"

// Elly talks to the local elly: its PR list, and annotations written with
// the bearer token it rotates on every start.
type Elly struct {
	Base      string
	TokenFile string
	HTTP      *http.Client
}

func (e Elly) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// PRs is elly's PR list, every row whatever its score.
func (e Elly) PRs() ([]EllyPR, error) {
	resp, err := e.client().Get(e.Base + "/api/v0/prs")
	if err != nil {
		return nil, fmt.Errorf("elly: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("elly: GET /api/v0/prs: %s", resp.Status)
	}
	var prs []EllyPR
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return nil, fmt.Errorf("elly: %w", err)
	}
	return prs, nil
}

func (e Elly) annotationURL(prURL string) string {
	return e.Base + "/api/v0/prs/" + base64.StdEncoding.EncodeToString([]byte(prURL)) + "/annotations/" + annotationSource
}

func (e Elly) send(method, url string, body []byte) error {
	tok, err := os.ReadFile(e.TokenFile) //nolint:gosec // the path is elly's token file, from config
	if err != nil {
		return fmt.Errorf("elly token: %w", err)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	resp, err := e.client().Do(req)
	if err != nil {
		return fmt.Errorf("elly: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("elly: %s %s: %s %s", method, url, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Put writes pr's annotation, expiring at expires.
func (e Elly) Put(prURL string, a Annotation, expires time.Time) error {
	links := a.Links
	if links == nil {
		links = []Link{}
	}
	body, err := json.Marshal(map[string]any{"state": a.State, "note": a.Note, "links": links, "expiresAt": expires.UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return e.send(http.MethodPut, e.annotationURL(prURL), body)
}

func (e Elly) Delete(prURL string) error {
	return e.send(http.MethodDelete, e.annotationURL(prURL), nil)
}
