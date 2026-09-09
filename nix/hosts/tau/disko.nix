# Disk layout for tau. One NVMe carrying three things: an ESP, a LUKS
# container, and ext4 inside the container. No LVM and no separate /home,
# because a split only creates a partition that can fill while the other has
# room to spare.
#
# Swap is a file on that same filesystem (swapDevices in configuration.nix)
# rather than a partition, for the same reason: 8G resized by editing a number
# instead of repartitioning. Hibernate is out of scope, so the file never has
# to hold all of RAM.
#
# The device is named directly rather than by-id: this laptop has one disk and
# no second OS to renumber it. The mediabox by-id rule was for two SATA disks
# that swapped names between the installer and the running system.
{
  disko.devices.disk.nvme = {
    type = "disk";
    device = "/dev/nvme0n1";
    content = {
      type = "gpt";
      partitions = {
        ESP = {
          size = "1G";
          type = "EF00";
          content = {
            type = "filesystem";
            format = "vfat";
            mountpoint = "/boot";
            mountOptions = [ "umask=0077" ];
          };
        };
        luks = {
          size = "100%";
          content = {
            type = "luks";
            name = "cryptroot";
            # nixos-anywhere copies the passphrase to this path before running
            # disko, via --disk-encryption-keys /tmp/luks.key <local file>. It
            # is not a file that exists on the installed system.
            passwordFile = "/tmp/luks.key";
            settings.allowDiscards = true;
            content = {
              type = "filesystem";
              format = "ext4";
              mountpoint = "/";
            };
          };
        };
      };
    };
  };
}
