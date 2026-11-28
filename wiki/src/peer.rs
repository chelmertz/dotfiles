//! Who is on the other end of the socket, and which run that is.

use serde::{Deserialize, Serialize};
use std::fs;
use std::io;
use std::os::fd::RawFd;
use std::path::Path;

/// Written by the starter before it starts an agent unit: which org and
/// command the run's tokens belong to. One file per run, <run>.json, by root.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Registration {
    pub run: String,
    pub org: String,
    pub command: String,
}

/// Must equal agentUserPrefix in nix/hosts/tau/wiki.nix, whose assertion
/// fails the system build when the two differ.
pub const AGENT_USER_PREFIX: &str = "wiki-run-";

/// The values a run may be registered under: one per employer, plus personal.
pub const ORGS: &[&str] = &["personal", "eversports"];

/// Run ids are [0-9a-z]{1,22}, so a unit instance name needs no escaping and
/// no path or unit name built from one can be anything else. 22 keeps the
/// agent unit's per-run user, AGENT_USER_PREFIX plus the id, within the
/// 31 characters systemd accepts as a user name.
pub fn valid_run_id(s: &str) -> bool {
    !s.is_empty() && s.len() <= 22 && s.bytes().all(|b| b.is_ascii_digit() || b.is_ascii_lowercase())
}

/// Commands are [a-z][a-z-]{0,31}.
pub fn valid_command(s: &str) -> bool {
    let b = s.as_bytes();
    !b.is_empty() && b.len() <= 32 && b[0].is_ascii_lowercase() && b.iter().all(|c| c.is_ascii_lowercase() || *c == b'-')
}

/// The cgroup systemd gives an instance of the wiki-agent@.service template.
const AGENT_PREFIX: &str = r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@";
const AGENT_SUFFIX: &str = ".service";

/// The run id of an agent unit's cgroup path, or None for anything else.
pub fn run_from_cgroup(cg: &str) -> Option<&str> {
    let run = cg.strip_prefix(AGENT_PREFIX)?.strip_suffix(AGENT_SUFFIX)?;
    valid_run_id(run).then_some(run)
}

/// Reads and validates the registration for a run id.
pub fn lookup_run(dir: &Path, run: &str) -> Result<Registration, String> {
    if !valid_run_id(run) {
        return Err(format!("bad run id {run:?}"));
    }
    let b = fs::read(dir.join(format!("{run}.json"))).map_err(|e| e.to_string())?;
    let rr: Registration = serde_json::from_slice(&b).map_err(|e| e.to_string())?;
    if rr.run != run || !ORGS.contains(&rr.org.as_str()) || !valid_command(&rr.command) {
        return Err(format!("registration for {run} is malformed"));
    }
    Ok(rr)
}

/// SO_PEERPIDFD from asm-generic/socket.h (Linux 6.5+).
const SO_PEERPIDFD: libc::c_int = 77;

/// The cgroup v2 path of the process at the other end of a unix socket. It
/// takes a pidfd for the peer, reads the pid and cgroup, then checks the pidfd
/// still refers to a live process, so a pid reused between the two reads
/// cannot be mistaken for the peer.
pub fn peer_cgroup(fd: RawFd) -> io::Result<String> {
    let mut pidfd: libc::c_int = -1;
    let mut len = std::mem::size_of::<libc::c_int>() as libc::socklen_t;
    // SAFETY: getsockopt writes at most len bytes into pidfd.
    let r = unsafe { libc::getsockopt(fd, libc::SOL_SOCKET, SO_PEERPIDFD, (&mut pidfd as *mut libc::c_int).cast(), &mut len) };
    if r != 0 {
        return Err(io::Error::last_os_error());
    }
    let _close = CloseFd(pidfd);
    let pid = parse_pidfd_info(&fs::read_to_string(format!("/proc/self/fdinfo/{pidfd}"))?)?;
    let cg = read_cgroup(&format!("/proc/{pid}/cgroup"))?;
    // pidfd_send_signal(pidfd, 0, NULL, 0): ESRCH once the process is gone.
    // SAFETY: plain syscall on an fd we own.
    let r = unsafe { libc::syscall(libc::SYS_pidfd_send_signal, pidfd, 0, std::ptr::null::<u8>(), 0) };
    if r != 0 {
        return Err(io::Error::other(format!("peer exited: {}", io::Error::last_os_error())));
    }
    Ok(cg)
}

