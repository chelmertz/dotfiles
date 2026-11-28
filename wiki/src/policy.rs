//! What the proxy forwards. The allowlists were taken from what claude-code
//! 2.1.325 sends as the ingest agent (--tools Read,Edit,Write, --model
//! claude-sonnet-5-5), captured through a logging proxy on 2026-11-28. They are
//! tied to that pinned version: a bump re-runs that capture and the
//! `allowlists_are_pinned` test, which holds each list literally so a change
//! shows up as a failing test, not a silent widening.

use hyper::{HeaderMap, Method, Uri};
use serde::de::{self, Deserialize, Deserializer, MapAccess, SeqAccess, Visitor};
use serde_json::{Map, Value};
use std::collections::HashSet;
use std::fmt;

/// Each anthropic-beta value the proxy forwards, with why it is harmless for
/// an agent that has no network of its own. Anything else is refused.
pub const ALLOWED_BETAS: &[(&str, &str)] = &[
    ("claude-code-20250219", "Claude Code's request shape; required with an OAuth token"),
    ("interleaved-thinking-2025-05-14", "thinking between client tool calls; no server action"),
    ("context-management-2025-06-27", "clears old thinking/tool results server-side within the request"),
    ("per-turn-control-2026-07-01", "per-message effort; changes depth, not capability"),
    ("mid-conversation-tool-changes-2026-07-01", "tool list may change mid-conversation; tools are still checked here"),
    ("thinking-token-count-2026-05-13", "reports thinking token counts"),
    ("effort-2025-11-24", "effort parameter"),
    ("prompt-caching-scope-2026-01-05", "cache scoping; seen with --tools \"\""),
    ("mid-conversation-system-clear-at-2026-08-21", "turn-scoped system messages; seen with --tools \"\""),
    ("thinking-binding-controls-2026-08-01", "preserved-thinking checks; seen with --tools \"\""),
    ("cache-diagnosis-2026-04-07", "cache diagnostics in the response; seen with --tools \"\""),
];

/// Added by the proxy itself: the seat token is an OAuth token.
pub const OAUTH_BETA: &str = "oauth-2025-04-20";

/// Request headers forwarded upstream. Authorization and x-api-key are never
/// forwarded: the proxy sets its own.
pub const ALLOWED_HEADERS: &[&str] = &[
    "accept",
    "anthropic-beta",
    "anthropic-dangerous-direct-browser-access",
    "anthropic-version",
    "content-type",
    "user-agent",
    "x-app",
    "x-claude-code-session-id",
    "x-stainless-arch",
    "x-stainless-lang",
    "x-stainless-os",
    "x-stainless-package-version",
    "x-stainless-retry-count",
    "x-stainless-runtime",
    "x-stainless-runtime-version",
    "x-stainless-timeout",
];

/// Headers accepted from the client and not forwarded. accept-encoding is
/// dropped so the usage parser sees plain text; host is replaced by the
/// upstream's; transfer-encoding only frames the body on the socket.
pub const DROPPED_HEADERS: &[&str] = &[
    "accept-encoding",
    "authorization",
    "connection",
    "content-length",
    "host",
    "transfer-encoding",
    "x-api-key",
];

pub const ALLOWED_TOP_KEYS: &[&str] = &[
    "context_management", "max_tokens", "messages", "metadata", "model",
    "output_config", "stream", "system", "thinking", "tool_choice", "tools",
];
pub const ALLOWED_MODELS: &[&str] = &["claude-sonnet-5-5"];
pub const ALLOWED_BLOCKS: &[&str] = &["image", "redacted_thinking", "text", "thinking", "tool_result", "tool_use"];
pub const ALLOWED_BLOCK_KEYS: &[&str] = &[
    "cache_control", "caller", "citations", "content", "data", "id", "input",
    "is_error", "name", "signature", "source", "text", "thinking", "tool_use_id", "type",
];
/// output_config on a message: per-message effort (per-turn-control beta).
pub const MESSAGE_KEYS: &[&str] = &["content", "output_config", "role"];
/// Inline data only: url and file sources make Anthropic fetch or resolve.
pub const ALLOWED_SOURCES: &[&str] = &["base64", "text"];
pub const ALLOWED_PATHS: &[&str] = &["/v1/messages", "/v1/messages/count_tokens"];

