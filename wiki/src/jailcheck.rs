//! `wiki jail-check` runs in place of Claude Code inside a wiki-agent@<run>
//! unit and writes what it could and could not reach to result.json. Every
//! request it sends the proxy is one the proxy must refuse; and should the
//! policy ever let one through, the proxy refuses to forward anything for the
//! `jail-check` command, so a probe never reaches Anthropic.

use serde::Serialize;
use std::fs;
use std::io::{self, Read, Write};
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

/// Everything the check touches, so a test can point it at temp paths.
pub struct Probe {
    pub proxy: String,
    /// The home of the user the starter serves; must be hidden.
    pub user_home: String,
    pub cwd: String,
    /// Places a run could leave something for the next run; each gets a
    /// marker, and each must be free of one from an earlier run.
    pub markers: Vec<String>,
    pub api: SocketAddr,
    /// Paths that must not be writable.
    pub write_targets: Vec<String>,
}

pub const MARKER: &str = ".wiki-jail-check-marker";

fn check(out: &mut Vec<Check>, name: &str, pass: bool, detail: impl Into<String>) {
    out.push(Check { name: name.into(), pass, detail: detail.into() });
}

/// Hidden means: does not exist or may not be opened. Any other error is a
/// failure, not a pass.
fn hidden(e: &io::Error) -> bool {
    matches!(e.kind(), io::ErrorKind::NotFound | io::ErrorKind::PermissionDenied)
}

fn unreachable_path(out: &mut Vec<Check>, name: &str, p: &str, allow: &str) {
    match fs::read_dir(p) {
        Ok(rd) => {
            let names: Vec<String> = rd.flatten().map(|e| e.file_name().to_string_lossy().to_string()).filter(|n| n != allow).collect();
            check(out, name, names.is_empty(), format!("{p} shows {names:?}"));
        }
        Err(e) if hidden(&e) => check(out, name, true, format!("{p}: {e}")),
        Err(e) => match fs::read(p) {
            Ok(_) => check(out, name, false, format!("{p} is readable")),
            Err(e2) if hidden(&e2) => check(out, name, true, format!("{p}: {e2}")),
            Err(_) => check(out, name, false, format!("{p}: unexpected {e}")),
        },
    }
}

fn no_unix(out: &mut Vec<Check>, name: &str, p: &str) {
    match UnixStream::connect(p) {
        Ok(_) => check(out, name, false, format!("connected to {p}")),
        Err(e) => check(out, name, true, format!("{p}: {e}")),
    }
}

/// Sends one raw request and returns the status code and the whole answer.
pub fn ask_proxy(sock: &str, raw: &str) -> Result<(u16, String), String> {
    let mut s = UnixStream::connect(sock).map_err(|e| e.to_string())?;
    s.set_read_timeout(Some(Duration::from_secs(10))).ok();
    s.write_all(raw.as_bytes()).map_err(|e| e.to_string())?;
    let mut buf = String::new();
    s.read_to_string(&mut buf).map_err(|e| e.to_string())?;
    Ok((parse_status(&buf)?, buf))
}

/// The code from an "HTTP/1.x NNN reason" status line.
pub fn parse_status(buf: &str) -> Result<u16, String> {
    let line = buf.lines().next().ok_or("empty answer")?;
    let mut parts = line.split(' ');
    match (parts.next(), parts.next()) {
        (Some(v), Some(code)) if v.starts_with("HTTP/1.") => code.parse().map_err(|_| format!("bad status line {line:?}")),
        _ => Err(format!("bad status line {line:?}")),
    }
}

pub fn req(method: &str, path: &str, extra: &str, body: &str) -> String {
    format!(
        "{method} {path} HTTP/1.1\r\nhost: wiki-proxy.invalid\r\ncontent-type: application/json\r\nconnection: close\r\n{extra}content-length: {}\r\n\r\n{body}",
        body.len()
    )
}

/// The number of refusals a full run asks the proxy for; the host script
/// compares the proxy's count with it.
pub const PROXY_PROBES: usize = 10;

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
        ("proxy forwards nothing for jail-check", req("POST", "/v1/messages", "", &msg("")), "jail-check"),
    ];
    debug_assert_eq!(cases.len(), PROXY_PROBES);
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

