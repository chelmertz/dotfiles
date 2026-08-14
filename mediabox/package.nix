# Package expression for mediabox-add, wired in from nix/mediabox.nix.
#
# The binary name is mediabox-add; the Go module is called mediabox, so the
# meta.mainProgram below is what makes `nix run` and lib.getExe agree.
{ lib, buildGoModule }:

buildGoModule {
  pname = "mediabox-add";
  version = "0.1.0";

  src = ./.;

  vendorHash = "sha256-OB3lHD+HRSjDrm2axmWzff0BbsSo+y91e6kJSZWaSHA=";

  subPackages = [ "." ];

  # Go names the binary after the module, which is "mediabox".
  postInstall = ''
    mv "$out/bin/mediabox" "$out/bin/mediabox-add"
  '';

  # rsync and ssh are called at runtime rather than linked, so they are not
  # inputs here. They come from the user's PATH on purpose: that is what lets
  # ~/.ssh/config, the agent and known_hosts apply as they normally would.

  meta = {
    description = "Copy files to the media box, identifying them by content";
    mainProgram = "mediabox-add";
    platforms = lib.platforms.linux;
  };
}
