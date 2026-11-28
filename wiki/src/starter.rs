//! Runs as root. Takes one run's inputs from the user's uid only, writes them
//! into a run directory it creates itself, registers the run with the proxy,
//! starts wiki-agent@<run>.service and returns its result.json. No path the
//! user controls is ever followed.

use crate::peer::{ORGS, Registration, valid_command, valid_run_id};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs;
use std::io::{self, Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::Duration;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StartRequest {
    pub run: String,
    pub org: String,
    pub command: String,
    pub prompt: String,
    /// relative path -> content, staged read-only under in/
    pub files: BTreeMap<String, String>,
}

#[derive(Debug, Default, Clone, PartialEq, Serialize, Deserialize)]
pub struct StartResponse {
    pub ok: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub unit_result: String,
    /// result.json as the agent wrote it, unvalidated
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub result: String,
}

pub const MAX_PROMPT: usize = 64 << 10;
pub const MAX_STAGED: usize = 16 << 20;
pub const MAX_FILES: usize = 2000;
pub const MAX_RESULT: u64 = 4 << 20;
/// JSON escaping can inflate content up to 6x (\u00XX); validate() then
/// enforces the real limits on the decoded content.
pub const MAX_REQUEST: u64 = (6 * (MAX_STAGED + MAX_PROMPT) + (1 << 20)) as u64;
pub const RESULT_NAME: &str = "result.json";
pub const PROMPT_NAME: &str = "prompt.md";
pub const STAGED: &str = "in";

/// Relative paths of lowercase segments ending .md/.txt/.json; no segment
/// starts with a dot or dash, so nothing staged is hidden or a traversal.
pub fn valid_staged_path(p: &str) -> bool {
    let Some(ext) = p.rsplit('.').next() else { return false };
    if !matches!(ext, "md" | "txt" | "json") || p.contains("..") {
        return false;
    }
    p.split('/').all(|seg| {
        let b = seg.as_bytes();
        !b.is_empty()
            && (b[0].is_ascii_lowercase() || b[0].is_ascii_digit())
            && b.iter().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || matches!(c, b'_' | b'-' | b'.'))
    })
}

/// Starts one agent unit and returns systemd's verdict once it has stopped;
/// cleanup removes the unit's state directory (with result.json) and its
/// failed state.
pub trait Units {
    fn run(&self, run: &str) -> (String, Result<(), String>);
    fn cleanup(&self, run: &str);
}

pub struct Starter<U: Units> {
    pub runs_dir: PathBuf,   // /var/lib/wiki/runs: <run>/prompt.md, <run>/in/...
    pub registry: PathBuf,   // /run/wiki/runs: <run>.json for the proxy
    pub result_dir: PathBuf, // /var/lib/private/wiki-agent: <run>/result.json
    pub uid: u32,            // the only uid allowed to ask
    pub units: U,
    pub read_limit: Duration,
}

impl<U: Units> Starter<U> {
    fn validate(&self, req: &StartRequest) -> Result<(), String> {
        if !valid_run_id(&req.run) {
            return Err(format!("bad run id {:?}", req.run));
        }
        if !ORGS.contains(&req.org.as_str()) {
            return Err(format!("bad org {:?}", req.org));
        }
        if !valid_command(&req.command) {
            return Err(format!("bad command {:?}", req.command));
        }
        if req.prompt.is_empty() || req.prompt.len() > MAX_PROMPT {
            return Err("prompt empty or too large".into());
        }
        if req.files.len() > MAX_FILES {
            return Err(format!("{} files, at most {MAX_FILES}", req.files.len()));
        }
        let mut total = 0;
        for (p, c) in &req.files {
            if !valid_staged_path(p) {
                return Err(format!("bad staged path {p:?}"));
            }
            total += c.len();
        }
        if total > MAX_STAGED {
            return Err(format!("staged {total} bytes, at most {MAX_STAGED}"));
        }
        Ok(())
    }

