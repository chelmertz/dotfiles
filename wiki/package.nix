# Package expression for wiki, wired in from nix/hosts/tau/wiki.nix. The
# tests run in checkPhase, so a failing test fails the system build.
{
  lib,
  rustPlatform,
  makeWrapper,
  git,
  gitleaks,
}:

rustPlatform.buildRustPackage {
  pname = "wiki";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./Cargo.toml
      ./Cargo.lock
      ./src
    ];
  };

  cargoLock.lockFile = ./Cargo.lock;

  # The ingest tests build real git repositories.
  nativeCheckInputs = [ git ];
  nativeBuildInputs = [ makeWrapper ];

  # `wiki ingest` forks git and gitleaks; carried here rather than trusted to
  # whatever PATH the caller has. --suffix so an interactive PATH still wins.
  postInstall = ''
    wrapProgram $out/bin/wiki --suffix PATH : ${
      lib.makeBinPath [
        git
        gitleaks
      ]
    }
  '';

  meta = {
    description = "Agent-maintained wiki: token-holding proxy and root starter for jailed ingest runs";
    mainProgram = "wiki";
    platforms = lib.platforms.linux;
  };
}
