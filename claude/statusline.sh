#!/usr/bin/env bash
# Claude Code statusline: model · dir · branch · PR URL (clickable via OSC 8).
# gh is slow, so the PR lookup is cached per repo+branch for 2 minutes.
input=$(cat)
dir=$(printf '%s' "$input" | jq -r '.workspace.current_dir // .cwd // empty')
model=$(printf '%s' "$input" | jq -r '.model.display_name // empty')

esc=$'\033'
dim="${esc}[2m"; reset="${esc}[0m"; cyan="${esc}[36m"
link() { printf '%s]8;;%s%s\\%s%s]8;;%s\\' "$esc" "$1" "$esc" "$2" "$esc" "$esc"; }

out="${model:+${model} }${dim}${dir/#$HOME/"~"}${reset}"

if branch=$(git -C "$dir" branch --show-current 2>/dev/null) && [ -n "$branch" ]; then
  out+=" ${cyan}${branch}${reset}"
  toplevel=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)
  cache="${TMPDIR:-/tmp}/claude-statusline-pr-$(printf '%s' "${toplevel}:${branch}" | md5sum | cut -c1-16)"
  if [ ! -f "$cache" ] || [ -n "$(find "$cache" -mmin +2 2>/dev/null)" ]; then
    url=$(cd "$toplevel" && timeout 5 gh pr view --json url -q .url 2>/dev/null)
    printf '%s' "$url" > "$cache"
  fi
  url=$(cat "$cache" 2>/dev/null)
  [ -n "$url" ] && out+=" $(link "$url" "$url")"
fi
printf '%s' "$out"
