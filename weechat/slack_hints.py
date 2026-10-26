# A single static bar across the bottom listing the wee-slack shortcuts worth
# learning. Dismiss it once they are muscle memory with: /bar del hints
# (or hide temporarily: /bar hide hints). Installed from the dotfiles repo.
import weechat as w

# (key, what it does). Keys are highlighted, labels dimmed, groups divided.
HINTS = [
    ("Ctrl+K", "channels"),
    ("Alt+A", "next unread"),
    ("/dm user", "new DM"),
    ("+:emoji:", "react"),
    ("Alt+Enter", "newline"),
]


def hints_item_cb(data, item, window):
    key = w.color("black,cyan")
    label = w.color("default")
    sep = w.color("240")
    reset = w.color("reset")
    parts = [
        "{k} {name} {r}{l}{desc}".format(
            k=key, name=name, r=reset, l=label, desc=desc
        )
        for name, desc in HINTS
    ]
    return " " + (" {s}│{r} ".format(s=sep, r=reset)).join(parts) + " "


def main():
    w.register(
        "slack_hints", "", "1.1", "MIT",
        "Static hint bar of wee-slack shortcuts", "", "",
    )
    w.bar_item_new("slack_hints", "hints_item_cb", "")
    # Create the bar once; leave it alone if the user has tweaked or removed it.
    if not w.config_string(w.config_get("weechat.bar.hints.items")):
        w.command("", "/bar add hints root bottom 1 0 slack_hints")


if __name__ == "__main__":
    main()
