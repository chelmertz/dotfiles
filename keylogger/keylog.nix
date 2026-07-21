# home-manager / nixpkgs package expression for keylog.
#
# Wire it into your home.packages, e.g.:
#   home.packages = [ (pkgs.callPackage ./keylogger/keylog.nix { }) ];
#
# On first build nix will report the correct vendorHash — paste it in below
# (start from lib.fakeHash and copy the "got:" value nix prints).
{ lib, buildGoModule }:

buildGoModule {
  pname = "keylog";
  version = "0.1.0";

  src = ./.;

  vendorHash = lib.fakeHash; # replace with the hash nix prints on first build

  # single binary from the module root
  subPackages = [ "." ];

  meta = with lib; {
    description = "Local keyboard-usage profiler for Glove80 layout decisions";
    mainProgram = "keylog";
    platforms = platforms.linux;
  };
}
