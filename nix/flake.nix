{
  description = "Home Manager configuration";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    # The system layer follows stable, as mediabox does. Unfree GUI apps are
    # not on cache.nixos.org and rebuild locally on every bump, the same
    # reason the overlay below takes spotify and vscode from here.
    nixpkgs-stable.url = "github:nixos/nixpkgs/nixos-26.05";
    disko = {
      url = "github:nix-community/disko";
      inputs.nixpkgs.follows = "nixpkgs-stable";
    };
    nixos-hardware = {
      url = "github:NixOS/nixos-hardware";
      inputs.nixpkgs.follows = "nixpkgs";
    };
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
      self,
      nixpkgs,
      nixpkgs-stable,
      home-manager,
      claude-code,
      elly,
      serve,
      disko,
      nixos-hardware,
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
            "dropbox"
            # jetbrains.idea (IntelliJ IDEA Ultimate); its pname is just "idea"
            "idea"
            # dropbox's FHS environment bundles a browser for its login flow;
            # it is not the Firefox the system installs.
            "firefox-bin"
            "firefox-bin-unwrapped"
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

      # `nix flake check` runs these. There is no CI in this repo yet, so they
      # only fail where someone runs them; wiring them to a workflow is the
      # obvious follow-up.
      checks.${system} =
        let
          pkgs = nixpkgs.legacyPackages.${system};
          homeFiles = builtins.attrNames self.homeConfigurations."ch@tau".config.home.file;
          localBin = builtins.filter (n: builtins.match "\\.local/bin/.*" n != null) homeFiles;
          declared = pkgs.writeText "declared-helpers" (
            builtins.concatStringsSep "\n" (map builtins.baseNameOf localBin)
          );
        in
        {
          # i3 and i3blocks call the scripts in bin/ by name. Three separate
          # fixes to the PATH that finds them looked like they had failed,
          # because each was applied to a system generation that was never
          # registered and so did not survive the next reboot. Both halves are
          # pinned here: every helper the configs call is installed, and the
          # PATH entry that finds them is declared.
          # The colour-scheme toggle spans two pieces of host configuration and
          # both failed silently for a week: the GSettings write went to a
          # memory backend for want of dconf's GIO module, and there was no
          # portal on the bus for ghostty to hear the result. Neither shows up
          # as an error anywhere, so assert them.
          color-scheme-plumbing =
            let
              tau = self.nixosConfigurations.tau.config;
            in
            assert tau.programs.dconf.enable;
            assert tau.xdg.portal.enable;
            assert nixpkgs.lib.any (p: nixpkgs.lib.hasPrefix "xdg-desktop-portal-gtk" p.name)
              tau.xdg.portal.extraPortals;
            pkgs.runCommand "color-scheme-plumbing" { } "touch $out";

          i3-helpers =
            assert self.nixosConfigurations.tau.config.environment.localBinInPath;
            pkgs.runCommand "i3-helpers" { nativeBuildInputs = [ pkgs.python3 ]; } ''
              python3 ${./checks/i3-helpers.py} \
                ${../.i3/config} ${../.i3blocks.conf} ${../bin} ${declared}
              touch $out
            '';
        };

      # tau's system layer. home.nix is applied separately by home-manager,
      # which resolves "ch@tau" from networking.hostName below.
      nixosConfigurations.tau = nixpkgs-stable.lib.nixosSystem {
        inherit system;
        modules = [
          disko.nixosModules.disko
          nixos-hardware.nixosModules.lenovo-thinkpad-x1-12th-gen
          ./hosts/tau/disko.nix
          ./hosts/tau/hardware.nix
          ./hosts/tau/configuration.nix
          # Unfree, and each was an apt or .deb package on gamma. Named
          # individually for the same reason unfreeConfig above does it: an
          # unexpected unfree dependency should fail the build, not slip in.
          {
            nixpkgs.config.allowUnfreePredicate =
              pkg:
              builtins.elem (pkg.pname or "") [
                "1password"
                "1password-cli"
                "cursor"
                "steam"
                "steam-original"
                "steam-run"
                "steam-unwrapped"
              ];
          }
          { system.configurationRevision = self.rev or "dirty"; }
        ];
      };
    };
}
