{
  description = "Home Manager configuration";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    nixpkgs-stable.url = "github:nixos/nixpkgs/nixos-25.11";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    # Without these follows each input locks its own nixpkgs, so the lock carried
    # five revisions and every bump refetched ~200M of source tree per extra copy.
    claude-code = {
      url = "github:sadjow/claude-code-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    elly = {
      url = "github:chelmertz/elly";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    serve = {
      url = "github:chelmertz/serve";
      inputs.nixpkgs.follows = "nixpkgs";
    };
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
      unfreeConfig = {
        allowUnfreePredicate =
          pkg:
          let
            name = pkg.pname or "";
          in
          builtins.elem name [
            "claude-code"
            "slack"
            "spotify"
            "vscode"
            "obsidian"
          ]
          || builtins.match "vscode-extension-.*" name != null;
      };
      # pkgsStable serves unfree packages too, so it needs the same predicate.
      pkgsStable = import nixpkgs-stable {
        inherit system;
        config = unfreeConfig;
      };
    in
    {
      homeConfigurations =
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [
              # flameshot 14 routes capture through xdg-desktop-portal and hangs 30s on bare i3/X11
              (final: prev: { flameshot = pkgsStable.flameshot; })
              # Unfree packages are not on cache.nixos.org (Hydra will not redistribute
              # them), so each bump downloads a vendor tarball and unpacks it locally —
              # 1.4G for vscode alone. Stable lags a release behind but rebuilds rarely.
              (final: prev: {
                inherit (pkgsStable)
                  spotify
                  vscode
                  ;
              })
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
            config = unfreeConfig;
          };
          mkHome =
            extraModules:
            home-manager.lib.homeManagerConfiguration {
              inherit pkgs;
              modules = [ ./home.nix ] ++ extraModules;
            };
        in
        {
          # gamma, Ubuntu 24.04. home-manager reaches "ch" only after
          # "ch@<hostname>" misses, so this is the fallback for any host
          # without an entry of its own.
          "ch" = mkHome [ ];
          # tau, NixOS. This hostname must equal networking.hostName in
          # hosts/tau/configuration.nix, or the lookup falls through to "ch"
          # and applies the Ubuntu variant.
          "ch@tau" = mkHome [ { dotfiles.nixos = true; } ];
        };
    };
}
