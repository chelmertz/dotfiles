#!/usr/bin/env bash
# Regression test for bin/git-freshen. Every remote here is a path on disk, so
# the test exercises the real fetch path without a network.
#
# The rule the whole script exists to keep is "no write that is not a proven
# fast-forward", so most of these assert what git-freshen *declines* to do.
set -uo pipefail

freshen=${1:?usage: git-freshen-test.sh /path/to/git-freshen}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work" || exit 1

export HOME=$work GIT_CONFIG_GLOBAL=$work/gitconfig GIT_CONFIG_NOSYSTEM=1
git config --global user.email t@example.com
git config --global user.name Test
git config --global init.defaultBranch main
git config --global advice.detachedHead false

fails=0
ok()   { printf 'ok   %s\n' "$1"; }
bad()  { printf 'FAIL %s\n     %s\n' "$1" "$2"; fails=$((fails + 1)); }
# is asserts an exact value, has a substring. Exact is the default because the
# output is a fixed set of columns: a substring test on a status field passes
# for any longer word that happens to contain it.
is() { # is <name> <expected> <actual>
  [ "$2" = "$3" ] && ok "$1" || bad "$1" "expected '$2', got '$3'"
}
has() { # has <name> <expected-substring> <actual>
  [ -n "$2" ] || { bad "$1" 'empty substring matches anything; use is()'; return; }
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1" "expected '$2' in: $3" ;; esac
}

# run invokes git-freshen through an explicit bash rather than its shebang:
# the nix check sandbox has no /usr/bin/env, so `#!/usr/bin/env bash` cannot
# resolve there and the script never starts. Silence from a run is a failure,
# not an empty result - without this guard a script that did not execute at all
# made twelve assertions fail against "", which reads as twelve bugs.
# run invokes git-freshen through an explicit bash rather than its shebang:
# the nix check sandbox has no /usr/bin/env, so `#!/usr/bin/env bash` cannot
# resolve there and the script never starts.
export GIT_FRESHEN_JOBS=4
run() { bash "$freshen" "$@" "$work/roots" 2>"$work/stderr"; }

# guard turns silence into one clear failure. Without it a script that did not
# start at all made twenty-three assertions fail against "", which reads as
# twenty-three bugs rather than one. It runs in the main shell on purpose:
# `exit` inside the $(run) substitution would only kill the subshell.
guard() {
  [ -n "$1" ] && return 0
  printf 'FATAL git-freshen produced no output; stderr:\n' >&2
  sed 's/^/     /' "$work/stderr" >&2
  exit 1
}

commit() { git -C "$1" commit -q --allow-empty -m "$2"; }

# origin holds one commit; every clone below starts from it, then origin moves
# on, so "behind" is the default state and each case differs only in what the
# clone did locally.
mkdir -p roots origins
git init -q --bare origins/up.git
git clone -q origins/up.git seed
commit seed base
git -C seed push -q origin main

clone() { git clone -q origins/up.git "roots/$1"; }
for name in behind dirty diverged detached noupstream onfeature mainahead inrebase busy; do
  clone "$name"
done

# origin gains a commit after everyone has cloned.
commit seed upstream-1
git -C seed push -q origin main
for name in behind dirty diverged detached noupstream onfeature mainahead inrebase busy; do
  git -C "roots/$name" fetch -q origin
done
# Cloned after origin moved, so this one is level with upstream and its single
# local commit makes it purely ahead - the case that must be reported, never
# pushed.
clone ahead
commit roots/ahead local-only

git -C roots/dirty checkout -q main && echo scratch > roots/dirty/tracked.txt
git -C roots/dirty add tracked.txt && commit roots/dirty tracked && git -C roots/dirty reset -q --soft HEAD~1
commit roots/diverged local-only
git -C roots/detached checkout -q --detach HEAD
git -C roots/noupstream checkout -q -b orphan-branch
git -C roots/onfeature checkout -q -b feature
commit roots/onfeature feature-work
# Local main has a commit origin does not, and is not checked out. It is the
# case that separates a fast-forward from a reset: update-ref would happily
# move main backwards onto origin/main and drop the commit, so the ancestry
# proof is the only thing standing between this repo and silent data loss.
commit roots/mainahead main-only
git -C roots/mainahead checkout -q -b feature
# A branch whose remote counterpart has been deleted - the state every merged
# PR leaves behind. git reports it only on stderr while still printing "@{u}"
# on stdout, so it is easy to read as a working upstream and then fail to count
# against it.
clone gonebranch
git -C roots/gonebranch checkout -q -b feature-merged
git -C roots/gonebranch push -q -u origin feature-merged
git -C origins/up.git update-ref -d refs/heads/feature-merged
# An interrupted rebase leaves rebase-merge behind; fabricating the directory
# is how git itself detects one, and is far steadier than racing a real
# conflict inside a test.
mkdir -p roots/inrebase/.git/rebase-merge

