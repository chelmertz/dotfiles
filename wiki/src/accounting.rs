//! The daily budget and per-run usage files. Every change is on disk (fsync,
//! then rename) before the request it covers goes upstream, so a crash leaves
//! at worst an over-count: the budget fails closed.

use crate::peer::Registration;
use crate::policy::{Refusal, refuse};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs;
use std::io::{self, Write};
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicU64, Ordering};

/// One run's token use as Anthropic reported it, plus what the proxy charged.
/// Budgeted tokens are input + cache writes + output; cache reads are
/// recorded but not budgeted.
#[derive(Debug, Default, Clone, PartialEq, Serialize, Deserialize)]
pub struct Usage {
    pub run: String,
    pub org: String,
    pub command: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub model: String,
    pub requests: i64,
    pub input_tokens: i64,
    pub cache_write_tokens: i64,
    pub cache_read_tokens: i64,
    pub output_tokens: i64,
    pub charged_tokens: i64,
    pub unsettled_requests: i64,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub refusals: BTreeMap<String, i64>,
}

#[derive(Debug, Default, Clone, PartialEq, Serialize, Deserialize)]
pub struct DayBudget {
    pub day: String,
    pub used: i64,
}

/// What one response said it used.
#[derive(Debug, Default, Clone, Copy, PartialEq)]
pub struct Reported {
    pub input: i64,
    pub cache_write: i64,
    pub cache_read: i64,
    pub output: i64,
}

impl Reported {
    fn budgeted(&self) -> i64 {
        self.input + self.cache_write + self.output
    }
}

pub struct Accounts {
    pub dir: PathBuf,
    pub day_limit: i64,
    pub run_limit: i64,
    pub today: Box<dyn Fn() -> String + Send + Sync>,
    lock: Mutex<()>,
}

const STATE: &str = "budget state unreadable";

impl Accounts {
    pub fn new(dir: PathBuf, day_limit: i64, run_limit: i64, today: Box<dyn Fn() -> String + Send + Sync>) -> Self {
        Accounts { dir, day_limit, run_limit, today, lock: Mutex::new(()) }
    }

    fn budget_path(&self) -> PathBuf {
        self.dir.join("budget.json")
    }

    fn usage_path(&self, run: &str) -> PathBuf {
        self.dir.join("usage").join(format!("{run}.json"))
    }

    fn load_day(&self) -> Result<DayBudget, String> {
        let today = (self.today)();
        match fs::read(self.budget_path()) {
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(DayBudget { day: today, used: 0 }),
            Err(_) => Err(STATE.into()),
            Ok(b) => match serde_json::from_slice::<DayBudget>(&b) {
                Ok(d) if d.day == today => Ok(d),
                Ok(d) if !d.day.is_empty() => Ok(DayBudget { day: today, used: 0 }),
                _ => Err(STATE.into()),
            },
        }
    }

    fn load_run(&self, rr: &Registration) -> Result<Usage, String> {
        let fresh = Usage { run: rr.run.clone(), org: rr.org.clone(), command: rr.command.clone(), ..Default::default() };
        match fs::read(self.usage_path(&rr.run)) {
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(fresh),
            Err(_) => Err(STATE.into()),
            Ok(b) => {
                let u: Usage = serde_json::from_slice(&b).map_err(|_| STATE.to_string())?;
                if u.org != rr.org || u.command != rr.command {
                    return Err(format!("run {} re-registered with another org or command", rr.run));
                }
                Ok(u)
            }
        }
    }

    /// Charges n tokens for one request before it is forwarded; returns the
    /// day it was charged to.
    pub fn reserve(&self, rr: &Registration, model: &str, n: i64) -> Result<String, Refusal> {
        let _g = self.lock.lock().unwrap();
        if n <= 0 {
            return Err(refuse("budget-run", format!("reservation {n}")));
        }
        let mut d = self.load_day().map_err(|e| refuse("state", e))?;
        let mut u = self.load_run(rr).map_err(|e| refuse("state", e))?;
        if d.used + n > self.day_limit {
            return Err(refuse("budget-day", format!("used {} + {n} > {}", d.used, self.day_limit)));
        }
        if u.charged_tokens + n > self.run_limit {
            return Err(refuse("budget-run", format!("charged {} + {n} > {}", u.charged_tokens, self.run_limit)));
        }
        d.used += n;
        u.charged_tokens += n;
        u.requests += 1;
        u.unsettled_requests += 1;
        u.model = model.to_string();
        write_json(&self.budget_path(), &d).map_err(|e| refuse("state", e.to_string()))?;
        write_json(&self.usage_path(&rr.run), &u).map_err(|e| refuse("state", e.to_string()))?;
        Ok(d.day)
    }

