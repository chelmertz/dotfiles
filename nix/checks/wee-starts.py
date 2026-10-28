"""Start wee the way F9 does and fail on any script error.

Usage: wee-starts.py <bin/wee> <slack_hints.py> <bin/wee-open>, with the
configured weechat on PATH.

Runs the real bin/wee (its setup block included) with -a, so wee-slack loads
but never connects: no network, no tokens. The terminal is a pty with a real
size, because weechat draws no bars into a 0x0 terminal and a broken bar item
then passes silently; weechat-headless never draws bars at all. The run prints
"Connecting to 2 slack teams." so the LOADING chip draws, and opens the
message menu on a fake Slack line so the menu draws too.
"""

import fcntl
import os
import pty
import re
import struct
import sys
import termios
import subprocess
import time

wee, hints, wee_open = sys.argv[1], sys.argv[2], sys.argv[3]
tmp = os.environ.get("TMPDIR", "/tmp")
weedir = os.path.join(tmp, "weechat")
os.makedirs(os.path.join(weedir, "python", "autoload"))
os.symlink(hints, os.path.join(weedir, "python", "autoload", "slack_hints.py"))
stubs = os.path.join(tmp, "stubs")
os.makedirs(stubs)
with open(os.path.join(stubs, "slack-session-ensure"), "w") as f:
    f.write("#!/bin/sh\n")
os.chmod(os.path.join(stubs, "slack-session-ensure"), 0o755)
# The message menu's "open link" hands URLs here; the run checks which.
opened = os.path.join(tmp, "opened")
with open(os.path.join(stubs, "xdg-open"), "w") as f:
    f.write('#!/bin/sh\necho "$1" >> %s\n' % opened)
os.chmod(os.path.join(stubs, "xdg-open"), 0o755)
# Ctrl+V reads the clipboard through xclip and previews images with feh. The
# stub clipboard holds an image unless clipmode says "text".
clipmode = os.path.join(tmp, "clipmode")
copied = os.path.join(tmp, "copied")
fehargs = os.path.join(tmp, "fehargs")
with open(os.path.join(stubs, "xclip"), "w") as f:
    f.write('#!/bin/sh\ncase "$*" in\n'
            '*TARGETS*) [ "$(cat %s 2>/dev/null)" = text ] || echo image/png ;;\n'
            '*image/png*) printf PNG ;;\n*-o*) printf PASTEDTEXT ;;\n*) cat > %s ;;\nesac\n'
            % (clipmode, copied))
os.chmod(os.path.join(stubs, "xclip"), 0o755)
with open(os.path.join(stubs, "feh"), "w") as f:
    f.write('#!/bin/sh\necho "$*" >> %s\nexec sleep 60\n' % fehargs)
os.chmod(os.path.join(stubs, "feh"), 0o755)
# wee-open focuses wee through i3 and reports a bad link with notify-send.
i3log = os.path.join(tmp, "i3log")
for name in ("i3-msg", "notify-send"):
    with open(os.path.join(stubs, name), "w") as f:
        f.write('#!/bin/sh\necho "%s $*" >> %s\n' % (name, i3log))
    os.chmod(os.path.join(stubs, name), 0o755)
os.makedirs(os.path.join(tmp, "run"), mode=0o700)
# --dir puts the fifo there rather than in $XDG_RUNTIME_DIR/weechat, where
# wee-open looks as it would for the real wee.
os.symlink(weedir, os.path.join(tmp, "run", "weechat"))
# A token list of "," (empty is "unset", which restores the placeholder):
# wee-slack connects to zero teams instead of trying its
# placeholder token and printing ERROR lines, which wee counts as a broken start.
with open(os.path.join(weedir, "plugins.conf"), "w") as f:
    f.write('[var]\npython.slack.slack_api_token = ","\n')

