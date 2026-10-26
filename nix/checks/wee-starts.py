"""Start wee the way F9 does and fail on any script error.

Usage: wee-starts.py <bin/wee> <slack_hints.py>, with the configured weechat
on PATH.

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
import struct
import sys
import termios
import time

wee, hints = sys.argv[1], sys.argv[2]
tmp = os.environ.get("TMPDIR", "/tmp")
weedir = os.path.join(tmp, "weechat")
os.makedirs(os.path.join(weedir, "python", "autoload"))
os.symlink(hints, os.path.join(weedir, "python", "autoload", "slack_hints.py"))
stubs = os.path.join(tmp, "stubs")
os.makedirs(stubs)
with open(os.path.join(stubs, "slack-session-ensure"), "w") as f:
    f.write("#!/bin/sh\n")
os.chmod(os.path.join(stubs, "slack-session-ensure"), 0o755)
os.makedirs(os.path.join(tmp, "run"), mode=0o700)
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
    return w.WEECHAT_RC_OK
w.hook_timer(100, 0, 0, "tick", "")
""")

script = ";".join([
    "/print Connecting to 2 slack teams.",
    "/print -tags slack_ts_1.1 alice\\tfirst",
    "/print -tags slack_ts_2.2 bob\\tsecond",
    # Newest: a link unfurl's worth of text over several lines.
    "/print -tags slack_ts_3.3 carol\\t" + "unfurled " * 300,
    "/print -tags slack_ts_3.3 \\t" + "more " * 300,
    "/python load " + barprobe,
    # Stand-in for the MATCHi team buffer bin/wee names as home.
    "/buffer add homeprobe",
    "/set plugins.var.wee.home_buffer core.homeprobe",
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
os.write(fd, b"\x1b ")  # Alt+Space: message menu
pump(1)
os.write(fd, b"k")  # step to the older message
pump(1)
menu_drawn = b"reply in thread" in screen
os.write(fd, b"q")  # closes it, swallowed
pump(1)
# Esc as ghostty sends it in the wee window (CSI 27 u, see .i3/config): it must
# close the menu and end Ctrl+K's /go, or the /print lines below never run.
ESC = b"\x1b[27u"
os.write(fd, b"\x1b ")
pump(1)
os.write(fd, ESC)
pump(1)
os.write(fd, b"/print MENU-CLOSED\r")
pump(1)
os.write(fd, b"\x0b")  # Ctrl+K: /go
pump(1)
os.write(fd, ESC)
pump(1)
os.write(fd, b"/print GO-CLOSED\r")
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
# A start that prints an error must stay on core, where it is visible.
os.write(fd, b"/print Connecting to 1 slack team.\r")
pump(0.3)
os.write(fd, b"/print -core =!=\\tSELFTEST startup error\r")
pump(0.3)
os.write(fd, b"/print Connected to Slack team T1 (a) with username x\r")
pump(1)
os.write(fd, b"/logger flush\r")
pump(1)
os.write(fd, b"/quit\r")
pump(3)
os.waitpid(pid, 0)

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
