package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestElly(t *testing.T) {
	var got struct {
		method, path, auth string
		body               map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `[{"Url":"https://github.com/acme/widgets/pull/42","RepoOwner":"acme","HeadRefOid":"h1","ReviewRequestedFromUsers":["me"],"ThreadsActionable":2,"LastUpdated":"2026-10-31T12:00:00Z"}]`)
			return
		}
		got.method, got.path, got.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		got.body = nil
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := Elly{Base: srv.URL, TokenFile: tokenFile}

	prs, err := e.PRs()
	if err != nil || len(prs) != 1 || prs[0].HeadRefOid != "h1" || prs[0].ThreadsActionable != 2 || !prs[0].LastUpdated.Equal(t0) {
		t.Fatalf("PRs: %+v %v", prs, err)
	}

	a := Annotation{State: stateNeedsYou, Note: "look", Links: []Link{{Label: "Open session", URL: "claude-review://acme/widgets/42/attach"}}}
	if err := e.Put(widgetsPR.URL(), a, t0); err != nil {
		t.Fatal(err)
	}
	wantPath := "/api/v0/prs/" + base64.StdEncoding.EncodeToString([]byte(widgetsPR.URL())) + "/annotations/claude-review"
	if got.method != "PUT" || got.path != wantPath || got.auth != "Bearer secret" {
		t.Errorf("PUT went to %s %s with %q", got.method, got.path, got.auth)
	}
	if got.body["state"] != stateNeedsYou || got.body["expiresAt"] != "2026-10-31T12:00:00Z" || !strings.Contains(asJSON(got.body["links"]), `"label":"Open session"`) {
		t.Errorf("PUT body %+v", got.body)
	}

	if err := e.Delete(widgetsPR.URL()); err != nil || got.method != "DELETE" {
		t.Errorf("DELETE: %v %s", err, got.method)
	}

	if err := (Elly{Base: srv.URL, TokenFile: filepath.Join(t.TempDir(), "missing")}).Delete(widgetsPR.URL()); err == nil {
		t.Error("a missing token file reported success")
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
