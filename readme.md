#usage
##installation
```bash
#install xcode

#install brew

#install brew formulas

mkdir -p ~/.config/dotfiles && cd ~/.config/dotfiles
git clone https://chelmertz@github.com/chelmertz/dotfiles.git
git submodule init
git submodule update

#symlink files to ~: http://www.commandlinefu.com/commands/view/1225/symlink-all-files-from-a-base-directory-to-a-target-directory
```

##updating
```bash
git pull origin master
./update-submodules.sh
```

After the repository is updated, make sure symlinks follow the possibly new filenames.

#good to have
 - zsh + oh-my-zsh
 - homebrew with ctags
 - gimli (md to pdf): https://github.com/walle/gimli

##todo
 - support project specific aliases
 - add zsh
 - check if vim plugin is loaded before mapping
 - find git repos of .lvimrc (vim script 1408)
 - homebrew formulas

# vim plugins
- gist-vim - `:Gist` posts buffer as public gist
- matchit.zip - *`%`matches html tags too
- nerdcommenter - `<leader>cc` comments visual selection, `<leader>ci` inverts already commented lines
- quicksilver.vim - `<leader>q` autocompletes your file browser
- supertab - `<tab>` for autocompletion (like `<C-n>`)
- syntastic - syntax check on write
- vim-conque - `<leader>bs` for bash in a split window
- vim-easymotion - `<leader>w` for shortcuts within view
- vim-extradite - `<leader>gs` shows commit log for current file
- vim-fugitive - git support, `:Gdiff` for example
- vim-json - json syntax validation & highlight
- vim-markdown - markdown syntax validation & highlight
- vim-pathogen - place plugins in `.vim/bundle/` to avoid clutter
- vim-repeat - use `.` to repeat plugin actions
- vim-space - repeat commands with `<space>`
- vim-surround - `cs"'` on `"hello"` gives `'hello'`, `ysiw'` on `Hello` gives `'Hello'`, `ds'` on `'Hello'` gives `Hello`
- VisIncr - on visual block selection, `:I` generates list (`:II` for padding on the left side)
- zencoding-vim - html generation with few keystrokes, `<C-y>,` on `html:5x` generates html5 boilerplate (where x is cursor position)
