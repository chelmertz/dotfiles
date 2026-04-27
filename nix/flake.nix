{
  description = "Home Manager configuration";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    nixpkgs-stable.url = "github:nixos/nixpkgs/nixos-25.11";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    claude-code.url = "github:sadjow/claude-code-nix";
    elly.url = "github:chelmertz/elly";
    serve.url = "github:chelmertz/serve";
  };

  outputs =
    {
      nixpkgs,
      nixpkgs-stable,
      home-manager,
      claude-code,
      elly,
      serve,
      ...
    }:
    let
      system = "x86_64-linux";
      pkgsStable = import nixpkgs-stable { inherit system; };
    in
    {
      homeConfigurations."ch" = home-manager.lib.homeManagerConfiguration {
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            # flameshot 14 routes capture through xdg-desktop-portal and hangs 30s on bare i3/X11
            (final: prev: { flameshot = pkgsStable.flameshot; })
            # cli-helpers 2.10.0 ships 3 test_style_output tests that compare hard-coded
            # ANSI sequences and break against current pygments output.
            (final: prev: {
              pythonPackagesExtensions = prev.pythonPackagesExtensions ++ [
                (pyfinal: pyprev: {
                  cli-helpers = pyprev.cli-helpers.overridePythonAttrs (old: {
                    disabledTests = (old.disabledTests or [ ]) ++ [
                      "test_style_output"
                      "test_style_output_with_newlines"
                      "test_style_output_custom_tokens"
                    ];
                  });
                })
              ];
            })
            claude-code.overlays.default
            elly.overlays.default
            serve.overlays.default
          ];
          config = {
            allowUnfreePredicate =
              pkg:
              let
                name = pkg.pname or "";
              in
              builtins.elem name [
                "1password"
                "1password-cli"
                "claude-code"
                "google-chrome"
                "slack"
                "spotify"
                "vscode"
                "obsidian"
              ]
              || builtins.match "vscode-extension-.*" name != null;
          };
        };
        modules = [ ./home.nix ];
      };
    };
}
