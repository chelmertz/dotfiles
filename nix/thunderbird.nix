{
  config,
  lib,
  pkgs,
  private-dotfiles,
  ...
}:
# Thunderbird for both Gmail accounts, driven by Gmail's own keys. Google
# sign-in is OAuth and its tokens live in the profile's password store
# (key4.db + logins.json), never here: a new profile asks once per account.
let
  # The work address is not published; it comes from the private repo and an
  # empty or missing value stops the build here rather than producing an
  # account with no address.
  private = import "${private-dotfiles}/work.nix";
  workEmail =
    assert lib.assertMsg ((private.workEmail or "") != "")
      "private-dotfiles/work.nix sets no workEmail. Add it there (or drop the work account from nix/thunderbird.nix), push, then `nix flake update private-dotfiles`.";
    private.workEmail;
  personalEmail = "helmertz@gmail.com";
  realName = "Calle Helmertz";

  # home-manager links every profile's extensions from this directory, the
  # Firefox application id included, even for Thunderbird.
  extensionDir = "share/mozilla/extensions/{ec8030f7-c20a-464f-9b0e-13a3a9e97384}";
  addon =
    {
      addonId,
      url,
      hash,
      patch ? "",
    }:
    pkgs.runCommand "thunderbird-addon-${addonId}"
      {
        src = pkgs.fetchurl { inherit url hash; };
        nativeBuildInputs = [
          pkgs.unzip
          pkgs.zip
        ];
      }
      ''
        mkdir -p "$out/${extensionDir}" work
        cd work
        unzip -q "$src"
        ${patch}
        zip -qr -X "$out/${extensionDir}/${addonId}.xpi" .
      '';

  # Gmail's keys. tbkeys-lite keeps its bindings in the add-on's own storage,
  # which nothing outside Thunderbird can write, but it falls back to the
  # defaults in background.js while that storage is empty, so the defaults are
  # replaced instead. "unset" frees Thunderbird's own one-key shortcuts (j is
  # junk, k ignores the thread), which is what the options page's "Unset
  # singles" button does for the same list of keys.
  tbkeysMain =
    let
      bound = {
        j = "cmd:cmd_nextMsg";
        k = "cmd:cmd_previousMsg";
        o = "cmd:cmd_openMessage";
        f = "cmd:cmd_forward";
        "#" = "cmd:cmd_delete";
        r = "cmd:cmd_reply";
        a = "cmd:cmd_replyall";
        e = "cmd:cmd_archive";
        c = "func:MsgNewMessage";
        u = "tbkeys:closeMessageAndRefresh";
        z = "cmd:cmd_undo";
        # Gmail's x selects a conversation. Nostalgy's move/copy act on the
        # selected messages only, so a collapsed thread row would move one
        # message and leave the rest of the conversation in Inbox.
        x = "cmd:cmd_selectThread";
        # Page the open message while focus stays in the list, via the two
        # builtins the patch below adds.
        pagedown = "tbkeys:messagePageDown";
        pageup = "tbkeys:messagePageUp";
      };
      singles = lib.stringToCharacters "0123456789abcfjkmoprstuwx#][";
    in
    lib.genAttrs singles (_: "unset") // bound;
  tbkeys = addon {
    addonId = "tbkeys-lite@addons.thunderbird.net";
    url = "https://addons.thunderbird.net/thunderbird/downloads/file/1050828/tbkeys_lite-2.4.4-tb.xpi";
    hash = "sha256-dOSeKWFUiaFsRYKN6rEEBip7Xo1tdjT/ear1wBzgWrU=";
    patch = ''
      ${pkgs.python3}/bin/python3 - <<'EOF'
      import re
      s = open("background.js").read()
      s, n = re.subn(r"mainkeys: `[^`]*`", lambda _: "mainkeys: `" + ${builtins.toJSON (builtins.toJSON tbkeysMain)} + "`", s, count=1)
      assert n == 1, "tbkeys-lite: default mainkeys block not found, the add-on changed"
      open("background.js", "w").write(s)

      # Thunderbird's Space is cmd_space, which pages up when the event it gets
      # has shiftKey set. tbkeys-lite's cmd: bindings pass no event and its eval
      # is disabled, so the up direction needs a builtin of its own.
      s = open("implementation.js").read()
      old = "var builtins = {\n"
      assert s.count(old) == 1, "tbkeys-lite: builtins object not found, the add-on changed"
      s = s.replace(old, old + """  messagePageDown: function (win) {
          win.goDoCommand("cmd_space", { shiftKey: false });
        },
        messagePageUp: function (win) {
          win.goDoCommand("cmd_space", { shiftKey: true });
        },
      """)
      open("implementation.js", "w").write(s)

      # Mousetrap listens on the main window in the bubbling phase, so the
      # message list sees PageUp/PageDown first, moves the selection and loads
      # another message before the binding runs. Capturing lets tbkeys act
      # first; a handled key returns false, which stops it there.
      s = open("modules/mousetrap.js").read()
      old = "object.addEventListener(type, callback, false);"
      assert s.count(old) == 1, "tbkeys-lite: Mousetrap _addEvent changed"
      s = s.replace(old, "object.addEventListener(type, callback, true);")
      open("modules/mousetrap.js", "w").write(s)
      EOF
    '';
  };
  # Folder completion for move (v, Gmail's "move to") and copy (l, Gmail's
  # "label"): a Gmail label is an IMAP folder, so copying adds the label.
  nostalgy = addon {
    addonId = "nostalgy@opto.one";
    url = "https://addons.thunderbird.net/thunderbird/downloads/file/1051299/nostalgy_emails_verwalten_suchen_archivieren-5.2.10-tb.xpi";
    hash = "sha256-i+qevgNNEr/z1kyR/kvf9a5EgcM8lN8Hxq0hVR+OWOU=";
  };

  gmailAccount =
    {
      address,
      primary ? false,
    }:
    let
      folder = name: "imap://${lib.replaceStrings [ "@" ] [ "%40" ] address}@imap.gmail.com/[Gmail]/${name}";
    in
    {
      inherit address primary realName;
      userName = address;
      flavor = "gmail.com";
      thunderbird = {
        enable = true;
        settings = id: { "mail.server.server_${id}.trash_folder_name" = "[Gmail]/Trash"; };
        # Archive means "remove the Inbox label", which in Gmail's IMAP is a move
        # to All Mail; Thunderbird's default is a per-year Archives folder that
        # shows up in Gmail as a stray label. Gmail files sent mail by itself, so
        # a copy from Thunderbird would duplicate it.
        perIdentitySettings = id: {
          "mail.identity.id_${id}.archive_folder" = folder "All Mail";
          "mail.identity.id_${id}.archives_folder_picker_mode" = "1";
          "mail.identity.id_${id}.archive_granularity" = 0;
          "mail.identity.id_${id}.archive_keep_folder_structure" = false;
          "mail.identity.id_${id}.draft_folder" = folder "Drafts";
          "mail.identity.id_${id}.drafts_folder_picker_mode" = "1";
          "mail.identity.id_${id}.fcc" = false;
          "mail.identity.id_${id}.compose_html" = true;
          "mail.identity.id_${id}.reply_on_top" = 1;
        };
      };
    };

  googleCalendar =
    {
      address,
      account,
      primary ? false,
      color,
    }:
    {
      inherit primary;
      remote = {
        type = "caldav";
        url = "https://apidata.googleusercontent.com/caldav/v2/${address}/events/";
        userName = address;
      };
      thunderbird = {
        enable = true;
        inherit color;
        # Invitations accepted from mail are answered as this account.
        settings = id: {
          "calendar.registry.calendar_${id}.imip.identity.key" = "id_${builtins.hashString "sha256" account}";
        };
      };
    };