    /// Stages, registers, runs and collects one agent run. The run's
    /// directories and registration are removed whatever happens.
    pub fn start(&self, req: &StartRequest) -> StartResponse {
        let fail = |e: String| StartResponse { error: e, ..Default::default() };
        if let Err(e) = self.validate(req) {
            return fail(e);
        }
        let run_dir = self.runs_dir.join(&req.run);
        // create_dir, not create_dir_all: a run id is used once, and an
        // existing directory is a reused id or a leftover not to be trusted.
        if let Err(e) = fs::create_dir(&run_dir) {
            return fail(format!("run directory: {e}"));
        }
        let reg = self.registry.join(format!("{}.json", req.run));
        let _cleanup = Cleanup(|| {
            let _ = fs::remove_file(&reg);
            let _ = fs::remove_dir_all(&run_dir);
            self.units.cleanup(&req.run);
        });
        if let Err(e) = write_new(&run_dir.join(PROMPT_NAME), req.prompt.as_bytes()) {
            return fail(e.to_string());
        }
        let staged = run_dir.join(STAGED);
        if let Err(e) = fs::create_dir(&staged) {
            return fail(e.to_string());
        }
        for (p, c) in &req.files {
            let dst = staged.join(p);
            if let Err(e) = fs::create_dir_all(dst.parent().unwrap()).and_then(|_| write_new(&dst, c.as_bytes())) {
                return fail(format!("{p}: {e}"));
            }
        }
        let rb = serde_json::to_vec(&Registration { run: req.run.clone(), org: req.org.clone(), command: req.command.clone() }).unwrap();
        if let Err(e) = write_new(&reg, &rb) {
            return fail(format!("registration: {e}"));
        }
        let (unit_result, run_err) = self.units.run(&req.run);
        let mut resp = StartResponse { unit_result, ..Default::default() };
        match (read_result(&self.result_dir.join(&req.run).join(RESULT_NAME)), run_err) {
            (Err(e), Err(r)) => resp.error = format!("agent unit: {r}; no result: {e}"),
            (Err(e), Ok(())) => resp.error = format!("no result: {e}"),
            (Ok(res), run_err) => {
                resp.result = res;
                match run_err {
                    Ok(()) => resp.ok = true,
                    Err(r) => resp.error = format!("agent unit: {r}"),
                }
            }
        }
        resp
    }

    /// One request per connection, one connection at a time, from the
    /// allowed uid only.
    pub fn serve(&self, l: UnixListener) -> io::Result<()> {
        for c in l.incoming() {
            let c = c?;
            self.handle(c);
        }
        Ok(())
    }

    fn handle(&self, c: UnixStream) {
        let reply = |r: StartResponse| {
            let mut w = &c;
            let _ = w.write_all(&serde_json::to_vec(&r).unwrap());
            let _ = w.write_all(b"\n");
        };
        let uid = peer_uid(&c);
        if uid.as_ref().ok() != Some(&self.uid) {
            eprintln!("starter: refused peer uid={uid:?}");
            return reply(StartResponse { error: "not allowed".into(), ..Default::default() });
        }
        // A client that never finishes its request must not block every later one.
        let _ = c.set_read_timeout(Some(self.read_limit));
        let req: StartRequest = {
            let mut lim = (&c).take(MAX_REQUEST);
            let mut de = serde_json::Deserializer::from_reader(&mut lim);
            match StartRequest::deserialize(&mut de) {
                Ok(r) => r,
                Err(_) => return reply(StartResponse { error: "bad or oversized request".into(), ..Default::default() }),
            }
        };
        let resp = self.start(&req);
        eprintln!("starter: run={} org={} command={} unit={} ok={} err={}", req.run, req.org, req.command, resp.unit_result, resp.ok, resp.error);
        reply(resp);
    }
}

struct Cleanup<F: FnMut()>(F);
impl<F: FnMut()> Drop for Cleanup<F> {
    fn drop(&mut self) {
        (self.0)()
    }
}

/// Creates a file that must not exist yet, never following a link.
fn write_new(path: &Path, content: &[u8]) -> io::Result<()> {
    let mut f = fs::OpenOptions::new().write(true).create_new(true).mode(0o644).custom_flags(libc::O_NOFOLLOW).open(path)?;
    f.write_all(content)
}