# Logs the hint bar's height whenever it changes, so the run can assert the
# message menu stays compact on a huge message.
barprobe = os.path.join(tmp, "barprobe.py")
with open(barprobe, "w") as f:
    f.write("""
import weechat as w
w.register("barprobe", "", "1", "MIT", "", "", "")
last = [None]
cur = [None]
def tick(data, calls):
    name = w.buffer_get_string(w.current_buffer(), "full_name")
    if name != cur[0]:
        cur[0] = name
        w.prnt("", "CURBUF=" + name)
    h_bar, h_win = w.hdata_get("bar"), w.hdata_get("bar_window")
    bar = w.hdata_get_list(h_bar, "gui_bars")
    while bar:
        if w.hdata_string(h_bar, bar, "name") == "hints":
            win = w.hdata_pointer(h_bar, bar, "bar_window")
            height = w.hdata_integer(h_win, win, "height") if win else -1
            if height != last[0]:
                last[0] = height
                w.prnt("", "BARHEIGHT=%d" % height)
        bar = w.hdata_move(h_bar, bar, 1)
    hot = w.infolist_get("hotlist", "", "")
    while w.infolist_next(hot):
        name = w.buffer_get_string(w.infolist_pointer(hot, "buffer_pointer"), "full_name")
        if name == "core.teamprobe":
            w.prnt("", "HOTLIST=" + name)
    w.infolist_free(hot)
    return w.WEECHAT_RC_OK
w.hook_timer(100, 0, 0, "tick", "")
# Stand-in for wee-slack's slack_people: one person with no buffer yet, and
# one whose DM buffer exists (wee-slack prefixes its short name with presence).
def people(data, name, args):
    home = w.buffer_search("core", "homeprobe")
    return "%s\\tzed.person\\n%s\\tyan.person" % (home, home)
w.hook_info("probe_people", "", "", "people", "")
# Ctrl+K on a person runs /query in that person's team buffer: record it.
def query(data, buffer, command):
    w.prnt("", "QUERY=%s %s" % (w.buffer_get_string(buffer, "full_name"), command))
    return w.WEECHAT_RC_OK_EAT
w.hook_command_run("/query *", "query", "")
# Ctrl+V's Enter hands the image to wee-slack's /slack upload: record it.
def slack(data, buffer, command):
    w.prnt("", "SLACK=%s" % command)
    if command.startswith("/slack download "):  # as wee-slack does when done
        path = command.split()[-1]
        open(path, "w").close()
        w.hook_signal_send("slack_download_done", w.WEECHAT_HOOK_SIGNAL_STRING, path)
    if command.startswith("/slack openlink "):  # as wee-slack: on bob's message
        w.hook_signal_send("slack_link_opened", w.WEECHAT_HOOK_SIGNAL_STRING,
                           w.current_buffer() + " 2.2")
    return w.WEECHAT_RC_OK_EAT
w.hook_command_run("/slack *", "slack", "")
# As wee-slack's linkarchive: types the message's Slack link.
def linkarchive(data, signal, hashtable):
    w.command(hashtable["_buffer"],
              "/input insert https://x.slack.com/archives/C1/p0000000002200000")
    return w.WEECHAT_RC_OK
w.hook_hsignal("slack_cursor_linkarchive", "linkarchive", "")
# The menu's delete goes to wee-slack's cursor action: record which message.
def delete(data, signal, hashtable):
    w.prnt("", "DELETE=" + hashtable["_chat_line_tags"].split(",")[0])
    return w.WEECHAT_RC_OK
w.hook_hsignal("slack_cursor_delete", "delete", "")
""")

script = ";".join([
    "/print Connecting to 2 slack teams.",
    "/print -tags slack_ts_0.5 fay\\thttps://files.slack.com/files-pri/T1-F1/pic.png (pic.png)",
    "/print -tags slack_ts_1.1 alice\\tfirst",
    "/print -tags slack_ts_2.2 bob\\tsecond",
    "/print -tags slack_ts_2.4 erin\\tsee https://one.example/a.",
    "/print -tags slack_ts_2.6 dave\\thttps://two.example/x or https://two.example/y)",
    # Newest: a link unfurl's worth of text over several lines.
    "/print -tags slack_ts_3.3 carol\\t" + "unfurled " * 300,
    "/print -tags slack_ts_3.3 \\t" + "more " * 300,
    "/python load " + barprobe,
    # Read, not current, not starred: shown only if active in the last 72 h.
    "/buffer add recentprobe",
    "/mute /command -buffer core.recentprobe core /buffer setauto"
    " localvar_set_slack_latest %d.000001" % (time.time() - 600),
    "/buffer add staleprobe",
    "/mute /command -buffer core.staleprobe core /buffer setauto"
    " localvar_set_slack_latest %d.000001" % (time.time() - 4 * 86400),
    # Stand-in for the MATCHi team buffer bin/wee names as home.
    "/buffer add homeprobe",
    "/set plugins.var.wee.home_buffer core.homeprobe",
    "/set plugins.var.python.go.people_info probe_people",
    "/buffer add yanbuf",
    "/mute /command -buffer core.yanbuf core /buffer set short_name +yan.person",
])

