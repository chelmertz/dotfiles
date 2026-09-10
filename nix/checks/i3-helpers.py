"""Assert every helper script i3 and i3blocks call is actually installed.

i3 and i3blocks invoke the scripts in bin/ by bare name or as
~/.local/bin/<name>. home-manager installs them from nix/bin.nix, and the
system puts that directory on PATH via environment.localBinInPath. A helper
that is called but never declared fails only at runtime, as rofi or a bar
block reporting "not found", which is how it stayed broken across three
separate attempts at fixing the PATH.

Usage: i3-helpers.py <i3-config> <i3blocks-conf> <bin-dir> <declared-names>
where <declared-names> is a file holding one installed basename per line.
"""

import os
import re
import sys

i3_config, i3blocks_conf, bin_dir, declared_file = sys.argv[1:5]

declared = {l.strip() for l in open(declared_file) if l.strip()}
repo_bins = set(os.listdir(bin_dir))

i3 = open(i3_config).read()
blocks = open(i3blocks_conf).read()

referenced = set()

# Explicit paths, the unambiguous case.
for text in (i3, blocks):
    referenced |= set(re.findall(r"~/\.local/bin/([\w.\-]+)", text))
    referenced |= set(re.findall(r"\$HOME/\.local/bin/([\w.\-]+)", text))

# Bare names. Only tokens that match a file in bin/ count, so that system
# binaries like pactl and pkill are not mistaken for missing helpers.
def bare_names(line):
    for token in re.findall(r"[\w.\-/~$]+", line):
        base = token.split("/")[-1]
        if base in repo_bins:
            yield base

for line in i3.splitlines():
    m = re.search(r"\bexec(?:_always)?\s+(?:--no-startup-id\s+)?(.*)", line)
    if m:
        referenced |= set(bare_names(m.group(1)))

for line in blocks.splitlines():
    m = re.match(r"\s*command=(.*)", line)
    if m:
        referenced |= set(bare_names(m.group(1)))

missing = sorted(referenced - declared)
if missing:
    print("i3 or i3blocks call helpers that home-manager does not install:")
    for name in missing:
        print(f"  {name}  (add home.file.\".local/bin/{name}\" in nix/bin.nix)")
    sys.exit(1)

print(f"ok: {len(referenced)} helpers referenced, all installed")
