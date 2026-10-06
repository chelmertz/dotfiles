# Package expression for git-freshen, wired in from nix/git-freshen.nix and
# exercised by nix/flake.nix's git-freshen check.
{
  lib,
  buildGoModule,
  makeWrapper,
  git,
  openssh,
  coreutils,
}:

buildGoModule {
  pname = "git-freshen";
  version = "0.1.0";

  src = ./.;

  # No dependencies outside the standard library.
  vendorHash = null;

  # The prune tests build real repositories and worktrees; without git they
  # would skip, and a build that skips them proves nothing.
  nativeCheckInputs = [ git ];

  nativeBuildInputs = [ makeWrapper ];

  # Wrapped with everything it forks rather than the unit pinning a PATH, for
  # the reason nix/p-launcher.nix spells out: four units in this repo have been
  # broken by a PATH kept in a file the program's author never opens. openssh
  # is here because it is *not* in home.packages - the same absence that broke
  # spotify-backup - and a git-freshen with no ssh would report every checkout
  # as a fetch failure. coreutils supplies `false`, the askpass that refuses.
  # --suffix, so an interactive session's own PATH still wins.
  postInstall = ''
    wrapProgram $out/bin/git-freshen \
      --suffix PATH : ${
        lib.makeBinPath [
          git
          openssh
          coreutils
        ]
      }
  '';

  meta = {
    description = "Fetch every clone, fast-forward what is safe, remove finished worktrees";
    mainProgram = "git-freshen";
    platforms = lib.platforms.linux;
  };
}