pid, fd = pty.fork()
if pid == 0:
    os.environ.update(
        HOME=tmp, XDG_RUNTIME_DIR=os.path.join(tmp, "run"),
        TERM="xterm-256color", PATH=stubs + ":" + os.environ["PATH"],
    )
    os.execvp("bash", ["bash", wee, "-a", "--dir", weedir, "-r", script])
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 160, 0, 0))

screen = bytearray()


def pump(seconds):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        try:
            screen.extend(os.read(fd, 65536))
        except BlockingIOError:
            pass
        except OSError:  # EIO: weechat exited
            return
        time.sleep(0.05)


os.set_blocking(fd, False)
pump(4)
loading_drawn = b"LOADING" in screen
recent_listed = b"recentprobe" in screen
stale_listed = b"staleprobe" in screen
os.write(fd, b"\x1ba")  # Alt+A with nothing unread
pump(1)
nothing_unread_drawn = b"nothing unread" in screen
os.write(fd, b"\x1b ")  # Alt+Space: message menu
pump(1)
os.write(fd, b"k")  # step to the older message
pump(1)
menu_drawn = b"reply in thread" in screen
os.write(fd, b"q")  # closes it, swallowed
pump(1)
# Open link: dave's message has two, so a digit picks; erin's has one; the
# newest (carol's) has none.
os.write(fd, b"\x1b ")  # the targeted message is marked in the chat itself
pump(0.5)
marked_drawn = "▶ carol".encode() in re.sub(rb"\x1b\[[0-9;?]*[A-Za-z]", b"", bytes(screen))
os.write(fd, b"q")
pump(0.5)
before = len(screen)
os.write(fd, b"/window refresh\r")
pump(1)
unmarked_after = "▶ carol".encode() not in re.sub(rb"\x1b\[[0-9;?]*[A-Za-z]", b"", bytes(screen[before:]))
for keys in (b"ko2", b"kko", b"o"):
    os.write(fd, b"\x1b ")
    pump(0.5)
    for key in keys:
        os.write(fd, bytes([key]))
        pump(0.5)
no_link_drawn = b"no link in this message" in screen
# Esc as ghostty sends it in the wee window (CSI 27 u, see .i3/config): it must
# close the menu and end Ctrl+K's /go, or the /print lines below never run.
ESC = b"\x1b[27u"
os.write(fd, b"\x1b ")
pump(1)
os.write(fd, ESC)
pump(1)
os.write(fd, b"/print MENU-CLOSED\r")
pump(1)
# Esc straight after Ctrl+K: go.py restores an input that never changed, and
# nothing redraws the prompt away unless the close signals a change.
os.write(fd, b"\x0b")
pump(1)
before = len(screen)
os.write(fd, ESC)
pump(1)
go_redrawn_on_close = len(screen) > before
os.write(fd, b"\x0b")  # Ctrl+K: /go
pump(1)
os.write(fd, b"ho")  # filtering, as Esc usually comes after typing
pump(1)
os.write(fd, ESC)
pump(1)
os.write(fd, b"/print GO-CLOSED\r")
pump(1)
os.write(fd, b"\x0b")  # Ctrl+K reaches people without a buffer too
pump(1)
os.write(fd, b"zed")
pump(1)
os.write(fd, b"\r")
pump(1)
os.write(fd, b"\x0b")  # a person with a DM buffer is listed once: Tab wraps
pump(1)
os.write(fd, b"yan")
pump(1)
os.write(fd, b"\t")
pump(1)
os.write(fd, b"\r")
pump(1)
os.write(fd, b"/buffer core.weechat\r")  # back where Connected is expected
pump(1)
os.write(fd, b"\x16")  # Ctrl+V with an image: attached, Enter sends the message
pump(1)
# The input line itself says so, where the eyes are while typing.
attached_shown = "[📎 1 image]".encode() in screen
os.write(fd, b"look here\r")  # the first Enter only closes the preview
pump(1)
enter_closed_preview = b"preview closed" in screen
os.write(fd, b"\r")  # the second sends
pump(1)
os.write(fd, b"\x16")  # two images and no text: one upload of both
pump(1)
os.write(fd, b"\x16")
pump(1)
os.write(fd, b"\r\r")
pump(1)
os.write(fd, b"\x1b ")  # menu on the oldest message, v: its image in feh
pump(1)
os.write(fd, b"kkkkkv")
pump(1)
os.write(fd, b"\r")  # Enter closes the viewer too
pump(1)
os.write(fd, b"\x16")  # attached; the first Esc only hides the preview
pump(1)
os.write(fd, ESC)
pump(1)
pastes = lambda: [f for f in os.listdir(os.path.join(tmp, "run")) if f.startswith("wee-paste-")]
kept_after_hide = len(pastes())
os.write(fd, ESC)  # the second drops the image and its file
pump(1)
with open(clipmode, "w") as f:
    f.write("text")
