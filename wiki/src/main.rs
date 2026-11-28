//! wiki maintains the agent-written wiki. `wiki proxy` is the only path from
//! an ingest agent's jail to Anthropic: it holds the seat token the agent
//! never sees, forwards only message requests, and charges every token to the
//! run's org and command. `wiki starter` is the root, socket-activated way to
//! start one jailed agent run for the user.

mod accounting;
mod ingest;
mod jailcheck;
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
/// The address the proxy dials for UPSTREAM; its unit's firewall allows only
/// Anthropic's /23 (anthropicApi in nix/hosts/tau/wiki.nix). jail-check probes
/// the same address.
pub const ANTHROPIC_ADDR: &str = "160.79.104.10:443";

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let res = match args.first().map(String::as_str) {
        Some("proxy") => run_proxy(&args[1..]),
        Some("starter") => run_starter(&args[1..]),
        Some("start") => run_start(&args[1..]),
        Some("jail-check") => run_jail_check(&args[1..]),
        Some("ingest") => run_ingest(&args[1..]),
        _ => Err("usage: wiki proxy|starter|start|jail-check|ingest [--flag value ...]".into()),
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
    let connect: std::net::SocketAddr = get("connect", ANTHROPIC_ADDR).parse().map_err(|_| "--connect")?;
    let listener = std_listener(f.get("listen"))?;
    listener.set_nonblocking(true).map_err(|e| e.to_string())?;
    raise_nofile();
    let rt = tokio::runtime::Builder::new_current_thread().enable_all().build().map_err(|e| e.to_string())?;
    rt.block_on(async move {
        let p = Arc::new(proxy::Proxy {
            upstream: UPSTREAM.parse().unwrap(),
            token: Box::new(move || read_token(&token)),
            registry: PathBuf::from(get("registry", "/run/wiki/runs")),
            accounts: Arc::new(accounting::Accounts::new(PathBuf::from(get("state", "/var/lib/wiki-proxy")), day, run, Box::new(accounting::local_today))),
            client: proxy::pinned_client(connect),
            peer: Box::new(peer::peer_cgroup),
            response_timeout: Duration::from_secs(60),
        });
        let l = tokio::net::UnixListener::from_std(listener).map_err(|e| e.to_string())?;
        eprintln!("proxy: serving");
        p.serve(l).await.map_err(|e| e.to_string())
    })
}

