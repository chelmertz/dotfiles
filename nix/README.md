# Nix / home-manager

Declarative package management for user environment.

## Setup

### 1. Install Nix

```bash
sh <(curl -L https://nixos.org/nix/install) --daemon
```

Open a new terminal after installation.

### 2. Enable flakes and set up symlinks

```bash
# Symlink nix.conf
mkdir -p ~/.config/nix
ln -sf ~/code/github/chelmertz/dotfiles/nix/nix.conf ~/.config/nix/nix.conf

# Symlink home-manager config
ln -sf ~/code/github/chelmertz/dotfiles/nix ~/.config/home-manager
```

### 3. Apply configuration

```bash
home-manager switch --flake ~/.config/home-manager
```

After first run, `home-manager` is on your PATH and you can just run:

```bash
home-manager switch
```

## Adding packages

Edit `home.nix` and add packages to `home.packages`:

```nix
home.packages = with pkgs; [
  fzf
  jq
  tree
  # add more here
];
```

Apply the configuration:

```bash {"name": "update-home-manager"}
home-manager switch
```

## Finding package names

```bash {"name": "search"}
# Search from CLI
read "name?Package name: "
nix search nixpkgs "$name"

# Or browse: https://search.nixos.org/packages
```

## Trying packages temporarily

Test a package without adding to config:

```bash
# Enter a shell with the package available
nix shell nixpkgs#htop

# Run a command directly
nix run nixpkgs#cowsay -- "hello"
```

If you like it, add to `home.nix` and run `home-manager switch`.

## Updating packages

Equivalent of `apt update && apt upgrade`:

```bash
nix flake update && home-manager switch
```

- `nix flake update` - updates `flake.lock` to latest nixpkgs (like `apt update`)
- `home-manager switch` - rebuilds with new versions (like `apt upgrade`)

## Rollback

```bash
# List generations
home-manager generations

# Roll back to previous generation
home-manager switch --rollback
```

## Remove unused packages

```bash {"name": "nix-gc"}
nix-collect-garbage
```

## Files

- `nix.conf` - Nix daemon config (enables flakes)
- `flake.nix` - Flake inputs (nixpkgs, home-manager versions)
- `flake.lock` - Pinned versions (auto-generated, commit this)
- `home.nix` - Your packages and config

## Packages staying outside nix

These are intentionally kept as apt/system/other and should not be migrated:

| Package | Source | Reason |
|---------|--------|--------|
| Docker (docker-ce, containerd, docker-compose) | apt | System daemon, needs root |
| i3 | apt | GDM session file (`/usr/share/xsessions/i3.desktop`) |
| Steam | apt | 32-bit libs; first-class on NixOS later |
| Cursor | apt | Not in nixpkgs |
| JetBrains Toolbox | standalone | Self-managing |
| Sober/Roblox | flatpak | Only option |
| Tailscale | apt | System service |
| Dropbox | apt | Daemon complexity |
| Emacs + Doom | apt (PPA) | Fragile, keeping until new computer |
| Java 11 | apt | Keep as-is |
| Rustup + cargo | ~/.cargo | Toolchain management; image-sorter not in nixpkgs |
| Firefox | snap | OpenGL/Mesa issues on non-NixOS; revisit on NixOS |
| gamemode | apt | Keep as-is |
| cheese, dconf-editor, gnome-tweaks | apt | GNOME-coupled |
| GNOME core (calculator, sysmon, disks, terminal) | apt | GNOME-coupled |
| build-essential, curl, gparted | apt | System-level |
| Work Go tools (serve, matchi-cli, etc.) | go install | Custom work tools |
| orgparse | pip | Keep for i3blocks |

## Legacy

`ansible-laptop.yml` is archived (commented out). Docker setup is the only remaining manual step - see comments in that file.
