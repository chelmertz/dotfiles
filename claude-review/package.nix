# Package expression for claude-review, wired in from nix/home.nix.
{
  lib,
  buildGoModule,
  makeWrapper,
  gh,
  git,
  procps,
  libnotify,
  xclip,
}:

buildGoModule {
  pname = "claude-review";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      (lib.fileset.fileFilter (f: f.hasExt "go") ./.)
    ];
  };

  # golang.org/x/term (raw mode for the Slack prompt) and its x/sys, and
  # modernc.org/sqlite for the runner's ledger, pgregory.net/rapid for the
  # property tests.
  vendorHash = "sha256-PmFbgue67HJD4EwdnX+OGXa1l0UKt2pGh9ntWOLfiFA=";

  # The repo tests build real git checkouts; the session tests read a real
  # process start time with ps.
  nativeCheckInputs = [
    git
    procps
  ];

  nativeBuildInputs = [ makeWrapper ];

  # Wrapped with what it forks besides claude and the terminal, which come
  # from the user's session like any program they launch. --suffix, so an
  # interactive session's own PATH still wins; see nix/git-freshen.nix.
  postInstall = ''
    wrapProgram $out/bin/claude-review \
      --suffix PATH : ${
        lib.makeBinPath [
          gh
          git
          procps
          libnotify
          xclip
        ]
      }
  '';

  meta = {
    description = "Start or resume a Claude Code review session for a GitHub PR";
    mainProgram = "claude-review";
    platforms = lib.platforms.linux;
  };
}
