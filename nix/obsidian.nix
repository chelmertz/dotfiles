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
      tag = "0.0.17";
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
    {
      id = "note-refactor-obsidian";
      repo = "lynchjames/note-refactor-obsidian";
    }
    {
      id = "obsidian-tasks-plugin";
      repo = "obsidian-tasks-group/obsidian-tasks";
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
    alwaysUpdateLinks = true;
    newLinkFormat = "absolute";
  };

  appearanceJson = builtins.toJSON {
    cssTheme = "";
    enabledCssSnippets = [ "dagbok" ];
    interfaceFontFamily = "";
    textFontFamily = "";
    monospaceFontFamily = "Berkeley Mono, JetBrains Mono, Fira Code, Go Mono, Consolas, monospace";
    baseFontSize = 12;
  };

  corePluginsJson = builtins.toJSON {
    file-explorer = true;
    global-search = true;
    switcher = true;
    graph = false;
    backlink = true;
    outgoing-link = false;
    tag-pane = false;
    page-preview = false;
    daily-notes = true;
    templates = true;
    note-composer = true;
    command-palette = true;
    slash-command = true;
    editor-status = true;
    markdown-importer = false;
    zk-prefixer = false;
    random-note = false;
    outline = true;
    word-count = true;
    slides = true;
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

  hotkeysJson = builtins.toJSON {
    "daily-notes:open" = [
      {
        modifiers = [
          "Mod"
          "Shift"
        ];
        key = "D";
      }
    ];
    "daily-notes" = [
      {
        modifiers = [ "Mod" ];
        key = "D";
      }
    ];
    "editor:delete-paragraph" = [ ]; # unbind Ctrl+D conflict
    "daily-notes:goto-prev" = [
      {
        modifiers = [ "Alt" ];
        key = "ArrowLeft";
      }
    ];
    "daily-notes:goto-next" = [
      {
        modifiers = [ "Alt" ];
        key = "ArrowRight";
      }
    ];
    "global-search:open" = [
      {
        modifiers = [
          "Mod"
          "Shift"
        ];
        key = "F";
      }
    ];
    "switcher:open" = [
      {
        modifiers = [
          "Mod"
          "Shift"
        ];
        key = "P";
      }
    ];
    "app:go-back" = [
      {
        modifiers = [ "Mod" ];
        key = "O";
      }
    ];
    "app:go-forward" = [
      {
        modifiers = [ "Mod" ];
        key = "I";
      }
    ];
    "editor:toggle-italics" = [ ]; # unbind Ctrl+I conflict
    "editor:open-search" = [ ]; # unbind default Ctrl+F
    "omnisearch:show-modal" = [
      {
        modifiers = [ "Mod" ];
        key = "F";
      }
    ];
  };

  # ── Plugin settings (data.json) ────────────────────────────────

  dataviewSettingsJson = builtins.toJSON {
    enableDataviewJs = true;
    enableInlineDataviewJs = true;
  };

  omnisearchSettingsJson = builtins.toJSON {
    useCache = true;
    hideExcluded = false;
    recencyBoost = "0";
    downrankedFoldersFilters = [ ];
    ignoreDiacritics = true;
    ignoreArabicDiacritics = false;
    indexedFileTypes = [ ];
    displayTitle = "";
    PDFIndexing = false;
    officeIndexing = false;
    imagesIndexing = false;
    aiImageIndexing = false;
    unsupportedFilesIndexing = "default";
    splitCamelCase = false;
    openInNewPane = false;
    vimLikeNavigationShortcut = true;
    ribbonIcon = true;
    showExcerpt = true;
    maxEmbeds = 5;
    renderLineReturnInExcerpts = true;
    showCreateButton = false;
    highlight = true;
    showPreviousQueryResults = true;
    simpleSearch = false;
    tokenizeUrls = false;
    fuzziness = "1";
    weightBasename = 10;
    weightDirectory = 7;
    weightH1 = 6;
    weightH2 = 5;
    weightH3 = 4;
    weightUnmarkedTags = 2;
    weightCustomProperties = [ ];
    httpApiEnabled = false;
    httpApiPort = "51361";
    httpApiNotice = true;
    verboseLogging = false;
  };

  templaterSettingsJson = builtins.toJSON {
    command_timeout = 5;
    templates_folder = "";
    templates_pairs = [
      [
        ""
        ""
      ]
    ];
    trigger_on_file_creation = true;
    auto_jump_to_cursor = false;
    enable_system_commands = false;
    shell_path = "";
    user_scripts_folder = "";
    enable_folder_templates = true;
    folder_templates = [
      {
        folder = "";
        template = "";
      }
    ];
    enable_file_templates = false;
    file_templates = [
      {
        regex = ".*";
        template = "";
      }
    ];
    syntax_highlighting = true;
    syntax_highlighting_mobile = false;
    enabled_templates_hotkeys = [ "" ];
    startup_templates = [ "" ];
    intellisense_render = 1;
  };

  # ── Read-only files tracked in dotfiles (no personal data) ─────
  dagbokCss = builtins.readFile ../obsidian/snippets/dagbok.css;
  vimrc = builtins.readFile ../obsidian/vimrc;

  # ── Daily template lives ONLY in the vault (Dropbox), not in ───
  # the dotfiles repo. Contains personal data (team member names).
  # Seeded once if missing, then never overwritten by nix.
  defaultTemplate = ''
    ## From the backlog
    ```tasks
    done on <% tp.date.now("YYYY-MM-DD") %>
    path does not include journal/<% tp.date.now("YYYY-MM-DD") %>
    short mode
    hide toolbar
    ```

    ---

    ## Sync checklist

    ---

    ## Notes

    ![[integrations/<% tp.date.now("YYYY-MM-DD") %>]]
  '';

  # ── Plugin installer script ────────────────────────────────────
  installPlugins = pkgs.writeShellScript "install-obsidian-plugins" (
    let
      downloadOne =
        p:
        let
          version = if p ? tag then "download/${p.tag}" else "latest/download";
        in
        ''
          dir="$VAULT/.obsidian/plugins/${p.id}"
          if [ ! -f "$dir/main.js" ]; then
            echo "  installing ${p.id}..."
            mkdir -p "$dir"
            ${pkgs.curl}/bin/curl -sfL "https://github.com/${p.repo}/releases/${version}/main.js" -o "$dir/main.js" || true
            ${pkgs.curl}/bin/curl -sfL "https://github.com/${p.repo}/releases/${version}/manifest.json" -o "$dir/manifest.json" || true
            ${pkgs.curl}/bin/curl -sfL "https://github.com/${p.repo}/releases/${version}/styles.css" -o "$dir/styles.css" 2>/dev/null || true
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
        echo '${hotkeysJson}' > "$VAULT/.obsidian/hotkeys.json"

        # Write files tracked in dotfiles (no personal data)
        cat > "$VAULT/.obsidian/snippets/dagbok.css" << 'CSSEOF'
    ${dagbokCss}
    CSSEOF

        cat > "$VAULT/.obsidian.vimrc" << 'VIMEOF'
    ${vimrc}
    VIMEOF

        # Write plugin settings (nix is source of truth)
        mkdir -p "$VAULT/.obsidian/plugins/dataview"
        echo '${dataviewSettingsJson}' > "$VAULT/.obsidian/plugins/dataview/data.json"
        mkdir -p "$VAULT/.obsidian/plugins/omnisearch"
        echo '${omnisearchSettingsJson}' > "$VAULT/.obsidian/plugins/omnisearch/data.json"
        mkdir -p "$VAULT/.obsidian/plugins/templater-obsidian"
        echo '${templaterSettingsJson}' > "$VAULT/.obsidian/plugins/templater-obsidian/data.json"

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

  systemd.user.services.obsidian-poll = {
    Unit.Description = "Poll GitHub PRs and update Obsidian integrations";
    Service = {
      Type = "oneshot";
      ExecStart = "${pkgs.python3}/bin/python3 %h/.local/bin/obsidian-poll";
      Environment = "PATH=${pkgs.gh}/bin:${pkgs.git}/bin:/usr/bin:/bin";
    };
  };

  systemd.user.timers.obsidian-poll = {
    Unit.Description = "Run obsidian-poll every 2 hours";
    Timer = {
      OnCalendar = "*-*-* 0/2:00:00";
      OnBootSec = "1min";
      Persistent = true;
    };
    Install.WantedBy = [ "timers.target" ];
  };
}
