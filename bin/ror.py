#!/usr/bin/env python3
#
# "ror" = raise or run
#
# from https://faq.i3wm.org/question/2473/run-or-focus-in-i3.1.html
# (with patches around --no-startup-id, s/\.call/check_output/ and focusing the workspace)

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


def walk_tree(tree):
    if tree["window"]:
        windows.append({"window": str(tree["window"]), "focused": tree["focused"]})
    if len(tree["nodes"]) > 0:
        for node in tree["nodes"]:
            walk_tree(node)


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
    wm_class = sys.argv[1]
    log.debug("argv 1: " + wm_class)
    matches = get_matches(wm_class)
    # Sort the list by window IDs
    matches = [(match["window"], match) for match in matches]
    matches.sort()
    matches = [match for (key, match) in matches]
    # Iterate over the matches to find the first focused one, then focus the
    # next one.
    for ind, match in enumerate(matches):
        if match["focused"] == True:
            subprocess.check_output(
                [
                    "i3-msg",
                    "[id=%s] focus" % matches[(ind + 1) % len(matches)]["window"],
                ]
            )
            return
    # No focused match was found, so focus the first one
    if len(matches) > 0:
        subprocess.check_output(["i3-msg", "[id=%s] focus" % matches[0]["window"]])
        return
    # No matches found, launch program
    program = sys.argv[2]
    log.debug("argv 2: " + program)
    subprocess.check_output(["i3-msg", "exec --no-startup-id", program])


if __name__ == "__main__":
    main()