/// Bounds one request (a 1M-token context is about 4 MiB of text) and, with
/// MemoryMax in the unit, what one request can hold in memory.
pub const MAX_BODY: usize = 8 << 20;
/// The largest max_tokens any current model accepts.
pub const MAX_OUTPUT: u64 = 128_000;

#[derive(Debug, Clone, PartialEq)]
pub struct Refusal {
    pub reason: &'static str,
    pub detail: String,
}

pub fn refuse(reason: &'static str, detail: impl Into<String>) -> Refusal {
    Refusal { reason, detail: detail.into() }
}

impl fmt::Display for Refusal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.reason, self.detail)
    }
}

/// Validates everything but the body; returns the betas to forward.
pub fn check_request(method: &Method, uri: &Uri, headers: &HeaderMap) -> Result<Vec<String>, Refusal> {
    if method != Method::POST || !ALLOWED_PATHS.contains(&uri.path()) {
        return Err(refuse("path", format!("{method} {}", uri.path())));
    }
    if let Some(q) = uri.query() {
        if !q.is_empty() && q != "beta=true" {
            return Err(refuse("query", q));
        }
    }
    for name in headers.keys() {
        let n = name.as_str();
        if !ALLOWED_HEADERS.contains(&n) && !DROPPED_HEADERS.contains(&n) {
            return Err(refuse("header", n));
        }
    }
    if let Some(te) = headers.get("transfer-encoding") {
        if te.as_bytes() != b"chunked" {
            return Err(refuse("encoding", format!("{te:?}")));
        }
    }
    let mut betas = Vec::new();
    for v in headers.get_all("anthropic-beta") {
        let v = v.to_str().map_err(|_| refuse("beta", "not ascii"))?;
        for b in v.split(',').map(str::trim).filter(|b| !b.is_empty()) {
            if !ALLOWED_BETAS.iter().any(|(k, _)| *k == b) {
                return Err(refuse("beta", b));
            }
            betas.push(b.to_string());
        }
    }
    Ok(betas)
}

#[derive(Debug, Default, PartialEq)]
pub struct RequestInfo {
    pub model: String,
    pub max_tokens: u64,
}

/// Validates a request body and returns the fields budgeting needs.
pub fn check_body(body: &[u8]) -> Result<RequestInfo, Refusal> {
    let top = match serde_json::from_slice::<Strict>(body) {
        Ok(Strict(v)) => v,
        Err(e) if e.to_string().starts_with(DUPLICATE) => return Err(refuse("duplicate", e.to_string())),
        Err(_) => return Err(refuse("body", "not JSON")),
    };
    let top = top.as_object().ok_or_else(|| refuse("body", "not a JSON object"))?;
    for k in top.keys() {
        if !ALLOWED_TOP_KEYS.contains(&k.as_str()) {
            return Err(refuse("key", k.as_str()));
        }
    }
    let model = top.get("model").and_then(Value::as_str).unwrap_or_default();
    if !ALLOWED_MODELS.contains(&model) {
        return Err(refuse("model", model));
    }
    let max_tokens = match top.get("max_tokens") {
        None => 0,
        Some(v) => match v.as_u64() {
            Some(n) if n <= MAX_OUTPUT => n,
            _ => return Err(refuse("body", "max_tokens")),
        },
    };
    if let Some(tools) = top.get("tools") {
        let tools = tools.as_array().ok_or_else(|| refuse("body", "tools"))?;
        for t in tools {
            let t = t.as_object().ok_or_else(|| refuse("body", "tool"))?;
            if let Some(typ) = t.get("type") {
                if typ != "custom" {
                    return Err(refuse("tool", typ.to_string()));
                }
            }
        }
    }
    if let Some(s) = top.get("system") {
        check_content(s)?;
    }
    let msgs = top.get("messages").and_then(Value::as_array).ok_or_else(|| refuse("body", "messages"))?;
    for m in msgs {
        let m = m.as_object().ok_or_else(|| refuse("body", "message"))?;
        for k in m.keys() {
            if !MESSAGE_KEYS.contains(&k.as_str()) {
                return Err(refuse("key", format!("message.{k}")));
            }
        }
        if let Some(c) = m.get("content") {
            check_content(c)?;
        }
    }
    Ok(RequestInfo { model: model.to_string(), max_tokens })
}

