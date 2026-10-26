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

script = ";".join([
    "/print Connecting to 2 slack teams.",
    "/print -tags slack_ts_1.1 alice\\tfirst",
    "/print -tags slack_ts_2.2 bob\\tsecond",
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
os.write(fd, b"/logger flush\r")
pump(1)
os.write(fd, b"/quit\r")
pump(3)
os.waitpid(pid, 0)

log = open(os.path.join(weedir, "logs", "core.weechat.weechatlog"),
           encoding="utf-8", errors="replace").read().splitlines()
errors = [l for l in log if "Traceback" in l or "\t=!=\t" in l]
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
if not loading_drawn:
    print("LOADING chip never drawn")
    failed = True
if not menu_drawn:
    print("message menu never drawn")
    failed = True
sys.exit(1 if failed else 0)