os.write(fd, b"/print \x16")  # no image: Ctrl+V pastes the text
pump(1)
os.write(fd, b"\r")
pump(1)
# L copies a wee:// link; wee-open hands it to wee through the fifo, and the
# menu opens on the message it names.
os.write(fd, b"\x1b ")
pump(0.5)
os.write(fd, b"kkL")
pump(1)
weelink = open(copied).read() if os.path.exists(copied) else ""
env = dict(os.environ, XDG_RUNTIME_DIR=os.path.join(tmp, "run"),
           PATH=stubs + ":" + os.environ["PATH"])
link = weelink[weelink.find("(") + 1:-1]
before = len(screen)
opened_rc = subprocess.run([sys.executable, wee_open, link], env=env).returncode
pump(1.5)
landed = "▶ bob".encode() in re.sub(rb"\x1b\[[0-9;?]*[A-Za-z]", b"", bytes(screen[before:]))
os.write(fd, ESC)
pump(0.5)
# A link that could carry a second command is refused before the fifo.
bad_rc = subprocess.run([sys.executable, wee_open, link + "\n*/quit"], env=env).returncode
# t in a thread goes back to its message (/slack parent), not deeper.
os.write(fd, b"/buffer add threadprobe\r")
pump(0.3)
os.write(fd, b"/buffer set localvar_set_slack_type thread\r")
pump(0.3)
os.write(fd, b"/print -tags slack_ts_4.4 gus\\treply\r")
pump(0.3)
os.write(fd, b"\x1b ")
pump(0.5)
os.write(fd, b"t")
pump(1)
os.write(fd, b"/buffer core.weechat\r")
pump(0.5)
# E on your own message (wee-slack tags it self_msg) starts an edit. An
# emptied input's Enter cancels it, as Esc does; no one else's is editable.
os.write(fd, b"/print -tags slack_ts_9.9,self_msg me\\tmine\r")
pump(0.3)
for cancel in (b"\r", ESC):
    os.write(fd, b"\x1b ")
    pump(0.5)
    os.write(fd, b"E")
    pump(1)
    os.write(fd, cancel)
    pump(1)
editing_shown = "✏ editing".encode() in screen and b"edit cancelled" in screen
os.write(fd, b"\x1b ")
pump(0.5)
os.write(fd, b"kE")
pump(1)
not_yours_shown = b"only your own messages" in screen
# D asks first: n keeps the message, y deletes it; no one else's.
for answer in (b"n", b"y"):
    os.write(fd, b"\x1b ")
    pump(0.5)
    os.write(fd, b"D")
    pump(1)
    os.write(fd, answer)
    pump(1)
delete_asked = b"delete this message?" in screen
os.write(fd, b"\x1b ")
pump(0.5)
os.write(fd, b"kD")
pump(1)
delete_refused = b"can be deleted" in screen
# Ctrl+Backspace, which ghostty sends as Ctrl+H, deletes the word before it.
os.write(fd, b"/print WORDS one two\x08\r")
pump(1)
# Both teams connect: wee leaves the core buffer for the home buffer.
os.write(fd, b"/print Connected to Slack team T1 (a) with username x\r")
pump(0.5)
os.write(fd, b"/print Connected to Slack team T2 (b) with username x\r")
pump(1)
# An error while away from core must show on the bar. SELFTEST lines are the
# one error the run expects; any other still fails it.
os.write(fd, b"/print -core =!=\\tSELFTEST error\r")
pump(1)
error_chip_drawn = b"1 error" in screen
os.write(fd, b"/buffer core.weechat\r")
pump(0.5)
# A start that prints an error must stay on core, where it is visible. The
# error is a request wee-slack gave up on, as in the FD-limit failures.
os.write(fd, b"/print Connecting to 1 slack team.\r")
pump(0.3)
os.write(fd, b"/print Failed requesting SELFTEST for team a, will not retry"
             b" after too many failed attempts.\r")