before_mainahead=$(git -C roots/mainahead rev-parse main)
before_behind=$(git -C roots/behind rev-parse HEAD)
before_feature_main=$(git -C roots/onfeature rev-parse main)
origin_main=$(git -C origins/up.git rev-parse main)

# fld pulls one field of one repo's record. Reading the output by column
# rather than by substring is the point of the format, and it means a column
# that silently shifts fails the suite instead of passing on a lucky grep.
#   1 status  2 path  3 branch  4 ahead  5 behind  6 upstream  7 fetch
#   8 moved   9 note
fld() { awk -F'\t' -v r="roots/$2" -v n="$3" '$2 == r { print $n }' <<<"$1"; }

printf '\n--- dry run ---\n'
dry=$(run --dry-run); guard "$dry"
printf '%s\n' "$dry" | column -t -s "$(printf '\t')"

is 'every record has nine fields' '' \
  "$(awk -F'\t' 'NF != 9 { print $0 }' <<<"$dry")"
is 'dry run plans the fast-forward'   'would-ff' "$(fld "$dry" behind 1)"
is 'dry run names the ref it would move' 'main'  "$(fld "$dry" onfeature 8)"
is 'dry run moves no checkout' "$before_behind"       "$(git -C roots/behind rev-parse HEAD)"
is 'dry run writes no ref'     "$before_feature_main" "$(git -C roots/onfeature rev-parse main)"

printf '\n--- real run ---\n'
# A process whose cwd is inside roots/busy: the liveness gate must see it.
( cd roots/busy && exec sleep 25 ) &
busy_pid=$!
sleep 0.3
real=$(run); guard "$real"
kill "$busy_pid" 2>/dev/null
printf '%s\n' "$real" | column -t -s "$(printf '\t')"

is 'clean and behind is fast-forwarded' 'fast-forwarded' "$(fld "$real" behind 1)"
is 'the moved column names the branch'  'main'           "$(fld "$real" behind 8)"
is 'a dirty tree is blocked'            'blocked'        "$(fld "$real" dirty 1)"
has 'and says why'                      'uncommitted'    "$(fld "$real" dirty 9)"
is 'an interrupted rebase is blocked'   'blocked'        "$(fld "$real" inrebase 1)"
has 'and says why'                      'in progress'    "$(fld "$real" inrebase 9)"
is 'an occupied checkout is blocked'    'blocked'        "$(fld "$real" busy 1)"
has 'and says why'                      'working in it'  "$(fld "$real" busy 9)"
is 'nothing blocked was moved'          '-'              "$(fld "$real" dirty 8)"
is 'a diverged branch is reported only' 'diverged'       "$(fld "$real" diverged 1)"
is 'diverged counts both directions'    '1 1'  "$(fld "$real" diverged 4) $(fld "$real" diverged 5)"
is 'a branch only ahead is reported'    'unpushed'       "$(fld "$real" ahead 1)"
is 'a deleted remote branch is named'   'upstream-gone'  "$(fld "$real" gonebranch 1)"
is 'no upstream is reported only'       'no-upstream'    "$(fld "$real" noupstream 1)"
is 'a detached HEAD is reported only'   'detached'       "$(fld "$real" detached 1)"
is 'a detached HEAD has no branch'      '-'              "$(fld "$real" detached 3)"
is 'a local fetch is recorded as ok'    'ok'             "$(fld "$real" behind 7)"

# The writes that must not have happened. Each compares against state captured
# before the run, so none can pass by comparing a value to itself.
has   'dirty tree still dirty'   'tracked.txt' "$(git -C roots/dirty status --porcelain)"
has   'diverged head untouched'  'local-only'  "$(git -C roots/diverged log -1 --format=%s)"
is    'detached stays detached'  ''            "$(git -C roots/detached symbolic-ref --quiet --short HEAD)"
is    'a gone upstream moves nothing' ''       "$(git -C roots/gonebranch status --porcelain)"
is    'nothing was pushed'       ''            "$(git -C origins/up.git log --format=%s "$origin_main"..main 2>/dev/null)"
is    'origin is where it started' "$origin_main" "$(git -C origins/up.git rev-parse main)"

