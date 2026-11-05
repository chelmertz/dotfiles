"""The chanmon feed takes what wee-slack prints and survives a restart.

Usage: wee-feed.py <bin/wee>, with the configured weechat on PATH.

Applies bin/wee's chanmon and logger lines, prints into fake buffers shaped
like wee-slack's (a 🔒 private channel, a thread, a DM), then starts weechat
again on the same directory and reads the feed back from logger's replay.
Only in the build sandbox: the packaged notify_send script would otherwise
put the fake DM on the desktop.
"""

import os
import subprocess
import sys

wee = sys.argv[1]
tmp = os.environ.get("TMPDIR", "/tmp")
weedir = os.path.join(tmp, "weechat")
os.makedirs(weedir)

setup = open(wee).read().split("setup='", 1)[1].split("'\n", 1)[0].splitlines()
setup = [l for l in setup if "chanmon" in l or "logger.look" in l]
assert len(setup) >= 6, setup

fake = os.path.join(tmp, "fake.py")
open(fake, "w").write('''
import weechat as w
w.register("fake", "t", "1", "MIT", "", "", "")
def mk(name, typ, stype, ch):
    b = w.buffer_new(name, "", "", "", "")
    for k, v in (("type", typ), ("slack_type", stype), ("channel", ch)):
        w.buffer_set(b, "localvar_set_" + k, v)
    return b
priv = mk("slack.matchi.\\U0001f512secret", "channel", "group", "\\U0001f512secret")
thr = mk("slack.matchi.#dev.b89", "channel", "thread", "#dev.b89")
dm = mk("slack.matchi.adam", "private", "im", "adam")
# Tags as wee-slack's tag() builds them: live, history (backlog), own message.
w.prnt_date_tags(priv, 0, "irc_privmsg,notify_message,log1,nick_bob", "bob\\tmsg-private")
w.prnt_date_tags(thr, 0, "irc_privmsg,notify_message,log1,nick_carl", "carl\\tmsg-thread")
w.prnt_date_tags(dm, 0, "irc_privmsg,notify_private,log1,nick_adam", "adam\\tmsg-dm")
w.prnt_date_tags(priv, 0, "irc_privmsg,no_highlight,notify_none,logger_backlog,nick_bob", "bob\\tmsg-history")
w.prnt_date_tags(priv, 0, "irc_privmsg,no_highlight,notify_none,self_msg,nick_me", "me\\tmsg-self")
''')

dump = os.path.join(tmp, "dump.py")
open(dump, "w").write('''
import os, weechat as w
w.register("dump", "t", "1", "MIT", "", "", "")
out = []
for name in ("perl.chanmon", "python.slack.matchi.adam"):
    b = w.buffer_search("==", name)
    out.append("BUFFER %s starred=%s short_name=%s" % (name, w.buffer_get_string(b, "localvar_starred"), w.buffer_get_string(b, "short_name")))
    l = w.hdata_pointer(w.hdata_get("lines"), w.hdata_pointer(w.hdata_get("buffer"), b, "lines"), "first_line")
    while l:
        d = w.hdata_pointer(w.hdata_get("line"), l, "data")
        out.append(w.string_remove_color(w.hdata_string(w.hdata_get("line_data"), d, "prefix") + " | " + w.hdata_string(w.hdata_get("line_data"), d, "message"), ""))
        l = w.hdata_move(w.hdata_get("line"), l, 1)
open(os.environ["DUMP"], "w").write("\\n".join(out) + "\\n")
''')

reopen = os.path.join(tmp, "reopen.py")
open(reopen, "w").write('''
import weechat as w
w.register("reopen", "t", "1", "MIT", "", "", "")
b = w.buffer_new("slack.matchi.adam", "", "", "", "")
w.hook_signal_send("logger_backlog", w.WEECHAT_HOOK_SIGNAL_POINTER, b)
''')


def run(*scripts):
    # -r runs before scripts autoload, so the fakes wait for chanmon's buffer.
    cmds = setup + ["/wait %d /python load %s" % (i + 1, s) for i, s in enumerate(scripts)]
    cmds.append("/wait %d /quit" % (len(scripts) + 1))
    out = os.path.join(tmp, "dump.txt")
    subprocess.run(["weechat-headless", "--dir", weedir, "-r", ";".join(cmds)],
                   env=dict(os.environ, DUMP=out), timeout=60,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    return open(out).read()


def feed(dumped):
    lines = dumped.split("BUFFER perl.chanmon ", 1)[1].split("BUFFER ", 1)[0]
    return lines.splitlines()


failed = False
first = run(fake, dump)
second = run(reopen, dump)
want = ["\U0001f512secret | <bob> msg-private", "#dev.b89 | <carl> msg-thread", "adam | <adam> msg-dm"]
for label, dumped in (("live", first), ("after restart", second)):
    got = feed(dumped)
    if got[0] != "starred=1 short_name=history":
        print(label, "feed not starred as history, so the buffer list hides or misnames it:", got[0])
        failed = True
    lines = [l for l in got[1:] if "msg-" in l]
    if lines != want:
        print(label, "feed holds", lines, "want", want)
        failed = True
slack = second.split("BUFFER python.slack.matchi.adam ", 1)[1].splitlines()[1:]
if slack:
    print("a Slack buffer replayed its log; only the feed should:", slack)
    failed = True
sys.exit(1 if failed else 0)
