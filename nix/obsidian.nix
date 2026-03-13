{ pkgs, lib, ... }:
let
  vaultPath = "Dropbox/notes";

  plugins = [
    {
      id = "obsidian-vimrc-support";
      repo = "esm7/obsidian-vimrc-support";
    }
    {
      id = "templater-obsidian";
      repo = "SilentVoid13/Templater";
    }
    {
      id = "calendar";
      repo = "liamcain/obsidian-calendar-plugin";
    }
    {
      id = "dataview";
      repo = "blacksmithgu/obsidian-dataview";
    }
    {
      id = "periodic-notes";
      repo = "liamcain/obsidian-periodic-notes";
    }
    {
      id = "obsidian-tasks-plugin";
      repo = "obsidian-tasks-group/obsidian-tasks";
    }
    {
      id = "obsidian-style-settings";
      repo = "mgmeyers/obsidian-style-settings";
    }
    {
      id = "omnisearch";
      repo = "scambier/obsidian-omnisearch";
    }
    {
      id = "obsidian-paste-image-rename";
      repo = "reorx/obsidian-paste-image-rename";
    }
    {
      id = "nldates-obsidian";
      repo = "argenos/nldates-obsidian";
    }
    {
      id = "obsidian-tracker";
      repo = "pyrochlore/obsidian-tracker";
    }
    {
      id = "obsidian-linter";
      repo = "platers/obsidian-linter";
    }
    {
      id = "obsidian-auto-link-title";
      repo = "zolrath/obsidian-auto-link-title";
    }
  ];

  pluginIds = map (p: p.id) plugins;

  # ── Vault config files (Obsidian reads + writes these) ──────────
  # Written on every `home-manager switch`. Obsidian UI changes are
  # ephemeral — nix is the source of truth, same as vscode.nix.

  appJson = builtins.toJSON {
    vimMode = true;
    livePreview = true;
    defaultViewMode = "source";
    showLineNumber = true;
    readableLineLength = true;
    strictLineBreaks = true;
    attachmentFolderPath = "attachments";
  };

  appearanceJson = builtins.toJSON {
    cssTheme = "";
    enabledCssSnippets = [ "dagbok" ];
    interfaceFontFamily = "";
    textFontFamily = "";
    monospaceFontFamily = "Berkeley Mono, JetBrains Mono, Fira Code, Go Mono, Consolas, monospace";
    baseFontSize = 18;
  };

  corePluginsJson = builtins.toJSON {
    file-explorer = true;
    global-search = true;
    switcher = true;
    graph = false;
    backlink = false;
    outgoing-link = false;
    tag-pane = false;
    page-preview = false;
    daily-notes = true;
    templates = true;
    note-composer = true;
    command-palette = true;
    slash-command = false;
    editor-status = true;
    markdown-importer = false;
    zk-prefixer = false;
    random-note = false;
    outline = true;
    word-count = true;
    slides = false;
    audio-recorder = false;
    workspaces = false;
    file-recovery = false;
    publish = false;
    sync = false;
    canvas = true;
    footnotes = false;
    properties = true;
    bookmarks = true;
    bases = true;
    webviewer = false;
  };

  dailyNotesJson = builtins.toJSON {
    folder = "journal";
    format = "YYYY-MM-DD";
    template = "templates/daily";
  };

  communityPluginsJson = builtins.toJSON pluginIds;

  # ── Read-only files tracked in dotfiles (no personal data) ─────
  dagbokCss = builtins.readFile ../obsidian/snippets/dagbok.css;
  vimrc = builtins.readFile ../obsidian/vimrc;

  # ── Daily template lives ONLY in the vault (Dropbox), not in ───
  # the dotfiles repo. Contains personal data (team member names).
  # Seeded once if missing, then never overwritten by nix.
  defaultTemplate = ''
    ## Work


    ## Personal


    ---

    ## Sync checklist

    ---

    ## Got cred #cred
    <!-- drop screenshots here with ![[image.png]], or just write what happened -->


    ## Notes
  '';

  # ── Plugin installer script ────────────────────────────────────
  installPlugins = pkgs.writeShellScript "install-obsidian-plugins" (
    let
      downloadOne = p: ''
        dir="$VAULT/.obsidian/plugins/${p.id}"
        if [ ! -f "$dir/main.js" ]; then
          echo "  installing ${p.id}..."
          mkdir -p "$dir"
          ${pkgs.curl}/bin/curl -sfL "https://github.com/${p.repo}/releases/latest/download/main.js" -o "$dir/main.js" || true
          ${pkgs.curl}/bin/curl -sfL "https://github.com/${p.repo}/releases/latest/download/manifest.json" -o "$dir/manifest.json" || true
          ${pkgs.curl}/bin/curl -sfL "https://github.com/${p.repo}/releases/latest/download/styles.css" -o "$dir/styles.css" 2>/dev/null || true
        fi
      '';
    in
    ''
      VAULT="$HOME/${vaultPath}"
      echo "obsidian: installing plugins..."
      ${lib.concatMapStrings downloadOne plugins}
      echo "obsidian: done"
    ''
  );
in
{
  home.activation.obsidianVault = ''
        VAULT="$HOME/${vaultPath}"

        # Create vault structure
        mkdir -p "$VAULT/.obsidian/snippets"
        mkdir -p "$VAULT/.obsidian/plugins"
        mkdir -p "$VAULT/templates"
        mkdir -p "$VAULT/journal"
        mkdir -p "$VAULT/attachments"

        # Write config files (nix is source of truth)
        echo '${appJson}' > "$VAULT/.obsidian/app.json"
        echo '${appearanceJson}' > "$VAULT/.obsidian/appearance.json"
        echo '${corePluginsJson}' > "$VAULT/.obsidian/core-plugins.json"
        echo '${dailyNotesJson}' > "$VAULT/.obsidian/daily-notes.json"
        echo '${communityPluginsJson}' > "$VAULT/.obsidian/community-plugins.json"

        # Write files tracked in dotfiles (no personal data)
        cat > "$VAULT/.obsidian/snippets/dagbok.css" << 'CSSEOF'
    ${dagbokCss}
    CSSEOF

        cat > "$VAULT/.obsidian.vimrc" << 'VIMEOF'
    ${vimrc}
    VIMEOF

        # Seed daily template ONLY if missing (contains personal data,
        # lives in Dropbox, not in dotfiles repo)
        if [ ! -f "$VAULT/templates/daily.md" ]; then
          cat > "$VAULT/templates/daily.md" << 'TPLEOF'
    ${defaultTemplate}
    TPLEOF
        fi

        # Install plugins (downloads from GitHub if not present)
        ${installPlugins}
  '';
}
