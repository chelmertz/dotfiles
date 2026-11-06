{ config, lib, private-dotfiles, ... }:
let
  # Skills that name work systems live in private-dotfiles/claude/skills, so this
  # public repo links them without naming any. The flake input only supplies the
  # list of names; each link is out-of-store into the clone, like
  # ~/.claude/CLAUDE.md in claude.nix, so an edit takes effect without a switch.
  # A new skill needs a push, `nix flake update private-dotfiles` and a switch.
  dir = "${private-dotfiles}/claude/skills";
  clone = "${config.home.homeDirectory}/code/github/chelmertz/private-dotfiles/claude/skills";
  names = lib.attrNames (lib.filterAttrs (_: t: t == "directory")
    (if builtins.pathExists dir then builtins.readDir dir else { }));
in
{
  home.file = lib.genAttrs (map (n: ".claude/skills/${n}") names) (path: {
    source = config.lib.file.mkOutOfStoreSymlink "${clone}/${lib.removePrefix ".claude/skills/" path}";
  });
}
