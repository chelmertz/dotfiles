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

  # The ingest tests build real git repositories and run the real gitleaks.
  nativeCheckInputs = [
    git
    gitleaks
  ];
  nativeBuildInputs = [ makeWrapper ];

  # `wiki ingest` forks git and gitleaks: the pinned ones come first, since
  # the commit's isolation and the secret scan depend on their behaviour.
  postInstall = ''
    wrapProgram $out/bin/wiki --prefix PATH : ${
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
