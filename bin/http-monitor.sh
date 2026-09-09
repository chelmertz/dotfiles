#!/usr/bin/env bash
# TODO: consider adding FallbackDNS=1.1.1.1#cloudflare-dns.com 8.8.8.8#dns.google
# to /etc/systemd/resolved.conf — systemd-resolved has no fallback when the
# router DNS (192.168.1.1) is briefly unreachable during DHCP renewals, causing
# curl resolve timeouts while dig works fine. Hold off until more data collected
# with the new diagnostics below to confirm root cause from home network too.
DOMAINS="https://github.com https://buildkite.com https://www.google.com https://www.reddit.com"
CANARY_URL="http://connectivitycheck.gserviceaccount.com/generate_204"
LOG="$HOME/http-monitor.log"

# Statuspage API endpoints for sites that have them
declare -A STATUS_API
STATUS_API[github.com]="https://www.githubstatus.com/api/v2/status.json"
STATUS_API[buildkite.com]="https://www.buildkitestatus.com/api/v2/status.json"

for url in $DOMAINS; do
  domain=$(echo "$url" | awk -F/ '{print $3}')
  output=$(curl -S -s -o /dev/null -w '%{http_code}' -L --max-time 5 -A 'Mozilla/5.0 http-monitor' "$url" 2>&1)
  code="${output: -3}"
  errmsg="${output:0:${#output}-3}"
  if [[ "$code" != "200" ]]; then
    if [[ "$code" == "000" ]]; then
      digout=$(dig +time=3 "$domain" 2>&1)
      gw=$(ip route show default | awk '{print $3; exit}')
      gw_ping=$(ping -c1 -W3 "$gw" 2>&1 && echo "OK" || echo "FAIL")
      ext_ping=$(ping -c1 -W3 8.8.8.8 2>&1 && echo "OK" || echo "FAIL")
      canary=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$CANARY_URL" 2>&1)
      canary_code="${canary: -3}"

      extra=""
      # If network is up but site is down, gather more diagnostics
      if [[ "$ext_ping" == *"OK" ]]; then
        mtrout=$(mtr --report -c3 -n "$domain" 2>&1)
        extra="mtr=${mtrout}"

        # Check status API if available
        api_url="${STATUS_API[$domain]}"
        if [[ -n "$api_url" ]]; then
          status_json=$(curl -s --max-time 5 "$api_url" 2>&1)
          extra="${extra} status_api=${status_json}"
        fi
      fi

      echo "$(date -Is) CONN_FAIL $url err=${errmsg} gw=$gw gw_ping=${gw_ping} ext_ping=${ext_ping} canary=${canary_code} ${extra}dig=${digout}" >> "$LOG"
    else
      echo "$(date -Is) HTTP_FAIL $url code=$code" >> "$LOG"
    fi
  fi
done
