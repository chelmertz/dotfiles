//! The only path from an agent's jail to Anthropic. It serves only peers that
//! are a registered run's agent unit, forwards only allowlisted message
//! requests, swaps the agent's placeholder key for the seat token, and charges
//! every token to the run's org and command.

use crate::accounting::{Accounts, Reported};
use crate::peer::{Registration, lookup_run, run_from_cgroup};
use crate::policy::{ALLOWED_HEADERS, MAX_BODY, OAUTH_BETA, Refusal, check_body, check_request, refuse};
use crate::stream::UsageParser;
use bytes::Bytes;
use http_body::{Body, Frame};
use http_body_util::combinators::BoxBody;
use http_body_util::{BodyExt, Full, Limited};
use hyper::body::Incoming;
use hyper::header::{AUTHORIZATION, CONTENT_TYPE, HeaderValue};
use hyper::service::service_fn;
use hyper::{Method, Request, Response, StatusCode, Uri};
use hyper_util::client::legacy::Client;
use hyper_util::client::legacy::connect::Connect;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::client::legacy::connect::dns::Name;
use hyper_util::rt::{TokioExecutor, TokioIo, TokioTimer};
use std::convert::Infallible;
use std::io;
use std::net::SocketAddr;
use std::os::fd::{AsRawFd, RawFd};
use std::path::PathBuf;
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};
use std::time::Duration;
use tokio::net::UnixListener;
use tokio::sync::mpsc;

pub type RespBody = BoxBody<Bytes, io::Error>;
pub type PeerFn = Box<dyn Fn(RawFd) -> io::Result<String> + Send + Sync>;
pub type TokenFn = Box<dyn Fn() -> Result<String, String> + Send + Sync>;

pub struct Proxy<C> {
    /// scheme://authority every request goes to, whatever Host the agent names.
    pub upstream: Uri,
    pub token: TokenFn,
    pub registry: PathBuf,
    pub accounts: Arc<Accounts>,
    pub client: Client<C, Full<Bytes>>,
    pub peer: PeerFn,
    /// How long upstream may take to answer with headers (TLS handshake
    /// included); a streamed answer sends its headers at once.
    pub response_timeout: Duration,
}

/// Every refusal answers 403. Claude Code's client retries 408, 409, 429 and
/// 5xx; a 403 ends the request, so a refusal ends the run instead of looping.
pub const REFUSAL_STATUS: StatusCode = StatusCode::FORBIDDEN;

fn full(b: impl Into<Bytes>) -> RespBody {
    Full::new(b.into()).map_err(|e: Infallible| match e {}).boxed()
}

/// A response body fed by the task that relays the upstream body.
struct ChannelBody(mpsc::Receiver<Result<Frame<Bytes>, io::Error>>);

impl Body for ChannelBody {
    type Data = Bytes;
    type Error = io::Error;
    fn poll_frame(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Result<Frame<Bytes>, io::Error>>> {
        self.0.poll_recv(cx)
    }
}

impl<C: Connect + Clone + Send + Sync + 'static> Proxy<C> {
    fn deny(&self, rr: Option<&Registration>, r: Refusal) -> Response<RespBody> {
        let run = rr.map_or("-", |rr| rr.run.as_str());
        if let Some(rr) = rr {
            // fsyncs, so off the async thread; the refusal answer does not wait.
            let (acc, rr, reason) = (self.accounts.clone(), rr.clone(), r.reason);
            tokio::task::spawn_blocking(move || {
                if let Err(e) = acc.refused(&rr, reason) {
                    eprintln!("run={} recording refusal: {e}", rr.run);
                }
            });
        }
        eprintln!("refused run={run} reason={} detail={}", r.reason, r.detail);
        let body = serde_json::json!({"type": "error", "error": {"type": "permission_error", "message": format!("wiki-proxy refused: {}", r.reason)}});
        Response::builder()
            .status(REFUSAL_STATUS)
            .header(CONTENT_TYPE, "application/json")
            .body(full(body.to_string()))
            .unwrap()
    }

    async fn settle(&self, rr: &Registration, reserved: i64, day: &str, rep: Option<Reported>) {
        if reserved == 0 {
            return;
        }
        let (acc, rr, day) = (self.accounts.clone(), rr.clone(), day.to_string());
        let res = tokio::task::spawn_blocking(move || acc.settle(&rr, reserved, &day, rep).map_err(|e| (rr.run, e))).await;
        if let Ok(Err((run, e))) = res {
            eprintln!("run={run} settling: {e}");
        }
    }

