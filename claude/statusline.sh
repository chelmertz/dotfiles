#!/usr/bin/env bash
# Claude Code statusline, two lines:
#   1. model · dir · branch · PR URL (clickable via OSC 8)
#   2. context left · what to do about it · rate limits when they start to bite
#
# Everything on line 2 comes from the JSON Claude Code puts on stdin
# (context_window, rate_limits) - no transcript parsing, no subprocess. The PR
# URL comes from .pr.url when Claude Code knows it; the `gh` lookup is only a
# fallback for versions that don't send it, cached per repo+branch for 2 min.
input=$(cat)

# One value per line, read with mapfile: a model display name like "Fable 5.1"
# contains a space, and `read a b c` on @tsv output splits on it (tab is IFS
# whitespace, so runs collapse and every later field shifts). That shift put
# the remaining-token count into the rate-limit slot and printed "5h 1000000%".
mapfile -t fld < <(printf '%s' "$input" | jq -r '
  (.workspace.current_dir // .cwd // ""),
  (.model.display_name // ""),
  (.pr.url // ""),
  (.context_window.used_percentage // -1 | floor),
  ((((.context_window.context_window_size // 0) - ((.context_window.total_input_tokens // 0) + (.context_window.total_output_tokens // 0))) | floor)),
  (.rate_limits.five_hour.used_percentage // 0 | floor),
  (.rate_limits.seven_day.used_percentage // 0 | floor)')
dir=${fld[0]}; model=${fld[1]}; pr_url=${fld[2]}
ctx_pct=${fld[3]:--1}; ctx_left=${fld[4]:-0}; rl5=${fld[5]:-0}; rl7=${fld[6]:-0}

esc=$'\033'
dim="${esc}[2m"; reset="${esc}[0m"; cyan="${esc}[36m"
yellow="${esc}[33m"; red="${esc}[31m"; bold="${esc}[1m"
link() { printf '%s]8;;%s%s\\%s%s]8;;%s\\' "$esc" "$1" "$esc" "$2" "$esc" "$esc"; }

line1="${model:+${model} }${dim}${dir/#$HOME/"~"}${reset}"

# `git -C ""` silently falls back to the process cwd, which is not this
# session's directory, so an absent cwd must not reach git at all.
if [ -n "$dir" ] && branch=$(git -C "$dir" branch --show-current 2>/dev/null) && [ -n "$branch" ]; then
  line1+=" ${cyan}${branch}${reset}"
  if [ -z "$pr_url" ]; then
    toplevel=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)
    cache="${TMPDIR:-/tmp}/claude-statusline-pr-$(printf '%s' "${toplevel}:${branch}" | md5sum | cut -c1-16)"
    if [ ! -f "$cache" ] || [ -n "$(find "$cache" -mmin +2 2>/dev/null)" ]; then
      url=$(cd "$toplevel" && timeout 5 gh pr view --json url -q .url 2>/dev/null)
      printf '%s' "$url" > "$cache"
    fi
    pr_url=$(cat "$cache" 2>/dev/null)
  fi
  [ -n "$pr_url" ] && line1+=" $(link "$pr_url" "$pr_url")"
fi

# Line 2. Intent: never make the reader judge which kind of handoff this is.
# Above 75% the hint is the single word `/handoff` in every tier - the command
# decides whether to stop now or finish the item first - and only the colour
# carries urgency. Tokens remaining are shown because a percentage cannot say
# whether the subproblem in hand still fits.
line2=""
if [ "$ctx_pct" -ge 0 ] 2>/dev/null; then
  left="$((ctx_left / 1000))k"
  if [ "$ctx_pct" -lt 50 ]; then
    line2="${dim}ctx ${ctx_pct}% · ${left} left${reset}"
  elif [ "$ctx_pct" -lt 75 ]; then
    line2="ctx ${ctx_pct}% · ${left} left"
  elif [ "$ctx_pct" -lt 90 ]; then
    line2="${yellow}ctx ${ctx_pct}% · ${left} left · /handoff${reset}"
  elif [ "$ctx_pct" -lt 97 ]; then
    line2="${red}ctx ${ctx_pct}% · ${left} left · /handoff${reset}"
  else
    line2="${red}${bold}ctx ${ctx_pct}% · ${left} left · /handoff${reset}"
  fi
fi

# Rate limits only once they are worth knowing about: they cap how much can
# run in parallel, and a reset time is no use if the bar is still low.
limits=""
[ "$rl5" -ge 70 ] 2>/dev/null && limits+=" 5h ${rl5}%"
[ "$rl7" -ge 70 ] 2>/dev/null && limits+=" 7d ${rl7}%"
if [ -n "$limits" ]; then
  colour="$yellow"; { [ "$rl5" -ge 90 ] || [ "$rl7" -ge 90 ]; } && colour="$red"
  line2+="${line2:+ ${dim}·${reset}}${colour}${limits# }${reset}"
fi

printf '%s' "$line1"
[ -n "$line2" ] && printf '\n%s' "$line2"
