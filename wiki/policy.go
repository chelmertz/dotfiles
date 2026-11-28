package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// The allowlists below were taken from what claude-code 2.1.325 sends as the
// ingest agent (--tools Read,Edit,Write, --model claude-sonnet-5-5), captured
// through a logging proxy on 2026-11-28. They are tied to that pinned version:
// a claude-code bump re-runs that capture and TestAllowlistsArePinned in
// proxy_test.go, which holds each list literally so a change shows up as a failing test, not a silent widening.

// allowedBetas maps each anthropic-beta value the proxy forwards to why it is
// harmless for a tool-less-of-the-network agent. Anything else is refused.
var allowedBetas = map[string]string{
	"claude-code-20250219":                        "Claude Code's own request shape; required with an OAuth token",
	"interleaved-thinking-2025-05-14":             "thinking between client tool calls; no server action",
	"context-management-2025-06-27":               "clears old thinking/tool results server-side within the request",
	"per-turn-control-2026-07-01":                 "per-message effort; changes depth, not capability",
	"mid-conversation-tool-changes-2026-07-01":    "tool list may change mid-conversation; tools are still checked here",
	"thinking-token-count-2026-05-13":             "reports thinking token counts",
	"effort-2025-11-24":                           "effort parameter",
	"prompt-caching-scope-2026-01-05":             "cache scoping; seen with --tools \"\"",
	"mid-conversation-system-clear-at-2026-08-21": "turn-scoped system messages; seen with --tools \"\"",
	"thinking-binding-controls-2026-08-01":        "preserved-thinking checks; seen with --tools \"\"",
	"cache-diagnosis-2026-04-07":                  "cache diagnostics in the response; seen with --tools \"\"",
}

// oauthBeta is added by the proxy itself: the seat token is an OAuth token.
const oauthBeta = "oauth-2025-04-20"

// allowedHeaders are the request headers forwarded upstream (canonical form).
// Authorization and X-Api-Key are never forwarded: the proxy sets its own.
// Accept-Encoding is dropped so Go's transport negotiates and decodes gzip
// itself and the usage parser sees plain text.
var allowedHeaders = map[string]bool{
	"Accept":         true,
	"Anthropic-Beta": true,
	"Anthropic-Dangerous-Direct-Browser-Access": true,
	"Anthropic-Version":                         true,
	"Content-Type":                              true,
	"User-Agent":                                true,
	"X-App":                                     true,
	"X-Claude-Code-Session-Id":                  true,
	"X-Stainless-Arch":                          true,
	"X-Stainless-Lang":                          true,
	"X-Stainless-Os":                            true,
	"X-Stainless-Package-Version":               true,
	"X-Stainless-Retry-Count":                   true,
	"X-Stainless-Runtime":                       true,
	"X-Stainless-Runtime-Version":               true,
	"X-Stainless-Timeout":                       true,
}

// droppedHeaders are accepted from the client and not forwarded.
var droppedHeaders = map[string]bool{
	"Authorization":   true, // placeholder at most; replaced
	"X-Api-Key":       true, // placeholder; replaced
	"Accept-Encoding": true,
	"Connection":      true,
	"Content-Length":  true,
}

// allowedTopKeys are the request body keys a message or count_tokens request
// may carry. mcp_servers, container, service_tier, inference_geo and anything
// new are refused by omission.
var allowedTopKeys = map[string]bool{
	"model": true, "messages": true, "system": true, "tools": true,
	"metadata": true, "max_tokens": true, "thinking": true,
	"context_management": true, "output_config": true, "stream": true,
	"tool_choice": true,
}

// allowedModels are the models the agent may ask for.
var allowedModels = map[string]bool{"claude-sonnet-5-5": true}

// allowedBlocks are content block types in messages, system and tool results.
var allowedBlocks = map[string]bool{
	"text": true, "tool_use": true, "tool_result": true,
	"thinking": true, "redacted_thinking": true, "image": true,
}

// allowedBlockKeys are the keys any allowed block may carry.
var allowedBlockKeys = map[string]bool{
	"type": true, "text": true, "cache_control": true, "citations": true,
	"id": true, "name": true, "input": true, "caller": true,
	"tool_use_id": true, "content": true, "is_error": true,
	"thinking": true, "signature": true, "data": true, "source": true,
}

// allowedSources are the only `source.type` values a block may carry: inline
// data. url and file sources make Anthropic fetch or resolve something.
var allowedSources = map[string]bool{"base64": true, "text": true}

var allowedPaths = map[string]bool{"/v1/messages": true, "/v1/messages/count_tokens": true}

// refusal is a request the proxy will not forward, with a short reason code
// that is logged and counted per run.
type refusal struct{ Reason, Detail string }

func (r *refusal) Error() string { return r.Reason + ": " + r.Detail }

func refuse(reason, format string, a ...any) *refusal {
	return &refusal{reason, fmt.Sprintf(format, a...)}
}

// maxBody bounds a request (a 1M-token context is about 4 MiB of text) and,
// with MemoryMax in the unit, what one request can hold in memory.
const maxBody = 8 << 20

// maxOutput is the largest max_tokens any current model accepts.
const maxOutput = 128_000

