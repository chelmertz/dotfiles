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

```bash
# Search from CLI
nix search nixpkgs <name>

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

## Migration TODO

Find what's installed outside nix:

```bash
# Brew (top-level only, not deps)
brew leaves

# Apt (manually installed, not deps)
apt-mark showmanual

# Go
ls ~/go/bin

# Cargo
ls ~/.cargo/bin
```

Then add to `home.nix` and uninstall the old versions, and remove them from ansible-laptop.yml.

## Legacy

`ansible-laptop.yml` contains the old Ansible-based setup. Gradually migrating packages to Nix.