pub fn run(p: &Probe) -> Report {
    let mut out = Vec::new();

    // Nothing survives a run: no marker an earlier run left may be visible.
    for dir in &p.markers {
        let m = Path::new(dir).join(MARKER);
        check(&mut out, &format!("no marker from an earlier run in {dir}"), !m.exists(), m.display().to_string());
    }

    // The host script plants a "decoy" entry in each shared directory, so
    // these find something unless the jail hides it.
    let own_run = Path::new(&p.cwd).file_name().map(|n| n.to_string_lossy().to_string()).unwrap_or_default();
    unreachable_path(&mut out, "the user's home is hidden", &p.user_home, "");
    unreachable_path(&mut out, "root's home is hidden", "/root", "");
    unreachable_path(&mut out, "other runs' staging is hidden", "/var/lib/wiki/runs", "");
    unreachable_path(&mut out, "other runs' state is hidden", "/var/lib/private/wiki-agent", &own_run);
    unreachable_path(&mut out, "proxy state and every run's usage are hidden", "/var/lib/wiki-proxy", "");
    unreachable_path(&mut out, "registrations are hidden", "/run/wiki/runs", "");

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
    match TcpStream::connect_timeout(&p.api, Duration::from_secs(3)) {
        Ok(_) => check(&mut out, "Anthropic is unreachable directly", false, "connected"),
        Err(e) => check(&mut out, "Anthropic is unreachable directly", true, e.to_string()),
    }

    for t in &p.write_targets {
        let r = fs::write(t, "x");
        if r.is_ok() {
            let _ = fs::remove_file(t);
        }
        check(&mut out, &format!("cannot write {t}"), r.is_err(), format!("{r:?}"));
    }

    match UnixStream::connect(&p.proxy) {
        Ok(_) => check(&mut out, "the proxy socket is reachable", true, p.proxy.clone()),
        Err(e) => check(&mut out, "the proxy socket is reachable", false, e.to_string()),
    }
    proxy_refusals(&mut out, &p.proxy);

    for dir in &p.markers {
        let _ = fs::write(Path::new(dir).join(MARKER), "x");
    }
    Report { pass: out.iter().all(|c| c.pass), checks: out }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testutil::TempDir;
    use std::os::unix::net::UnixListener;

    #[test]
    fn status_lines() {
        assert_eq!(parse_status("HTTP/1.1 403 Forbidden\r\n\r\n").unwrap(), 403);
        assert_eq!(parse_status("HTTP/1.0 200 OK\r\n").unwrap(), 200);
        assert!(parse_status("").is_err());
        assert!(parse_status("garbage\r\nHTTP/1.1 403 x").is_err());
        assert!(parse_status("HTTP/1.1 abc x").is_err());
        assert!(parse_status("XYZ 403 x").is_err());
    }

    /// ask_proxy against a local socket serving a canned refusal.
    #[test]
    fn ask_proxy_reads_status_and_reason() {
        let d = TempDir::new();
        let sock = d.path().join("s");
        let l = UnixListener::bind(&sock).unwrap();
        let srv = std::thread::spawn(move || {
            let (mut c, _) = l.accept().unwrap();
            let mut buf = [0u8; 4096];
            let _ = c.read(&mut buf);
            let body = r#"{"type":"error","error":{"type":"permission_error","message":"wiki-proxy refused: path"}}"#;
            write!(c, "HTTP/1.1 403 Forbidden\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{body}", body.len()).unwrap();
        });
        let (st, body) = ask_proxy(sock.to_str().unwrap(), &req("GET", "/v1/models", "", "")).unwrap();
        srv.join().unwrap();
        assert_eq!(st, 403);
        assert!(body.contains("wiki-proxy refused: path"));
    }

    /// Outside a jail the checks must fail, so they are not vacuous. Writes
    /// go only to a temp dir; it does connect to this host's system sockets
    /// (dropping each connection at once) and reads /proc and /root.
    #[test]
    fn fails_outside_the_jail() {
        let d = TempDir::new();
        let dir = d.path().display().to_string();
        let home = d.path().join("home");
        fs::create_dir_all(&home).unwrap();
        fs::write(home.join("secret"), "x").unwrap();
        let p = Probe {
            proxy: "/nonexistent/proxy.sock".into(),
            user_home: home.display().to_string(),
            cwd: dir.clone(),
            markers: vec![dir.clone()],
            api: "127.0.0.1:1".parse().unwrap(),
            write_targets: vec![format!("{dir}/writable")],
        };
        let r = run(&p);
        assert!(!r.pass);
        for name in ["the proxy socket is reachable", "no IP sockets (so no DNS)", "the user's home is hidden"] {
            assert!(r.checks.iter().any(|c| c.name == name && !c.pass), "{name} passed outside a jail");
        }
        assert!(r.checks.iter().any(|c| c.name.starts_with("cannot write") && !c.pass));
        assert!(!Path::new(&format!("{dir}/writable")).exists(), "a successful write probe is removed");
        // A second run sees the first run's marker.
        assert!(run(&p).checks.iter().any(|c| c.name.starts_with("no marker") && !c.pass));
    }
}
