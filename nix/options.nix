{ lib, ... }:
{
  options.dotfiles.nixos = lib.mkOption {
    type = lib.types.bool;
    default = false;
    description = ''
      True on a NixOS host, false on the Ubuntu one. Guards the workarounds
      that exist only because home-manager runs on a foreign distribution:
      EGL wrapping for GTK terminals, absolute /usr paths for binaries Ubuntu
      owns, and the Yaru cursor theme Ubuntu ships as a system package. Set by
      homeConfigurations."ch@tau"; gamma takes the default.
    '';
  };
}
