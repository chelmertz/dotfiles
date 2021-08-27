#!/usr/bin/env python3

# depends on changelog.sh in PATH

import re
import subprocess
import sys


def bump(tag):
    """
    >>> bump("1")
    '2'
    >>> bump("0.1")
    '0.2'
    >>> bump("0.1.2")
    '0.1.3'
    """
    parts = tag.split(".")
    parts[-1] = str(int(parts[-1]) + 1)
    return ".".join(parts)


def main():
    sp = subprocess.run(["changelog.sh"], capture_output=True)
    if sp.returncode != 0:
        print(f"Bad error code {sp.returncode}", file=sys.stderr)
        print(sp.stderr, file=sys.stderr)
        sys.exit(1)

    changes = sp.stdout
    if len(changes) == 0:
        print("No changes, not bumping tag", file=sys.stderr)
        sys.exit(0)

    tag_re = r"comparing latest tag \(([^\)]+)\) with"
    stderr = sp.stderr.decode("utf-8")
    current_tag_match = re.search(tag_re, stderr)
    if not current_tag_match:
        print(f"Could not detect a tag in: {stderr}", file=sys.stderr)
        print(sp.stderr, file=sys.stderr)
        sys.exit(1)
    current_tag = current_tag_match[1]

    new_version = bump(current_tag)

    subprocess.check_output(["git", "tag", "-a", new_version, "-F", "-"], input=changes)
    subprocess.run(["git", "show", new_version])


if __name__ == "__main__":
    main()