# The update-ref half: on a feature branch, local main advances to origin's tip
# without the checkout moving.
is 'local main advanced off-checkout' \
  "$(git -C roots/onfeature rev-parse origin/main)" "$(git -C roots/onfeature rev-parse main)"
is 'local main actually changed' 'moved' \
  "$([ "$(git -C roots/onfeature rev-parse main)" != "$before_feature_main" ] && echo moved)"
has 'feature branch itself did not move' \
  'feature-work' "$(git -C roots/onfeature log -1 --format=%s)"
# main is checked out in roots/behind, so update-ref must decline it and leave
# the fast-forward to `merge --ff-only`. If the ref write had ignored its own
# worktree guard, moved would name main twice.
is 'update-ref skips a checked-out branch' 'main' "$(fld "$real" behind 8)"

# The ancestry proof. Local main here is ahead of origin/main and not checked
# out, so update-ref is otherwise free to run - and would reset the branch,
# discarding main-only. Nothing may touch it.
is  'a diverged local main is left alone' "$before_mainahead" "$(git -C roots/mainahead rev-parse main)"
has 'the commit only local main had survives' \
  'main-only' "$(git -C roots/mainahead log -1 --format=%s main)"
is  'no ref was moved for a diverged local main' '-' "$(fld "$real" mainahead 8)"

# --- the prompts ------------------------------------------------------------
# The first real run put a GTK "Username for 'https://git.heroku.com'" dialog
# on screen, because GIT_TERMINAL_PROMPT=0 shuts only the terminal door and
# git prefers an askpass helper for HTTPS credentials. A timer has nobody to
# answer that.
#
# Asserting it end to end would need a server that returns 401, so this
# asserts the invariant one level down instead: every git the worker runs is
# handed an environment in which all four prompt paths are closed. A `git`
# shim first on PATH records what it was given. The ambient GIT_ASKPASS below
# is what a desktop session supplies, and it must not survive.
mkdir -p "$work/shim"
cat > "$work/shim/git" <<'SHIM'
#!/bin/sh
# /bin/sh, not /usr/bin/env: the nix sandbox has the former and not the latter.
{
  echo "GIT_ASKPASS=${GIT_ASKPASS-unset}"
  echo "GIT_TERMINAL_PROMPT=${GIT_TERMINAL_PROMPT-unset}"
  echo "SSH_ASKPASS_REQUIRE=${SSH_ASKPASS_REQUIRE-unset}"
  echo "SSH_ASKPASS=${SSH_ASKPASS-unset}"
  echo "GIT_SSH_COMMAND=${GIT_SSH_COMMAND-unset}"
} >> "$ENVLOG"
exit 1
SHIM
chmod +x "$work/shim/git"
ENVLOG=$work/gitenv
export ENVLOG
GIT_ASKPASS=/desktop/gui-askpass SSH_ASKPASS=/desktop/ssh-askpass \
  PATH="$work/shim:$PATH" bash "$freshen" --one "$work/roots/behind" >/dev/null 2>&1
seen() { grep -m1 "^$1=" "$ENVLOG" | cut -d= -f2-; }

is  'the desktop askpass is overridden' '' \
  "$(grep -c '^GIT_ASKPASS=/desktop/gui-askpass$' "$ENVLOG" | grep -v '^0$')"
has 'git is given an askpass that refuses' 'false' "$(seen GIT_ASKPASS)"
is  'the terminal prompt is off'      '0'     "$(seen GIT_TERMINAL_PROMPT)"
is  'the ssh askpass is never used'   'never' "$(seen SSH_ASKPASS_REQUIRE)"
is  'the ssh askpass is unset'        'unset' "$(seen SSH_ASKPASS)"
has 'ssh itself cannot prompt'        '-oBatchMode=yes' "$(seen GIT_SSH_COMMAND)"

printf '\n%s\n' "$([ "$fails" -eq 0 ] && echo 'all checks passed' || echo "$fails check(s) failed")"
exit $((fails > 0))