pump(0.3)
os.write(fd, b"/print Connected to Slack team T1 (a) with username x\r")
pump(1)
# A team buffer's connect notice must not put it on the hotlist (Alt+A).
os.write(fd, b"/buffer add teamprobe\r")
pump(0.3)
os.write(fd, b"/command -buffer core.teamprobe core /buffer set localvar_set_type server\r")
pump(0.3)
# A conversation in that team: its name extends the team buffer's, and
# weechat.notify options are inherited along that prefix.
os.write(fd, b"/buffer add teamprobe.dm\r")
pump(0.3)
os.write(fd, b"/print -buffer core.teamprobe Connected to Slack team T3 (c) with username x\r")
pump(1)
# A followed thread's new reply, which wee-slack announces there, must count.
os.write(fd, b"/print -core THREAD-NOTICE\r")
pump(0.3)
os.write(fd, b"/print -buffer core.teamprobe New message in thread $855, channel #c\r")
pump(1)
os.write(fd, b"/command -buffer core.teamprobe.dm core /eval /print -core DMNOTIFY=${buffer.notify}\r")
pump(0.5)
os.write(fd, b"/logger flush\r")
pump(1)
os.write(fd, b"/quit\r")
pump(3)
os.waitpid(pid, 0)
# No wee running: the Slack link opens in the browser instead.
subprocess.run([sys.executable, wee_open, link], env=env)

log = open(os.path.join(weedir, "logs", "core.weechat.weechatlog"),
           encoding="utf-8", errors="replace").read().splitlines()
errors = [l for l in log if ("Traceback" in l or "\t=!=\t" in l)
          and "SELFTEST" not in l]
# slack_hints is autoloaded, which logs differently from the /script loads.
missing = [name for name in ("slack", "go", "url_hint", "edit")
           if not any('registered script "%s"' % name in l for l in log)]
if not any("python scripts loaded: slack_hints" in l for l in log):
    missing.append("slack_hints")
failed = False
for line in errors:
    print("error:", line)
    failed = True
for name in missing:
    print("script never registered:", name)
    failed = True
for mark in ("MENU-CLOSED", "GO-CLOSED"):
    if not any(l.endswith("\t" + mark) for l in log):
        print("Esc did not close it:", mark)
        failed = True
bufs = [l.rsplit("=", 1)[1] for l in log if "\tCURBUF=" in l]
if bufs.count("core.homeprobe") != 1:
    print("expected one move to the home buffer (clean start) and none after"
          " a start with errors:", bufs)
    failed = True
heights = [int(l.rsplit("=", 1)[1]) for l in log if "\tBARHEIGHT=" in l]
if not heights or max(heights) < 2:
    print("message menu never opened (bar heights %s)" % heights)
    failed = True
elif max(heights) > 3:
    print("message menu bar grew to %d rows on a long message" % max(heights))
    failed = True
notice = next((i for i, l in enumerate(log) if l.endswith("\tTHREAD-NOTICE")), len(log))
hot = [i for i, l in enumerate(log) if "\tHOTLIST=core.teamprobe" in l]
if any(i < notice for i in hot):
    print("team buffer's connect notice put it on the hotlist")
    failed = True
if not any(i > notice for i in hot):
    print("a thread notice in the team buffer did not reach the hotlist")
    failed = True
if not go_redrawn_on_close:
    print("Esc closed Ctrl+K on an empty prompt but nothing was redrawn")
    failed = True
if not recent_listed or stale_listed:
    print("buflist: recent buffer listed=%s (want True), stale listed=%s"
          " (want False)" % (recent_listed, stale_listed))
    failed = True
urls = open(opened).read().split()[:-1] if os.path.exists(opened) else []
if urls != ["https://two.example/y", "https://one.example/a"]:
    print("open link: want the second of two, then the only one; got", urls)
    failed = True
if not marked_drawn or not unmarked_after:
    print("message marker: drawn while open=%s, gone after close=%s"
          % (marked_drawn, unmarked_after))
    failed = True
if not no_link_drawn:
    print("open link on a message without one said nothing")
    failed = True
if not nothing_unread_drawn:
    print("Alt+A with nothing unread showed no notice")
    failed = True
if not any(l.endswith("\tDMNOTIFY=3") for l in log):
    print("handling the team buffer changed its conversations' notify level:",
          [l.rsplit("\t", 1)[-1] for l in log if "DMNOTIFY=" in l])
    failed = True
if not any(l.endswith("\tQUERY=core.homeprobe /query zed.person") for l in log):
    print("Ctrl+K on a person did not open the DM:",
          [l.rsplit("\t", 1)[-1] for l in log if "QUERY=" in l])
    failed = True
