//! `wiki jail-check` runs in place of Claude Code inside a wiki-agent@<run>
//! unit and writes what it could and could not reach to result.json. Every
//! request it sends the proxy is one the proxy must refuse, so it never
//! reaches Anthropic and spends nothing.

use serde::Serialize;
use std::fs;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream, UdpSocket};
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::time::Duration;

#[derive(Serialize)]
pub struct Check {
    pub name: String,
    pub pass: bool,
    pub detail: String,
}

#[derive(Serialize)]
pub struct Report {
    pub pass: bool,
    pub checks: Vec<Check>,
}

const MARKER: &str = ".wiki-jail-check-marker";

fn check(out: &mut Vec<Check>, name: &str, pass: bool, detail: impl Into<String>) {
    out.push(Check { name: name.into(), pass, detail: detail.into() });
}

fn unreachable_path(out: &mut Vec<Check>, name: &str, p: &str) {
    match fs::read_dir(p) {
        Ok(rd) => {
            let n = rd.count();
            check(out, name, n == 0, format!("{p} lists {n} entries"));
        }
        Err(e) => match fs::read(p) {
            Ok(_) => check(out, name, false, format!("{p} is readable")),
            Err(_) => check(out, name, true, format!("{p}: {e}")),
        },
    }
}

fn no_unix(out: &mut Vec<Check>, name: &str, p: &str) {
    match UnixStream::connect(p) {
        Ok(_) => check(out, name, false, format!("connected to {p}")),
        Err(e) => check(out, name, true, format!("{p}: {e}")),
    }
}

/// Sends one raw request to the proxy and returns the status and body.
fn ask_proxy(sock: &str, raw: &str) -> Result<(u16, String), String> {
    let mut s = UnixStream::connect(sock).map_err(|e| e.to_string())?;
    s.set_read_timeout(Some(Duration::from_secs(10))).ok();
    s.write_all(raw.as_bytes()).map_err(|e| e.to_string())?;
    let mut buf = String::new();
    s.read_to_string(&mut buf).map_err(|e| e.to_string())?;
    let status = buf.get(9..12).and_then(|s| s.parse().ok()).ok_or("no status line")?;
    Ok((status, buf))
}

fn req(method: &str, path: &str, extra: &str, body: &str) -> String {
    format!(
        "{method} {path} HTTP/1.1\r\nhost: wiki-proxy.invalid\r\ncontent-type: application/json\r\nconnection: close\r\n{extra}content-length: {}\r\n\r\n{body}",
        body.len()
    )
}

