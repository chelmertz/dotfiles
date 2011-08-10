#usage
```bash
mkdir -p ~/.config/dotfiles && cd ~/.config/dotfiles
git clone https://chelmertz@github.com/chelmertz/dotfiles.git
git submodule init
git submodule update
#symlink files to ~: http://www.commandlinefu.com/commands/view/1225/symlink-all-files-from-a-base-directory-to-a-target-directory
```

After the repository is updated, make sure symlinks follow the possibly new filenames.

#good to have
 - zsh + oh-my-zsh
 - the font inconsolata: http://www.levien.com/type/myfonts/inconsolata.html
 - homebrew with ctags

##todo
 - support project specific aliases
 - add zsh
 - check if vim plugin is loaded before mapping
 - find git repos of .lvimrc (vim script 1408)
 - homebrew formulas