    pub async fn handle(self: Arc<Self>, peer: Result<String, String>, req: Request<Incoming>) -> Response<RespBody> {
        let cg = match peer {
            Err(e) => return self.deny(None, refuse("peer", e)),
            Ok(cg) => cg,
        };
        let Some(run) = run_from_cgroup(&cg) else {
            return self.deny(None, refuse("peer", format!("cgroup {cg:?} is not an agent unit")));
        };
        let rr = match lookup_run(&self.registry, run) {
            Ok(rr) => rr,
            Err(e) => return self.deny(None, refuse("unregistered", format!("run {run}: {e}"))),
        };
        let (parts, body) = req.into_parts();
        let betas = match check_request(&parts.method, &parts.uri, &parts.headers) {
            Ok(b) => b,
            Err(r) => return self.deny(Some(&rr), r),
        };
        let collected = match Limited::new(body, MAX_BODY).collect().await {
            Ok(c) => c,
            Err(_) => return self.deny(Some(&rr), refuse("body", format!("unreadable or over {MAX_BODY} bytes"))),
        };
        // Trailers arrive after the body and would skip the header allowlist.
        if collected.trailers().is_some() {
            return self.deny(Some(&rr), refuse("trailer", "request trailers"));
        }
        let body = collected.to_bytes();
        let info = match check_body(&body) {
            Ok(i) => i,
            Err(r) => return self.deny(Some(&rr), r),
        };
        // `wiki jail-check` runs only send requests the policy must refuse;
        // should one pass, it still never leaves the machine.
        if rr.command == "jail-check" {
            return self.deny(Some(&rr), refuse("jail-check", "jail-check runs never reach Anthropic"));
        }
        let token = match (self.token)() {
            Ok(t) if !t.is_empty() => t,
            Ok(_) => return self.deny(Some(&rr), refuse("token", "empty")),
            Err(e) => return self.deny(Some(&rr), refuse("token", e)),
        };
        // count_tokens is free; a message request reserves its worst case: the
        // whole body as input (bytes/2 overestimates tokens for text and JSON)
        // plus every output token it may generate.
        let (reserved, day) = if parts.uri.path() == "/v1/messages" {
            let n = (body.len() / 2) as i64 + info.max_tokens as i64;
            let (acc, rr2, model) = (self.accounts.clone(), rr.clone(), info.model.clone());
            match tokio::task::spawn_blocking(move || acc.reserve(&rr2, &model, n)).await {
                Ok(Ok(day)) => (n, day),
                Ok(Err(r)) => return self.deny(Some(&rr), r),
                Err(e) => return self.deny(Some(&rr), refuse("state", e.to_string())),
            }
        } else {
            (0, String::new())
        };

        let pq = match parts.uri.query() {
            Some("beta=true") => format!("{}?beta=true", parts.uri.path()),
            _ => parts.uri.path().to_string(),
        };
        let uri = Uri::builder()
            .scheme(self.upstream.scheme().unwrap().clone())
            .authority(self.upstream.authority().unwrap().clone())
            .path_and_query(pq)
            .build()
            .unwrap();
        let mut out = Request::builder().method(Method::POST).uri(uri);
        for (k, v) in &parts.headers {
            if ALLOWED_HEADERS.contains(&k.as_str()) && k != "anthropic-beta" {
                out = out.header(k, v);
            }
        }
        let mut betas = betas;
        betas.push(OAUTH_BETA.to_string());
        let auth = match HeaderValue::from_str(&format!("Bearer {token}")) {
            Ok(v) => v,
            Err(_) => return self.deny(Some(&rr), refuse("token", "not a header value")),
        };
        let out = out.header("anthropic-beta", betas.join(",")).header(AUTHORIZATION, auth).body(Full::new(body)).unwrap();

        // Never reached Anthropic, failed mid-way or stalled: keep the charge.
        let resp = match tokio::time::timeout(self.response_timeout, self.client.request(out)).await {
            Ok(Ok(r)) => r,
            Ok(Err(e)) => {
                eprintln!("run={} upstream: {e}", rr.run);
                return Response::builder().status(StatusCode::BAD_GATEWAY).body(full("")).unwrap();
            }
            Err(_) => {
                eprintln!("run={} upstream: no answer within {:?}", rr.run, self.response_timeout);
                return Response::builder().status(StatusCode::GATEWAY_TIMEOUT).body(full("")).unwrap();
            }
        };
        let (rparts, mut rbody) = resp.into_parts();
        let mut parser = if rparts.status.is_success() {
            let ct = rparts.headers.get(CONTENT_TYPE).and_then(|v| v.to_str().ok()).unwrap_or("");
            Some(UsageParser::new(ct))
        } else {
            // Anthropic does not bill a request it answered with an error.
            self.settle(&rr, reserved, &day, Some(Reported::default())).await;
            None
        };
        let (tx, rx) = mpsc::channel(16);
        let me = self.clone();
        tokio::spawn(async move {
            loop {
                match rbody.frame().await {
                    None => break,
                    Some(Err(e)) => {
                        let _ = tx.send(Err(io::Error::other(e))).await;
                        break;
                    }
                    Some(Ok(f)) => {
                        if let (Some(p), Some(d)) = (parser.as_mut(), f.data_ref()) {
                            p.feed(d);
                        }
                        if tx.send(Ok(f)).await.is_err() {
                            break; // client gone
                        }
                    }
                }
            }
            // Whichever way the relay ended: a complete report settles, and
            // without one the reservation stays charged.
            if let Some(p) = parser.take() {
                me.settle(&rr, reserved, &day, p.finish()).await;
            }
        });
        let mut builder = Response::builder().status(rparts.status);
        for (k, v) in &rparts.headers {
            if !matches!(k.as_str(), "connection" | "transfer-encoding" | "keep-alive" | "content-length") {
                builder = builder.header(k, v);
            }
        }
        builder.body(ChannelBody(rx).boxed()).unwrap()
    }

