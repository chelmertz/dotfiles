# Nix / home-manager

- The home-manager flake lives in `nix/`. `~/.config/home-manager` is a symlink to
  `nix/` in the primary clone, so a bare `home-manager switch` applies whatever main
  has checked out. From a worktree, apply with
  `home-manager --option warn-dirty false switch --flake ./nix#ch`.
- Every change in this repo must be applied with `home-manager switch`; a commit alone
  changes nothing on the machine.
- `nixpkgs` tracks nixos-unstable, `nixpkgs-stable` tracks the current NixOS release.
  An overlay in `nix/flake.nix` takes spotify and vscode from `pkgsStable` because
  unfree packages are not on cache.nixos.org and every bump rebuilds them locally.
  Slack is deliberately on unstable: the stable build crashed. Move an app between
  channels by adding or removing it from that `inherit (pkgsStable)` list.
- Compare what each channel ships before moving a package:
  `nix eval --raw ./nix#homeConfigurations.ch.pkgs.<name>.version` gives the version
  the config resolves to today.
- `bin/nix-bump` updates the lock, switches, and commits only `nix/flake.lock`. A dirty
  `nix/flake.lock` in the primary clone usually means a bump that was applied but not
  committed; do not discard it.
