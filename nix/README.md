# Nix / home-manager

Declarative package management for user environment.

## Two hosts

`gamma` is Ubuntu 24.04 with home-manager on top; `tau` is NixOS. This flake
carries both.

| Attribute | Host | Applied with |
|---|---|---|
| `homeConfigurations.ch` | gamma | `home-manager switch` |
| `homeConfigurations."ch@tau"` | tau | `home-manager switch` |
| `nixosConfigurations.tau` | tau | `sudo nixos-rebuild switch --flake ~/.config/home-manager#tau` |

The `home-manager` script tries `$USER@$(hostname)` before `$USER`, so the bare
command is right on both. `dotfiles.nixos` in `options.nix` is what differs
between them: the EGL wrappers, the polkit agent path and the Yaru cursor
package are Ubuntu-only.

On tau the system layer is a second thing to apply. A change under
`hosts/tau/` needs `nixos-rebuild`, not `home-manager switch`. `flake.lock` is
shared, so `bin/nix-bump` moves both hosts at once.

This repository is public, so no credential, key or address belongs in it.
`hosts/tau/configuration.nix` reads the account password from a file placed
during the install rather than declaring one.

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

**This table is about gamma only.** It records what stayed outside nix on
Ubuntu, and most rows no longer describe tau, where the system layer is
declared in `hosts/tau/`: Docker, i3, the GDM session, Firefox, 1Password,
Emacs, Java and gamemode are all in the flake there, and the GNOME-coupled
rows simply do not exist. Read it as gamma's inventory until that machine is
handed in, at which point the section goes with it.

These are intentionally kept as apt/system/other on gamma:

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
| Rustup + cargo | ~/.cargo | Toolchain management |
| Firefox | apt (Mozilla repo) | snap confinement blocks 1Password native messaging; pinned via /etc/apt/preferences.d/mozilla |
| 1Password (gui + cli) | apt (1Password repo) | nix can't run the privileged post-install (onepassword group, setgid on 1Password-BrowserSupport, polkit policy in /usr/share/polkit-1/actions/) needed for desktop↔extension integration and polkit unlock |
| gamemode | apt | Keep as-is |
| cheese, dconf-editor, gnome-tweaks | apt | GNOME-coupled |
| GNOME core (calculator, sysmon, disks, terminal) | apt | GNOME-coupled |
| build-essential, curl, gparted | apt | System-level |
| Work Go tools (serve, matchi-cli, etc.) | go install | Custom work tools |
| orgparse | pip | Keep for i3blocks |

## Legacy

The `ansible-laptop.yml` playbook is gone. What it documented is now either declared in `hosts/tau/configuration.nix` (Docker, the video and input groups, keyd, the nix daemon settings) or, for the pieces that stay imperative on Ubuntu, only relevant to gamma until it is retired.
