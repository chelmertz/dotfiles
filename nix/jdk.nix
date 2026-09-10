# JDKs, and the stable paths that make them usable from tools that record a
# path once. A nix store path changes on every nixpkgs bump, so an IDEA
# jdk.table.xml or a gradle.properties naming one breaks silently at the next
# update. The symlink names below never change; home-manager repoints them.
#
# NixOS only: gamma still has sdkman, and these are large downloads on a
# machine being retired.
{ config, lib, pkgs, ... }:
lib.mkIf config.dotfiles.nixos {
  home.file.".local/share/jdks/21".source = pkgs.temurin-bin-21;
  home.file.".local/share/jdks/17".source = pkgs.temurin-bin-17;

  # The same JDK 21 on PATH, so `java`, `jar`, `keytool` and `jshell` are
  # commands and not just files under JAVA_HOME. gamma has them from sdkman;
  # here nothing put them anywhere until this, and `java -version` in a login
  # shell answered "command not found". Only 21: adding 17 as well would
  # collide on every binary name it ships.
  home.packages = [ pkgs.temurin-bin-21 ];

  # Gradle does not look in the nix store, so it is told where to look. With
  # this, a toolchain of 17 or 21 resolves without auto-provisioning.
  home.file.".gradle/gradle.properties".text = ''
    org.gradle.java.installations.paths=${config.home.homeDirectory}/.local/share/jdks/21,${config.home.homeDirectory}/.local/share/jdks/17
    org.gradle.java.installations.auto-download=false
  '';

  # Default for anything outside Gradle, and for Gradle's own JVM. Projects
  # that declare a toolchain override it per build; webapp asks for 17.
  home.sessionVariables.JAVA_HOME = "${config.home.homeDirectory}/.local/share/jdks/21";
}
