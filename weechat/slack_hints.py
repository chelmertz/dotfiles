# A single bar across the bottom listing the wee-slack shortcuts worth
# learning, led by a LOADING chip while wee-slack is still connecting or
# weechat is too busy to take keys. Dismiss the bar once the shortcuts are
# muscle memory with: /bar del hints (or hide it: /bar hide hints). After
# LOADING, a quieter "fetching" chip stays until wee-slack's post-connect
# requests have drained, since conversations keep turning up until then.
#
# Alt+Space or Shift+Space (/msgmenu, bound in bin/wee; ghostty sends
# Alt+Space's bytes for Shift+Space in the wee window only) swaps the bar for a menu of actions
# on one message: the newest, or another stepped to with j/k. The next key
# picks an action, q or Esc closes the menu, and any other key closes it and
# then does what it always does.
#
# /escape (Esc in the wee window) closes the menu or ends Ctrl+K's /go. A
# terminal sends a bare Esc as the first byte of every Alt+key, so weechat
# waits for the key after it and merges the two; ghostty sends Esc there as
# CSI 27 u instead (i3's F9 line), a complete key bin/wee binds to /escape.
#
# Ctrl+V (/pasteimage, bound in bin/wee) with an image on the clipboard
# attaches it to the current buffer and previews it in feh, which i3 floats
# without focus; each Ctrl+V adds one. Enter sends the input line as one
# message with them all. Esc or Enter first closes the preview; then Enter
# sends, and Esc (or leaving the buffer) drops them. Without an image Ctrl+V pastes the text.
#
# The menu's L copies a wee:// link to the message for notes; bin/wee-open
# (the xdg handler for wee://) runs /slack openlink on it, and this script
# then opens the menu on that message, as it does on t in a thread, which
# goes back to the message the thread started from.
#
# The menu's E puts one of your messages in the input (wee-slack's /slack
# edit); Enter replaces it, and Esc, an emptied input or leaving the buffer
# cancels. D deletes one of your messages after a y to confirm.
#
# The menu's T (todo) and ! (todo now, ⏫) append the message as an Obsidian
# Tasks line with its wee link to slack.md in plugins.var.wee.todo_vault, whose
# daily template shows the next few in a Slack section. A message already
# linked anywhere in the vault, open, done or cancelled, is not added again.
# notify-send reports either way.
#
# The menu's R opens one ghostty per GitHub PR linked in the message, each
# running claude on a review that ends in an approval hint and posts nothing
# unasked, and reacts 👀 to the message.
# Installed from the dotfiles repo.
import math
import os
import re
import subprocess
import time

import weechat as w

# Rows of (key, what it does). Keys are highlighted, labels dimmed, groups
# divided. The bar sizes itself to the number of rows.
HINTS = [
    [
        ("Ctrl+K", "channels, people"),
        ("Alt+A", "next unread"),
        ("+:emoji:", "react"),
        ("Ctrl+X", "nvim"),
    ],
    [
        ("Shift+Space", "message menu"),
        ("Ctrl+click", "link; Alt+L if wrapped"),
        ("/all", "all buffers"),
        ("/rehistory -remote", "reload from Slack"),
    ],
]

# (key, label, action). Actions are below; "older"/"newer" move the target
# and keep the menu open.
MENU = [
    ("r", "reply in thread", "reply"),
    ("t", "open thread", "thread"),
    ("e", "react", "react"),
    ("E", "edit yours", "edit"),
    ("D", "delete yours", "delete"),
    ("y", "copy", "copy"),
    ("Y", "copy all (whole thread)", "copy_all"),
    ("l", "copy permalink", "permalink"),
    ("L", "copy wee link", "weelink"),
    ("R", "claude review PRs", "review"),
    ("T", "todo", "todo"),
    ("!", "todo now", "todo_now"),
    ("o", "open link", "open"),
    ("v", "view images", "view"),
    ("k", "older", "older"),
    ("j", "newer", "newer"),
    ("q", "close", "close"),
]
# A lone Esc never completes a key combo. key_pressed sees it on its own, so
# the menu at least closes at once rather than on the merged key after it.
ESC = "\x01["

staged = {"buffer": "", "paths": [], "fehs": []}
# The message menu's "view images": downloads still running, then one feh.
viewer = {"pending": [], "paths": [], "feh": None}
# The buffer whose input holds a message being edited (/slack edit).
editing = {"buffer": ""}
# A message /slack openlink or /slack parent showed, until its line is in.
landing = {"buffer": "", "ts": "", "tries": 0}
LANDING_TRIES = 25  # of 200 ms: a thread's replies arrive over the network
IMAGE = re.compile(r"https://files\.slack\.com/files-pri/\S+\.(png|jpe?g|gif|webp)$", re.I)
menu = {"open": False, "buffer": "", "index": 0, "links": [], "marked": None,
        "scrolled": False, "confirm": False}
