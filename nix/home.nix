{ pkgs, ... }:
{
  home.username = "ch";
  home.homeDirectory = "/home/ch";
  home.stateVersion = "24.05";

  # for standard packages, without any custom configuration; otherwise, remove from this list and do "program.myprogram = { enable = true; .. other options}"
  home.packages = with pkgs; [
    arandr
    asciinema
    autorandr
    bat
    brightnessctl
    bruno
    cloc
    copyq
    entr
    fd
    figlet
    flameshot
    font-awesome
    fzf
    gh
    git
    git-filter-repo
    gleam
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
    nmap
    pandoc
    pavucontrol
    peek
    pgcli
    php
    prometheus
    prometheus-blackbox-exporter
    playerctl
    redshift
    ripdrag
    ripgrep
    rofi
    runme
    screenkey
    shellcheck
    shfmt
    slop
    sqlite
    sqlitebrowser
    tree
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

  programs.direnv = {
    # this requires eval direnv in ~/.zshrc as long as I manage it manually -
    # it will be autosolved when home-manager manages zshrc
    enable = true;
    nix-direnv.enable = true;
  };

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
}