// checkRequest validates everything but the body.
func checkRequest(r *http.Request) ([]string, *refusal) {
	if r.Method != http.MethodPost || !allowedPaths[r.URL.Path] {
		return nil, refuse("path", "%s %s", r.Method, r.URL.Path)
	}
	if q := r.URL.RawQuery; q != "" && q != "beta=true" {
		return nil, refuse("query", "%q", q)
	}
	// Trailers arrive after the body and are not in r.Header, so they would
	// skip the header allowlist below.
	if len(r.Trailer) > 0 {
		return nil, refuse("trailer", "%d declared", len(r.Trailer))
	}
	if ce := r.Header.Get("Content-Encoding"); ce != "" && ce != "identity" {
		return nil, refuse("encoding", "%q", ce)
	}
	for k := range r.Header {
		if !allowedHeaders[k] && !droppedHeaders[k] && k != "Content-Encoding" {
			return nil, refuse("header", "%s", k)
		}
	}
	var betas []string
	for _, v := range r.Header.Values("Anthropic-Beta") {
		for _, b := range strings.Split(v, ",") {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			if _, ok := allowedBetas[b]; !ok {
				return nil, refuse("beta", "%s", b)
			}
			betas = append(betas, b)
		}
	}
	return betas, nil
}

// requestInfo is what the proxy needs from a valid body.
type requestInfo struct {
	Model     string
	MaxTokens int64
}

// checkBody validates a request body and returns the fields budgeting needs.
func checkBody(body []byte) (requestInfo, *refusal) {
	var info requestInfo
	if r := checkStrictJSON(body); r != nil {
		return info, r
	}
	var top map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&top); err != nil {
		return info, refuse("body", "not a JSON object")
	}
	for k := range top {
		if !allowedTopKeys[k] {
			return info, refuse("key", "%s", k)
		}
	}
	if err := json.Unmarshal(top["model"], &info.Model); err != nil || !allowedModels[info.Model] {
		return info, refuse("model", "%q", info.Model)
	}
	if mt, ok := top["max_tokens"]; ok {
		if err := json.Unmarshal(mt, &info.MaxTokens); err != nil || info.MaxTokens < 0 || info.MaxTokens > maxOutput {
			return info, refuse("body", "max_tokens")
		}
	}
	if t, ok := top["tools"]; ok {
		var tools []map[string]any
		if err := json.Unmarshal(t, &tools); err != nil {
			return info, refuse("body", "tools")
		}
		for _, tool := range tools {
			if typ, ok := tool["type"]; ok && typ != "custom" {
				return info, refuse("tool", "%v", typ)
			}
		}
	}
	if s, ok := top["system"]; ok {
		if r := checkContent(s); r != nil {
			return info, r
		}
	}
	// Exact-key maps, never structs: encoding/json matches struct fields
	// case-insensitively, which would let "Content" stand in for "content".
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(top["messages"], &msgs); err != nil {
		return info, refuse("body", "messages")
	}
	for _, m := range msgs {
		for k := range m {
			// output_config: per-message effort (per-turn-control beta).
			if k != "role" && k != "content" && k != "output_config" {
				return info, refuse("key", "message.%s", k)
			}
		}
		if r := checkContent(m["content"]); r != nil {
			return info, r
		}
	}
	return info, nil
}

// checkContent accepts a string or an array of blocks; tool_result content is
// checked recursively the same way.
func checkContent(raw json.RawMessage) *refusal {
	if len(raw) == 0 || raw[0] == '"' {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return refuse("body", "content is neither string nor block list")
	}
	for _, b := range blocks {
		var typ string
		json.Unmarshal(b["type"], &typ)
		if !allowedBlocks[typ] {
			return refuse("block", "%q", typ)
		}
		for k := range b {
			if !allowedBlockKeys[k] {
				return refuse("key", "%s.%s", typ, k)
			}
		}
		if src, ok := b["source"]; ok {
			var s map[string]json.RawMessage
			var st string
			if json.Unmarshal(src, &s) != nil || json.Unmarshal(s["type"], &st) != nil || !allowedSources[st] {
				return refuse("source", "%q", st)
			}
		}
		if typ == "tool_result" {
			if r := checkContent(b["content"]); r != nil {
				return r
			}
		}
	}
	return nil
}

// checkStrictJSON refuses what the proxy and the API could read differently:
// a duplicate key in any object (parsers disagree on which one wins), and a
// key that differs only in case from another key in the same object. The
// checks after it can then read exact keys and trust that the API sees the
// same values.
func checkStrictJSON(body []byte) *refusal {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var walk func() *refusal
	walk = func() *refusal {
		tok, err := dec.Token()
		if err != nil {
			return refuse("body", "not JSON")
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return refuse("body", "not JSON")
				}
				k := kt.(string)
				if seen[strings.ToLower(k)] {
					return refuse("duplicate", "key %q", k)
				}
				seen[strings.ToLower(k)] = true
				if r := walk(); r != nil {
					return r
				}
			}
		case '[':
			for dec.More() {
				if r := walk(); r != nil {
					return r
				}
			}
		}
		if _, err := dec.Token(); err != nil { // closing delimiter
			return refuse("body", "not JSON")
		}
		return nil
	}
	if r := walk(); r != nil {
		return r
	}
	if dec.More() {
		return refuse("body", "trailing data")
	}
	return nil
}