/// A string, or a list of blocks; tool_result content is checked the same way.
fn check_content(v: &Value) -> Result<(), Refusal> {
    let blocks = match v {
        Value::String(_) => return Ok(()),
        Value::Array(a) => a,
        _ => return Err(refuse("body", "content is neither string nor block list")),
    };
    for b in blocks {
        let b = b.as_object().ok_or_else(|| refuse("body", "block"))?;
        let typ = b.get("type").and_then(Value::as_str).unwrap_or_default();
        if !ALLOWED_BLOCKS.contains(&typ) {
            return Err(refuse("block", typ));
        }
        for k in b.keys() {
            if !ALLOWED_BLOCK_KEYS.contains(&k.as_str()) {
                return Err(refuse("key", format!("{typ}.{k}")));
            }
        }
        if let Some(src) = b.get("source") {
            let st = src.get("type").and_then(Value::as_str).unwrap_or_default();
            if !src.is_object() || !ALLOWED_SOURCES.contains(&st) {
                return Err(refuse("source", st));
            }
        }
        if typ == "tool_result" {
            if let Some(c) = b.get("content") {
                check_content(c)?;
            }
        }
    }
    Ok(())
}

const DUPLICATE: &str = "duplicate key";

/// A JSON value parsed so the proxy and the API cannot read it differently:
/// a duplicate key in any object (parsers disagree on which one wins), or a
/// key differing only in case from another in the same object, is an error.
struct Strict(Value);

impl<'de> Deserialize<'de> for Strict {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        d.deserialize_any(StrictVisitor).map(Strict)
    }
}

struct StrictVisitor;

