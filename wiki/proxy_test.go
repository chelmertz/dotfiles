package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const agentCg = `/system.slice/system-wiki\x2dagent.slice/wiki-agent@run1.service`

// fakeAPI is a fake api.anthropic.com that records what reached it.
type fakeAPI struct {
	mu   sync.Mutex
	reqs []*http.Request
	body string // response body to send
	ct   string
	code int
	cut  bool // close after the body without finishing the stream
}

func (u *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.ReadAll(r.Body)
	u.mu.Lock()
	u.reqs = append(u.reqs, r)
	u.mu.Unlock()
	w.Header().Set("Content-Type", u.ct)
	w.WriteHeader(u.code)
	io.WriteString(w, u.body)
	if u.cut {
		hj, _ := w.(http.Hijacker)
		if c, _, err := hj.Hijack(); err == nil {
			c.Close()
		}
	}
}

func (u *fakeAPI) hits() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.reqs) }

type rig struct {
	t      *testing.T
	up     *fakeAPI
	state  string
	reg    string
	client *http.Client
	cg     string
}

const sseOK = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"cache_creation_input_tokens\":2000,\"cache_read_input_tokens\":30000,\"output_tokens\":1}}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":50}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	rg := &rig{t: t, state: filepath.Join(dir, "state"), reg: filepath.Join(dir, "runs"), cg: agentCg,
		up: &fakeAPI{body: sseOK, ct: "text/event-stream", code: 200}}
	os.MkdirAll(rg.reg, 0o755)
	rg.register("run1", "eversports", "ingest")
	us := httptest.NewServer(rg.up)
	t.Cleanup(us.Close)
	u, _ := url.Parse(us.URL)
	tok := filepath.Join(dir, "seat-token")
	os.WriteFile(tok, []byte("seat-secret\n"), 0o600)
	p := &Proxy{
		Upstream:  u,
		Token:     func() (string, error) { return readToken(tok) },
		Registry:  rg.reg,
		Accounts:  &Accounts{Dir: rg.state, DayLimit: 1_000_000, RunLimit: 600_000, Now: func() time.Time { return time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local) }},
		PeerGroup: func(net.Conn) (string, error) { return rg.cg, nil },
		Transport: http.DefaultTransport,
	}
	sock := filepath.Join(dir, "p.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: p, ConnContext: p.ConnContext}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	rg.client = &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return net.Dial("unix", sock) },
		DisableKeepAlives: true,
	}}
	return rg
}

func (rg *rig) register(run, org, cmd string) {
	b, _ := json.Marshal(Registration{Run: run, Org: org, Command: cmd})
	if err := os.WriteFile(filepath.Join(rg.reg, run+".json"), b, 0o644); err != nil {
		rg.t.Fatal(err)
	}
}

// goodBody mirrors the shape claude-code 2.1.325 sends as the agent.
func goodBody() map[string]any {
	return map[string]any{
		"model":              "claude-sonnet-5-5",
		"max_tokens":         128000,
		"stream":             true,
		"metadata":           map[string]any{"user_id": "x"},
		"thinking":           map[string]any{"type": "adaptive", "display": "omitted"},
		"context_management": map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}},
		"output_config":      map[string]any{"effort": "medium"},
		"system":             []any{map[string]any{"type": "text", "text": "You are Claude Code", "cache_control": map[string]any{"type": "ephemeral"}}},
		"tools": []any{
			map[string]any{"name": "Read", "description": "d", "input_schema": map[string]any{}},
			map[string]any{"name": "DeferredToolPlaceholder", "description": "d", "input_schema": map[string]any{}, "defer_loading": true},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "Read in/item.md"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{"file_path": "in/item.md"}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{map[string]any{"type": "text", "text": "# Decisions"}}}}},
			map[string]any{"role": "system", "content": "# Environment"},
		},
	}
}

func (rg *rig) post(path string, body any, hdr map[string]string) (*http.Response, string) {
	rg.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "http://wiki-proxy.invalid"+path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "placeholder-not-a-key")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := rg.client.Do(req)
	if err != nil {
		// A stream cut mid-way reaches the client as an error.
		return &http.Response{StatusCode: 0}, ""
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(out)
}

