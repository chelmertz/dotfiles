#!/usr/bin/env bash
# Replays past claude-review sessions through `claude-review tick --dry-run`:
# a fake elly listing the given PRs with a re-review requested, a scratch
# ledger holding each session's last round, real GitHub reads and the real
# transcripts. Nothing is written anywhere but a temp dir.
#
#   [CLAUDE_REVIEW=./claude-review] replay/replay.sh [--as-open] <pr-url>...
#
# --as-open puts bin/gh first on PATH: it reports every PR open, so a merged
# PR's last round reaches the outcome rules, and refuses any gh call that
# could write.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
ghpath=""
if [ "${1:-}" = --as-open ]; then ghpath="$here/bin:"; shift; fi
run=$(mktemp -d)
trap '[ -z "${srv:-}" ] || kill "$srv"; rm -rf "$run"' EXIT
mkdir -p "$run/state/claude-review" "$run/config/claude-review"
sed -n '/^const ledgerSchema = `/,/^`/p' "$here/../ledger.go" | sed '1d;$d' > "$run/schema.sql"
python3 "$here/seed.py" "$run" "$@"
port=$((20000 + RANDOM % 10000))
python3 -m http.server -d "$run/elly" -b 127.0.0.1 "$port" >/dev/null 2>&1 &
srv=$!
conf=${XDG_CONFIG_HOME:-$HOME/.config}/claude-review
jq --arg u "http://127.0.0.1:$port" '. + {elly_url: $u}' "$conf/config.json" > "$run/config/claude-review/config.json"
\cp -f "$conf/review-repos" "$run/config/claude-review/"
sleep 0.5
PATH="$ghpath$PATH" GH_CONFIG_DIR=${GH_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/gh} \
  XDG_CONFIG_HOME="$run/config" XDG_STATE_HOME="$run/state" "${CLAUDE_REVIEW:-claude-review}" tick --dry-run