impl<'de> Visitor<'de> for StrictVisitor {
    type Value = Value;
    fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str("JSON")
    }
    fn visit_bool<E>(self, v: bool) -> Result<Value, E> {
        Ok(Value::Bool(v))
    }
    fn visit_i64<E>(self, v: i64) -> Result<Value, E> {
        Ok(v.into())
    }
    fn visit_u64<E>(self, v: u64) -> Result<Value, E> {
        Ok(v.into())
    }
    fn visit_f64<E>(self, v: f64) -> Result<Value, E> {
        Ok(serde_json::Number::from_f64(v).map_or(Value::Null, Value::Number))
    }
    fn visit_str<E>(self, v: &str) -> Result<Value, E> {
        Ok(Value::String(v.to_string()))
    }
    fn visit_string<E>(self, v: String) -> Result<Value, E> {
        Ok(Value::String(v))
    }
    fn visit_unit<E>(self) -> Result<Value, E> {
        Ok(Value::Null)
    }
    fn visit_seq<A: SeqAccess<'de>>(self, mut seq: A) -> Result<Value, A::Error> {
        let mut out = Vec::new();
        while let Some(Strict(v)) = seq.next_element()? {
            out.push(v);
        }
        Ok(Value::Array(out))
    }
    fn visit_map<A: MapAccess<'de>>(self, mut map: A) -> Result<Value, A::Error> {
        let mut out = Map::new();
        let mut seen = HashSet::new();
        while let Some(k) = map.next_key::<String>()? {
            if !seen.insert(k.to_lowercase()) {
                return Err(de::Error::custom(format!("{DUPLICATE} {k:?}")));
            }
            let Strict(v) = map.next_value()?;
            out.insert(k, v);
        }
        Ok(Value::Object(out))
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use serde_json::json;

    pub fn good_body() -> Value {
        json!({
            "model": "claude-sonnet-5-5", "max_tokens": 128000, "stream": true,
            "metadata": {"user_id": "x"},
            "thinking": {"type": "adaptive", "display": "omitted"},
            "context_management": {"edits": [{"type": "clear_thinking_20251015", "keep": "all"}]},
            "output_config": {"effort": "medium"},
            "system": [{"type": "text", "text": "You are Claude Code", "cache_control": {"type": "ephemeral"}}],
            "tools": [
                {"name": "Read", "description": "d", "input_schema": {"type": "object", "additionalProperties": false}},
                {"name": "DeferredToolPlaceholder", "description": "d", "input_schema": {}, "defer_loading": true}
            ],
            "messages": [
                {"role": "user", "content": "Read in/item.md"},
                {"role": "assistant", "content": [{"type": "tool_use", "id": "t1", "name": "Read", "input": {"file_path": "in/item.md"}}]},
                {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "t1", "content": [{"type": "text", "text": "# Decisions"}]}]},
                {"role": "system", "output_config": {"effort": "medium"}}
            ]
        })
    }

    fn reason(body: &str) -> &'static str {
        check_body(body.as_bytes()).err().map(|r| r.reason).unwrap_or("accepted")
    }

    #[test]
    fn accepts_the_agents_shape() {
        let info = check_body(good_body().to_string().as_bytes()).unwrap();
        assert_eq!(info, RequestInfo { model: "claude-sonnet-5-5".into(), max_tokens: 128000 });
    }

    #[test]
    fn refuses_bodies() {
        let msg = |m: Value| {
            let mut b = good_body();
            b["messages"].as_array_mut().unwrap().push(json!({"role": "user", "content": [m]}));
            b.to_string()
        };
        let with = |k: &str, v: Value| {
            let mut b = good_body();
            b[k] = v;
            b.to_string()
        };
        let cases: Vec<(&str, String, &str)> = vec![
            ("mcp servers", with("mcp_servers", json!([{"type": "url", "url": "https://x"}])), "key"),
            ("container", with("container", json!("c")), "key"),
            ("service tier", with("service_tier", json!("priority")), "key"),
            ("other model", with("model", json!("claude-fable-5-2")), "model"),
            ("server tool", with("tools", json!([{"type": "web_fetch_20260209", "name": "web_fetch"}])), "tool"),
            ("max tokens over cap", with("max_tokens", json!(MAX_OUTPUT + 1)), "body"),
            ("max tokens huge", with("max_tokens", json!(u64::MAX)), "body"),
            ("max tokens negative", with("max_tokens", json!(-1)), "body"),
            ("url image", msg(json!({"type": "image", "source": {"type": "url", "url": "https://x/?d=s"}})), "source"),
            ("file document", msg(json!({"type": "document", "source": {"type": "file", "file_id": "f"}})), "block"),
            ("url in result", msg(json!({"type": "tool_result", "tool_use_id": "t", "content": [{"type": "image", "source": {"type": "url", "url": "https://x"}}]})), "source"),
            ("search result", msg(json!({"type": "search_result", "source": "https://x"})), "block"),
            ("unknown block key", msg(json!({"type": "text", "text": "x", "url": "https://x"})), "key"),
        ];
        for (name, body, want) in cases {
            assert_eq!(reason(&body), want, "{name}");
        }
    }

    #[test]
    fn refuses_parser_differentials() {
        let base = |m: &str| format!(r#"{{"model":"claude-sonnet-5-5","max_tokens":10,"messages":[{m}]}}"#);
        let cases = [
            ("case-variant content", r#"{"role":"user","content":[{"type":"document","source":{"type":"url","url":"http://x"}}],"Content":"hi"}"#, "duplicate"),
            ("case-variant type", r#"{"role":"user","content":[{"type":"image","source":{"type":"url","url":"http://x","TYPE":"base64"}}]}"#, "duplicate"),
            ("duplicate content", r#"{"role":"user","content":[{"type":"image","source":{"type":"url","url":"http://x"}}],"content":"hi"}"#, "duplicate"),
            ("only Content", r#"{"role":"user","Content":[{"type":"image","source":{"type":"url","url":"http://x"}}]}"#, "key"),
        ];
        for (name, m, want) in cases {
            assert_eq!(reason(&base(m)), want, "{name}");
        }
    }

    fn req(method: Method, uri: &str, headers: &[(&str, &str)]) -> Result<Vec<String>, Refusal> {
        let mut h = HeaderMap::new();
        for (k, v) in headers {
            h.append(hyper::header::HeaderName::from_bytes(k.as_bytes()).unwrap(), v.parse().unwrap());
        }
        check_request(&method, &uri.parse().unwrap(), &h)
    }

    #[test]
    fn checks_request_line_and_headers() {
        let ok = [("content-type", "application/json"), ("x-api-key", "placeholder"), ("host", "wiki-proxy.invalid"), ("anthropic-beta", "claude-code-20250219, effort-2025-11-24")];
        assert_eq!(req(Method::POST, "/v1/messages?beta=true", &ok).unwrap(), vec!["claude-code-20250219", "effort-2025-11-24"]);
        let cases: Vec<(&str, Method, &str, Vec<(&str, &str)>, &str)> = vec![
            ("models list", Method::GET, "/v1/models", vec![], "path"),
            ("files api", Method::POST, "/v1/files", vec![], "path"),
            ("extra query", Method::POST, "/v1/messages?beta=true&x=1", vec![], "query"),
            ("gzip body", Method::POST, "/v1/messages", vec![("content-encoding", "gzip")], "header"),
            ("unknown header", Method::POST, "/v1/messages", vec![("x-forwarded-for", "1.2.3.4")], "header"),
            ("trailer declared", Method::POST, "/v1/messages", vec![("trailer", "anthropic-beta")], "header"),
            ("unknown beta", Method::POST, "/v1/messages", vec![("anthropic-beta", "claude-code-20250219,files-api-2025-04-14")], "beta"),
            ("odd transfer encoding", Method::POST, "/v1/messages", vec![("transfer-encoding", "gzip, chunked")], "encoding"),
        ];
        for (name, m, uri, h, want) in cases {
            assert_eq!(req(m, uri, &h).unwrap_err().reason, want, "{name}");
        }
    }

    /// Widening any list must show up here, next to the claude-code version
    /// that needed it.
    #[test]
    fn allowlists_are_pinned() {
        let betas: Vec<&str> = ALLOWED_BETAS.iter().map(|(k, _)| *k).collect();
        let pins: [(&str, Vec<&str>, &str); 10] = [
            ("betas", betas, "cache-diagnosis-2026-04-07 claude-code-20250219 context-management-2025-06-27 effort-2025-11-24 interleaved-thinking-2025-05-14 mid-conversation-system-clear-at-2026-08-21 mid-conversation-tool-changes-2026-07-01 per-turn-control-2026-07-01 prompt-caching-scope-2026-01-05 thinking-binding-controls-2026-08-01 thinking-token-count-2026-05-13"),
            ("headers", ALLOWED_HEADERS.to_vec(), "accept anthropic-beta anthropic-dangerous-direct-browser-access anthropic-version content-type user-agent x-app x-claude-code-session-id x-stainless-arch x-stainless-lang x-stainless-os x-stainless-package-version x-stainless-retry-count x-stainless-runtime x-stainless-runtime-version x-stainless-timeout"),
            ("dropped headers", DROPPED_HEADERS.to_vec(), "accept-encoding authorization connection content-length host transfer-encoding x-api-key"),
            ("top keys", ALLOWED_TOP_KEYS.to_vec(), "context_management max_tokens messages metadata model output_config stream system thinking tool_choice tools"),
            ("models", ALLOWED_MODELS.to_vec(), "claude-sonnet-5-5"),
            ("blocks", ALLOWED_BLOCKS.to_vec(), "image redacted_thinking text thinking tool_result tool_use"),
            ("block keys", ALLOWED_BLOCK_KEYS.to_vec(), "cache_control caller citations content data id input is_error name signature source text thinking tool_use_id type"),
            ("message keys", MESSAGE_KEYS.to_vec(), "content output_config role"),
            ("sources", ALLOWED_SOURCES.to_vec(), "base64 text"),
            ("paths", ALLOWED_PATHS.to_vec(), "/v1/messages /v1/messages/count_tokens"),
        ];
        for (name, mut got, want) in pins {
            got.sort();
            assert_eq!(got.join(" "), want, "{name} allowlist changed");
        }
    }
}