PR = re.compile(r"https://github\.com/[\w.-]+/[\w.-]+/pull/\d+")
REVIEW = ("Review {}. Post nothing to GitHub unless asked. End with an approval"
          " hint: approve, comment or request changes, and the one reason that"
          " decides it.")
# Trailing punctuation is the sentence's, not the URL's.
URL = re.compile(r"https?://[^\s<>\"]+[^\s<>\").,;:!?'\]]")

# "Safe to type" means keys get through: every team has connected AND the main
# loop has run this timer on time for SETTLE_TICKS ticks in a row. Right after
# connecting, wee-slack processes the whole workspace on the main thread and
# key presses queue up for tens of seconds, so "connected" alone is not enough.
TICK_MS = 500
LATE_S = 1.0
SETTLE_TICKS = 6

# wee-slack runs every API request as a forked weechat process. After a
# connect it fetches every conversation, and "fetching" lasts until no such
# child has been running for IDLE_TICKS ticks in a row; it then stays off, so
# the occasional single request later does not flicker it.
IDLE_TICKS = 2

state = {"expected": 0, "connected": 0, "on_time": SETTLE_TICKS, "last": None,
         "fetching": False, "idle": 0, "errors": 0, "notice": ""}
NOTICE_MS = 2000


def loading():
    return state["connected"] < state["expected"] or state["on_time"] < SETTLE_TICKS


def requests_running():
    pid = os.getpid()
    for task in os.listdir("/proc/%d/task" % pid):
        try:
            children = open("/proc/%d/task/%s/children" % (pid, task)).read().split()
        except OSError:
            continue
        for child in children:
            try:
                if "weechat" in open("/proc/%s/comm" % child).read():
                    return True
            except OSError:
                pass
    return False


def render_rows(rows):
    key = w.color("black,cyan")
    label = w.color("default")
    sep = w.color("240")
    reset = w.color("reset")
    return "\n".join(
        " " + (" {s}│{r} ".format(s=sep, r=reset)).join(
            "{k} {name} {r}{l} {desc}".format(
                k=key, name=name, r=reset, l=label, desc=desc
            )
            for name, desc in row
        ) + " "
        for row in rows
    )


def messages(buffer):
    """Slack messages in buffer, newest first, as
    (tags, [(date, prefix, text, line_data pointer)]).
    A message spans every consecutive line sharing its slack_ts_ tag."""
    h_buf, h_lines, h_line, h_data = (
        w.hdata_get(n) for n in ("buffer", "lines", "line", "line_data"))
    own = w.hdata_pointer(h_buf, buffer, "own_lines")
    line = w.hdata_pointer(h_lines, own, "last_line")
    found = []
    while line:
        data = w.hdata_pointer(h_line, line, "data")
        line = w.hdata_move(h_line, line, -1)
        if not w.hdata_char(h_data, data, "displayed"):
            continue
        tags = [w.hdata_string(h_data, data, "%d|tags_array" % i)
                for i in range(w.hdata_integer(h_data, data, "tags_count"))]
        ts = next((t for t in tags if t.startswith("slack_ts_")), None)
        if not ts:
            continue
        prefix = w.hdata_string(h_data, data, "prefix")
        if menu["marked"] and menu["marked"][1] == data:
            prefix = menu["marked"][2]  # as it was before mark()
        entry = (w.hdata_time(h_data, data, "date"),
                 w.string_remove_color(prefix, ""),
                 w.string_remove_color(w.hdata_string(h_data, data, "message"), ""),
                 data)
        if found and ts in found[-1][0]:
            found[-1][1].insert(0, entry)
        else:
            found.append((tags, [entry]))
    return found


def as_text(msg):
    date, prefix, _, _ = msg[1][0]
    head = time.strftime("%Y-%m-%d %H:%M", time.localtime(date))
    return "{} {}: {}".format(head, prefix.strip(), "\n".join(e[2] for e in msg[1]))


