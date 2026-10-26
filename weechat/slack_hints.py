# A single bar across the bottom listing the wee-slack shortcuts worth
# learning, led by a LOADING chip while wee-slack is still connecting or
# weechat is too busy to take keys. Dismiss the bar once the shortcuts are
# muscle memory with: /bar del hints (or hide it: /bar hide hints).
# Installed from the dotfiles repo.
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
    ],
]

# "Safe to type" means keys get through: every team has connected AND the main
# loop has run this timer on time for SETTLE_TICKS ticks in a row. Right after
# connecting, wee-slack processes the whole workspace on the main thread and
# key presses queue up for tens of seconds, so "connected" alone is not enough.
TICK_MS = 500
LATE_S = 1.0
SETTLE_TICKS = 6

state = {"expected": 0, "connected": 0, "on_time": SETTLE_TICKS, "last": None}


def loading():
    return state["connected"] < state["expected"] or state["on_time"] < SETTLE_TICKS


def hints_item_cb(data, item, window):
    key = w.color("black,cyan")
    label = w.color("default")
    sep = w.color("240")
    reset = w.color("reset")
    rows = [
        " " + (" {s}│{r} ".format(s=sep, r=reset)).join(
            "{k} {name} {r}{l} {desc}".format(
                k=key, name=name, r=reset, l=label, desc=desc
            )
            for name, desc in row
        ) + " "
        for row in HINTS
    ]
    hints = "\n".join(rows)
    if not loading():
        return hints
    # Deliberately loud: first on the bar so a narrow window cannot clip it.
    chip = "{c} ⏳ LOADING {n}/{e} – keys may lag {r}".format(
        c=w.color("*yellow,red"), n=state["connected"],
        e=state["expected"], r=reset,
    )
    return chip + hints


def connecting_cb(data, buffer, date, tags, displayed, highlight, prefix, message):
    # "Connecting to 2 slack teams." / "Connecting to 1 slack team."
    words = message.split()
    if len(words) > 2 and words[2].isdigit():
        state.update(expected=int(words[2]), connected=0, on_time=0)
        w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def connected_cb(data, buffer, date, tags, displayed, highlight, prefix, message):
    state["connected"] += 1
    state["on_time"] = 0
    w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def tick_cb(data, remaining_calls):
    now = time.monotonic()
    late = state["last"] is not None and now - state["last"] > TICK_MS / 1000.0 + LATE_S
    state["last"] = now
    was_loading = loading()
    state["on_time"] = 0 if late else min(state["on_time"] + 1, SETTLE_TICKS)
    if late or was_loading != loading():
        w.bar_item_update("slack_hints")
    return w.WEECHAT_RC_OK


def main():
    w.register(
        "slack_hints", "", "1.3", "MIT",
        "Hint bar of wee-slack shortcuts with a loading indicator", "", "",
    )
    w.bar_item_new("slack_hints", "hints_item_cb", "")
    # Create the bar once; leave it alone if the user has tweaked or removed it.
    if not w.config_string(w.config_get("weechat.bar.hints.items")):
        w.command("", "/bar add hints root bottom 1 0 slack_hints")
    w.hook_print("", "", "Connecting to ", 1, "connecting_cb", "")
    w.hook_print("", "", "Connected to Slack team", 1, "connected_cb", "")
    w.hook_timer(TICK_MS, 0, 0, "tick_cb", "")


if __name__ == "__main__":
    main()
