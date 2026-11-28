//! wiki maintains the agent-written wiki. `wiki proxy` is the only path from
//! an ingest agent's jail to Anthropic: it holds the seat token the agent
//! never sees, forwards only message requests, and charges every token to the
//! run's org and command. `wiki starter` is the root, socket-activated way to
//! start one jailed agent run for the user.

mod accounting;
mod peer;
mod policy;
mod proxy;
mod starter;
mod stream;
#[cfg(test)]
mod testutil;

use std::collections::HashMap;
use std::os::fd::FromRawFd;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

/// Fixed: the proxy never forwards anywhere else, whatever Host the agent names.
pub const UPSTREAM: &str = "https://api.anthropic.com";

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let res = match args.first().map(String::as_str) {
        Some("proxy") => run_proxy(&args[1..]),
        Some("starter") => run_starter(&args[1..]),
        _ => Err("usage: wiki proxy|starter [--flag value ...]".into()),
    };
    if let Err(e) = res {
        eprintln!("wiki: {e}");
        std::process::exit(1);
    }
}

/// --name value pairs; every flag must be known.
fn flags(args: &[String], known: &[&str]) -> Result<HashMap<String, String>, String> {
    let mut out = HashMap::new();
    let mut it = args.iter();
    while let Some(k) = it.next() {
        let name = k.strip_prefix("--").ok_or_else(|| format!("unexpected argument {k:?}"))?;
        if !known.contains(&name) {
            return Err(format!("unknown flag --{name}"));
        }
        let v = it.next().ok_or_else(|| format!("--{name} needs a value"))?;
        out.insert(name.to_string(), v.clone());
    }
    Ok(out)
}

/// Reads the seat token from a systemd credential file.
pub fn read_token(path: &Path) -> Result<String, String> {
    let s = std::fs::read_to_string(path).map_err(|e| format!("{}: {e}", path.display()))?;
    let t = s.trim();
    if t.is_empty() || t.contains(char::is_whitespace) {
        return Err(format!("{} does not hold one token", path.display()));
    }
    Ok(t.to_string())
}

/// The socket systemd passed (LISTEN_FDS=1, fd 3), or one bound at path.
fn std_listener(path: Option<&String>) -> Result<std::os::unix::net::UnixListener, String> {
    if let Some(p) = path {
        let _ = std::fs::remove_file(p);
        return std::os::unix::net::UnixListener::bind(p).map_err(|e| format!("{p}: {e}"));
    }
    let pid_ok = std::env::var("LISTEN_PID").ok() == Some(std::process::id().to_string());
    if !pid_ok || std::env::var("LISTEN_FDS").ok().as_deref() != Some("1") {
        return Err("no socket passed by systemd and no --listen".into());
    }
    // SAFETY: systemd passed fd 3 to this process as the listening socket.
    Ok(unsafe { std::os::unix::net::UnixListener::from_raw_fd(3) })
}

fn run_proxy(args: &[String]) -> Result<(), String> {
    let f = flags(args, &["registry", "state", "token", "day-tokens", "run-tokens", "connect", "listen"])?;
    let get = |k: &str, d: &str| f.get(k).cloned().unwrap_or_else(|| d.to_string());
    let token = match f.get("token") {
        Some(t) => PathBuf::from(t),
        None => PathBuf::from(std::env::var("CREDENTIALS_DIRECTORY").map_err(|_| "no --token and no CREDENTIALS_DIRECTORY")?).join("seat-token"),
    };
    read_token(&token)?;
    let day: i64 = get("day-tokens", "2000000").parse().map_err(|_| "--day-tokens")?;
    let run: i64 = get("run-tokens", "1000000").parse().map_err(|_| "--run-tokens")?;
    // Anthropic's API address; the unit's firewall allows only its /23 and no DNS.
    let connect: std::net::SocketAddr = get("connect", "160.79.104.10:443").parse().map_err(|_| "--connect")?;
    let listener = std_listener(f.get("listen"))?;
    listener.set_nonblocking(true).map_err(|e| e.to_string())?;
    let rt = tokio::runtime::Builder::new_current_thread().enable_all().build().map_err(|e| e.to_string())?;
    rt.block_on(async move {
        let p = Arc::new(proxy::Proxy {
            upstream: UPSTREAM.parse().unwrap(),
            token: Box::new(move || read_token(&token)),
            registry: PathBuf::from(get("registry", "/run/wiki/runs")),
            accounts: Arc::new(accounting::Accounts::new(PathBuf::from(get("state", "/var/lib/wiki-proxy")), day, run, Box::new(accounting::local_today))),
            client: proxy::pinned_client(connect),
            peer: Box::new(peer::peer_cgroup),
        });
        let l = tokio::net::UnixListener::from_std(listener).map_err(|e| e.to_string())?;
        eprintln!("proxy: serving");
        p.serve(l).await.map_err(|e| e.to_string())
    })
}

fn run_starter(args: &[String]) -> Result<(), String> {
    let f = flags(args, &["runs", "registry", "results", "user", "systemctl", "listen"])?;
    let get = |k: &str, d: &str| f.get(k).cloned().unwrap_or_else(|| d.to_string());
    // SAFETY: geteuid has no preconditions.
    if unsafe { libc::geteuid() } != 0 {
        return Err("starter: must run as root".into());
    }
    let user = f.get("user").ok_or("starter: --user is required")?;
    let uid = uid_of(user)?;
    let s = starter::Starter {
        runs_dir: PathBuf::from(get("runs", "/var/lib/wiki/runs")),
        registry: PathBuf::from(get("registry", "/run/wiki/runs")),
        result_dir: PathBuf::from(get("results", "/var/lib/private/wiki-agent")),
        uid,
        units: starter::Systemd { systemctl: PathBuf::from(get("systemctl", "systemctl")) },
        read_limit: Duration::from_secs(60),
    };
    s.serve(std_listener(f.get("listen"))?).map_err(|e| e.to_string())
}

/// The uid of a user from /etc/passwd; never 0.
fn uid_of(name: &str) -> Result<u32, String> {
    let passwd = std::fs::read_to_string("/etc/passwd").map_err(|e| e.to_string())?;
    passwd
        .lines()
        .filter_map(|l| {
            let mut f = l.split(':');
            let n = f.next()?;
            let uid = f.nth(1)?.parse::<u32>().ok()?;
            (n == name).then_some(uid)
        })
        .find(|&u| u != 0)
        .ok_or_else(|| format!("starter: no non-root user {name:?}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn flags_are_strict() {
        let a = |v: &[&str]| v.iter().map(|s| s.to_string()).collect::<Vec<_>>();
        assert_eq!(flags(&a(&["--user", "ch"]), &["user"]).unwrap()["user"], "ch");
        assert!(flags(&a(&["--uid", "0"]), &["user"]).is_err());
        assert!(flags(&a(&["--user"]), &["user"]).is_err());
        assert!(flags(&a(&["user"]), &["user"]).is_err());
    }

    #[test]
    fn token_file_holds_one_token() {
        let d = testutil::TempDir::new();
        let p = d.path().join("t");
        std::fs::write(&p, "abc\n").unwrap();
        assert_eq!(read_token(&p).unwrap(), "abc");
        std::fs::write(&p, "a b").unwrap();
        assert!(read_token(&p).is_err());
        std::fs::write(&p, "\n").unwrap();
        assert!(read_token(&p).is_err());
    }
}