    /// Serves the socket. The peer is identified once per connection, right
    /// after accept, before any byte from it is read.
    pub async fn serve(self: Arc<Self>, listener: UnixListener) -> io::Result<()> {
        loop {
            let stream = match listener.accept().await {
                Ok((s, _)) => s,
                Err(e) => {
                    // EMFILE and friends pass; the proxy must not exit on them.
                    eprintln!("proxy: accept: {e}");
                    tokio::time::sleep(Duration::from_millis(100)).await;
                    continue;
                }
            };
            let peer = (self.peer)(stream.as_raw_fd()).map_err(|e| e.to_string());
            let me = self.clone();
            tokio::spawn(async move {
                let svc = service_fn(move |req| {
                    let me = me.clone();
                    let peer = peer.clone();
                    async move { Ok::<_, Infallible>(me.handle(peer, req).await) }
                });
                let _ = hyper::server::conn::http1::Builder::new()
                    .timer(TokioTimer::new())
                    .header_read_timeout(Duration::from_secs(10))
                    .serve_connection(TokioIo::new(stream), svc)
                    .await;
            });
        }
    }
}

/// Resolves every name to one fixed address, so the proxy needs no DNS and
/// its unit can deny every other destination. TLS still verifies the
/// certificate for the URI's host.
#[derive(Clone)]
pub struct FixedResolver(pub SocketAddr);

impl tower_service::Service<Name> for FixedResolver {
    type Response = std::iter::Once<SocketAddr>;
    type Error = io::Error;
    type Future = std::future::Ready<Result<Self::Response, io::Error>>;
    fn poll_ready(&mut self, _: &mut Context<'_>) -> Poll<Result<(), io::Error>> {
        Poll::Ready(Ok(()))
    }
    fn call(&mut self, _: Name) -> Self::Future {
        std::future::ready(Ok(std::iter::once(self.0)))
    }
}

pub type PinnedClient = Client<hyper_rustls::HttpsConnector<HttpConnector<FixedResolver>>, Full<Bytes>>;