/// Reads the agent's result without following a link, refusing anything but
/// a bounded regular file.
fn read_result(path: &Path) -> io::Result<String> {
    let f = fs::OpenOptions::new().read(true).custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK).open(path)?;
    let md = f.metadata()?;
    if !md.is_file() || md.len() > MAX_RESULT {
        return Err(io::Error::other(format!("{RESULT_NAME} is not a regular file under {MAX_RESULT} bytes")));
    }
    let mut s = String::new();
    f.take(MAX_RESULT + 1).read_to_string(&mut s)?;
    if s.len() as u64 > MAX_RESULT {
        return Err(io::Error::other(format!("{RESULT_NAME} over {MAX_RESULT} bytes")));
    }
    Ok(s)
}

fn peer_uid(c: &UnixStream) -> io::Result<u32> {
    let mut cred: libc::ucred = unsafe { std::mem::zeroed() };
    let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
    // SAFETY: getsockopt writes at most len bytes into cred.
    let r = unsafe { libc::getsockopt(c.as_raw_fd(), libc::SOL_SOCKET, libc::SO_PEERCRED, (&mut cred as *mut libc::ucred).cast(), &mut len) };
    if r != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(cred.uid)
}

/// wiki-agent@<run>.service is Type=oneshot, so `systemctl start` returns
/// only once the agent has exited.
pub struct Systemd {
    pub systemctl: PathBuf,
}

impl Systemd {
    fn unit(run: &str) -> String {
        format!("wiki-agent@{run}.service")
    }
}

impl Units for Systemd {
    fn run(&self, run: &str) -> (String, Result<(), String>) {
        let unit = Self::unit(run);
        let started = Command::new(&self.systemctl).args(["start", &unit]).status();
        let result = Command::new(&self.systemctl)
            .args(["show", "--property=Result", "--value", &unit])
            .output()
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
            .unwrap_or_else(|_| "unknown".into());
        let err = match started {
            Err(e) => Err(e.to_string()),
            Ok(s) if !s.success() => Err(format!("systemctl start: {s}")),
            Ok(_) if result != "success" => Err(format!("unit result {result}")),
            Ok(_) => Ok(()),
        };
        (result, err)
    }