func (rg *rig) usage(run string) Usage {
	rg.t.Helper()
	var u Usage
	b, err := os.ReadFile(filepath.Join(rg.state, "usage", run+".json"))
	if err != nil {
		rg.t.Fatal(err)
	}
	json.Unmarshal(b, &u)
	return u
}

func (rg *rig) dayUsed() int64 {
	var d dayBudget
	b, _ := os.ReadFile(filepath.Join(rg.state, "budget.json"))
	json.Unmarshal(b, &d)
	return d.Used
}

func TestForwardsWithSeatTokenAndPinnedHost(t *testing.T) {
	rg := newRig(t)
	resp, body := rg.post("/v1/messages?beta=true", goodBody(), map[string]string{"Host": "evil.example"})
	if resp.StatusCode != 200 || !strings.Contains(body, "message_stop") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if rg.up.hits() != 1 {
		t.Fatalf("fakeAPI hits = %d", rg.up.hits())
	}
	got := rg.up.reqs[0]
	if a := got.Header.Get("Authorization"); a != "Bearer seat-secret" {
		t.Errorf("Authorization = %q", a)
	}
	if k := got.Header.Get("X-Api-Key"); k != "" {
		t.Errorf("placeholder key forwarded: %q", k)
	}
	if b := got.Header.Get("Anthropic-Beta"); b != "claude-code-20250219,interleaved-thinking-2025-05-14,oauth-2025-04-20" {
		t.Errorf("Anthropic-Beta = %q", b)
	}
	if got.Host == "evil.example" || got.Host == "wiki-proxy.invalid" {
		t.Errorf("Host forwarded as %q", got.Host)
	}
	if got.URL.RawQuery != "beta=true" {
		t.Errorf("query = %q", got.URL.RawQuery)
	}
}

func TestSettlesStreamUsageToRunAndOrg(t *testing.T) {
	rg := newRig(t)
	rg.post("/v1/messages?beta=true", goodBody(), nil)
	u := rg.usage("run1")
	want := Usage{Run: "run1", Org: "eversports", Command: "ingest", Model: "claude-sonnet-5-5", Requests: 1,
		Input: 100, CacheWrite: 2000, CacheRead: 30000, Output: 50, Charged: 2150}
	if fmt.Sprint(u) != fmt.Sprint(want) {
		t.Fatalf("usage\n got %+v\nwant %+v", u, want)
	}
	if rg.dayUsed() != 2150 {
		t.Fatalf("day used = %d, want 2150", rg.dayUsed())
	}
}

func TestCutStreamKeepsReservation(t *testing.T) {
	rg := newRig(t)
	rg.up.body = strings.SplitAfter(sseOK, "\n\n")[0] // message_start only
	rg.up.cut = true
	rg.post("/v1/messages?beta=true", goodBody(), nil)
	u := rg.usage("run1")
	if u.Unsettled != 1 || u.Charged < 128000 {
		t.Fatalf("cut stream settled: %+v", u)
	}
	if rg.dayUsed() != u.Charged {
		t.Fatalf("day used %d != charged %d", rg.dayUsed(), u.Charged)
	}
}

func TestUpstreamErrorIsNotCharged(t *testing.T) {
	rg := newRig(t)
	rg.up.code, rg.up.ct, rg.up.body = 529, "application/json", `{"type":"error","error":{"type":"overloaded_error"}}`
	resp, _ := rg.post("/v1/messages?beta=true", goodBody(), nil)
	if resp.StatusCode != 529 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if u := rg.usage("run1"); u.Charged != 0 || u.Unsettled != 0 {
		t.Fatalf("error response charged: %+v", u)
	}
}