pub fn pinned_client(addr: SocketAddr) -> PinnedClient {
    let mut http = HttpConnector::new_with_resolver(FixedResolver(addr));
    http.enforce_http(false);
    http.set_connect_timeout(Some(Duration::from_secs(10)));
    let https = hyper_rustls::HttpsConnectorBuilder::new()
        .with_webpki_roots()
        .https_only()
        .enable_http1()
        .wrap_connector(http);
    Client::builder(TokioExecutor::new()).build(https)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::accounting::{DayBudget, Usage, write_json};
    use crate::stream::tests::SSE_OK;
    use crate::testutil::TempDir;
    use serde_json::{Value, json};
    use std::sync::Mutex;
    use tokio::net::{TcpListener, UnixStream};

    const AGENT_CG: &str = r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@run1.service";

    #[derive(Clone)]
    struct Seen {
        uri: Uri,
        headers: hyper::HeaderMap,
    }

    #[derive(Clone)]
    struct Fake {
        status: u16,
        ct: &'static str,
        body: String,
        cut: bool,
        hang: bool,
        seen: Arc<Mutex<Vec<Seen>>>,
    }

    impl Fake {
        fn hits(&self) -> usize {
            self.seen.lock().unwrap().len()
        }
    }

    /// A fake api.anthropic.com on 127.0.0.1 that records what reached it.
    async fn start_fake(f: Fake) -> SocketAddr {
        let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = l.local_addr().unwrap();
        tokio::spawn(async move {
            loop {
                let (s, _) = l.accept().await.unwrap();
                let f = f.clone();
                tokio::spawn(async move {
                    let svc = service_fn(move |req: Request<Incoming>| {
                        let f = f.clone();
                        async move {
                            let (p, b) = req.into_parts();
                            let _ = b.collect().await;
                            f.seen.lock().unwrap().push(Seen { uri: p.uri, headers: p.headers });
                            if f.hang {
                                std::future::pending::<()>().await;
                            }
                            // A cut stream sends its data, lets it flush, then breaks.
                            let (tx, rx) = mpsc::channel(4);
                            let (data, cut) = (f.body.clone(), f.cut);
                            tokio::spawn(async move {
                                let _ = tx.send(Ok(Frame::data(Bytes::from(data)))).await;
                                if cut {
                                    tokio::time::sleep(Duration::from_millis(50)).await;
                                    let _ = tx.send(Err(io::Error::other("cut"))).await;
                                }
                            });
                            let body = ChannelBody(rx).boxed();
                            Ok::<_, Infallible>(Response::builder().status(f.status).header(CONTENT_TYPE, f.ct).body(body).unwrap())
                        }
                    });
                    let _ = hyper::server::conn::http1::Builder::new().serve_connection(TokioIo::new(s), svc).await;
                });
            }
        });
        addr
    }

    struct Rig {
        dir: TempDir,
        fake: Fake,
        sock: PathBuf,
        cg: Arc<Mutex<String>>,
    }

    impl Rig {
        async fn new() -> Rig {
            Rig::with(Fake { status: 200, ct: "text/event-stream", body: SSE_OK.into(), cut: false, hang: false, seen: Default::default() }).await
        }

        async fn with(fake: Fake) -> Rig {
            let dir = TempDir::new();
            std::fs::create_dir_all(dir.path().join("runs")).unwrap();
            let addr = start_fake(fake.clone()).await;
            let cg = Arc::new(Mutex::new(AGENT_CG.to_string()));
            let cg2 = cg.clone();
            let tok = dir.path().join("seat-token");
            std::fs::write(&tok, "seat-secret\n").unwrap();
            let p = Arc::new(Proxy {
                upstream: format!("http://{addr}").parse().unwrap(),
                token: Box::new(move || crate::read_token(&tok)),
                registry: dir.path().join("runs"),
                accounts: Arc::new(Accounts::new(dir.path().join("state"), 1_000_000, 600_000, Box::new(|| "2026-11-28".into()))),
                client: Client::builder(TokioExecutor::new()).build(HttpConnector::new()),
                peer: Box::new(move |_| Ok(cg2.lock().unwrap().clone())),
                response_timeout: Duration::from_millis(500),
            });
            let sock = dir.path().join("p.sock");
            let l = UnixListener::bind(&sock).unwrap();
            tokio::spawn(p.serve(l));
            let r = Rig { dir, fake, sock, cg };
            r.register("run1", "eversports", "ingest");
            r
        }

        fn register(&self, run: &str, org: &str, cmd: &str) {
            let v = json!({"run": run, "org": org, "command": cmd});
            std::fs::write(self.dir.path().join("runs").join(format!("{run}.json")), v.to_string()).unwrap();
        }

        async fn send(&self, req: Request<RespBody>) -> (StatusCode, String) {
            let s = UnixStream::connect(&self.sock).await.unwrap();
            let (mut tx, conn) = hyper::client::conn::http1::handshake(TokioIo::new(s)).await.unwrap();
            tokio::spawn(conn);
            match tx.send_request(req).await {
                Ok(r) => {
                    let st = r.status();
                    let b = r.into_body().collect().await.map(|c| c.to_bytes()).unwrap_or_default();
                    (st, String::from_utf8_lossy(&b).into())
                }
                Err(_) => (StatusCode::from_u16(599).unwrap(), String::new()),
            }
        }

        async fn post(&self, path: &str, body: &Value, hdr: &[(&str, &str)]) -> (StatusCode, String) {
            self.post_raw(path, body.to_string(), hdr).await
        }

        async fn post_raw(&self, path: &str, body: String, hdr: &[(&str, &str)]) -> (StatusCode, String) {
            let mut b = Request::builder()
                .method("POST")
                .uri(path)
                .header("host", "wiki-proxy.invalid")
                .header("content-type", "application/json")
                .header("x-api-key", "placeholder-not-a-key")
                .header("anthropic-version", "2023-06-01")
                .header("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14");
            for (k, v) in hdr {
                b = b.header(*k, *v);
            }
            self.send(b.body(full(body)).unwrap()).await
        }

        fn usage(&self, run: &str) -> Usage {
            std::fs::read(self.dir.path().join("state/usage").join(format!("{run}.json")))
                .ok()
                .and_then(|b| serde_json::from_slice(&b).ok())
                .unwrap_or_default()
        }

        /// Settlement and refusal counts are written after the response; poll
        /// rather than guess how long that takes.
        async fn usage_when(&self, run: &str, ok: impl Fn(&Usage) -> bool) -> Usage {
            for _ in 0..250 {
                let u = self.usage(run);
                if ok(&u) {
                    return u;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
            self.usage(run)
        }

        fn day_used(&self) -> i64 {
            std::fs::read(self.dir.path().join("state/budget.json"))
                .ok()
                .and_then(|b| serde_json::from_slice::<DayBudget>(&b).ok())
                .map_or(0, |d| d.used)
        }
    }

    fn good() -> Value {
        crate::policy::tests::good_body()
    }

    #[tokio::test]
    async fn forwards_with_seat_token_and_pinned_host() {
        let r = Rig::new().await;
        let (st, body) = r.post("/v1/messages?beta=true", &good(), &[]).await;
        assert_eq!(st, 200);
        assert!(body.contains("message_stop"), "{body}");
        let seen = r.fake.seen.lock().unwrap()[0].clone();
        assert_eq!(seen.headers.get("authorization").unwrap(), "Bearer seat-secret");
        assert!(seen.headers.get("x-api-key").is_none());
        assert_eq!(seen.headers.get("anthropic-beta").unwrap(), "claude-code-20250219,interleaved-thinking-2025-05-14,oauth-2025-04-20");
        let host = seen.headers.get("host").unwrap().to_str().unwrap();
        assert!(host.starts_with("127.0.0.1"), "host {host}");
        assert_eq!(seen.uri.query(), Some("beta=true"));
    }

    #[tokio::test]
    async fn settles_stream_usage_to_run_and_org() {
        let r = Rig::new().await;
        r.post("/v1/messages?beta=true", &good(), &[]).await;
        let u = r.usage_when("run1", |u| u.unsettled_requests == 0 && u.requests == 1).await;
        let want = Usage {
            run: "run1".into(), org: "eversports".into(), command: "ingest".into(), model: "claude-sonnet-5-5".into(),
            requests: 1, input_tokens: 100, cache_write_tokens: 2000, cache_read_tokens: 30000, output_tokens: 50,
            charged_tokens: 2150, ..Default::default()
        };
        assert_eq!(u, want);
        assert_eq!(r.day_used(), 2150);
    }

    #[tokio::test]
    async fn cut_stream_keeps_reservation() {
        let r = Rig::with(Fake { status: 200, ct: "text/event-stream", body: SSE_OK.split("\n\n").next().unwrap().into(), cut: true, hang: false, seen: Default::default() }).await;
        r.post("/v1/messages?beta=true", &good(), &[]).await;
        tokio::time::sleep(Duration::from_millis(50)).await;
        let u = r.usage("run1");
        assert!(u.unsettled_requests == 1 && u.charged_tokens >= 128_000, "{u:?}");
        assert_eq!(r.day_used(), u.charged_tokens);
    }

    /// A stream that ends cleanly but never reports final usage is not a
    /// settlement either.
    #[tokio::test]
    async fn stream_without_final_usage_keeps_reservation() {
        let r = Rig::with(Fake { status: 200, ct: "text/event-stream", body: SSE_OK.split("\n\n").next().unwrap().into(), cut: false, hang: false, seen: Default::default() }).await;
        r.post("/v1/messages?beta=true", &good(), &[]).await;
        tokio::time::sleep(Duration::from_millis(50)).await;
        let u = r.usage("run1");
        assert!(u.unsettled_requests == 1 && u.charged_tokens >= 128_000, "{u:?}");
    }

    /// Final usage seen, then upstream breaks before the end: still settled.
    #[tokio::test]
    async fn complete_report_settles_even_if_the_stream_then_breaks() {
        let r = Rig::with(Fake { status: 200, ct: "text/event-stream", body: SSE_OK.into(), cut: true, hang: false, seen: Default::default() }).await;
        r.post("/v1/messages?beta=true", &good(), &[]).await;
        let u = r.usage_when("run1", |u| u.unsettled_requests == 0 && u.requests == 1).await;
        assert!(u.unsettled_requests == 0 && u.charged_tokens == 2150, "{u:?}");
    }

    /// Upstream accepts but never answers: 504, and the charge stays.
    #[tokio::test]
    async fn stalled_upstream_times_out_and_keeps_charge() {
        let r = Rig::with(Fake { status: 200, ct: "text/event-stream", body: String::new(), cut: false, hang: true, seen: Default::default() }).await;
        let (st, _) = r.post("/v1/messages?beta=true", &good(), &[]).await;
        assert_eq!(st, StatusCode::GATEWAY_TIMEOUT);
        let u = r.usage("run1");
        assert!(u.unsettled_requests == 1 && u.charged_tokens >= 128_000, "{u:?}");
    }

    #[tokio::test]
    async fn upstream_error_is_not_charged() {
        let r = Rig::with(Fake { status: 529, ct: "application/json", body: r#"{"type":"error"}"#.into(), cut: false, hang: false, seen: Default::default() }).await;
        let (st, _) = r.post("/v1/messages?beta=true", &good(), &[]).await;
        assert_eq!(st, 529);
        let u = r.usage("run1");
        assert!(u.charged_tokens == 0 && u.unsettled_requests == 0, "{u:?}");
    }

    #[tokio::test]
    async fn run_budget_refuses_fifth_unsettled_request() {
        let r = Rig::with(Fake { status: 200, ct: "text/event-stream", body: String::new(), cut: true, hang: false, seen: Default::default() }).await;
        let mut last = StatusCode::OK;
        for _ in 0..5 {
            last = r.post("/v1/messages?beta=true", &good(), &[]).await.0;
        }
        assert_eq!(last, REFUSAL_STATUS);
        assert_eq!(r.usage_when("run1", |u| !u.refusals.is_empty()).await.refusals.get("budget-run"), Some(&1));
    }

    #[tokio::test]
    async fn day_budget_refuses_and_resets_next_day() {
        let r = Rig::new().await;
        let state = r.dir.path().join("state");
        write_json(&state.join("budget.json"), &DayBudget { day: "2026-11-28".into(), used: 950_000 }).unwrap();
        assert_eq!(r.post("/v1/messages?beta=true", &good(), &[]).await.0, REFUSAL_STATUS);
        assert_eq!(r.fake.hits(), 0);
        write_json(&state.join("budget.json"), &DayBudget { day: "2026-11-27".into(), used: 950_000 }).unwrap();
        assert_eq!(r.post("/v1/messages?beta=true", &good(), &[]).await.0, 200);
    }

    #[tokio::test]
    async fn unreadable_state_refuses() {
        let r = Rig::new().await;
        let state = r.dir.path().join("state");
        std::fs::create_dir_all(&state).unwrap();
        std::fs::write(state.join("budget.json"), r#"{"day":"#).unwrap();
        assert_eq!(r.post("/v1/messages?beta=true", &good(), &[]).await.0, REFUSAL_STATUS);
        assert_eq!(r.fake.hits(), 0);
    }

    #[tokio::test]
    async fn count_tokens_is_free() {
        let r = Rig::with(Fake { status: 200, ct: "application/json", body: r#"{"input_tokens":1234}"#.into(), cut: false, hang: false, seen: Default::default() }).await;
        let mut b = good();
        b.as_object_mut().unwrap().remove("stream");
        b.as_object_mut().unwrap().remove("max_tokens");
        assert_eq!(r.post("/v1/messages/count_tokens?beta=true", &b, &[]).await.0, 200);
        assert_eq!(r.day_used(), 0);
    }

    #[tokio::test]
    async fn refuses_peers() {
        for cg in [
            "/user.slice/user-1000.slice/user@1000.service/app.slice/wiki-agent@run1.service",
            r"/system.slice/system-wiki\x2dagent\x2devil.slice/wiki-agent-evil@run1.service",
            "/system.slice/sshd.service",
            r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@run2.service",
        ] {
            let r = Rig::new().await;
            *r.cg.lock().unwrap() = cg.to_string();
            assert_eq!(r.post("/v1/messages?beta=true", &good(), &[]).await.0, REFUSAL_STATUS, "{cg}");
            assert_eq!(r.fake.hits(), 0, "{cg}");
        }
    }

    #[tokio::test]
    async fn jail_check_runs_never_reach_upstream() {
        let r = Rig::new().await;
        r.register("run1", "personal", "jail-check");
        assert_eq!(r.post("/v1/messages?beta=true", &good(), &[]).await.0, REFUSAL_STATUS);
        assert_eq!(r.fake.hits(), 0);
        assert_eq!(r.usage_when("run1", |u| !u.refusals.is_empty()).await.refusals.get("jail-check"), Some(&1));
    }

    #[tokio::test]
    async fn refuses_malformed_registration() {
        let r = Rig::new().await;
        r.register("run1", "acme", "ingest");
        assert_eq!(r.post("/v1/messages?beta=true", &good(), &[]).await.0, REFUSAL_STATUS);
    }

    /// Every refusal class reaches the HTTP layer, is counted against the run
    /// and never reaches upstream.
    #[tokio::test]
    async fn refusals_are_counted_and_not_forwarded() {
        let mut mcp = good();
        mcp["mcp_servers"] = json!([{"type": "url", "url": "https://x"}]);
        let dup = r#"{"model":"claude-sonnet-5-5","max_tokens":10,"messages":[{"role":"user","content":"a","Content":"b"}]}"#.to_string();
        let cases: Vec<(&str, String, Vec<(&str, &str)>, &str)> = vec![
            ("/v1/models", good().to_string(), vec![], "path"),
            ("/v1/messages?beta=true&x=1", good().to_string(), vec![], "query"),
            ("/v1/messages", good().to_string(), vec![("x-forwarded-for", "1")], "header"),
            ("/v1/messages", good().to_string(), vec![("content-encoding", "gzip")], "header"),
            ("/v1/messages", mcp.to_string(), vec![], "key"),
            ("/v1/messages", dup, vec![], "duplicate"),
        ];
        for (path, body, hdr, reason) in cases {
            let r = Rig::new().await;
            let (st, _) = r.post_raw(path, body, &hdr).await;
            assert_eq!(st, REFUSAL_STATUS, "{reason}");
            assert_eq!(r.fake.hits(), 0, "{reason}");
            assert_eq!(r.usage_when("run1", |u| !u.refusals.is_empty()).await.refusals.get(reason), Some(&1), "{reason}");
        }
    }

    /// Raw bytes, because hyper's own client sends only trailers the Trailer
    /// header declares, and the proxy refuses that header.
    #[tokio::test]
    async fn refuses_undeclared_trailers() {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let r = Rig::new().await;
        let body = good().to_string();
        let raw = format!(
            "POST /v1/messages?beta=true HTTP/1.1\r\nhost: wiki-proxy.invalid\r\ncontent-type: application/json\r\ntransfer-encoding: chunked\r\n\r\n{:x}\r\n{body}\r\n0\r\nanthropic-beta: mcp-client-2025-04-04\r\n\r\n",
            body.len()
        );
        let mut s = UnixStream::connect(&r.sock).await.unwrap();
        s.write_all(raw.as_bytes()).await.unwrap();
        let mut buf = vec![0u8; 64];
        let n = s.read(&mut buf).await.unwrap();
        let head = String::from_utf8_lossy(&buf[..n]);
        assert!(head.starts_with("HTTP/1.1 403"), "{head}");
        assert_eq!(r.fake.hits(), 0);
    }

    /// The real proxy code, the real pinned TLS client and real Claude Code in
    /// a network-less namespace; only the peer lookup is stubbed, since the
    /// agent's cgroup exists only once the system unit is installed. Spends a
    /// few hundred tokens.
    #[tokio::test]
    #[ignore = "live: WIKI_LIVE_TOKEN_FILE=<file with one OAuth token> WIKI_LIVE_CLAUDE=<claude binary>"]
    async fn live_claude_code_through_the_proxy() {
        let tok = PathBuf::from(std::env::var("WIKI_LIVE_TOKEN_FILE").unwrap());
        let claude = std::env::var("WIKI_LIVE_CLAUDE").unwrap();
        let dir = TempDir::new();
        std::fs::create_dir_all(dir.path().join("runs")).unwrap();
        std::fs::write(dir.path().join("runs/run1.json"), r#"{"run":"run1","org":"personal","command":"ingest"}"#).unwrap();
        let p = Arc::new(Proxy {
            upstream: crate::UPSTREAM.parse().unwrap(),
            token: Box::new(move || crate::read_token(&tok)),
            registry: dir.path().join("runs"),
            accounts: Arc::new(Accounts::new(dir.path().join("state"), 2_000_000, 1_000_000, Box::new(crate::accounting::local_today))),
            client: pinned_client("160.79.104.10:443".parse().unwrap()),
            peer: Box::new(|_| Ok(AGENT_CG.to_string())),
            response_timeout: Duration::from_secs(60),
        });
        let sock = dir.path().join("p.sock");
        tokio::spawn(p.serve(UnixListener::bind(&sock).unwrap()));
        let work = dir.path().join("work");
        std::fs::create_dir_all(work.join("in")).unwrap();
        std::fs::write(work.join("in/item.md"), "# Decisions\n\n## 2026-01-01 Use SQLite\nThe queue uses SQLite because it needs no server.\n").unwrap();
        let home = dir.path().join("home");
        std::fs::create_dir_all(&home).unwrap();
        let (w, h, s) = (work.clone(), home.clone(), sock.clone());
        let out = tokio::task::spawn_blocking(move || {
            std::process::Command::new("unshare")
                .args(["-rn", "env", "-i"])
                .arg(format!("HOME={}", h.display()))
                .arg("PATH=/usr/bin:/bin")
                .arg(format!("ANTHROPIC_UNIX_SOCKET={}", s.display()))
                .args(["ANTHROPIC_BASE_URL=http://wiki-proxy.invalid", "ANTHROPIC_API_KEY=placeholder-not-a-key", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_GZIP_REQUEST_BODIES=0"])
                .arg(&claude)
                .args(["-p", r#"Read in/item.md. Write result.json containing {"pages":[{"path":"decisions/queue.md","content":"<one-line fact>"}]} with the fact from the file. Reply done."#])
                .args(["--model", "claude-sonnet-5-5", "--tools", "Read,Edit,Write", "--permission-mode", "acceptEdits", "--strict-mcp-config", "--setting-sources", ""])
                .current_dir(&w)
                .output()
                .unwrap()
        })
        .await
        .unwrap();
        let stdout = String::from_utf8_lossy(&out.stdout);
        assert!(out.status.success(), "claude failed: {stdout} {}", String::from_utf8_lossy(&out.stderr));
        let result = std::fs::read_to_string(work.join("result.json")).unwrap();
        assert!(result.contains("SQLite"), "{result}");
        tokio::time::sleep(Duration::from_millis(200)).await;
        let u: Usage = serde_json::from_slice(&std::fs::read(dir.path().join("state/usage/run1.json")).unwrap()).unwrap();
        eprintln!("usage: {}", serde_json::to_string(&u).unwrap());
        assert!(u.requests >= 2 && u.output_tokens > 0 && u.unsettled_requests == 0 && u.refusals.is_empty() && u.org == "personal", "{u:?}");
    }

}
