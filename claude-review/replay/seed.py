# Seeds a scratch ledger and a fake elly list from past claude-review sessions:
# each PR accepted at its first commit, its last round unread at the head it had then.
import glob, json, os, re, sqlite3, subprocess, sys, uuid
from datetime import datetime

out, urls = sys.argv[1], sys.argv[2:]
db = sqlite3.connect(os.path.join(out, "state/claude-review/ledger.db"))
db.executescript(open(os.path.join(out, "schema.sql")).read())
user = subprocess.check_output(["gh", "api", "user", "--jq", ".login"], text=True).strip()
elly = []
for url in urls:
    sid = str(uuid.uuid5(uuid.NAMESPACE_URL, url))
    f = glob.glob(os.path.expanduser(f"~/.claude/projects/*/{sid}.jsonl"))[0]
    rounds = []
    for line in open(f, errors="ignore"):
        e = json.loads(line)
        if e.get("type") == "user" and "adversarial code review of" in json.dumps(e.get("message", {})):
            rounds.append(datetime.fromisoformat(e["timestamp"].replace("Z", "+00:00")))
    if not rounds:
        print(f"{url}: no round prompt in the transcript, skipped", file=sys.stderr)
        continue
    started = rounds[-1]
    owner, repo, _, num = url.split("/")[3:7]
    try:
        pr = json.loads(subprocess.check_output(["gh", "api", f"repos/{owner}/{repo}/pulls/{num}"], text=True, stderr=subprocess.DEVNULL))
    except subprocess.CalledProcessError:
        print(f"{url}: GitHub does not return it, skipped", file=sys.stderr)
        continue
    commits = json.loads(subprocess.check_output(["gh", "api", "--paginate", f"repos/{owner}/{repo}/pulls/{num}/commits"], text=True))
    before = [c["sha"] for c in commits if datetime.fromisoformat(c["commit"]["committer"]["date"].replace("Z", "+00:00")) <= started]
    first = commits[0]["sha"]
    head_then = before[-1] if before else first
    db.execute("insert into acceptance values (?,?,?,?)", (url, first, pr["base"]["ref"], int(started.timestamp()) - 3600))
    db.execute("insert into rounds (pr, head, trigger, started_at) values (?,?,?,?)", (url, head_then, "replay", int(started.timestamp()) - 1))
    elly.append({"Url": url, "RepoOwner": owner, "RepoName": repo, "Author": pr["user"]["login"],
                 "HeadRefOid": pr["head"]["sha"], "LastUpdated": pr["updated_at"],
                 "ReviewRequestedFromUsers": [], "RereviewFrom": [user], "ThreadsActionable": 0, "Buried": False})
    print(f"{url}: {len(rounds)} rounds, last {started:%Y-%m-%d %H:%M}, head then {head_then[:9]}, now {pr['head']['sha'][:9]}, {pr['state']}{' merged' if pr.get('merged') else ''}, {len(commits)} commits", file=sys.stderr)
db.commit()
os.makedirs(os.path.join(out, "elly/api/v0"), exist_ok=True)
json.dump(elly, open(os.path.join(out, "elly/api/v0/prs"), "w"))
