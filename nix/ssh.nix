# ~/.ssh/config. Before 2026-11-16 the file was unmanaged and held only
# comments, so ssh ran on its built-in defaults; enableDefaultConfig = false
# keeps it that way apart from the block below.
{ ... }:
{
  programs.ssh = {
    enable = true;
    enableDefaultConfig = false;
    # GitHub over port 443. A network on the way stalls or times out port 22
    # (a push hung, then `git pull` timed out connecting, 2026-11-16), and
    # 443 passes as HTTPS. Covers every git remote, the flake's git+ssh
    # inputs and anything else that reaches github.com over ssh.
    # ssh.github.com:443 serves github.com's own host key (ED25519
    # SHA256:+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU, checked against
    # GitHub's published fingerprint), so the alias reuses github.com's
    # known_hosts entry instead of trusting a new one.
    settings."github.com" = {
      HostName = "ssh.github.com";
      Port = 443;
      HostKeyAlias = "github.com";
    };
  };
}