if any("/query yan.person" in l for l in log):
    print("Ctrl+K listed a person a second time beside their DM buffer")
    failed = True
if not attached_shown:
    print("Ctrl+V with an image did not mark the input line")
    failed = True
if not enter_closed_preview:
    print("the first Enter with the preview open did not just close it")
    failed = True
slacks = [l.split("SLACK=", 1)[1] for l in log if "\tSLACK=" in l]
if weelink != "[wee](wee://x.slack.com/archives/C1/p0000000002200000)":
    print("menu L copied:", repr(weelink))
    failed = True
if (opened_rc != 0 or "/slack openlink https://x.slack.com/archives/C1/p0000000002200000" not in slacks
        or not landed):
    print("wee-open: rc=%s, openlink sent=%s, menu landed on the message=%s"
          % (opened_rc, [c for c in slacks if "openlink" in c], landed))
    failed = True
calls = open(i3log).read().splitlines() if os.path.exists(i3log) else []
if bad_rc == 0 or not any(c.startswith("i3-msg") and "org" in c for c in calls) or not any(
        c.startswith("notify-send") for c in calls):
    print("wee-open: a bad link should be refused and a good one focus wee:", bad_rc, calls)
    failed = True
edits = [c for c in slacks if c.startswith("/slack edit")]
if edits != ["/slack edit 9.9", "/slack edit"] * 2 or not editing_shown or not not_yours_shown:
    print("menu E: edits=%s, editing marker and cancel notice=%s, refused another's=%s"
          % (edits, editing_shown, not_yours_shown))
    failed = True
deletes = [l.rsplit("=", 1)[1] for l in log if "\tDELETE=" in l]
if deletes != ["slack_ts_9.9"] or not delete_asked or not delete_refused:
    print("menu D: deleted=%s, asked=%s, refused another's=%s"
          % (deletes, delete_asked, delete_refused))
    failed = True
if slacks.count("/slack parent") != 1:
    print("menu t in a thread should run /slack parent once:", slacks)
    failed = True
browsed = open(opened).read().split() if os.path.exists(opened) else []
if browsed[-1:] != ["https://x.slack.com/archives/C1/p0000000002200000"]:
    print("wee-open with no wee should open the Slack link:", browsed)
    failed = True
uploads = [c for c in slacks if c.startswith("/slack upload")]
downloads = [l.split("SLACK=", 1)[1] for l in log if "\tSLACK=/slack download " in l]
if (len(downloads) != 1 or not re.match(
        r"/slack download https://files\.slack\.com/files-pri/T1-F1/pic\.png \S+/run/wee-view/0-pic\.png$",
        downloads[0])):
    print("menu v should download the message's one image:", downloads)
    failed = True
png = r"\S+/run/wee-paste-\d+\.png"
if (len(uploads) != 2 or not re.match(r"/slack upload %s -- look here$" % png, uploads[0])
        or not re.match(r"/slack upload %s %s$" % (png, png), uploads[1])):
    print("Ctrl+V uploads: want one image with the message, then two with none:", uploads)
    failed = True
fehs = open(fehargs).read().splitlines() if os.path.exists(fehargs) else []
if not any(l.endswith("/run/wee-view/0-pic.png") for l in fehs):
    print("menu v did not open the downloaded image in feh:", fehs)
    failed = True
if sum("wee-paste-" in l for l in fehs) != 4:
    print("Ctrl+V did not preview each image in feh:",
          open(fehargs).read() if os.path.exists(fehargs) else None)
    failed = True
kept = pastes()
if kept_after_hide != 4:
    print("the first Esc should keep the attached image:", kept_after_hide, "files")
    failed = True
if len(kept) != 3:
    print("expected only the sent image left (Esc deletes a dropped one):", kept)
    failed = True
if not any(l.rstrip().endswith("\tWORDS one") for l in log):
    print("Ctrl+H did not delete the previous word:",
          [l.rsplit("\t", 1)[-1] for l in log if "\tWORDS" in l])
    failed = True
if not any(l.endswith("\tPASTEDTEXT") for l in log):
    print("Ctrl+V without an image did not paste the text")
    failed = True
if not error_chip_drawn:
    print("error chip never drawn for an error printed while away from core")
    failed = True
if not loading_drawn:
    print("LOADING chip never drawn")
    failed = True
if not menu_drawn:
    print("message menu never drawn")
    failed = True
sys.exit(1 if failed else 0)
