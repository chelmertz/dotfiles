#!/usr/bin/env bash
# Checks the installed wiki jail end to end, through the real starter and
# proxy, without Claude Code and without reaching Anthropic: two agent runs
# execute `wiki jail-check` in place of claude (a runtime drop-in per run id),
# every request they send the proxy is one it must refuse, and the proxy
# forwards nothing for the jail-check command in any case.
#
#   sudo wiki-jail-check
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "run with sudo" >&2; exit 1; }
user=${SUDO_USER:?run with sudo from the user the starter serves}
user_home=$(getent passwd "$user" | cut -d: -f6)
wiki=$(systemctl show -p ExecStart --value wiki-starter.service | grep -o '/nix/store/[^ ;]*/bin/wiki' | head -1)
[ -x "$wiki" ] || { echo "wiki-starter.service not installed" >&2; exit 1; }
probes=10 # PROXY_PROBES in wiki/src/jailcheck.rs

runs=(jailchecka jailcheckb)
decoys=(/var/lib/wiki/runs/decoy /var/lib/private/wiki-agent/decoy)
prompt=$(mktemp)
err=$(mktemp)
cleanup() {
  for r in "${runs[@]}"; do rm -rf "/run/systemd/system/wiki-agent@$r.service.d"; done
  systemctl daemon-reload
  rm -rf "${decoys[@]}"
  rm -f "$prompt" "$err"
}
trap cleanup EXIT
for r in "${runs[@]}"; do
  d="/run/systemd/system/wiki-agent@$r.service.d"
  mkdir -p "$d"
  printf '[Service]\nExecStart=\nExecStart=%s jail-check --user-home %s\n' "$wiki" "$user_home" > "$d/jail-check.conf"
  # Fresh counts: the proxy keeps usage files, and these run ids repeat.
  rm -f "/var/lib/wiki-proxy/usage/$r.json"
done
# Something for the cross-run checks to miss.
mkdir -p "${decoys[@]}"
systemctl daemon-reload
chmod 0644 "$prompt"
echo "jail check; no model runs" > "$prompt"

fail=0
for r in "${runs[@]}"; do
  echo "== run $r (as $user, through the starter)"
  out=$(sudo -u "$user" "$wiki" start --run "$r" --org personal --command jail-check --prompt "$prompt" 2>"$err") || true
  if ! jq -e '.result | fromjson' <<<"$out" >/dev/null 2>&1; then
    echo "FAIL no report: $out $(cat "$err")"
    fail=1
    continue
  fi
  jq -r '.result | fromjson | .checks[] | "\(if .pass then "PASS" else "FAIL" end) \(.name): \(.detail)"' <<<"$out"
  jq -e '.result | fromjson | .pass' <<<"$out" >/dev/null || fail=1
  u="/var/lib/wiki-proxy/usage/$r.json"
  if [ -f "$u" ] && jq -e --argjson n "$probes" '.org == "personal" and .charged_tokens == 0 and ([.refusals[]] | add) == $n' "$u" >/dev/null; then
    echo "PASS proxy charged nothing and counted all $probes refusals under org personal"
  else
    echo "FAIL proxy usage for $r: $(cat "$u" 2>/dev/null || echo missing)"
    fail=1
  fi
done

echo "== from outside the jail"
if [ ! -S /run/wiki/proxy.sock ]; then
  echo "FAIL /run/wiki/proxy.sock does not exist"
  fail=1
else
  rc=0
  sudo -u "$user" curl -s -m 5 --unix-socket /run/wiki/proxy.sock -o /dev/null http://wiki-proxy.invalid/v1/messages || rc=$?
  if [ "$rc" = 7 ]; then
    echo "PASS $user cannot connect to the proxy socket"
  else
    echo "FAIL $user connecting to the proxy socket: curl exit $rc, want 7 (refused)"
    fail=1
  fi
fi
body=$(curl -s -m 5 --unix-socket /run/wiki/proxy.sock -X POST http://wiki-proxy.invalid/v1/messages -d '{}' || true)
if grep -q 'wiki-proxy refused: peer' <<<"$body"; then
  echo "PASS root outside an agent unit is refused as a peer"
else
  echo "FAIL root got: $body"
  fail=1
fi
for u in wiki-agent@jailchecka wiki-proxy wiki-starter; do
  systemd-analyze security --no-pager "$u.service" | tail -1
done

if [ "$fail" = 0 ]; then
  echo "ALL PASS"
else
  echo "SOME CHECKS FAILED"
  exit 1
fi
