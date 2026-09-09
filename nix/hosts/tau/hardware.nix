# ThinkPad X1 Carbon Gen 12 (Meteor Lake). Written by hand so the flake builds
# before the machine is in hand; nixos-anywhere regenerates it during the
# install with --generate-hardware-config, and that version replaces this one.
# Graphics, trackpoint and power defaults come from nixos-hardware's
# lenovo-thinkpad-x1-12th-gen module, not from here.
#
# Filesystems and swap are absent on purpose: disko.nix declares the former,
# configuration.nix the latter.
{ modulesPath, ... }:
{
  imports = [ (modulesPath + "/installer/scan/not-detected.nix") ];

  boot.initrd.availableKernelModules = [
    "xhci_pci"
    "thunderbolt"
    "nvme"
    "usb_storage"
    "sd_mod"
    "sdhci_pci"
  ];
  boot.initrd.kernelModules = [ ];
  boot.kernelModules = [ "kvm-intel" ];
  boot.extraModulePackages = [ ];

  hardware.cpu.intel.updateMicrocode = true;
  hardware.enableRedistributableFirmware = true;

  nixpkgs.hostPlatform = "x86_64-linux";
}