/// Lifts the soft open-files limit to the hard one, as Go's runtime does, so
/// a burst of connections meets the unit's real limit.
fn raise_nofile() {
    // SAFETY: getrlimit/setrlimit on a zeroed rlimit.
    unsafe {
        let mut r: libc::rlimit = std::mem::zeroed();
        if libc::getrlimit(libc::RLIMIT_NOFILE, &mut r) == 0 && r.rlim_cur < r.rlim_max {
            r.rlim_cur = r.rlim_max;
            libc::setrlimit(libc::RLIMIT_NOFILE, &r);
        }
    }
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

/// Runs in place of Claude Code inside an agent unit (see wiki/jail-check.sh)
/// and leaves its report as result.json for the starter to return.
fn run_jail_check(args: &[String]) -> Result<(), String> {
    let f = flags(args, &["proxy", "user-home"])?;
    let sock = f.get("proxy").cloned().unwrap_or_else(|| "/run/wiki/proxy.sock".into());
    let user_home = f.get("user-home").ok_or("--user-home is required: the home that must be hidden")?;
    let home = std::env::var("HOME").unwrap_or_default();
    let cwd = std::env::current_dir().map(|p| p.display().to_string()).map_err(|e| e.to_string())?;
    let probe = jailcheck::Probe {
        proxy: sock,
        user_home: user_home.clone(),
        markers: vec![home, cwd.clone(), "/var/tmp".into(), "/dev/shm".into()],
        cwd,
        api: ANTHROPIC_ADDR.parse().unwrap(),
        write_targets: vec!["/etc/wiki-jail-check".into(), "/nix/store/wiki-jail-check".into(), "/var/lib/wiki-jail-check".into()],
    };
    let report = jailcheck::run(&probe);
    for c in &report.checks {
        println!("{} {}: {}", if c.pass { "PASS" } else { "FAIL" }, c.name, c.detail);
    }
    std::fs::write("result.json", serde_json::to_vec(&report).unwrap()).map_err(|e| e.to_string())
}

/// Asks the starter for one run: stages every file under --files (relative
/// paths kept), sends --prompt, prints the starter's answer as JSON.
fn run_start(args: &[String]) -> Result<(), String> {
    let f = flags(args, &["run", "org", "command", "prompt", "files", "socket"])?;
    let need = |k: &str| f.get(k).cloned().ok_or_else(|| format!("--{k} is required"));
    let prompt = std::fs::read_to_string(need("prompt")?).map_err(|e| format!("--prompt: {e}"))?;
    let mut files = std::collections::BTreeMap::new();
    if let Some(dir) = f.get("files") {
        collect_files(Path::new(dir), Path::new(dir), &mut files)?;
    }
    let req = starter::StartRequest { run: need("run")?, org: need("org")?, command: need("command")?, prompt, files };
    let sock = f.get("socket").cloned().unwrap_or_else(|| "/run/wiki/starter.sock".into());
    let resp = starter::ask(Path::new(&sock), &req).map_err(|e| format!("{sock}: {e}"))?;
    println!("{}", serde_json::to_string(&resp).unwrap());
    if resp.ok { Ok(()) } else { Err(resp.error) }
}

fn collect_files(root: &Path, dir: &Path, out: &mut std::collections::BTreeMap<String, String>) -> Result<(), String> {
    for e in std::fs::read_dir(dir).map_err(|e| format!("{}: {e}", dir.display()))? {
        let e = e.map_err(|e| e.to_string())?;
        let ft = e.file_type().map_err(|e| e.to_string())?;
        let p = e.path();
        if ft.is_dir() {
            collect_files(root, &p, out)?;
        } else if ft.is_file() {
            let rel = p.strip_prefix(root).unwrap().to_string_lossy().to_string();
            let c = std::fs::read_to_string(&p).map_err(|e| format!("{}: {e}", p.display()))?;
            out.insert(rel, c);
        } else {
            return Err(format!("{}: not a regular file or directory", p.display()));
        }
    }
    Ok(())
}

/// Ingests one archived ~/p project folder into one org's pages.
fn run_ingest(args: &[String]) -> Result<(), String> {
    let f = flags(args, &["org", "source", "wiki", "state", "usage", "socket", "p-root"])?;
    let home = std::env::var("HOME").map_err(|_| "no HOME")?;
    let get = |k: &str, d: String| f.get(k).cloned().unwrap_or(d);
    let wiki = PathBuf::from(get("wiki", format!("{home}/sync/wiki")));
    let state = PathBuf::from(get("state", format!("{home}/.local/state/wiki")));
    let ing = ingest::Ingest {
        git: ingest::Git { git_dir: state.join("git"), work_tree: wiki.clone() },
        lock: state.join("ingest.lock"),
        p_root: PathBuf::from(get("p-root", format!("{home}/p"))),
        wiki,
        usage_dir: PathBuf::from(get("usage", "/var/lib/wiki-proxy/usage".into())),
        runner: ingest::SocketRunner(PathBuf::from(get("socket", "/run/wiki/starter.sock".into()))),
        now: ingest::now_utc,
        run_id: ingest::new_run_id,
        scan: ingest::gitleaks_scan,
    };
    let source = f.get("source").ok_or("--source is required")?;
    std::fs::create_dir_all(&ing.wiki).map_err(|e| e.to_string())?;
    let sha = ing.project(f.get("org").map(String::as_str), Path::new(source))?;
    println!("{sha}");
    Ok(())
}