struct CloseFd(libc::c_int);
impl Drop for CloseFd {
    fn drop(&mut self) {
        // SAFETY: we own this fd and close it once.
        unsafe { libc::close(self.0) };
    }
}

fn parse_pidfd_info(s: &str) -> io::Result<i32> {
    for line in s.lines() {
        if let Some(v) = line.strip_prefix("Pid:") {
            return match v.trim().parse::<i32>() {
                Ok(p) if p > 0 => Ok(p),
                _ => Err(io::Error::other(format!("pidfd refers to no live process ({v:?})"))),
            };
        }
    }
    Err(io::Error::other("no Pid line in pidfd fdinfo"))
}

/// The unified-hierarchy path from a /proc/<pid>/cgroup file.
pub fn read_cgroup(path: &str) -> io::Result<String> {
    let s = fs::read_to_string(path)?;
    s.lines()
        .find_map(|l| l.strip_prefix("0::").map(str::to_string))
        .ok_or_else(|| io::Error::other("no cgroup v2 entry"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::fd::AsRawFd;
    use std::os::unix::net::{UnixListener, UnixStream};

    #[test]
    fn run_from_cgroup_matches_only_agent_units() {
        let cases = [
            (r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@run1.service", Some("run1")),
            (r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@run1.service/extra", None),
            (r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@RUN1.service", None),
            (r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@a-b.service", None),
            (r"/system.slice/system-wiki\x2dagent.slice/wiki-agent@.service", None),
            (r"/system.slice/system-wiki\x2dagent\x2devil.slice/wiki-agent-evil@run1.service", None),
            ("/user.slice/user-1000.slice/user@1000.service/app.slice/wiki-agent@run1.service", None),
            ("/system.slice/wiki-agent.service", None),
        ];
        for (cg, want) in cases {
            assert_eq!(run_from_cgroup(cg), want, "{cg}");
        }
    }

    #[test]
    fn pidfd_info_parsing() {
        assert_eq!(parse_pidfd_info("Pid:\t42\nNSpid:\t42\n").unwrap(), 42);
        assert!(parse_pidfd_info("Pid:\t-1\n").is_err());
        assert!(parse_pidfd_info("pos:\t0\n").is_err());
    }

    /// The real lookup, against this test process at the other end.
    #[test]
    fn peer_cgroup_is_the_connecting_process() {
        let dir = crate::testutil::TempDir::new();
        let sock = dir.path().join("s");
        let l = UnixListener::bind(&sock).unwrap();
        let _c = UnixStream::connect(&sock).unwrap();
        let (srv, _) = l.accept().unwrap();
        let got = peer_cgroup(srv.as_raw_fd()).unwrap();
        assert!(!got.is_empty());
        assert_eq!(got, read_cgroup("/proc/self/cgroup").unwrap());
    }

    #[test]
    fn validates_ids() {
        assert!(valid_run_id("r1") && !valid_run_id("") && !valid_run_id("a-b") && !valid_run_id("../x") && !valid_run_id(&"a".repeat(23)));
        assert!(valid_run_id(&"a".repeat(22)));
    }

    /// The agent unit's User= is the prefix plus the run id, and systemd
    /// refuses a user name over 31 characters.
    #[test]
    fn agent_user_names_fit_systemd() {
        assert_eq!(AGENT_USER_PREFIX, "wiki-run-");
        assert!(AGENT_USER_PREFIX.len() + 22 <= 31);
        assert!(valid_command("ingest") && valid_command("re-ingest") && !valid_command("Ingest") && !valid_command("-x") && !valid_command("in gest"));
    }
}