/// Each must come back 403 with the proxy's own reason, which proves the
/// proxy refused it rather than Anthropic.
fn proxy_refusals(out: &mut Vec<Check>, sock: &str) {
    let msg = |extra: &str| {
        format!(r#"{{"model":"claude-sonnet-5-5","max_tokens":10,"messages":[{{"role":"user","content":[{{"type":"text","text":"x"}}]}}]{extra}}}"#)
    };
    let url_image = r#"{"model":"claude-sonnet-5-5","max_tokens":10,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/x"}}]}]}"#;
    let dup = r#"{"model":"claude-sonnet-5-5","max_tokens":10,"messages":[{"role":"user","content":"a","Content":"b"}]}"#;
    let cases: Vec<(&str, String, &str)> = vec![
        ("proxy refuses the models list", req("GET", "/v1/models", "", ""), "path"),
        ("proxy refuses the files API", req("POST", "/v1/files", "", "{}"), "path"),
        ("proxy refuses extra query parameters", req("POST", "/v1/messages?beta=true&x=1", "", &msg("")), "query"),
        ("proxy refuses an unknown header", req("POST", "/v1/messages", "x-forwarded-for: 1.2.3.4\r\n", &msg("")), "header"),
        ("proxy refuses an unknown beta", req("POST", "/v1/messages", "anthropic-beta: files-api-2025-04-14\r\n", &msg("")), "beta"),
        ("proxy refuses mcp_servers", req("POST", "/v1/messages", "", &msg(r#","mcp_servers":[{"type":"url","url":"https://example.invalid"}]"#)), "key"),
        ("proxy refuses another model", req("POST", "/v1/messages", "", &msg("").replace("claude-sonnet-5-5", "claude-fable-5-2")), "model"),
        ("proxy refuses a url content source", req("POST", "/v1/messages", "", url_image), "source"),
        ("proxy refuses duplicate keys", req("POST", "/v1/messages", "", dup), "duplicate"),
    ];
    for (name, raw, reason) in cases {
        match ask_proxy(sock, &raw) {
            Ok((st, body)) => {
                let ok = st == 403 && body.contains(&format!("wiki-proxy refused: {reason}"));
                check(out, name, ok, format!("status {st}"));
            }
            Err(e) => check(out, name, false, e),
        }
    }
}

pub fn run(proxy_sock: &str, home: &str, cwd: &str, api: SocketAddr) -> Report {
    let mut out = Vec::new();
    let (home, cwd) = (home.to_string(), cwd.to_string());

    // Nothing survives a run: the markers the previous run left must be gone.
    for dir in [&home, &cwd] {
        let m = Path::new(dir).join(MARKER);
        check(&mut out, &format!("no marker from an earlier run in {dir}"), !m.exists(), m.display().to_string());
    }

    unreachable_path(&mut out, "home is hidden", "/home/ch");
    unreachable_path(&mut out, "root's home is hidden", "/root");
    unreachable_path(&mut out, "other runs' staging is hidden", "/var/lib/wiki/runs");
    unreachable_path(&mut out, "proxy state and other runs' usage are hidden", "/var/lib/wiki-proxy");
    unreachable_path(&mut out, "registrations are hidden", "/run/wiki/runs");
    // Its own state directory is visible to it; no other run's may be.
    let own_run = Path::new(&cwd).file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_default();
    let others: Vec<String> = fs::read_dir("/var/lib/private/wiki-agent")
        .map(|rd| rd.flatten().map(|e| e.file_name().to_string_lossy().to_string()).filter(|n| *n != own_run).collect())
        .unwrap_or_default();
    check(&mut out, "other runs' state is hidden", others.is_empty(), format!("other runs {others:?}"));

    no_unix(&mut out, "nscd is unreachable", "/run/nscd/socket");
    no_unix(&mut out, "the system bus is unreachable", "/run/dbus/system_bus_socket");
    no_unix(&mut out, "journald is unreachable", "/run/systemd/journal/socket");
    no_unix(&mut out, "nix-daemon is unreachable", "/nix/var/nix/daemon-socket/socket");
    no_unix(&mut out, "the starter is unreachable", "/run/wiki/starter.sock");

    // Every visible process is in this unit: no other process's environ.
    let own = fs::read_to_string("/proc/self/cgroup").unwrap_or_default();
    let mut foreign = Vec::new();
    if let Ok(rd) = fs::read_dir("/proc") {
        for e in rd.flatten() {
            let n = e.file_name().to_string_lossy().to_string();
            if n.parse::<u32>().is_ok() && fs::read_to_string(format!("/proc/{n}/cgroup")).unwrap_or_default() != own {
                foreign.push(n);
            }
        }
    }
    check(&mut out, "no other unit's process is visible", foreign.is_empty(), format!("foreign pids {foreign:?}"));

    match UdpSocket::bind("0.0.0.0:0") {
        Ok(_) => check(&mut out, "no IP sockets (so no DNS)", false, "udp socket created"),
        Err(e) => check(&mut out, "no IP sockets (so no DNS)", true, e.to_string()),
    }
    match TcpStream::connect_timeout(&api, Duration::from_secs(3)) {
        Ok(_) => check(&mut out, "Anthropic is unreachable directly", false, "connected"),
        Err(e) => check(&mut out, "Anthropic is unreachable directly", true, e.to_string()),
    }

    for p in ["/etc/wiki-jail-check", "/nix/store/wiki-jail-check", "/var/lib/wiki-jail-check"] {
        let r = fs::write(p, "x");
        check(&mut out, &format!("cannot write {p}"), r.is_err(), format!("{r:?}"));
    }

    match UnixStream::connect(proxy_sock) {
        Ok(_) => check(&mut out, "the proxy socket is reachable", true, proxy_sock),
        Err(e) => check(&mut out, "the proxy socket is reachable", false, e.to_string()),
    }
    proxy_refusals(&mut out, proxy_sock);

    for dir in [&home, &cwd] {
        let _ = fs::write(Path::new(dir).join(MARKER), "x");
    }
    Report { pass: out.iter().all(|c| c.pass), checks: out }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn status_line_parsing() {
        let raw = req("GET", "/v1/models", "", "");
        assert!(raw.starts_with("GET /v1/models HTTP/1.1\r\n") && raw.ends_with("content-length: 0\r\n\r\n"), "{raw}");
    }

    /// Outside a jail most checks must fail: the check is not vacuous. It
    /// touches only a temp dir and a refused local port.
    #[test]
    fn fails_outside_the_jail() {
        let d = crate::testutil::TempDir::new();
        let dir = d.path().display().to_string();
        let r = run("/nonexistent/proxy.sock", &dir, &dir, "127.0.0.1:1".parse().unwrap());
        assert!(!r.pass);
        assert!(r.checks.iter().any(|c| c.name == "the proxy socket is reachable" && !c.pass));
        assert!(r.checks.iter().any(|c| c.name == "no IP sockets (so no DNS)" && !c.pass));
        // A second run sees the first run's markers.
        let again = run("/nonexistent/proxy.sock", &dir, &dir, "127.0.0.1:1".parse().unwrap());
        assert!(again.checks.iter().any(|c| c.name.starts_with("no marker") && !c.pass));
    }
}
