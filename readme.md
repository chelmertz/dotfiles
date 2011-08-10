#usage
```bash
mkdir -p ~/.config/dotfiles && cd ~/.config/dotfiles
git clone https://chelmertz@github.com/chelmertz/dotfiles.git
git submodule init
git submodule update
#symlink files to ~: http://www.commandlinefu.com/commands/view/1225/symlink-all-files-from-a-base-directory-to-a-target-directory
```

After the repository is updated, make sure symlinks follow the possibly new filenames.

##todo
 - support project specific aliases
 - add zsh
 - add vim
 - check if vim plugin is loaded before mapping
 - vim solarized color scheme
 - find git repos of .lvimrc (vim script 1408)
 - homebrew formulas
