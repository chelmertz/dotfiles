# Cargo's own config file, not an exported variable, because the cap has to
# survive the shells that actually run the builds. `nix develop` and direnv
# bring their own cargo and can start from a clean environment, which drops an
# exported CARGO_BUILD_JOBS; every cargo reads $CARGO_HOME/config.toml no
# matter which binary it is or who launched it. CARGO_HOME is unset here, so
# that is ~/.cargo/config.toml.
#
# 4 of tau's 14 cores. Uncapped, cargo runs one codegen job per core, and a
# large tree — padelboard's vendored Rust API is 169 crates — makes the machine
# unresponsive for everything else while it builds. The trade is about 40% wall
# clock: `cargo test --lib` there goes from 32s to 45s.
#
# Still overridable where it matters: `cargo -j N` and CARGO_BUILD_JOBS both
# win over this file, and a project's own .cargo/config.toml is nearer and wins
# too. Nothing here reaches CI, which builds on its own runners.
#
# Not host-gated: the file costs nothing, and the weaker machine wants the cap
# more than this one does.
{ ... }:
{
  home.file.".cargo/config.toml".text = ''
    [build]
    jobs = 4
  '';
}