func TestBudgetRefusals(t *testing.T) {
	rg := newRig(t)
	// Run cap 600k: four requests reserving ~128k each fit, the fifth does not
	// while the stream never settles.
	rg.up.body = ""
	rg.up.cut = true
	var last *http.Response
	for i := 0; i < 5; i++ {
		last, _ = rg.post("/v1/messages?beta=true", goodBody(), nil)
	}
	if last.StatusCode != refusalStatus {
		t.Fatalf("fifth request status %d, want refusal", last.StatusCode)
	}
	if u := rg.usage("run1"); u.Refusals["budget-run"] != 1 {
		t.Fatalf("refusals = %v", u.Refusals)
	}
}

func TestDayBudgetRefusal(t *testing.T) {
	rg := newRig(t)
	os.MkdirAll(rg.state, 0o755)
	os.WriteFile(filepath.Join(rg.state, "budget.json"), []byte(`{"day":"2026-11-28","used":950000}`), 0o644)
	resp, _ := rg.post("/v1/messages?beta=true", goodBody(), nil)
	if resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
		t.Fatalf("status %d, fakeAPI hits %d", resp.StatusCode, rg.up.hits())
	}
	// Yesterday's use does not count today.
	os.WriteFile(filepath.Join(rg.state, "budget.json"), []byte(`{"day":"2026-11-27","used":950000}`), 0o644)
	if resp, _ := rg.post("/v1/messages?beta=true", goodBody(), nil); resp.StatusCode != 200 {
		t.Fatalf("new day refused: %d", resp.StatusCode)
	}
}

func TestUnreadableStateRefuses(t *testing.T) {
	rg := newRig(t)
	os.MkdirAll(rg.state, 0o755)
	os.WriteFile(filepath.Join(rg.state, "budget.json"), []byte(`{"day":`), 0o644)
	resp, _ := rg.post("/v1/messages?beta=true", goodBody(), nil)
	if resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
		t.Fatalf("corrupt state: status %d, fakeAPI hits %d", resp.StatusCode, rg.up.hits())
	}
}

func TestCountTokensIsFree(t *testing.T) {
	rg := newRig(t)
	rg.up.ct, rg.up.body = "application/json", `{"input_tokens":1234}`
	b := goodBody()
	delete(b, "stream")
	delete(b, "max_tokens")
	resp, _ := rg.post("/v1/messages/count_tokens?beta=true", b, nil)
	if resp.StatusCode != 200 || rg.dayUsed() != 0 {
		t.Fatalf("status %d day used %d", resp.StatusCode, rg.dayUsed())
	}
}

func TestRefusesPeers(t *testing.T) {
	for name, cg := range map[string]string{
		"user unit":      "/user.slice/user-1000.slice/user@1000.service/app.slice/wiki-agent@run1.service",
		"lookalike unit": `/system.slice/system-wiki\x2dagent\x2devil.slice/wiki-agent-evil@run1.service`,
		"other service":  "/system.slice/sshd.service",
		"unregistered":   `/system.slice/system-wiki\x2dagent.slice/wiki-agent@run2.service`,
	} {
		t.Run(name, func(t *testing.T) {
			rg := newRig(t)
			rg.cg = cg
			resp, _ := rg.post("/v1/messages?beta=true", goodBody(), nil)
			if resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
				t.Fatalf("status %d fakeAPI hits %d", resp.StatusCode, rg.up.hits())
			}
		})
	}
}

