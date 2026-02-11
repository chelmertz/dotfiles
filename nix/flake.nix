{
  description = "Home Manager configuration";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    claude-code.url = "github:sadjow/claude-code-nix";
    elly.url = "github:chelmertz/elly";
  };

  outputs = { nixpkgs, home-manager, claude-code, elly, ... }: {
    homeConfigurations."ch" = home-manager.lib.homeManagerConfiguration {
      pkgs = import nixpkgs {
        system = "x86_64-linux";
        overlays = [ claude-code.overlays.default elly.overlays.default ];
        config = {
          allowUnfreePredicate = pkg: builtins.elem (pkg.pname or "") [
            "claude-code"
          ];
        };
      };
      modules = [ ./home.nix ];
    };
  };
}