    /// systemd removes the DynamicUser state directory and its link, which
    /// the starter cannot reach under ProtectSystem=strict.
    fn cleanup(&self, run: &str) {
        let unit = Self::unit(run);
        match Command::new(&self.systemctl).args(["clean", "--what=state", &unit]).output() {
            Ok(o) if o.status.success() => {}
            Ok(o) => eprintln!("starter: clean {unit}: {} {}", o.status, String::from_utf8_lossy(&o.stderr)),
            Err(e) => eprintln!("starter: clean {unit}: {e}"),
        }
        let _ = Command::new(&self.systemctl).args(["reset-failed", &unit]).status();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::peer::lookup_run;
    use crate::testutil::TempDir;
    use std::cell::RefCell;
    use std::io::BufRead;

    /// Plays the agent: while "running" it records what was staged and
    /// registered, then writes result.json the way the unit would.
    struct Fake {
        dirs: (PathBuf, PathBuf, PathBuf),
        write: Box<dyn Fn(&Path) -> io::Result<()>>,
        fail: bool,
        saw: RefCell<(String, String, Option<Registration>)>,
        cleanups: RefCell<u32>,
    }

    impl Units for Fake {
        fn run(&self, run: &str) -> (String, Result<(), String>) {
            let (runs, reg, res) = &self.dirs;
            let prompt = fs::read_to_string(runs.join(run).join(PROMPT_NAME)).unwrap_or_default();
            let file = fs::read_to_string(runs.join(run).join(STAGED).join("pages/eversports/queue.md")).unwrap_or_default();
            *self.saw.borrow_mut() = (prompt, file, lookup_run(reg, run).ok());
            let d = res.join(run);
            fs::create_dir_all(&d).unwrap();
            (self.write)(&d).unwrap();
            if self.fail { ("exit-code".into(), Err("exit status 1".into())) } else { ("success".into(), Ok(())) }
        }
        fn cleanup(&self, run: &str) {
            *self.cleanups.borrow_mut() += 1;
            let _ = fs::remove_dir_all(self.dirs.2.join(run));
        }
    }

    fn starter(dir: &TempDir) -> Starter<Fake> {
        let (runs, reg, res) = (dir.path().join("runs"), dir.path().join("reg"), dir.path().join("res"));
        for d in [&runs, &reg, &res] {
            fs::create_dir_all(d).unwrap();
        }
        Starter {
            units: Fake {
                dirs: (runs.clone(), reg.clone(), res.clone()),
                write: Box::new(|d| fs::write(d.join(RESULT_NAME), r#"{"pages":[]}"#)),
                fail: false,
                saw: Default::default(),
                cleanups: Default::default(),
            },
            runs_dir: runs,
            registry: reg,
            result_dir: res,
            uid: unsafe { libc::getuid() },
            read_limit: Duration::from_secs(60),
        }
    }

    fn good() -> StartRequest {
        StartRequest {
            run: "r1".into(),
            org: "eversports".into(),
            command: "ingest".into(),
            prompt: "distil in/item.md".into(),
            files: [("item.md".to_string(), "# Decisions".to_string()), ("pages/eversports/queue.md".to_string(), "queue facts".to_string())].into(),
        }
    }

    #[test]
    fn stages_registers_runs_and_cleans_up() {
        let dir = TempDir::new();
        let s = starter(&dir);
        let resp = s.start(&good());
        assert_eq!(resp, StartResponse { ok: true, unit_result: "success".into(), result: r#"{"pages":[]}"#.into(), ..Default::default() });
        let saw = s.units.saw.borrow().clone();
        assert_eq!((saw.0.as_str(), saw.1.as_str()), ("distil in/item.md", "queue facts"));
        assert_eq!(saw.2, Some(Registration { run: "r1".into(), org: "eversports".into(), command: "ingest".into() }));
        for p in [s.runs_dir.join("r1"), s.registry.join("r1.json"), s.result_dir.join("r1")] {
            assert!(fs::symlink_metadata(&p).is_err(), "{p:?} left behind");
        }
        assert_eq!(*s.units.cleanups.borrow(), 1);
    }

    #[test]
    fn refuses_bad_requests() {
        let edits: Vec<(&str, Box<dyn Fn(&mut StartRequest)>)> = vec![
            ("run id with dash", Box::new(|r| r.run = "a-b".into())),
            ("run id traversal", Box::new(|r| r.run = "../x".into())),
            ("unknown org", Box::new(|r| r.org = "acme".into())),
            ("bad command", Box::new(|r| r.command = "Ingest!".into())),
            ("empty prompt", Box::new(|r| r.prompt.clear())),
            ("dotdot path", Box::new(|r| { r.files.insert("../../etc/x.md".into(), String::new()); })),
            ("hidden segment", Box::new(|r| { r.files.insert("pages/.claude/x.md".into(), String::new()); })),
            ("absolute path", Box::new(|r| { r.files.insert("/etc/x.md".into(), String::new()); })),
            ("settings file", Box::new(|r| { r.files.insert("settings.yaml".into(), String::new()); })),
            ("oversized staging", Box::new(|r| { r.files.insert("big.md".into(), "x".repeat(MAX_STAGED + 1)); })),
        ];
        for (name, edit) in edits {
            let dir = TempDir::new();
            let s = starter(&dir);
            let mut req = good();
            edit(&mut req);
            let resp = s.start(&req);
            assert!(!resp.ok && !resp.error.is_empty(), "{name}: {resp:?}");
            assert!(s.units.saw.borrow().0.is_empty(), "{name}: agent ran");
        }
    }

    #[test]
    fn refuses_reused_run_id() {
        let dir = TempDir::new();
        let s = starter(&dir);
        fs::create_dir(s.runs_dir.join("r1")).unwrap();
        assert!(!s.start(&good()).ok);
        assert!(s.units.saw.borrow().0.is_empty());
    }

    #[test]
    fn refuses_linked_odd_or_huge_result() {
        let writers: Vec<(&str, Box<dyn Fn(&Path) -> io::Result<()>>)> = vec![
            ("symlink", Box::new(|d| std::os::unix::fs::symlink("/etc/passwd", d.join(RESULT_NAME)))),
            ("fifo", Box::new(|d| {
                let p = std::ffi::CString::new(d.join(RESULT_NAME).to_str().unwrap()).unwrap();
                if unsafe { libc::mkfifo(p.as_ptr(), 0o644) } == 0 { Ok(()) } else { Err(io::Error::last_os_error()) }
            })),
            ("huge", Box::new(|d| fs::write(d.join(RESULT_NAME), vec![b'x'; MAX_RESULT as usize + 1]))),
            ("missing", Box::new(|_| Ok(()))),
        ];
        for (name, w) in writers {
            let dir = TempDir::new();
            let mut s = starter(&dir);
            s.units.write = w;
            let resp = s.start(&good());
            assert!(!resp.ok && resp.result.is_empty(), "{name}: {resp:?}");
        }
    }

    #[test]
    fn reports_failed_unit_with_its_result() {
        let dir = TempDir::new();
        let mut s = starter(&dir);
        s.units.fail = true;
        let resp = s.start(&good());
        assert!(!resp.ok && resp.unit_result == "exit-code" && !resp.result.is_empty() && !resp.error.is_empty(), "{resp:?}");
    }

    fn ask(sock: &Path, req: Option<&StartRequest>) -> StartResponse {
        let c = UnixStream::connect(sock).unwrap();
        c.set_read_timeout(Some(Duration::from_secs(120))).unwrap();
        if let Some(r) = req {
            // The server may already have refused and closed; its reply is
            // still readable.
            let _ = (&c).write_all(&serde_json::to_vec(r).unwrap());
        }
        let mut line = String::new();
        io::BufReader::new(&c).read_line(&mut line).unwrap();
        serde_json::from_str(&line).unwrap()
    }

    /// The Fake holds RefCells, so serving runs on this thread; the client
    /// thread connects and asks.
    fn serve_once(s: Starter<Fake>, req: Option<StartRequest>) -> StartResponse {
        let sock = std::env::temp_dir().join(format!("wiki-starter-{}-{:?}.sock", std::process::id(), std::thread::current().id()));
        let _ = fs::remove_file(&sock);
        let l = UnixListener::bind(&sock).unwrap();
        let sock2 = sock.clone();
        let client = std::thread::spawn(move || ask(&sock2, req.as_ref()));
        let (c, _) = l.accept().unwrap();
        s.handle(c);
        let _ = fs::remove_file(&sock);
        client.join().unwrap()
    }

    #[test]
    fn serves_only_its_uid() {
        let dir = TempDir::new();
        assert!(serve_once(starter(&dir), Some(good())).ok);
        let dir = TempDir::new();
        let mut s = starter(&dir);
        s.uid += 1;
        let resp = serve_once(s, Some(good()));
        assert!(!resp.ok && resp.error == "not allowed", "{resp:?}");
    }

    #[test]
    fn silent_client_does_not_block() {
        let dir = TempDir::new();
        let mut s = starter(&dir);
        s.read_limit = Duration::from_millis(100);
        let resp = serve_once(s, None);
        assert!(!resp.ok, "{resp:?}");
    }

    #[test]
    fn staged_paths() {
        for ok in ["item.md", "pages/eversports/queue.md", "a_b/c-d.v2.txt", "x.json"] {
            assert!(valid_staged_path(ok), "{ok}");
        }
        for bad in ["", "/etc/x.md", "../x.md", "a/../x.md", ".hidden.md", "a/.b/x.md", "-x.md", "A.md", "x.yaml", "a//b.md", "x.md/"] {
            assert!(!valid_staged_path(bad), "{bad}");
        }
    }
}