in
lib.mkIf config.dotfiles.nixos {
  programs.thunderbird = {
    enable = true;
    profiles.main = {
      isDefault = true;
      extensions = [
        tbkeys
        nostalgy
      ];
      settings = {
        # Extensions installed by home-manager are otherwise disabled until
        # approved one by one in the UI.
        "extensions.autoDisableScopes" = 0;
        # Nostalgy's options dialog does not persist key changes; its prefs do.
        # "(disabled)" is the literal the dialog writes to unbind.
        "extensions.nostalgy.keys.save" = "V";
        "extensions.nostalgy.keys.copy" = "L";
        "extensions.nostalgy.keys.hide_folders" = "(disabled)";
        "extensions.nostalgy.keys.convert_rule" = "(disabled)";
        # Headers only: a message body is fetched when opened. The defaults copy
        # every folder, All Mail included, and index it for global search,
        # which took over a gigabyte of memory and steady CPU on the first
        # sync. Gmail web covers deep search.
        "mail.server.default.offline_download" = false;
        "mail.server.default.autosync_offline_stores" = false;
        "mailnews.database.global.indexer.enabled" = false;
        # Space and Shift+Space page the open message while focus stays in the
        # list for j/k. At the bottom, Space stops instead of jumping to the next
        # unread message.
        "mail.advance_on_spacebar" = false;
      };
    };
  };

  accounts.email.accounts = {
    work = gmailAccount {
      address = workEmail;
      primary = true;
    };
    personal = gmailAccount { address = personalEmail; };
  };

  accounts.calendar.accounts = {
    work = googleCalendar {
      address = workEmail;
      account = "work";
      primary = true;
      color = "#3f51b5";
    };
    personal = googleCalendar {
      address = personalEmail;
      account = "personal";
      color = "#0b8043";
    };
  };
}