def copy(text):
    subprocess.run(["xclip", "-selection", "clipboard"], input=text.encode(),
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def links(msg):
    found = []
    for _, _, text, _ in msg[1]:
        for url in URL.findall(text):
            if url not in found:
                found.append(url)
    return found


def open_url(url):
    subprocess.Popen(["xdg-open", url], stdout=subprocess.DEVNULL,
                     stderr=subprocess.DEVNULL, start_new_session=True)


def clipboard(target=None):
    cmd = ["xclip", "-selection", "clipboard", "-o"]
    if target:
        cmd += ["-t", target]
    try:
        return subprocess.run(cmd, capture_output=True, timeout=3).stdout
    except (OSError, subprocess.TimeoutExpired):
        return b""


def paste_cmd_cb(data, buffer, args):
    if b"image/png" not in clipboard("TARGETS").split():
        text = clipboard().decode("utf-8", "replace")
        line = w.buffer_get_string(buffer, "input")
        pos = w.buffer_get_integer(buffer, "input_pos")
        w.buffer_set(buffer, "input", line[:pos] + text + line[pos:])
        w.buffer_set(buffer, "input_pos", str(pos + len(text)))
        return w.WEECHAT_RC_OK
    if staged["buffer"] != buffer:
        drop_images()
    path = os.path.join(os.environ.get("XDG_RUNTIME_DIR", "/tmp"),
                        "wee-paste-%d.png" % (time.time() * 1000))
    with open(path, "wb") as f:
        f.write(clipboard("image/png"))
    feh = subprocess.Popen(
        ["feh", "--class", "wee-preview", "--scale-down", "--geometry", "800x600",
         "--title", "Enter in wee sends, Esc drops", path],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    staged["buffer"] = buffer
    staged["paths"].append(path)
    staged["fehs"].append(feh)
    w.bar_item_update("slack_hints")
    w.bar_item_update("input_text")
    return w.WEECHAT_RC_OK


def drop_images(sent=False):
    for feh in staged["fehs"]:
        feh.terminate()
    for path in [] if sent else staged["paths"]:
        try:
            os.remove(path)
        except OSError:
            pass
    staged.update(buffer="", paths=[], fehs=[])
    w.bar_item_update("slack_hints")
    w.bar_item_update("input_text")


def input_display_cb(data, modifier, buffer, string):
    # The bar is out of sight while typing: say it where the eyes are.
    if buffer == editing["buffer"]:
        return "{c}[✏ editing – Enter saves, Esc cancels]{r} {t}".format(
            c=w.color("black,yellow"), r=w.color("reset"), t=string)
    if not staged["paths"] or buffer != staged["buffer"]:
        return string
    n = len(staged["paths"])
    return "{c}[📎 {n} image{s}]{r} {t}".format(
        c=w.color("black,cyan"), n=n, s="" if n == 1 else "s",
        r=w.color("reset"), t=string)


def input_return_cb(data, buffer, command):
    text = w.buffer_get_string(buffer, "input")
    if text.startswith("/") and not text.startswith("//"):
        return w.WEECHAT_RC_OK  # a command still runs; previews and images stay
    if buffer == editing["buffer"]:
        # wee-slack's input callback saves the edit; weechat never calls it
        # for an empty input, so that cancels instead.
        stop_editing(cancel=not text)
        return w.WEECHAT_RC_OK_EAT if not text else w.WEECHAT_RC_OK
    # While a preview is open, Enter only closes it, as the first Esc does;
    # the next Enter sends.
    if close_viewer():
        return w.WEECHAT_RC_OK_EAT
    if not staged["paths"] or buffer != staged["buffer"]:
        return w.WEECHAT_RC_OK
    if any(feh.poll() is None for feh in staged["fehs"]):
        for feh in staged["fehs"]:
            feh.terminate()
        staged["fehs"] = []  # gone, even before it has exited
        show_notice("preview closed – Enter sends")
        return w.WEECHAT_RC_OK_EAT
    w.buffer_set(buffer, "input", "")
    # curl reads the files after this returns: keep them (XDG_RUNTIME_DIR is
    # a tmpfs that goes at logout).
    paths = " ".join(staged["paths"])
    drop_images(sent=True)
    if text.startswith("//"):  # weechat's escape for a leading "/"
        text = text[1:]
    w.command(buffer, "/slack upload {}{}".format(
        paths, " -- " + text if text else ""))
    return w.WEECHAT_RC_OK_EAT


def stop_editing(cancel):
    buffer = editing["buffer"]
    editing["buffer"] = ""
    if cancel:
        w.command(buffer, "/slack edit")
        w.buffer_set(buffer, "input", "")
        show_notice("edit cancelled")
    w.bar_item_update("input_text")


def show_notice(text):
    state["notice"] = text
    w.bar_item_update("slack_hints")
    w.hook_timer(NOTICE_MS, 0, 1, "notice_end_cb", "")


def slack_line_event(event, msg):
    # wee-slack's own cursor-mode handlers, given this message's line.
    w.hook_hsignal_send("slack_cursor_" + event, {
        "_buffer": menu["buffer"], "_chat_line_tags": ",".join(msg[0])})


def run_action(action, msgs):
    buffer = menu["buffer"]
    msg = msgs[menu["index"]]
    if action == "thread" and w.buffer_get_string(buffer, "localvar_slack_type") == "thread":
        w.command(buffer, "/slack parent")  # t again: back out of the thread
    elif action in ("reply", "thread"):
        slack_line_event(action, msg)
    elif action == "edit":
        # wee-slack tags the lines you sent self_msg; it refuses anyone
        # else's message too.
        if "self_msg" not in msg[0]:
            show_notice("only your own messages can be edited")
            return
        ts = next(t for t in msg[0] if t.startswith("slack_ts_"))[len("slack_ts_"):]
        w.command(buffer, "/slack edit " + ts)
        editing["buffer"] = buffer
        w.bar_item_update("input_text")
    elif action == "react":
        slack_line_event("message", msg)  # types the message's $id
        w.command(buffer, "/input insert +:")
    elif action == "copy":
        copy(as_text(msg))
    elif action == "copy_all":
        copy("\n".join(as_text(m) for m in reversed(msgs)))
    elif action == "permalink":
        copy(permalink(buffer, msg))
    elif action == "weelink":
        # Markdown, for Obsidian: it hands the unknown scheme to xdg-open.
        copy("[wee]({})".format(weelink(buffer, msg)))
    elif action in ("todo", "todo_now"):
        try:
            result = add_todo(msg, weelink(buffer, msg), action == "todo_now")
        except (OSError, ValueError) as e:
            result = "todo failed: {}".format(e)
        show_notice(result)
        subprocess.Popen(["notify-send", "-a", "wee", "wee", result],
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    elif action == "view":
        view_images(buffer, [u for u in links(msg) if IMAGE.match(u)])
    elif action == "review":
        prs = []
        for url in links(msg):
            pr = PR.match(url)
            if pr and pr.group() not in prs:
                prs.append(pr.group())
        if not prs:
            show_notice("no PR link in this message")
            return
        # 👀 tells the author someone is on it. "$id+:eyes:" adds, never
        # toggles, so a second R leaves it in place.
        w.command(buffer, typed(buffer, "message", msg) + "+:eyes:")
        for pr in prs:
            subprocess.Popen(["ghostty", "--working-directory=" + os.path.expanduser("~/p"),
                              "-e", "claude", REVIEW.format(pr)],
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                             start_new_session=True)
    elif action == "open":
        urls = links(msg)
        if not urls:
            show_notice("no link in this message")
        elif len(urls) == 1:
            open_url(urls[0])
        else:
            # Several: the bar lists them and a digit picks one.
            menu.update(open=True, links=urls[:9])
            w.bar_item_update("slack_hints")


def typed(buffer, event, msg):
    # Some cursor events type their result into the input; take it and put
    # back what was there.
    saved = w.buffer_get_string(buffer, "input")
    pos = w.buffer_get_integer(buffer, "input_pos")
    w.buffer_set(buffer, "input", "")
    slack_line_event(event, msg)
    text = w.buffer_get_string(buffer, "input")
    w.buffer_set(buffer, "input", saved)
    w.buffer_set(buffer, "input_pos", str(pos))
    return text


def permalink(buffer, msg):
    return typed(buffer, "linkarchive", msg)


def weelink(buffer, msg):
    return permalink(buffer, msg).replace("https://", "wee://", 1)


THREAD_SUMMARY = re.compile(r"\s*\[ Thread: [^\]]*\]$")
TODO_HEAD = 100  # screen columns of the message on the task line itself
TODO_NOTE = "slack.md"  # in the vault; the daily template queries it by name
QUOTE_LINES = 5
QUOTE_CHARS = 400


def add_todo(msg, link, now):
    vault = os.path.expanduser(w.config_string(w.config_get("plugins.var.wee.todo_vault")))
    if not os.path.isdir(vault):
        raise ValueError("no vault at " + vault)
    # The dedup key leaves out the scheme, so a pasted https permalink counts
    # too, and ?thread_ts=…, which the message's own ts already pins down.
    key = link.split("?")[0].split("://", 1)[1]
    for root, dirs, files in os.walk(vault):
        dirs[:] = [d for d in dirs if not d.startswith(".")]  # .obsidian, .trash
        for name in files:
            if name.endswith(".md"):
                path = os.path.join(root, name)
                with open(path, encoding="utf-8", errors="replace") as f:
                    if key in f.read():
                        return "todo already in " + os.path.relpath(path, vault)
    _, author, _, _ = msg[1][0]
    lines = [e[2] for e in msg[1]]
    lines[-1] = THREAD_SUMMARY.sub("", lines[-1])
    # "#chan" would turn into a tag, which the backlog queries filter on; a
    # "#" inside a URL is not a tag and stays.
    text = re.sub(r"(^|\s)#", r"\1\\#", "\n".join(lines).strip())
    # ➕ is Tasks' created date: the daily note's Slack section sorts on it.
    task = "- [ ] {}: {} [wee]({}) [Slack]({}){} ➕ {}".format(
        author.strip(), one_line(text, TODO_HEAD), link,
        link.replace("wee://", "https://", 1), " ⏫" if now else "",
        time.strftime("%Y-%m-%d"))
    note = os.path.join(vault, TODO_NOTE)
    body = open(note).read() if os.path.exists(note) else ""
    if body and not body.endswith("\n"):
        body += "\n"
    with open(note, "w") as f:
        f.write(body + task + quote_head(text) + "\n")
    return "todo added to " + TODO_NOTE


def quote_head(text):
    """The message quoted under its task, cut at QUOTE_LINES lines or
    QUOTE_CHARS characters, about five rows in Obsidian: the wee link has the
    rest."""
    quoted, left = [], QUOTE_CHARS
    for line in text.split("\n")[:QUOTE_LINES]:
        quoted.append(line[:left])
        left -= len(line)
        if left <= 0:
            break
    if "\n".join(quoted) != text:
        quoted[-1] += "…"
    return "".join("\n  > " + l if l else "\n  >" for l in quoted)


def link_opened_cb(data, signal, args):
    buffer, _, ts = args.partition(" ")
    landing.update(buffer=buffer, ts=ts, tries=0)
    if ts:
        land_cb("", 0)
    return w.WEECHAT_RC_OK


def land_cb(data, remaining_calls):
    # Opens the menu on the linked message once its line is printed, which
    # marks it and scrolls to it; one not loaded yet gets a few seconds.
    if not landing["ts"] or w.current_buffer() != landing["buffer"]:
        return w.WEECHAT_RC_OK  # the user moved on
    msgs = messages(landing["buffer"])
    tag = "slack_ts_" + landing["ts"]
    index = next((i for i, m in enumerate(msgs) if tag in m[0]), None)
    if index is not None:
        landing["ts"] = ""
        if menu["open"]:
            close_menu()
        menu.update(open=True, buffer=landing["buffer"], index=index)
        mark(msgs)
        w.bar_item_update("slack_hints")
    elif landing["tries"] < LANDING_TRIES:
        landing["tries"] += 1
        w.hook_timer(200, 0, 1, "land_cb", "")
    else:
        landing["ts"] = ""
        show_notice("that message is older than wee has loaded")
    return w.WEECHAT_RC_OK


def view_images(buffer, urls):
    # wee-slack holds the credentials: it downloads each file and signals
    # slack_download_done, and feh opens once the last one is in.
    if not urls:
        show_notice("no image in this message")
        return
    close_viewer()
    folder = os.path.join(os.environ.get("XDG_RUNTIME_DIR", "/tmp"), "wee-view")
    os.makedirs(folder, exist_ok=True)
    for name in os.listdir(folder):
        os.remove(os.path.join(folder, name))
    for i, url in enumerate(urls):
        path = os.path.join(folder, "%d-%s" % (i, url.rsplit("/", 1)[-1]))
        viewer["pending"].append(path)
        w.command(buffer, "/slack download {} {}".format(url, path))
    show_notice("loading image…")


def download_done_cb(data, signal, path):
    if path in viewer["pending"]:
        viewer["pending"].remove(path)
        viewer["paths"].append(path)
    elif path == "" and viewer["pending"]:
        viewer["pending"].pop()  # failed; wee-slack printed why
    if viewer["pending"] or not viewer["paths"]:
        return w.WEECHAT_RC_OK
    viewer["feh"] = subprocess.Popen(
        ["feh", "--class", "wee-preview", "--scale-down", "--geometry", "1200x900",
         "--title", "Esc in wee closes; arrows: next image"] + sorted(viewer["paths"]),
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    viewer["paths"] = []
    return w.WEECHAT_RC_OK


def close_viewer():
    viewer["pending"] = []
    viewer["paths"] = []
    if viewer["feh"] and viewer["feh"].poll() is None:
        viewer["feh"].terminate()
        viewer["feh"] = None
        return True
    viewer["feh"] = None
    return False


def go_active():
    # go.py hooks this modifier only while its prompt is open.
    hooks = w.infolist_get("hook", "", "modifier")
    active = False
    while w.infolist_next(hooks):
        if (w.infolist_string(hooks, "modifier") == "input_text_display_with_cursor"
                and w.infolist_string(hooks, "subplugin") == "go"):
            active = True
    w.infolist_free(hooks)
    return active


def escape_cmd_cb(data, buffer, args):
    if menu["open"]:
        close_menu()
    elif go_active():
        w.command(buffer, "/go")
    elif close_viewer():
        pass
    elif editing["buffer"]:
        stop_editing(cancel=True)
    elif any(feh.poll() is None for feh in staged["fehs"]):
        # First Esc only hides the preview; the images stay attached.
        for feh in staged["fehs"]:
            feh.terminate()
        staged["fehs"] = []  # gone, even before it has exited
        show_notice("preview closed – Esc again drops the images")
    elif staged["paths"]:
        drop_images()
        show_notice("images dropped")
    return w.WEECHAT_RC_OK


def close_menu():
    menu["open"] = False
    menu["links"] = []
    menu["confirm"] = False
    unmark()


def mark(msgs):
    # The targeted message's author column shows reversed with a ▶ in the
    # chat itself, so stepping with k/j is visible where the message is.
    unmark()
    if menu["index"] >= len(msgs):
        return
    h_data = w.hdata_get("line_data")
    data = msgs[menu["index"]][1][0][3]
    raw = w.hdata_string(h_data, data, "prefix")
    menu["marked"] = (menu["buffer"], data, raw)
    w.hdata_update(h_data, data, {"prefix": "{}▶ {}{}".format(
        w.color("reverse"), w.string_remove_color(raw, ""), w.color("-reverse"))})
    show_line(menu["buffer"], data)


def show_line(buffer, data):
    """Scroll the chat so the line is on screen. WeeChat cannot scroll to a
    line, but after scroll_bottom, "/window scroll -N" puts the line N+1 from
    the end at the top (measured). Rows per line are estimated from width."""
    win = w.current_window()
    if w.window_get_pointer(win, "buffer") != buffer:
        return
    height = w.window_get_integer(win, "win_chat_height")
    width = max(20, w.window_get_integer(win, "win_chat_width") - 20)
    h_buf, h_lines, h_line, h_data = (
        w.hdata_get(n) for n in ("buffer", "lines", "line", "line_data"))
    line = w.hdata_pointer(h_lines, w.hdata_pointer(h_buf, buffer, "own_lines"), "last_line")
    lines = rows = 0
    while line:
        here = w.hdata_pointer(h_line, line, "data")
        if w.hdata_char(h_data, here, "displayed"):
            lines += 1
            rows += max(1, math.ceil(w.strlen_screen(
                w.hdata_string(h_data, here, "message")) / width))
        if here == data:
            break
        line = w.hdata_move(h_line, line, -1)
    w.command(buffer, "/window scroll_bottom")
    menu["scrolled"] = rows > height
    if menu["scrolled"]:
        w.command(buffer, "/window scroll -%d" % (lines - 1))


def unmark():
    if not menu["marked"]:
        return
    buffer, data, raw = menu["marked"]
    menu["marked"] = None
    if menu["scrolled"]:
        menu["scrolled"] = False
        w.command(buffer, "/window scroll_bottom")
    # wee-slack reprints buffers, freeing their lines: only write back to a
    # line that is still there.
    if any(e[3] == data for m in messages(buffer) for e in m[1]):
        w.hdata_update(w.hdata_get("line_data"), data, {"prefix": raw})
    w.bar_item_update("slack_hints")


def msgmenu_cmd_cb(data, buffer, args):
    # Shift+Space is bound here too, and gets hit by accident mid-sentence: with
    # anything typed, it stays a space.
    if w.buffer_get_string(buffer, "input"):
        w.command(buffer, "/input insert \\x20")
        return w.WEECHAT_RC_OK
    if not messages(buffer):
        w.prnt(buffer, "msgmenu: no Slack message in this buffer")
        return w.WEECHAT_RC_OK
    menu.update(open=True, buffer=buffer, index=0)
    mark(messages(buffer))
    w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def key_pressed_cb(data, signal, key):
    if menu["open"] and key == ESC:
        close_menu()
    return w.WEECHAT_RC_OK


def key_combo_cb(data, signal, key):
    # Only this signal can eat a key; key_pressed ignores WEECHAT_RC_OK_EAT.
    if not menu["open"]:
        return w.WEECHAT_RC_OK
    if menu["links"]:
        urls = menu["links"]
        close_menu()
        if key.isdigit() and 1 <= int(key) <= len(urls):
            open_url(urls[int(key) - 1])
            return w.WEECHAT_RC_OK_EAT
        return w.WEECHAT_RC_OK_EAT if key == "q" else w.WEECHAT_RC_OK
    msgs = messages(menu["buffer"])
    if menu["confirm"]:
        # Only y deletes; any other key keeps the message and is swallowed.
        close_menu()
        if key == "y" and menu["index"] < len(msgs):
            # wee-slack sends "$id s///", which deletes only your own.
            slack_line_event("delete", msgs[menu["index"]])
        return w.WEECHAT_RC_OK_EAT
    action = next((a for k, _, a in MENU if k == key), None)
    if not action:
        close_menu()
        return w.WEECHAT_RC_OK
    if action == "delete" and menu["index"] < len(msgs):
        if "self_msg" in msgs[menu["index"]][0]:
            menu["confirm"] = True  # the menu stays, asking
        else:
            close_menu()
            show_notice("only your own messages can be deleted")
        w.bar_item_update("slack_hints")
        return w.WEECHAT_RC_OK_EAT
    if action == "older":
        menu["index"] = min(menu["index"] + 1, len(msgs) - 1)
        mark(msgs)
        w.bar_item_update("slack_hints")
        return w.WEECHAT_RC_OK_EAT
    if action == "newer":
        menu["index"] = max(menu["index"] - 1, 0)
        mark(msgs)
        w.bar_item_update("slack_hints")
        return w.WEECHAT_RC_OK_EAT
    close_menu()
    if action != "close" and menu["index"] < len(msgs):
        run_action(action, msgs)
    return w.WEECHAT_RC_OK_EAT


def one_line(text, width):
    """First line of text, cut to width screen columns. Link unfurls can make
    a message thousands of characters long; the preview only has to say
    which message is targeted, and the bar would grow to fit all of it."""
    lines = text.split("\n")
    line = lines[0] + (" ⏎…" if len(lines) > 1 else "")
    if w.strlen_screen(line) <= width:
        return line
    while line and w.strlen_screen(line + "…") > width:
        line = line[:-1]
    return line + "…"


def menu_rows():
    if menu["links"]:
        width = int(w.info_get("term_width", "") or 80)
        return "\n".join(
            render_rows([[(str(i), one_line(url, width - 8))]])
            for i, url in enumerate(menu["links"], 1))
    msgs = messages(menu["buffer"])
    if menu["index"] >= len(msgs):
        return render_rows([[(k, d) for k, d, _ in MENU]])
    head = " ▸ {}/{} ".format(menu["index"] + 1, len(msgs))
    width = int(w.info_get("term_width", "") or 80)
    preview = one_line(as_text(msgs[menu["index"]]), width - w.strlen_screen(head) - 2)
    target = "{c}{h}{r} {p}".format(
        c=w.color("black,yellow"), h=head, r=w.color("reset"), p=preview)
    if menu["confirm"]:
        return target + "\n{c} delete this message? {r}".format(
            c=w.color("*white,red"), r=w.color("reset")) + render_rows(
            [[("y", "delete it"), ("any other key", "keep it")]])
    return target + "\n" + render_rows([[(k, d) for k, d, _ in MENU]])


def core_print_cb(data, buffer, date, tags, displayed, highlight, prefix, message):
    # Errors printed in core (script tracebacks, wee-slack's ERROR lines and
    # requests it gave up on, weechat's =!= prefix) are counted until the core buffer is switched to:
    # the bar shows the count, and connected_cb will not move a start that
    # had errors off core. Kept apart from the bar item, which can itself be
    # what crashes.
    if (w.string_remove_color(prefix, "").strip() == "=!="
            or "Traceback" in message or message.startswith("ERROR")
            or (message.startswith("Failed requesting")
                and "will not retry" in message)):
        state["errors"] += 1
        w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def buffer_switch_cb(data, signal, buffer):
    if editing["buffer"] and buffer != editing["buffer"]:
        stop_editing(cancel=True)
    if staged["paths"] and buffer != staged["buffer"]:
        drop_images()
        show_notice("images dropped")
    if state["errors"] and w.buffer_get_string(buffer, "full_name") == "core.weechat":
        state["errors"] = 0
        w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def hints_item_cb(data, item, window):
    hints = menu_rows() if menu["open"] else render_rows(HINTS)
    if state["errors"]:
        hints = "{c} ⚠ {n} error{s} – Alt+1 {r}".format(
            c=w.color("*white,red"), n=state["errors"],
            s="" if state["errors"] == 1 else "s", r=w.color("reset")) + hints
    if staged["paths"]:
        n = len(staged["paths"])
        hints = "{c} 📎 {n} image{s} – Enter sends with your message, Esc Esc drops {r}".format(
            c=w.color("black,cyan"), n=n, s="" if n == 1 else "s",
            r=w.color("reset")) + hints
    if state["notice"]:
        hints = "{c} {t} {r}".format(c=w.color("black,green"), t=state["notice"],
                                     r=w.color("reset")) + hints
    if not loading():
        if state["fetching"]:
            return "{c} ⏳ fetching {r}".format(
                c=w.color("black,yellow"), r=w.color("reset")) + hints
        return hints
    # Deliberately loud: first on the bar so a narrow window cannot clip it.
    chip = "{c} ⏳ LOADING {n}/{e} – keys may lag {r}".format(
        c=w.color("*yellow,red"), n=state["connected"],
        e=state["expected"], r=w.color("reset"),
    )
    return chip + hints


def nextunread_cmd_cb(data, buffer, args):
    # /buffer jump smart does nothing, silently, when nothing is unread and it
    # has already returned to where it started. Say so, so that is not mistaken
    # for wee lagging.
    before = w.current_buffer()
    w.command(buffer, "/buffer jump smart")
    if w.current_buffer() == before:
        show_notice("nothing unread")
    return w.WEECHAT_RC_OK


def notice_end_cb(data, remaining_calls):
    state["notice"] = ""
    w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def connecting_cb(data, buffer, date, tags, displayed, highlight, prefix, message):
    # "Connecting to 2 slack teams." / "Connecting to 1 slack team."
    words = message.split()
    if len(words) > 2 and words[2].isdigit():
        state.update(expected=int(words[2]), connected=0, on_time=0,
                     fetching=True, idle=0)
        w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def connected_cb(data, buffer, date, tags, displayed, highlight, prefix, message):
    state["connected"] += 1
    state["on_time"] = 0
    # The connect notice put the team buffer on the hotlist and made Alt+A
    # stop there first; drop just that. Later lines still count: wee-slack
    # posts new replies in followed threads and thread mentions there.
    if w.buffer_get_string(buffer, "localvar_type") == "server":
        w.buffer_set(buffer, "hotlist", "-1")
    # /query needs a team, which the core buffer has none of: once every team
    # is up, move from core to plugins.var.wee.home_buffer (set in bin/wee).
    # Anyone who has already moved stays put, and a start that printed errors
    # stays on core so they are seen, as they were before this switch existed.
    home = w.config_string(w.config_get("plugins.var.wee.home_buffer"))
    current = w.buffer_get_string(w.current_buffer(), "full_name")
    if (home and not state["errors"] and current == "core.weechat"
            and state["connected"] >= state["expected"]):
        w.command("", "/buffer " + home)
    w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def tick_cb(data, remaining_calls):
    now = time.monotonic()
    late = state["last"] is not None and now - state["last"] > TICK_MS / 1000.0 + LATE_S
    state["last"] = now
    was_loading = loading()
    state["on_time"] = 0 if late else min(state["on_time"] + 1, SETTLE_TICKS)
    changed = late or was_loading != loading()
    if state["fetching"] and not loading():
        state["idle"] = 0 if requests_running() else state["idle"] + 1
        if state["idle"] >= IDLE_TICKS:
            state["fetching"] = False
            changed = True
    if changed:
        w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def main():
    w.register(
        "slack_hints", "", "1.22", "MIT",
        "Hint bar of wee-slack shortcuts with a loading indicator", "", "",
    )
    w.bar_item_new("slack_hints", "hints_item_cb", "")
    # Create the bar once; leave it alone if the user has tweaked or removed it.
    if not w.config_string(w.config_get("weechat.bar.hints.items")):
        w.command("", "/bar add hints root bottom 1 0 slack_hints")
    w.hook_print("", "", "Connecting to ", 1, "connecting_cb", "")
    w.hook_print("", "", "Connected to Slack team", 1, "connected_cb", "")
    w.hook_timer(TICK_MS, 0, 0, "tick_cb", "")
    w.hook_print(w.buffer_search_main(), "", "", 1, "core_print_cb", "")
    w.hook_signal("buffer_switch", "buffer_switch_cb", "")
    w.hook_command("msgmenu", "Menu of actions on a Slack message", "", "", "",
                   "msgmenu_cmd_cb", "")
    w.hook_command("nextunread", "Jump to the next unread buffer, or say there is none",
                   "", "", "", "nextunread_cmd_cb", "")
    w.hook_command("escape", "Close the message menu or end /go", "", "", "",
                   "escape_cmd_cb", "")
    w.hook_command("pasteimage", "Attach the clipboard's image, or paste its text",
                   "", "", "", "paste_cmd_cb", "")
    w.hook_command_run("/input return", "input_return_cb", "")
    w.hook_modifier("input_text_display_with_cursor", "input_display_cb", "")
    w.hook_signal("slack_download_done", "download_done_cb", "")
    w.hook_signal("slack_link_opened", "link_opened_cb", "")
    w.hook_signal("key_pressed", "key_pressed_cb", "")
    w.hook_signal("key_combo_default", "key_combo_cb", "")


if __name__ == "__main__":
    main()
