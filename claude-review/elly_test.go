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
		method, path, auth, contentType string
		body                            map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v0/prs" {
			_, _ = io.WriteString(w, `[{"Url":"https://github.com/acme/widgets/pull/42","RepoOwner":"acme","HeadRefOid":"h1","ReviewRequestedFromUsers":["me"],"ThreadsActionable":2,"LastUpdated":"2026-10-31T12:00:00Z"}]`)
			return
		}
		if r.URL.Path == "/api/v0/config/status" {
			_, _ = io.WriteString(w, `{"configured":true,"username":"me"}`)
			return
		}
		got.method, got.path, got.auth, got.contentType = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
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
	// elly refuses a write with a body that is not JSON, unless it carries
	// a bearer token; the header keeps it from resting on that exemption.
	if got.contentType != "application/json" {
		t.Errorf("PUT sent Content-Type %q", got.contentType)
	}
	if got.body["state"] != stateNeedsYou || got.body["expiresAt"] != "2026-10-31T12:00:00Z" || !strings.Contains(asJSON(got.body["links"]), `"label":"Open session"`) {
		t.Errorf("PUT body %+v", got.body)
	}

	if err := e.Delete(widgetsPR.URL()); err != nil || got.method != "DELETE" || got.contentType != "" {
		t.Errorf("DELETE: %v %s, Content-Type %q", err, got.method, got.contentType)
	}

	if login, err := e.Username(); err != nil || login != "me" {
		t.Errorf("Username: %q %v", login, err)
	}

	if err := (Elly{Base: srv.URL, TokenFile: filepath.Join(t.TempDir(), "missing")}).Delete(widgetsPR.URL()); err == nil {
		t.Error("a missing token file reported success")
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestEllyUsername(t *testing.T) {
	for body, want := range map[string]string{
		`{"configured":true,"username":"me"}`: "me",
		`{"configured":false}`:                "",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v0/config/status" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, body)
		}))
		got, err := Elly{Base: srv.URL}.Username()
		srv.Close()
		if err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", body, got, err, want)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := (Elly{Base: srv.URL}).Username(); err == nil {
		t.Error("a 404 from elly reported a login")
	}
}

// The PR id is a contract with elly (decodePrId in its server.go): standard
// padded base64 of the canonical URL, pinned here as a literal.
func TestAnnotationURLIsStandardBase64OfThePrURL(t *testing.T) {
	got := Elly{Base: "http://e"}.annotationURL(widgetsPR.URL())
	if got != "http://e/api/v0/prs/aHR0cHM6Ly9naXRodWIuY29tL2FjbWUvd2lkZ2V0cy9wdWxsLzQy/annotations/claude-review" {
		t.Errorf("annotation URL %q", got)
	}
}
