# The wiki's system layer on tau: the auth proxy, the root starter and the
# jailed ingest agent. Design and its security reviews live outside this
# public repository; the shape is: an agent unit with no network and no
# credential reaches Anthropic only through the proxy's socket, the proxy holds
# the seat token and charges every token to the run's org, and the starter is
# the only way to start an agent, for the user's uid only.
#
# One-time setup, as root: put the long-lived seat token (`claude setup-token`)
# in a systemd credential, outside home, the Nix store and backups:
#   systemd-creds encrypt --name=seat-token - /etc/credstore.encrypted/wiki-seat-token
{ claudeForWiki }:
{ config, lib, pkgs, ... }:
let
  wiki = pkgs.callPackage ../../../wiki/package.nix { };
  # Anthropic's published API range. The proxy dials 160.79.104.10 directly
  # (wiki proxy --connect) so it needs no DNS and nothing else is reachable.
  anthropicApi = "160.79.104.0/23";
  hardening = {
    NoNewPrivileges = true;
    ProtectSystem = "strict";
    ProtectHome = true;
    PrivateTmp = true;
    PrivateDevices = true;
    PrivateIPC = true;
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectKernelLogs = true;
    ProtectControlGroups = true;
    ProtectClock = true;
    ProtectHostname = true;
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    SystemCallArchitectures = "native";
    SystemCallFilter = [ "@system-service" ];
    KeyringMode = "private";
  };
in
{
  users.groups.wiki-agent = { };
  users.groups.wiki-proxy = { };
  users.users.wiki-proxy = {
    isSystemUser = true;
    group = "wiki-proxy";
  };

  systemd.tmpfiles.rules = [
    "d /run/wiki/runs 0755 root root -"
    "d /var/lib/wiki 0755 root root -"
    "d /var/lib/wiki/runs 0711 root root -"
  ];

  # ── Proxy ───────────────────────────────────────────────────────────────
  # The socket outlives proxy restarts, so a running agent's bind mount of it
  # never points at a dead inode.
  systemd.sockets.wiki-proxy = {
    wantedBy = [ "sockets.target" ];
    socketConfig = {
      ListenStream = "/run/wiki/proxy.sock";
      SocketUser = "root";
      SocketGroup = "wiki-agent";
      SocketMode = "0660";
    };
  };
  systemd.services.wiki-proxy = {
    description = "wiki: token-holding proxy for jailed agent runs";
    requires = [ "wiki-proxy.socket" ];
    serviceConfig = hardening // {
      User = "wiki-proxy";
      Group = "wiki-proxy";
      ExecStart = "${lib.getExe wiki} proxy";
      # usage/<run>.json is read by the user's wrapper after each run.
      StateDirectory = "wiki-proxy";
      StateDirectoryMode = "0755";
      LoadCredentialEncrypted = "seat-token:/etc/credstore.encrypted/wiki-seat-token";
      IPAddressDeny = "any";
      IPAddressAllow = anthropicApi;
      RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" ];
      CapabilityBoundingSet = "";
      # MAX_BODY in wiki/src/policy.rs caps a request at 8 MiB; a few fit.
      MemoryMax = "512M";
      # It reads /proc/<pid>/cgroup of the connecting agent; ProtectProc
      # would hide other users' processes.
    };
  };

  # ── Starter ─────────────────────────────────────────────────────────────
  systemd.sockets.wiki-starter = {
    wantedBy = [ "sockets.target" ];
    socketConfig = {
      ListenStream = "/run/wiki/starter.sock";
      SocketUser = "ch";
      SocketMode = "0600";
    };
  };
  systemd.services.wiki-starter = {
    description = "wiki: start one jailed agent run for the user";
    requires = [ "wiki-starter.socket" ];
    serviceConfig = hardening // {
      ExecStart = "${lib.getExe wiki} starter --systemctl ${config.systemd.package}/bin/systemctl --user ch";
      # The agent's state directory is removed by `systemctl clean`, not by
      # the starter, so it needs no write access under /var/lib/private.
      ReadWritePaths = [ "/var/lib/wiki" "/run/wiki" ];
      PrivateNetwork = true;
      RestrictAddressFamilies = [ "AF_UNIX" ];
      # Reads result.json, 0600 in the agent's 0700 state directory.
      CapabilityBoundingSet = [ "CAP_DAC_OVERRIDE" ];
    };
  };

  # ── Agent ───────────────────────────────────────────────────────────────
  # One instance per run id; Type=oneshot so `systemctl start` in the starter
  # returns when the agent has exited.
  systemd.services."wiki-agent@" = {
    description = "wiki: jailed ingest agent, run %i";
    serviceConfig = hardening // {
      Type = "oneshot";
      DynamicUser = true;
      SupplementaryGroups = [ "wiki-agent" ];
      # The agent's only writable place; result.json lands here and the
      # starter removes it after reading.
      StateDirectory = "wiki-agent/%i";
      WorkingDirectory = "/var/lib/wiki-agent/%i";
      StandardInput = "file:/var/lib/wiki/runs/%i/prompt.md";
      StandardOutput = "journal";
      # Nothing of a run survives into the next: HOME is the private /tmp.
      Environment = [
        "HOME=/tmp"
        "ANTHROPIC_UNIX_SOCKET=/run/wiki/proxy.sock"
        "ANTHROPIC_BASE_URL=http://wiki-proxy.invalid"
        "ANTHROPIC_API_KEY=placeholder-not-a-key"
        "DISABLE_AUTOUPDATER=1"
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"
        "CLAUDE_CODE_GZIP_REQUEST_BODIES=0"
      ];
      ExecStart = "${claudeForWiki}/bin/claude -p --model claude-sonnet-5-5 --tools Read,Edit,Write --permission-mode acceptEdits --strict-mcp-config --setting-sources \"\"";
      PrivateNetwork = true;
      IPAddressDeny = "any";
      RestrictAddressFamilies = [ "AF_UNIX" ];
      PrivatePIDs = true;
      ProtectProc = "invisible";
      ProcSubset = "pid";
      TemporaryFileSystem = [ "/run" "/var/lib/wiki" ];
      # AF_UNIX still reaches filesystem sockets. nix-daemon's would let the
      # agent run a fixed-output build, which has network.
      InaccessiblePaths = [ "/nix/var" ];
      BindPaths = [ "/run/wiki/proxy.sock" ];
      BindReadOnlyPaths = [ "/var/lib/wiki/runs/%i/in:/var/lib/wiki-agent/%i/in" ];
      CapabilityBoundingSet = "";
      UMask = "0077";
      MemoryMax = "2G";
      TasksMax = 64;
      CPUQuota = "200%";
      TimeoutStartSec = "15min";
    };
  };
}
