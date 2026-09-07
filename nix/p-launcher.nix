{ pkgs, lib, ... }:
{
  # Launcher for ~/p projects (F5). Source in ../p-launcher; design notes in
  # ~/p/personal/p-launcher/. Bumping a Go dependency changes vendorHash: set
  # it to lib.fakeHash, run home-manager switch, copy the "got:" hash back.
  home.packages = [
    (pkgs.buildGoModule {
      pname = "p-launcher";
      version = "0.1.0";
      src = ../p-launcher;
      vendorHash = "sha256-JIqffDGU076jdWEEW6Uw7c1oC3ZHrr+gEqi6sfdIgAk=";
      meta.mainProgram = "p-launcher";
    })
  ];
}
