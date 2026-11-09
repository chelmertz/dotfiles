"""The obsidian-todo native host: framing, the task line, dedup, refusals.

Usage: thunderbird-todo.py <obsidian-todo-host.py>
"""
import json
import os
import struct
import subprocess
import sys
import tempfile

HOST = sys.argv[1]


def call(vault, msg):
    data = json.dumps(msg).encode()
    out = subprocess.run(
        [sys.executable, "-I", HOST], input=struct.pack("=I", len(data)) + data,
        capture_output=True, check=True,
        env={"OBSIDIAN_TODO_VAULT": vault, "TZ": "UTC"},
    ).stdout
    n = struct.unpack("=I", out[:4])[0]
    assert len(out) == 4 + n, out
    return json.loads(out[4:])


def lines(vault):
    with open(os.path.join(vault, "mail.md"), encoding="utf-8") as f:
        return f.read().splitlines()


with tempfile.TemporaryDirectory() as vault:
    pr = {"id": "<owner/repo/pull/7/c1@github.com>", "author": "Ada L <ada@example.com>",
          "subject": "Re: fix #12 in the #parser"}
    r = call(vault, pr)
    assert r == {"ok": True, "result": "todo added to mail.md"}, r
    [task] = lines(vault)
    assert task.startswith(
        "- [ ] Ada L: Re: fix \\#12 in the \\#parser [mail](mid:owner/repo/pull/7/c1@github.com) ➕ "
    ), task

    # Already linked: the same message, or the id pasted into another note.
    r = call(vault, pr)
    assert r == {"ok": True, "result": "todo already in mail.md"}, r
    os.makedirs(os.path.join(vault, "journal"))
    with open(os.path.join(vault, "journal", "x.md"), "w") as f:
        f.write("see [it](mid:abc@example.com)\n")
    r = call(vault, {"id": "abc@example.com", "author": "", "subject": "x"})
    assert r == {"ok": True, "result": "todo already in journal/x.md"}, r
    assert len(lines(vault)) == 1

    # Hidden folders (.obsidian, .trash) do not count as linked.
    os.makedirs(os.path.join(vault, ".trash"))
    with open(os.path.join(vault, ".trash", "old.md"), "w") as f:
        f.write("mid:gone@example.com\n")
    r = call(vault, {"id": "gone@example.com", "author": "bob@example.com", "subject": ""})
    assert r["ok"], r
    assert lines(vault)[-1].startswith("- [ ] bob@example.com: (no subject) [mail](mid:gone@example.com)"), lines(vault)

    # An id that would end the Markdown link early is refused, not mangled.
    for bad in ["a b@example.com", "a)b@example.com", "", "no-at-sign"]:
        r = call(vault, {"id": bad, "author": "", "subject": "x"})
        assert not r["ok"], (bad, r)
    assert len(lines(vault)) == 2

with tempfile.TemporaryDirectory() as tmp:
    r = call(os.path.join(tmp, "missing"), pr)
    assert r == {"ok": False, "result": "no vault at " + os.path.join(tmp, "missing")}, r

print("thunderbird-todo: ok")
