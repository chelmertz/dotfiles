{ pkgs, ... }:
{
  programs.vscode = {
    enable = true;
    profiles.default.extensions = with pkgs.vscode-extensions; [
      bierner.markdown-mermaid
      docker.docker
      eamodio.gitlens
      github.copilot
      github.copilot-chat
      github.vscode-github-actions
      golang.go
      graphql.vscode-graphql
      graphql.vscode-graphql-syntax
      hashicorp.terraform
      jock.svg
      ms-azuretools.vscode-docker
      ms-azuretools.vscode-containers
      ms-vscode-remote.remote-containers
      ms-vsliveshare.vsliveshare
      pkief.material-icon-theme
      redhat.vscode-yaml
      rust-lang.rust-analyzer
      vscodevim.vim
      waderyan.gitblame
    ] ++ [
      pkgs.vscode-extensions."42crunch".vscode-openapi
    ] ++ (map (ext: pkgs.vscode-utils.extensionFromVscodeMarketplace ext) [
      # Not in nixpkgs - fetched from marketplace
      { name = "mermaid-markdown-syntax-highlighting"; publisher = "bpruitt-goddard"; version = "1.7.6"; sha256 = "sha256-vBqMclDOI0LYIsFyTBKW+cZ7Hjcnl6N5Z8Qlx0ez4SQ="; }
      { name = "bruno"; publisher = "bruno-api-client"; version = "4.5.0"; sha256 = "sha256-YxxSTfa7VbHNniWBfquvtC79NyM0uko7hUIZMUmW2yc="; }
      { name = "subtle-monochrome"; publisher = "covertcj"; version = "0.0.2"; sha256 = "sha256-4T0mNgzj+tjt3FO/R1dMlcyFgUBlpICMCEu6JCt13Gs="; }
      { name = "theme-nohappinessincolors"; publisher = "notoroszbig"; version = "1.0.2"; sha256 = "sha256-31ERyMX90W1ZvJLmeqvhmTjCOKh3fpg25eGq8wETbsw="; }
      { name = "rsl-vsc-focused-folder"; publisher = "rslfrkndmrky"; version = "0.2.1"; sha256 = "sha256-YOkf1OLSvSIvEKkHHZyFsVyF1At78LK6hSe4suHm9tU="; }
    ]);
    profiles.default.userSettings = {
      "editor.fontFamily" = "'Go Mono', Inconsolata,'Droid Sans Mono','Fira Mono', 'monospace', monospace";
      "editor.fontSize" = 16;
      "editor.fontWeight" = "normal";
      "editor.inlineSuggest.enabled" = true;
      "editor.bracketPairColorization.enabled" = false;
      "explorer.confirmDragAndDrop" = false;
      "explorer.confirmDelete" = false;
      "files.autoSave" = "onWindowChange";
      "github.copilot.enable" = {
        "*" = true;
        "markdown" = false;
        "plaintext" = false;
        "yaml" = false;
      };
      "github.copilot.editor.enableAutoCompletions" = true;
      "gitlens.defaultDateFormat" = null;
      "gitlens.defaultDateLocale" = "sv-SE";
      "gitlens.defaultDateShortFormat" = null;
      "gitlens.defaultTimeFormat" = null;
      "gitlens.advanced.messages" = {
        "suppressFileNotUnderSourceControlWarning" = true;
      };
      "go.autocompleteUnimportedPackages" = true;
      "go.toolsManagement.autoUpdate" = true;
      "go.lintTool" = "golangci-lint";
      "go.lintFlags" = [ "--fast" ];
      "go.inlayHints.assignVariableTypes" = true;
      "go.inlayHints.compositeLiteralFields" = true;
      "go.inlayHints.compositeLiteralTypes" = true;
      "go.inlayHints.constantValues" = true;
      "go.inlayHints.ignoredError" = true;
      "go.inlayHints.rangeVariableTypes" = true;
      "go.inlayHints.parameterNames" = true;
      "json.schemas" = [];
      "redhat.telemetry.enabled" = true;
      "sqltools.useNodeRuntime" = true;
      "workbench.iconTheme" = "material-icon-theme";
      "workbench.colorTheme" = "No Happiness in Colors Theme";
      "workbench.preferredDarkColorTheme" = "No Happiness in Colors Theme";
      "workbench.preferredLightColorTheme" = "Subtle Monochrome (Light)";
      "workbench.editor.revealIfOpen" = true;
      "workbench.sideBar.location" = "right";
      "workbench.tree.indent" = 24;
      "window.autoDetectColorScheme" = true;
      "window.zoomLevel" = 2;
      "terminal.integrated.scrollback" = 100000;
      "vim.useCtrlKeys" = true;
      "vim.normalModeKeyBindingsNonRecursive" = [
        { before = [ "u" ]; commands = [ "undo" ]; }
        { before = [ "<C-r>" ]; commands = [ "redo" ]; }
      ];
      "vim.visualModeKeyBindings" = [
        { before = [ "<" ]; after = [ "<" "g" "v" ]; }
        { before = [ ">" ]; after = [ ">" "g" "v" ]; }
      ];
      "rust-analyzer.procMacro.enabled" = false;
      "diffEditor.hideUnchangedRegions.enabled" = true;
      "git.openRepositoryInParentFolders" = "never";
      "githubPullRequests.terminalLinksHandler" = "github";
      "githubPullRequests.pullBranch" = "never";
      "yaml.maxItemsComputed" = 10000;
      "yaml.customTags" = [
        "!And" "!And sequence" "!If" "!If sequence"
        "!Not" "!Not sequence" "!Equals" "!Equals sequence"
        "!Or" "!Or sequence" "!FindInMap" "!FindInMap sequence"
        "!Base64" "!Join" "!Join sequence" "!Cidr"
        "!Ref" "!Sub" "!Sub sequence" "!GetAtt" "!GetAZs"
        "!ImportValue" "!ImportValue sequence"
        "!Select" "!Select sequence" "!Split" "!Split sequence"
      ];
      "vale.doNotShowWarningForFileToBeSavedBeforeLinting" = true;
      "markdown.preview.scrollEditorWithPreview" = false;
      "markdown.preview.scrollPreviewWithEditor" = false;
      "[python]" = { "editor.formatOnType" = true; };
      "[dockercompose]" = {
        "editor.insertSpaces" = true;
        "editor.tabSize" = 2;
        "editor.autoIndent" = "advanced";
        "editor.quickSuggestions" = {
          "other" = true;
          "comments" = false;
          "strings" = true;
        };
        "editor.defaultFormatter" = "redhat.vscode-yaml";
      };
      "[github-actions-workflow]" = {
        "editor.defaultFormatter" = "redhat.vscode-yaml";
      };
    };
  };
}