    /// Replaces a reservation made on `day` with the reported use. Without a
    /// complete report (stream cut, client gone) the reservation stays charged.
    pub fn settle(&self, rr: &Registration, reserved: i64, day: &str, rep: Option<Reported>) -> Result<(), String> {
        let Some(rep) = rep else { return Ok(()) };
        let _g = self.lock.lock().unwrap();
        let mut d = self.load_day()?;
        let mut u = self.load_run(rr)?;
        let delta = rep.budgeted() - reserved;
        // A reservation made on an earlier day was charged to that day; neither
        // its refund nor its overrun touches today's budget.
        if day == d.day {
            d.used += delta;
        }
        u.charged_tokens += delta;
        u.unsettled_requests -= 1;
        u.input_tokens += rep.input;
        u.cache_write_tokens += rep.cache_write;
        u.cache_read_tokens += rep.cache_read;
        u.output_tokens += rep.output;
        write_json(&self.budget_path(), &d).map_err(|e| e.to_string())?;
        write_json(&self.usage_path(&rr.run), &u).map_err(|e| e.to_string())
    }

    /// Counts a refusal against a known run.
    pub fn refused(&self, rr: &Registration, reason: &str) -> Result<(), String> {
        let _g = self.lock.lock().unwrap();
        let mut u = self.load_run(rr)?;
        *u.refusals.entry(reason.to_string()).or_default() += 1;
        write_json(&self.usage_path(&rr.run), &u).map_err(|e| e.to_string())
    }
}

static TMP: AtomicU64 = AtomicU64::new(0);

/// Replaces path atomically: new temp file, fsync, rename, fsync the directory.
pub fn write_json<T: Serialize>(path: &Path, v: &T) -> io::Result<()> {
    let b = serde_json::to_vec(v)?;
    let dir = path.parent().unwrap_or(Path::new("."));
    fs::create_dir_all(dir)?;
    let tmp = dir.join(format!(".tmp-{}-{}", std::process::id(), TMP.fetch_add(1, Ordering::Relaxed)));
    let res = (|| {
        let mut f = fs::OpenOptions::new().write(true).create_new(true).mode(0o644).open(&tmp)?;
        f.write_all(&b)?;
        f.sync_all()?;
        fs::rename(&tmp, path)?;
        fs::File::open(dir)?.sync_all()
    })();
    if res.is_err() {
        let _ = fs::remove_file(&tmp);
    }
    res
}

/// Today's local date, YYYY-MM-DD.
pub fn local_today() -> String {
    // SAFETY: time(NULL) and localtime_r with a zeroed tm are sound.
    unsafe {
        let t = libc::time(std::ptr::null_mut());
        let mut tm: libc::tm = std::mem::zeroed();
        libc::localtime_r(&t, &mut tm);
        format!("{:04}-{:02}-{:02}", tm.tm_year + 1900, tm.tm_mon + 1, tm.tm_mday)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::testutil::TempDir;
    use std::sync::Arc;
    use std::sync::atomic::AtomicBool;

    fn rr() -> Registration {
        Registration { run: "r1".into(), org: "personal".into(), command: "ingest".into() }
    }

    #[test]
    fn settle_across_midnight_leaves_today_alone() {
        let dir = TempDir::new();
        let tomorrow = Arc::new(AtomicBool::new(false));
        let t = tomorrow.clone();
        let a = Accounts::new(dir.path(), 1_000_000, 1_000_000, Box::new(move || {
            if t.load(Ordering::SeqCst) { "2026-11-29".into() } else { "2026-11-28".into() }
        }));
        let day = a.reserve(&rr(), "m", 400_000).unwrap();
        tomorrow.store(true, Ordering::SeqCst);
        write_json(&a.budget_path(), &DayBudget { day: "2026-11-29".into(), used: 500_000 }).unwrap();
        a.settle(&rr(), 400_000, &day, Some(Reported { output: 10_000, ..Default::default() })).unwrap();
        assert_eq!(a.load_day().unwrap().used, 500_000);
    }

    #[test]
    fn reserve_refuses_non_positive() {
        let dir = TempDir::new();
        let a = Accounts::new(dir.path(), 10, 10, Box::new(local_today));
        for n in [0, -1, i64::MIN] {
            assert!(a.reserve(&rr(), "m", n).is_err(), "reserve({n})");
        }
    }

    #[test]
    fn local_today_is_a_date() {
        let d = local_today();
        assert_eq!(d.len(), 10, "{d}");
        assert!(d.starts_with("20"), "{d}");
    }
}
