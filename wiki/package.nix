# Package expression for wiki, wired in from nix/hosts/tau/wiki.nix.
{ lib, buildGoModule }:

buildGoModule {
  pname = "wiki";
  version = "0.1.0";

  src = ./.;

  # Standard library only.
  vendorHash = null;

  env.CGO_ENABLED = "0";

  meta = {
    description = "Agent-maintained wiki: token-holding proxy and root starter for jailed ingest runs";
    mainProgram = "wiki";
    platforms = lib.platforms.linux;
  };
}
