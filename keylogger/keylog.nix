# home-manager / nixpkgs package expression for keylog.
#
# Wired into home.packages in nix/home.nix. It used to be installed with
# `go install`, which is why gamma had the binary as ~/go/bin/keylogger and a
# fresh machine did not: neovim's keylog autocmd then threw E475 on every
# BufEnter until nix/neovim.nix learned to no-op without it.
#
# If go.mod's required version moves past what nixpkgs ships, the build fails
# with "go.mod requires go >= X"; bump nixpkgs rather than the module.
{ lib, buildGoModule }:

buildGoModule {
  pname = "keylog";
  version = "0.1.0";

  src = ./.;

  vendorHash = "sha256-zR6/I4uZEeqA+OtgE1QtsvdNNNkYL1Pwi3DVbp+bOz8=";

  # single binary from the module root
  subPackages = [ "." ];

  meta = with lib; {
    description = "Local keyboard-usage profiler for Glove80 layout decisions";
    mainProgram = "keylog";
    platforms = platforms.linux;
  };
}
