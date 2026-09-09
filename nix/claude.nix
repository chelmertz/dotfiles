{ pkgs, lib, ... }:
{
  # Public half of the Claude Code configuration (hooks, theme, plugins,
  # status line). Deliberately no `model`: a pin here outranks whatever /model
  # writes, so the interactive choice never survived, and a new model release
  # would leave the pin quietly stale. The merge below only adds and overwrites
  # keys, so removing one here does not remove it from a live file; that has to
  # be deleted once per machine. ~/.claude/settings.json is MERGED on every switch, not
  # symlinked: Claude Code writes its own keys into that file (permissions,
  # autoMode, skip* prompts, enabledPlugins toggles) and a read-only store
  # path would break those writes. Keys present in ../claude/settings.json
  # win; every other key in the live file is kept.
  #
  # The private half (permissions allowlist, autoMode rules) names work
  # paths and is deliberately not in this public repo; on a new machine it is
  # restored by hand. Credentials live in ~/.claude/.credentials.json and are
  # never versioned (log in again).
  home.activation.claudeSettings = lib.hm.dag.entryAfter [ "writeBoundary" ] ''
    live="$HOME/.claude/settings.json"
    src="${../claude/settings.json}"
    mkdir -p "$HOME/.claude"
    if [ -f "$live" ]; then
      ${pkgs.jq}/bin/jq -s '.[0] * .[1]' "$live" "$src" > "$live.tmp" \
        && $DRY_RUN_CMD mv "$live.tmp" "$live"
    else
      $DRY_RUN_CMD cp "$src" "$live" && $DRY_RUN_CMD chmod 644 "$live"
    fi
  '';

  home.file.".claude/statusline.sh" = {
    source = ../claude/statusline.sh;
    executable = true;
  };

  # Shows what the live file has that the source does not, for the public
  # keys: run it after editing ~/.claude/settings.json by hand (or letting
  # Claude do it) and copy the wanted changes into claude/settings.json.
  home.file.".local/bin/claude-settings-drift" = {
    executable = true;
    text = ''
      #!/usr/bin/env bash
      set -euo pipefail
      keys='{hooks, model, theme, tui, editorMode, effortLevel, preferredNotifChannel, statusLine, enabledPlugins, extraKnownMarketplaces}'
      diff -u <(${pkgs.jq}/bin/jq -S "$keys" "${../claude/settings.json}") \
              <(${pkgs.jq}/bin/jq -S "$keys" "$HOME/.claude/settings.json") \
        && echo "no drift"
    '';
  };
}
