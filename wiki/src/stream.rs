//! Extracts usage from a response as it passes through: from an event stream
//! (message_start carries input, message_delta the cumulative totals and
//! closes the report) or from a JSON body.

use crate::accounting::Reported;
use crate::policy::MAX_BODY;
use serde::Deserialize;

#[derive(Deserialize, Default)]
struct ApiUsage {
    #[serde(default)]
    input_tokens: i64,
    #[serde(default)]
    cache_creation_input_tokens: i64,
    #[serde(default)]
    cache_read_input_tokens: i64,
    #[serde(default)]
    output_tokens: i64,
}

impl From<&ApiUsage> for Reported {
    fn from(u: &ApiUsage) -> Self {
        Reported { input: u.input_tokens, cache_write: u.cache_creation_input_tokens, cache_read: u.cache_read_input_tokens, output: u.output_tokens }
    }
}

pub struct UsageParser {
    stream: bool,
    buf: Vec<u8>,
    rep: Reported,
    started: bool,
    complete: bool,
}

impl UsageParser {
    pub fn new(content_type: &str) -> Self {
        UsageParser { stream: content_type.starts_with("text/event-stream"), buf: Vec::new(), rep: Reported::default(), started: false, complete: false }
    }

    pub fn feed(&mut self, chunk: &[u8]) {
        if self.buf.len() >= MAX_BODY {
            return;
        }
        self.buf.extend_from_slice(chunk);
        if self.stream {
            self.scan_events();
        }
    }

    /// The report, if one was seen complete.
    pub fn finish(mut self) -> Option<Reported> {
        if !self.stream {
            self.parse_json();
        }
        self.complete.then_some(self.rep)
    }

    fn scan_events(&mut self) {
        while let Some(nl) = self.buf.iter().position(|&b| b == b'\n') {
            let line: Vec<u8> = self.buf.drain(..=nl).collect();
            let line = line.strip_suffix(b"\n").unwrap_or(&line);
            let line = line.strip_suffix(b"\r").unwrap_or(line);
            let Some(data) = line.strip_prefix(b"data:") else { continue };
            #[derive(Deserialize)]
            struct Msg {
                #[serde(default)]
                usage: ApiUsage,
            }
            #[derive(Deserialize)]
            struct Ev {
                #[serde(rename = "type")]
                typ: String,
                message: Option<Msg>,
                usage: Option<ApiUsage>,
            }
            let Ok(ev) = serde_json::from_slice::<Ev>(data.trim_ascii()) else { continue };
            match ev.typ.as_str() {
                "message_start" => {
                    if let Some(m) = &ev.message {
                        self.rep = (&m.usage).into();
                        self.started = true;
                    }
                }
                "message_delta" => {
                    if let (Some(u), true) = (&ev.usage, self.started) {
                        // Cumulative: any field the delta carries supersedes the start's.
                        self.rep.output = u.output_tokens;
                        for (dst, src) in [
                            (&mut self.rep.input, u.input_tokens),
                            (&mut self.rep.cache_write, u.cache_creation_input_tokens),
                            (&mut self.rep.cache_read, u.cache_read_input_tokens),
                        ] {
                            if src > 0 {
                                *dst = src;
                            }
                        }
                        self.complete = true;
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_json(&mut self) {
        #[derive(Deserialize)]
        struct Resp {
            usage: Option<ApiUsage>,
            input_tokens: Option<i64>, // count_tokens
        }
        let Ok(r) = serde_json::from_slice::<Resp>(&self.buf) else { return };
        if let Some(u) = &r.usage {
            self.rep = u.into();
            self.complete = true;
        } else if r.input_tokens.is_some() {
            // count_tokens costs nothing: a complete report of zero use.
            self.complete = true;
        }
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;

    pub const SSE_OK: &str = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"cache_creation_input_tokens\":2000,\"cache_read_input_tokens\":30000,\"output_tokens\":1}}}\n\n\
event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n\
event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":50}}\n\n\
event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n";

    #[test]
    fn across_one_byte_reads() {
        let mut p = UsageParser::new("text/event-stream; charset=utf-8");
        for b in SSE_OK.as_bytes() {
            p.feed(&[*b]);
        }
        assert_eq!(p.finish(), Some(Reported { input: 100, cache_write: 2000, cache_read: 30000, output: 50 }));
    }

    #[test]
    fn cut_stream_has_no_report() {
        let mut p = UsageParser::new("text/event-stream");
        p.feed(SSE_OK.split("\n\n").next().unwrap().as_bytes());
        p.feed(b"\n\n");
        assert_eq!(p.finish(), None);
    }

    #[test]
    fn cumulative_cache_from_delta() {
        let mut p = UsageParser::new("text/event-stream");
        p.feed(b"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n");
        p.feed(b"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7,\"cache_creation_input_tokens\":20000,\"cache_read_input_tokens\":300}}\n\n");
        assert_eq!(p.finish(), Some(Reported { input: 5, cache_write: 20000, cache_read: 300, output: 7 }));
    }

    #[test]
    fn json_bodies() {
        let mut p = UsageParser::new("application/json");
        p.feed(br#"{"usage":{"input_tokens":3,"output_tokens":4}}"#);
        assert_eq!(p.finish(), Some(Reported { input: 3, output: 4, ..Default::default() }));
        let mut p = UsageParser::new("application/json");
        p.feed(br#"{"input_tokens":1234}"#);
        assert_eq!(p.finish(), Some(Reported::default()));
        let mut p = UsageParser::new("application/json");
        p.feed(br#"{"type":"error"}"#);
        assert_eq!(p.finish(), None);
    }
}
