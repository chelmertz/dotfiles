"""Native messaging host for the obsidian-todo Thunderbird add-on.

Reads one length-prefixed JSON message {id, author, subject}, appends an
Obsidian Tasks line linking mid:<id> to mail.md in the vault, and answers
{ok, result}. The mail twin of the wee menu's T (weechat/slack_hints.py
add_todo): a message already linked anywhere in the vault, open, done or
cancelled, is not added again.

The vault is ~/sync/notes (nix/obsidian.nix), or $OBSIDIAN_TODO_VAULT.
"""
import email.utils
import json
import os
import re
import struct
import sys
import time

TODO_NOTE = "mail.md"  # in the vault; the daily template queries it by name
TODO_HEAD = 100
# Thunderbird's mid: handler looks the id up verbatim, with no URL decoding,
# so an id that would end the Markdown link early cannot be linked at all.
SAFE_ID = re.compile(r"[^\s<>()\[\]]+@[^\s<>()\[\]]+")


def one_line(text, width):
    text = " ".join(text.split())
    return text if len(text) <= width else text[: width - 1] + "…"


def add_todo(vault, msg):
    if not os.path.isdir(vault):
        return False, "no vault at " + vault
    mid = (msg.get("id") or "").strip().strip("<>")
    if not SAFE_ID.fullmatch(mid):
        return False, "cannot link Message-ID " + repr(mid)
    key = "mid:" + mid
    for root, dirs, files in os.walk(vault):
        dirs[:] = [d for d in dirs if not d.startswith(".")]  # .obsidian, .trash
        for name in files:
            if name.endswith(".md"):
                path = os.path.join(root, name)
                with open(path, encoding="utf-8", errors="replace") as f:
                    if key in f.read():
                        return True, "todo already in " + os.path.relpath(path, vault)
    name, addr = email.utils.parseaddr(msg.get("author") or "")
    # "#word" would turn into a tag, which the backlog queries filter on.
    subject = re.sub(r"(^|\s)#", r"\1\\#", one_line(msg.get("subject") or "(no subject)", TODO_HEAD))
    # ➕ is Tasks' created date: the daily note's Mail section sorts on it.
    task = "- [ ] {}: {} [mail]({}) ➕ {}".format(
        name or addr or "?", subject, key, time.strftime("%Y-%m-%d"))
    note = os.path.join(vault, TODO_NOTE)
    body = open(note, encoding="utf-8").read() if os.path.exists(note) else ""
    if body and not body.endswith("\n"):
        body += "\n"
    with open(note, "w", encoding="utf-8") as f:
        f.write(body + task + "\n")
    return True, "todo added to " + TODO_NOTE


def main():
    n = struct.unpack("=I", sys.stdin.buffer.read(4))[0]
    msg = json.loads(sys.stdin.buffer.read(n))
    vault = os.environ.get("OBSIDIAN_TODO_VAULT") or os.path.expanduser("~/sync/notes")
    ok, result = add_todo(vault, msg)
    out = json.dumps({"ok": ok, "result": result}).encode()
    sys.stdout.buffer.write(struct.pack("=I", len(out)) + out)
    sys.stdout.buffer.flush()


if __name__ == "__main__":
    main()
