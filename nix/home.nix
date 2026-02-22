{ pkgs, ... }:
{
  imports = [
    ./zsh.nix
    ./git.nix
    ./bin.nix
    ./neovim.nix
    ./espanso.nix
  ];

  xsession.windowManager.i3 = {
    enable = true;
    config = null;
    extraConfig = builtins.readFile ../.i3/config;
  };

  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  news.display = "silent";

  # for standard packages, without any custom configuration; otherwise, remove from this list and do "program.myprogram = { enable = true; .. other options}"
  home.packages = with pkgs; [
    arandr
    asciinema
    autorandr
    awscli2
    bat
    brightnessctl
    bruno
    cargo
    claude-code
    cloc
    copyq
    (symlinkJoin {
      name = "element-desktop";
      paths = [ element-desktop ];
      buildInputs = [ makeWrapper ];
      postBuild = ''
        wrapProgram $out/bin/element-desktop --add-flags "--no-sandbox"
      '';
    })
    entr
    fd
    figlet
    vivid
    elly
    flameshot
    font-awesome
    fzf
    gh
    git-filter-repo
    gleam
    go
    gopls
    graphviz
    gron
    html-tidy
    htop
    hyperfine
    i3blocks
    jq
    lazydocker
    lazygit
    libreoffice
    litecli
    meld
    mycli
    mermaid-cli
    navi
    nixfmt
    nmap
    nodejs_22
    pandoc
    pavucontrol
    peek
    pgcli
    php
    pnpm
    prometheus
    prometheus-blackbox-exporter
    playerctl
    ripdrag
    ripgrep
    rofi
    rofimoji
    runme
    screenkey
    shellcheck
    shfmt
    (symlinkJoin {
      name = "signal-desktop";
      paths = [ signal-desktop ];
      buildInputs = [ makeWrapper ];
      postBuild = ''
        wrapProgram $out/bin/signal-desktop --add-flags "--no-sandbox"
      '';
    })
    (symlinkJoin {
      name = "slack";
      paths = [ slack ];
      buildInputs = [ makeWrapper ];
      postBuild = ''
        wrapProgram $out/bin/slack --add-flags "--no-sandbox"
      '';
    })
    slop
    spotify
    sqlite
    sqlitebrowser
    tree
    texliveSmall
    treemd
    trurl
    uni
    visidata
    vlc
    w3m
    wmctrl
    xcape
    xclip
    xdotool
    xsel
    yq
    yt-dlp
    zizmor
  ];

  programs.home-manager.enable = true;

  # Set up git hooks for the dotfiles repo so editor settings are synced on commit
  home.activation.dotfilesGitHooks = ''
    if [ -d "$HOME/dotfiles/.git" ]; then
      ${pkgs.git}/bin/git -C "$HOME/dotfiles" config core.hooksPath .githooks
    fi
  '';

  programs.direnv = {
    enable = true;
    nix-direnv.enable = true;
  };

  programs.wezterm = {
    enable = true;
    extraConfig = ''
      local wezterm = require 'wezterm'
      local config = wezterm.config_builder()

      function get_appearance()
        local success, stdout = wezterm.run_child_process {
          'gsettings',
          'get',
          'org.gnome.desktop.interface',
          'color-scheme',
        }

        stdout = stdout:lower():gsub('%s+', ''')
        local mapping = {
          ['prefer-light'] = 'Light',
          ['prefer-dark'] = 'Dark',
        }
        if mapping[stdout] then
          return mapping[stdout]
        end
        if stdout:find 'dark' then
          return 'Dark'
        end
        return 'Light'
      end

      function scheme_for_appearance(appearance)
        if appearance:find 'Dark' then
          -- return 'Batman' -- doesn't colorize green/red git index status in lazygit
          -- return 'Builtin Solarized Dark' -- has too many colors going on
          -- return '3024 (base16)' -- ok
          -- return 'Mono Amber (Gogh)' -- orange monochrome, doesn't work with lazygit
          -- return 'Mono Theme (terminal.sexy)' -- black and white mono (very mono, not even colors for git log or lazygit)
          -- return 'Night' -- OK, cyan and other colors, a bit noisy
          -- return 'Afterglow' -- grey and too little contrast
          -- return 'Atom' -- too purple
          -- return 'Blazer' -- too blue
          -- return 'BlulocoDark' -- gray + non contrast
          -- return 'Borland' -- insanely blue
          return 'Bright Lights' -- OK, many colors, monokai style, a little noisy
        else
          -- ...maybe the problem is with `fd`
          -- return 'Builtin Solarized Light' -- works OK mostly, not very big on contrasts
          -- return 'Gruvbox light, medium (base16)' -- has a very very light turquoise & green, hard to read against the white background
          -- return 'Gruvbox light, hard (base16)' -- has a very very light turquoise & green, hard to read against the white background
          -- return 'Gruvbox (Gogh)' -- has a very very light turquoise & green, hard to read against the white background
          -- return 'ayu_light' -- has a very very light turquoise & green, hard to read against the white background
          -- return 'CLRS' -- has a very very light turquoise & green, hard to read against the white background
          -- return 'Google (light) (terminal.sexy)' -- has good base colors in vim, fd output is too light still
          -- return 'Catppuccin Latte' -- warm off-white, well-tuned ANSI but greens are too intense
          -- return 'Rosé Pine Dawn' -- warm pinkish-white background, good contrast
          return 'Modus-Operandi-Tinted' -- light and nice, creamy warm background, good colors in lazygit
        end
      end

      wezterm.on('update-status', function(window, pane)
        local overrides = window:get_config_overrides() or {}
        local wanted = scheme_for_appearance(get_appearance())
        if overrides.color_scheme ~= wanted then
          overrides.color_scheme = wanted
          window:set_config_overrides(overrides)
        end
      end)

      config.color_scheme = scheme_for_appearance(get_appearance())
      config.enable_tab_bar = false
      config.scrollback_lines = 10000
      config.enable_scroll_bar = true
      config.font = wezterm.font("Go Mono", {weight="Regular", stretch="Normal", style="Normal"})
      config.font_size = 15

      return config
    '';
  };

  services.redshift = {
    enable = true;
    temperature = {
      day = 5700;
      night = 3500;
    };
    settings = {
      redshift = {
        fade = 1;
        gamma = 0.8;
        adjustment-method = "randr";
      };
    };
    latitude = 57.7363152;
    longitude = 12.1292249;
  };

  # Safety net: if redshift was toggled off via i3blocks during the day,
  # re-start it at 21:00. Timers can't target another service directly,
  # so we need this oneshot wrapper to start redshift.service.
  systemd.user.services.redshift-ensure = {
    Unit.Description = "Ensure redshift is running";
    Service = {
      Type = "oneshot";
      ExecStart = "${pkgs.systemd}/bin/systemctl --user start redshift.service";
    };
  };

  systemd.user.timers.redshift-ensure = {
    Unit.Description = "Start redshift by 21:00";
    Timer.OnCalendar = "*-*-* 21:00:00";
    Install.WantedBy = [ "timers.target" ];
  };

  xresources.properties = {
    # good for curved external monitor at home
    "Xft.dpi" = 70;
    "rofi.dpi" = 70;
    "*.dpi" = 70;
    "Xcursor.size" = 24;
  };

  home.file.".ideavimrc".text = ''
    set surround
    set incsearch
    set ignorecase
    set smartcase

    vnoremap < <gv
    vnoremap > >gv

    nnoremap ,w :tabclose<cr>

    " https://github.com/JetBrains/ideavim/blob/9eeab756e401ba2c5198c4b28b1c732cddac46c2/doc/ideajoin-examples.md
    set ideajoin

    " sync vim marks & idea bookmarks
    set ideamarks

    " https://github.com/JetBrains/ideavim/wiki/%22set%22-commands there actually is decent vim support...
    set hlsearch
  '';

  services.dunst = {
    enable = true;
    settings = {
      global = {
        monitor = 0;
        follow = "keyboard";
        geometry = "400x5-30+20";
        width = 300;
        height = 300;
        origin = "top-right";
        offset = "10x50";
        scale = 0;
        notification_limit = 0;
        progress_bar = true;
        progress_bar_height = 10;
        progress_bar_frame_width = 1;
        progress_bar_min_width = 150;
        progress_bar_max_width = 300;
        indicate_hidden = true;
        transparency = 0;
        separator_height = 2;
        padding = 16;
        horizontal_padding = 12;
        text_icon_padding = 0;
        frame_width = 0;
        frame_color = "#aaaaaa";
        separator_color = "frame";
        sort = true;
        font = "Monospace 12";
        line_height = 0;
        markup = "full";
        format = "<b>%s</b>\\n%b";
        alignment = "left";
        vertical_alignment = "center";
        show_age_threshold = 60;
        ellipsize = "middle";
        ignore_newline = false;
        stack_duplicates = true;
        hide_duplicate_count = false;
        show_indicators = true;
        icon_position = "left";
        min_icon_size = 0;
        max_icon_size = 32;
        icon_path = "/usr/share/icons/gnome/16x16/status/:/usr/share/icons/gnome/16x16/devices/";
        sticky_history = true;
        history_length = 20;
        dmenu = "rofi -dmenu -p dunst";
        browser = "/usr/bin/xdg-open";
        always_run_script = true;
        title = "Dunst";
        class = "Dunst";
        corner_radius = 6;
        ignore_dbusclose = false;
        force_xwayland = false;
        force_xinerama = false;
        mouse_left_click = "close_current";
        mouse_middle_click = "do_action, close_current";
        mouse_right_click = "close_all";
      };
      experimental = {
        per_monitor_dpi = false;
      };
      urgency_low = {
        background = "#222222";
        foreground = "#888888";
        timeout = 10;
      };
      urgency_normal = {
        background = "#41484d";
        foreground = "#ffffff";
        timeout = 10;
      };
      urgency_critical = {
        background = "#900000";
        foreground = "#ffffff";
        frame_color = "#ff0000";
        timeout = 0;
      };
    };
  };

  # lazygit: use reverse video for selection so it adapts to any terminal color scheme
  xdg.configFile."lazygit/config.yml".text = ''
gui:
  theme:
    selectedLineBgColor:
      - reverse
    selectedRangeBgColor:
      - reverse
  '';

  # Route portal interfaces to the GTK backend when running under i3.
  # After changing, restart with: systemctl --user restart xdg-desktop-portal
  xdg.configFile."xdg-desktop-portal/i3-portals.conf".text = ''
# Without this file, xdg-desktop-portal doesn't expose org.freedesktop.portal.Settings
# on i3 because the GTK/GNOME portal backends only declare UseIn=gnome.
# This means Electron apps (Slack, etc.) can't detect or react to light/dark theme
# changes set via "gsettings set org.gnome.desktop.interface color-scheme".
# With this config, the portal relays color-scheme changes to apps in real time.
[preferred]
default=gtk
  '';

  xdg.configFile."prometheus/prometheus.yml".text = ''
    global:
      scrape_interval: 1m

    rule_files:
      - rules.yml

    scrape_configs:
      - job_name: 'blackbox-http'
        metrics_path: /probe
        params:
          module: [http_2xx]
        static_configs:
          - targets:
            - https://iamnearlythere.com
            - https://helmertz.com
            - https://matchi.se
        relabel_configs:
          - source_labels: [__address__]
            target_label: __param_target
          - source_labels: [__param_target]
            target_label: instance
          - target_label: __address__
            replacement: localhost:9115

      - job_name: 'elly'
        static_configs:
          - targets:
            - localhost:9876

      - job_name: 'domain'
        metrics_path: /probe
        static_configs:
          - targets:
            - iamnearlythere.com
            - helmertz.com
            - matchi.se
        relabel_configs:
          - source_labels: [__address__]
            target_label: __param_target
          - target_label: __address__
            replacement: localhost:9222
  '';

  xdg.configFile."prometheus/blackbox.yml".text = ''
    modules:
      http_2xx:
        prober: http
        timeout: 5s
        http:
          valid_http_versions: ["HTTP/1.1", "HTTP/2.0"]
          valid_status_codes: [200, 201, 202, 203, 204, 301, 302]
          method: GET
          follow_redirects: true
          preferred_ip_protocol: ip4
  '';

  xdg.configFile."prometheus/rules.yml".text = ''
    groups:
      - name: alerts
        rules:
          - alert: EndpointDown
            expr: probe_success == 0
            for: 2m
            labels:
              severity: critical
            annotations:
              summary: "{{ $labels.instance }} is down"

          - alert: DomainExpirySoon
            expr: domain_expiry_days < 50
            for: 1h
            labels:
              severity: warning
            annotations:
              summary: "Domain {{ $labels.domain }} expires in {{ $value }} days"

          - alert: DomainExpiryCritical
            expr: domain_expiry_days < 30
            for: 1h
            labels:
              severity: critical
            annotations:
              summary: "Domain {{ $labels.domain }} expires in {{ $value }} days"
  '';

  systemd.user.services.blackbox-exporter = {
    Unit = {
      Description = "Blackbox Exporter";
    };
    Service = {
      ExecStart = "${pkgs.prometheus-blackbox-exporter}/bin/blackbox_exporter --config.file=%h/.config/prometheus/blackbox.yml";
      Restart = "on-failure";
    };
    Install = {
      WantedBy = [ "default.target" ];
    };
  };

  systemd.user.services.prometheus = {
    Unit = {
      Description = "Prometheus";
      After = [ "blackbox-exporter.service" ];
    };
    Service = {
      ExecStart = "${pkgs.prometheus}/bin/prometheus --config.file=%h/.config/prometheus/prometheus.yml --storage.tsdb.path=%h/.local/share/prometheus";
      Restart = "on-failure";
    };
    Install = {
      WantedBy = [ "default.target" ];
    };
  };

  systemd.user.services.domain-exporter = {
    Unit = {
      Description = "Domain Expiry Exporter";
    };
    Service = {
      ExecStart = "/usr/bin/docker run --rm --name domain-exporter -p 9222:9222 docker.io/caarlos0/domain_exporter";
      ExecStop = "/usr/bin/docker stop domain-exporter";
      Restart = "on-failure";
    };
    Install = {
      WantedBy = [ "default.target" ];
    };
  };

  # flameshot as a daemon, otherwise ctrl+c doesn't work for me (using X11, in
  # the source of this derivation I see USE_WAYLAND_CLIPBOARD=true .. maybe
  # relevant)
  systemd.user.services.flameshot = {
    Unit = {
      Description = "Flameshot";
    };
    Service = {
      ExecStart = "${pkgs.flameshot}/bin/flameshot";
      Restart = "on-failure";
    };
    Install = {
      WantedBy = [ "default.target" ];
    };
  };

  systemd.user.services.elly = {
    Unit = {
      Description = "Elly - Github PR dashbaord";
    };
    Service = {
      ExecStart = "${pkgs.elly}/bin/elly -db %h/.local/share/elly/elly.db";
      Restart = "on-failure";
    };
    Install = {
      WantedBy = [ "default.target" ];
    };
  };
}