func TestRefusesMalformedRegistration(t *testing.T) {
	rg := newRig(t)
	rg.register("run1", "acme", "ingest") // not an org
	if resp, _ := rg.post("/v1/messages?beta=true", goodBody(), nil); resp.StatusCode != refusalStatus {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestRefusesRequests(t *testing.T) {
	type tc struct {
		path, reason string
		hdr          map[string]string
		edit         func(map[string]any)
	}
	msg := func(content ...any) func(map[string]any) {
		return func(b map[string]any) {
			b["messages"] = append(b["messages"].([]any), map[string]any{"role": "user", "content": content})
		}
	}
	cases := map[string]tc{
		"models list":    {path: "/v1/models", reason: "path"},
		"files api":      {path: "/v1/files", reason: "path"},
		"extra query":    {path: "/v1/messages?beta=true&x=1", reason: "query"},
		"gzip body":      {hdr: map[string]string{"Content-Encoding": "gzip"}, reason: "encoding"},
		"unknown header": {hdr: map[string]string{"X-Forwarded-For": "1.2.3.4"}, reason: "header"},
		"unknown beta":   {hdr: map[string]string{"Anthropic-Beta": "claude-code-20250219,files-api-2025-04-14"}, reason: "beta"},
		"mcp servers":    {edit: func(b map[string]any) { b["mcp_servers"] = []any{map[string]any{"type": "url", "url": "https://x"}} }, reason: "key"},
		"container":      {edit: func(b map[string]any) { b["container"] = "c" }, reason: "key"},
		"service tier":   {edit: func(b map[string]any) { b["service_tier"] = "priority" }, reason: "key"},
		"other model":    {edit: func(b map[string]any) { b["model"] = "claude-fable-5-2" }, reason: "model"},
		"server tool": {edit: func(b map[string]any) {
			b["tools"] = []any{map[string]any{"type": "web_fetch_20260209", "name": "web_fetch"}}
		}, reason: "tool"},
		"url image":     {edit: msg(map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://x/?d=secret"}}), reason: "source"},
		"file document": {edit: msg(map[string]any{"type": "document", "source": map[string]any{"type": "file", "file_id": "f"}}), reason: "block"},
		"url in result": {edit: msg(map[string]any{"type": "tool_result", "tool_use_id": "t", "content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://x"}}}}), reason: "source"},
		"search result": {edit: msg(map[string]any{"type": "search_result", "source": "https://x"}), reason: "block"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rg := newRig(t)
			b := goodBody()
			if c.edit != nil {
				c.edit(b)
			}
			path := c.path
			if path == "" {
				path = "/v1/messages?beta=true"
			}
			resp, _ := rg.post(path, b, c.hdr)
			if resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
				t.Fatalf("status %d fakeAPI hits %d", resp.StatusCode, rg.up.hits())
			}
			if u := rg.usage("run1"); u.Refusals[c.reason] != 1 {
				t.Fatalf("refusals = %v, want %s", u.Refusals, c.reason)
			}
		})
	}
}

