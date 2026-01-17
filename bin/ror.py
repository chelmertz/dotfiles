#!/usr/bin/env python3
#
# "ror" = raise or run
#
# from https://faq.i3wm.org/question/2473/run-or-focus-in-i3.1.html
# (with patches around --no-startup-id, s/\.call/check_output/ and focusing the workspace)

import argparse
import json
import subprocess
import sys
import logging
import logging.handlers

log = logging.getLogger(__name__)
# log.setLevel(logging.DEBUG)
log.addHandler(logging.StreamHandler(sys.stderr))
log.addHandler(logging.handlers.SysLogHandler())


def get_output(cmd):
    process = subprocess.Popen(cmd, stdout=subprocess.PIPE)
    out = process.communicate()[0].decode()
    process.stdout.close()
    return out


def get_tree():
    cmd = ["i3-msg", "-t", "get_tree"]
    return json.loads(get_output(cmd))


def get_matching_class(wm_class):
    cmd = ["xdotool", "search", "--class", wm_class]
    return get_output(cmd).split("\n")


windows = []


def walk_tree(tree, workspace=None):
    if tree.get("type") == "workspace":
        workspace = tree.get("name")
    if tree["window"]:
        windows.append({"window": str(tree["window"]), "focused": tree["focused"], "workspace": workspace})
    if len(tree["nodes"]) > 0:
        for node in tree["nodes"]:
            walk_tree(node, workspace)


def get_matches(wm_class):
    matches = []
    tree = get_tree()
    check = get_matching_class(wm_class)
    walk_tree(tree)
    for window in windows:
        for winid in check:
            if window["window"] == winid:
                matches.append(window)
    return matches


def main():
    parser = argparse.ArgumentParser(description="Raise or run: focus existing window or launch program")
    parser.add_argument("wm_class", help="Window manager class to match")
    parser.add_argument("program", help="Program to launch if no window found")
    parser.add_argument("--workspace", "-w", help="Preferred workspace to prioritize")
    args = parser.parse_args()

    log.debug("wm_class: " + args.wm_class)
    matches = get_matches(args.wm_class)

    # Sort the list by window IDs
    matches = [(match["window"], match) for match in matches]
    matches.sort()
    matches = [match for (key, match) in matches]

    # Split matches by preferred workspace
    ws_matches = []
    other_matches = []
    if args.workspace:
        for m in matches:
            if m.get("workspace") == args.workspace:
                ws_matches.append(m)
            else:
                other_matches.append(m)
    else:
        other_matches = matches

    # Check if a window is currently focused
    focused_match = None
    for match in matches:
        if match["focused"]:
            focused_match = match
            break

    if focused_match:
        # If focused window is on preferred workspace, cycle within that workspace only
        if args.workspace and focused_match.get("workspace") == args.workspace:
            if len(ws_matches) > 1:
                ind = ws_matches.index(focused_match)
                next_window = ws_matches[(ind + 1) % len(ws_matches)]
                subprocess.check_output(["i3-msg", "[id=%s] focus" % next_window["window"]])
            # If only one window on preferred workspace, do nothing (stay focused)
            return
        else:
            # Focused window is not on preferred workspace, switch to preferred workspace
            if ws_matches:
                subprocess.check_output(["i3-msg", "[id=%s] focus" % ws_matches[0]["window"]])
                return
            # No windows on preferred workspace, cycle through others
            if len(other_matches) > 1:
                ind = other_matches.index(focused_match)
                next_window = other_matches[(ind + 1) % len(other_matches)]
                subprocess.check_output(["i3-msg", "[id=%s] focus" % next_window["window"]])
            return

    # No focused match was found, focus the first one (prefer workspace matches)
    if ws_matches:
        subprocess.check_output(["i3-msg", "[id=%s] focus" % ws_matches[0]["window"]])
        return
    if other_matches:
        subprocess.check_output(["i3-msg", "[id=%s] focus" % other_matches[0]["window"]])
        return
    # No matches found, launch program
    log.debug("program: " + args.program)
    subprocess.check_output(["i3-msg", "exec --no-startup-id", args.program])


if __name__ == "__main__":
    main()
