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
# Installed from the dotfiles repo.
import os
import subprocess
import time

import weechat as w

# Rows of (key, what it does). Keys are highlighted, labels dimmed, groups
# divided. The bar sizes itself to the number of rows.
HINTS = [
    [
        ("Ctrl+K", "channels"),
        ("Alt+A", "next unread"),
        ("/query name", "DM"),
        ("+:emoji:", "react"),
        ("Ctrl+X", "nvim"),
        ("/all", "all buffers"),
    ],
    [
        ("/cursor", "pick msg: T thread, R reply"),
        ("/thread $id", "open thread"),
        ("/close", "back"),
        ("/u N", "open link N"),
        ("Ctrl+click", "link; Alt+L if wrapped"),
        ("Shift+Space", "message menu"),
    ],
]

# (key, label, action). Actions are below; "older"/"newer" move the target
# and keep the menu open.
MENU = [
    ("r", "reply in thread", "reply"),
    ("t", "open thread", "thread"),
    ("e", "react", "react"),
    ("y", "copy", "copy"),
    ("Y", "copy all (whole thread)", "copy_all"),
    ("l", "copy permalink", "permalink"),
    ("k", "older", "older"),
    ("j", "newer", "newer"),
    ("q", "close", "close"),
]
# A lone Esc never completes a key combo. key_pressed sees it on its own, so
# the menu at least closes at once rather than on the merged key after it.
ESC = "\x01["

menu = {"open": False, "buffer": "", "index": 0}

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
         "fetching": False, "idle": 0, "errors": 0}


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
    """Slack messages in buffer, newest first, as (tags, [(date, prefix, text)]).
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
        entry = (w.hdata_time(h_data, data, "date"),
                 w.string_remove_color(w.hdata_string(h_data, data, "prefix"), ""),
                 w.string_remove_color(w.hdata_string(h_data, data, "message"), ""))
        if found and ts in found[-1][0]:
            found[-1][1].insert(0, entry)
        else:
            found.append((tags, [entry]))
    return found


def as_text(msg):
    date, prefix, _ = msg[1][0]
    head = time.strftime("%Y-%m-%d %H:%M", time.localtime(date))
    return "{} {}: {}".format(head, prefix.strip(), "\n".join(t for _, _, t in msg[1]))


def copy(text):
    subprocess.run(["xclip", "-selection", "clipboard"], input=text.encode(),
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def slack_line_event(event, msg):
    # wee-slack's own cursor-mode handlers, given this message's line.
    w.hook_hsignal_send("slack_cursor_" + event, {
        "_buffer": menu["buffer"], "_chat_line_tags": ",".join(msg[0])})


def run_action(action, msgs):
    buffer = menu["buffer"]
    msg = msgs[menu["index"]]
    if action in ("reply", "thread"):
        slack_line_event(action, msg)
    elif action == "react":
        slack_line_event("message", msg)  # types the message's $id
        w.command(buffer, "/input insert +:")
    elif action == "copy":
        copy(as_text(msg))
    elif action == "copy_all":
        copy("\n".join(as_text(m) for m in reversed(msgs)))
    elif action == "permalink":
        # linkarchive types the URL into the input; take it and put back what
        # was there.
        saved = w.buffer_get_string(buffer, "input")
        pos = w.buffer_get_integer(buffer, "input_pos")
        w.buffer_set(buffer, "input", "")
        slack_line_event("linkarchive", msg)
        copy(w.buffer_get_string(buffer, "input"))
        w.buffer_set(buffer, "input", saved)
        w.buffer_set(buffer, "input_pos", str(pos))


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
    return w.WEECHAT_RC_OK


def close_menu():
    menu["open"] = False
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
    action = next((a for k, _, a in MENU if k == key), None)
    if not action:
        close_menu()
        return w.WEECHAT_RC_OK
    msgs = messages(menu["buffer"])
    if action == "older":
        menu["index"] = min(menu["index"] + 1, len(msgs) - 1)
        w.bar_item_update("slack_hints")
        return w.WEECHAT_RC_OK_EAT
    if action == "newer":
        menu["index"] = max(menu["index"] - 1, 0)
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
    msgs = messages(menu["buffer"])
    if menu["index"] >= len(msgs):
        return render_rows([[(k, d) for k, d, _ in MENU]])
    head = " ▸ {}/{} ".format(menu["index"] + 1, len(msgs))
    width = int(w.info_get("term_width", "") or 80)
    preview = one_line(as_text(msgs[menu["index"]]), width - w.strlen_screen(head) - 2)
    target = "{c}{h}{r} {p}".format(
        c=w.color("black,yellow"), h=head, r=w.color("reset"), p=preview)
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
    # The team buffer only ever shows connect notices, which put it on the
    # hotlist and made Alt+A stop there first. Keep it off the hotlist.
    if w.buffer_get_string(buffer, "localvar_type") == "server":
        w.buffer_set(buffer, "notify", "0")
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
        "slack_hints", "", "1.9", "MIT",
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
    w.hook_command("escape", "Close the message menu or end /go", "", "", "",
                   "escape_cmd_cb", "")
    w.hook_signal("key_pressed", "key_pressed_cb", "")
    w.hook_signal("key_combo_default", "key_combo_cb", "")


if __name__ == "__main__":
    main()