// The allowlists are held literally: widening one must show up as a failing
// test next to the claude-code version that needed it.
func TestAllowlistsArePinned(t *testing.T) {
	keys := func(m any) string {
		var out []string
		switch m := m.(type) {
		case map[string]string:
			for k := range m {
				out = append(out, k)
			}
		case map[string]bool:
			for k := range m {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	pins := []struct{ name, got, want string }{
		{"betas", keys(allowedBetas), "cache-diagnosis-2026-04-07 claude-code-20250219 context-management-2025-06-27 effort-2025-11-24 interleaved-thinking-2025-05-14 mid-conversation-system-clear-at-2026-08-21 mid-conversation-tool-changes-2026-07-01 per-turn-control-2026-07-01 prompt-caching-scope-2026-01-05 thinking-binding-controls-2026-08-01 thinking-token-count-2026-05-13"},
		{"models", keys(allowedModels), "claude-sonnet-5-5"},
		{"top keys", keys(allowedTopKeys), "context_management max_tokens messages metadata model output_config stream system thinking tool_choice tools"},
		{"blocks", keys(allowedBlocks), "image redacted_thinking text thinking tool_result tool_use"},
		{"sources", keys(allowedSources), "base64 text"},
		{"paths", keys(allowedPaths), "/v1/messages /v1/messages/count_tokens"},
		{"headers", keys(allowedHeaders), "Accept Anthropic-Beta Anthropic-Dangerous-Direct-Browser-Access Anthropic-Version Content-Type User-Agent X-App X-Claude-Code-Session-Id X-Stainless-Arch X-Stainless-Lang X-Stainless-Os X-Stainless-Package-Version X-Stainless-Retry-Count X-Stainless-Runtime X-Stainless-Runtime-Version X-Stainless-Timeout"},
		{"dropped headers", keys(droppedHeaders), "Accept-Encoding Authorization Connection Content-Length X-Api-Key"},
		{"block keys", keys(allowedBlockKeys), "cache_control caller citations content data id input is_error name signature source text thinking tool_use_id type"},
	}
	for _, p := range pins {
		if p.got != p.want {
			t.Errorf("%s allowlist changed:\n got %s\nwant %s", p.name, p.got, p.want)
		}
	}
}

func TestRunFromCgroup(t *testing.T) {
	for cg, want := range map[string]string{
		agentCg: "run1",
		`/system.slice/system-wiki\x2dagent.slice/wiki-agent@run1.service/extra`: "",
		`/system.slice/system-wiki\x2dagent.slice/wiki-agent@RUN1.service`:       "",
		`/system.slice/system-wiki\x2dagent.slice/wiki-agent@a-b.service`:        "",
		"/system.slice/wiki-agent.service":                                       "",
	} {
		if got := runFromCgroup(cg); got != want {
			t.Errorf("runFromCgroup(%q) = %q, want %q", cg, got, want)
		}
	}
}

// The real peer lookup, against this test process at the other end.
func TestPeerCgroupIsTheConnectingProcess(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan net.Conn, 1)
	go func() { c, _ := l.Accept(); done <- c }()
	cl, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	srv := <-done
	defer srv.Close()
	got, err := peerCgroup(srv)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := readCgroup("/proc/self/cgroup")
	if got != want || got == "" {
		t.Fatalf("peer cgroup %q, want %q", got, want)
	}
}

func TestUsageTapAcrossSplitReads(t *testing.T) {
	var rep *Reported
	calls := 0
	tap := newUsageTap(io.NopCloser(&oneByte{s: sseOK}), "text/event-stream", func(r *Reported) { rep = r; calls++ })
	io.ReadAll(tap)
	tap.Close()
	if calls != 1 || rep == nil || rep.Input != 100 || rep.Output != 50 || rep.CacheWrite != 2000 {
		t.Fatalf("calls %d rep %+v", calls, rep)
	}
}

type oneByte struct{ s string }

func (o *oneByte) Read(p []byte) (int, error) {
	if o.s == "" {
		return 0, io.EOF
	}
	p[0] = o.s[0]
	o.s = o.s[1:]
	return 1, nil
}

// Bodies the API and Go's encoding/json could read differently are refused
// before anything is forwarded.
func TestRefusesParserDifferentials(t *testing.T) {
	base := `{"model":"claude-sonnet-5-5","max_tokens":10,"messages":[%s]}`
	for name, c := range map[string]struct{ msg, reason string }{
		"case-variant content": {`{"role":"user","content":[{"type":"document","source":{"type":"url","url":"http://x"}}],"Content":"hi"}`, "duplicate"},
		"case-variant type":    {`{"role":"user","content":[{"type":"image","source":{"type":"url","url":"http://x","TYPE":"base64"}}]}`, "duplicate"},
		"duplicate content":    {`{"role":"user","content":[{"type":"image","source":{"type":"url","url":"http://x"}}],"content":"hi"}`, "duplicate"},
		"struct-style Content": {`{"role":"user","Content":[{"type":"image","source":{"type":"url","url":"http://x"}}]}`, "key"},
	} {
		t.Run(name, func(t *testing.T) {
			rg := newRig(t)
			req, _ := http.NewRequest("POST", "http://wiki-proxy.invalid/v1/messages?beta=true", strings.NewReader(fmt.Sprintf(base, c.msg)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := rg.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
				t.Fatalf("status %d upstream hits %d", resp.StatusCode, rg.up.hits())
			}
			if u := rg.usage("run1"); u.Refusals[c.reason] != 1 {
				t.Fatalf("refusals %v, want %s", u.Refusals, c.reason)
			}
		})
	}
}

// Trailers are sent after the body and never pass the header allowlist.
func TestRefusesTrailers(t *testing.T) {
	rg := newRig(t)
	b, _ := json.Marshal(goodBody())
	req, _ := http.NewRequest("POST", "http://wiki-proxy.invalid/v1/messages?beta=true", io.NopCloser(strings.NewReader(string(b))))
	req.ContentLength = -1 // chunked, so trailers can follow
	req.Header.Set("Content-Type", "application/json")
	req.Trailer = http.Header{"Anthropic-Beta": {"mcp-client-2025-04-04"}}
	resp, err := rg.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
		t.Fatalf("status %d upstream hits %d", resp.StatusCode, rg.up.hits())
	}
}

// A reservation made before midnight settles against its own day, never
// against today's budget.
func TestSettleAcrossMidnightLeavesTodayAlone(t *testing.T) {
	now := time.Date(2026, 11, 28, 23, 59, 0, 0, time.Local)
	a := &Accounts{Dir: t.TempDir(), DayLimit: 1_000_000, RunLimit: 1_000_000, Now: func() time.Time { return now }}
	rr := Registration{Run: "r1", Org: "personal", Command: "ingest"}
	day, ref := a.Reserve(rr, "claude-sonnet-5-5", 400_000)
	if ref != nil {
		t.Fatal(ref)
	}
	now = now.Add(2 * time.Minute)
	writeJSON(filepath.Join(a.Dir, "budget.json"), dayBudget{Day: "2026-11-29", Used: 500_000})
	if err := a.Settle(rr, 400_000, day, &Reported{Output: 10_000}); err != nil {
		t.Fatal(err)
	}
	d, _ := a.loadDay()
	if d.Used != 500_000 {
		t.Fatalf("today's use %d, want 500000 untouched", d.Used)
	}
}

func TestRefusesHugeMaxTokens(t *testing.T) {
	rg := newRig(t)
	b := goodBody()
	b["max_tokens"] = int64(9223372036854775807)
	if resp, _ := rg.post("/v1/messages?beta=true", b, nil); resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
		t.Fatalf("status %d hits %d", resp.StatusCode, rg.up.hits())
	}
	if rg.dayUsed() < 0 {
		t.Fatalf("budget went negative: %d", rg.dayUsed())
	}
}

func TestUsageTapTakesCumulativeCacheFromDelta(t *testing.T) {
	s := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0,\"output_tokens\":1}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7,\"cache_creation_input_tokens\":20000,\"cache_read_input_tokens\":300}}\n\n"
	var rep *Reported
	tap := newUsageTap(io.NopCloser(strings.NewReader(s)), "text/event-stream", func(r *Reported) { rep = r })
	io.ReadAll(tap)
	if rep == nil || rep.CacheWrite != 20000 || rep.CacheRead != 300 || rep.Output != 7 || rep.Input != 5 {
		t.Fatalf("rep %+v", rep)
	}
}

func TestRefusesMaxTokensOverModelCap(t *testing.T) {
	rg := newRig(t)
	b := goodBody()
	b["max_tokens"] = maxOutput + 1
	if resp, _ := rg.post("/v1/messages?beta=true", b, nil); resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
		t.Fatalf("status %d hits %d", resp.StatusCode, rg.up.hits())
	}
}

func TestReserveRefusesNonPositive(t *testing.T) {
	a := &Accounts{Dir: t.TempDir(), DayLimit: 10, RunLimit: 10, Now: time.Now}
	rr := Registration{Run: "r1", Org: "personal", Command: "ingest"}
	for _, n := range []int64{0, -1, -9223372036854775808} {
		if _, ref := a.Reserve(rr, "m", n); ref == nil {
			t.Errorf("Reserve(%d) accepted", n)
		}
	}
}

func TestRefusesUnknownBlockKey(t *testing.T) {
	rg := newRig(t)
	b := goodBody()
	b["messages"] = append(b["messages"].([]any), map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "x", "url": "https://x"}}})
	if resp, _ := rg.post("/v1/messages?beta=true", b, nil); resp.StatusCode != refusalStatus || rg.up.hits() != 0 {
		t.Fatalf("status %d hits %d", resp.StatusCode, rg.up.hits())
	}
}
